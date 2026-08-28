package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/timbrinded/freeagent-cli/internal/catalog"
	"github.com/timbrinded/freeagent-cli/internal/output"
	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

const (
	maxInputBytes = 16 << 20
	maxErrorBytes = 64 << 10
)

type operationOptions struct {
	input    string
	queries  []string
	dryRun   bool
	confirm  string
	page     int
	perPage  int
	all      bool
	maxPages int
	maxItems int
	maxBytes int64
}

func (a *App) addCatalogCommands(root *cobra.Command, state *executionState) {
	nouns := make(map[string]*cobra.Command)
	for _, operation := range a.deps.Catalog.Operations() {
		noun := nouns[operation.Noun]
		if noun == nil {
			noun = &cobra.Command{Use: operation.Noun, Short: "Use FreeAgent " + humanCommand(operation.Noun), Args: noArgs, RunE: showHelpOrUsage(state, "an operation verb is required")}
			nouns[operation.Noun] = noun
			root.AddCommand(noun)
		}
		noun.AddCommand(a.newOperationCommand(state, operation))
	}
}

func (a *App) newOperationCommand(state *executionState, operation catalog.Operation) *cobra.Command {
	options := &operationOptions{page: 1, perPage: 25, maxPages: 10, maxItems: 1000, maxBytes: 64 << 20}
	command := &cobra.Command{
		Use:   operation.Verb,
		Short: operation.Summary,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.executeOperation(cmd, state, operation, options)
		},
	}
	for _, parameter := range operation.PathParameters {
		command.Flags().String(parameter.Flag, "", parameter.Description)
	}
	for _, parameter := range operation.QueryParameters {
		command.Flags().String(parameter.Flag, "", parameter.Description+" (FreeAgent query parameter)")
	}

	command.Flags().StringArrayVar(&options.queries, "query", nil, "Query parameter as NAME=VALUE (repeatable)")
	if operation.BodyAllowed {
		command.Flags().StringVar(&options.input, "input", "", "JSON request body file, or - for stdin")
	}
	if operation.Mutation() {
		command.Flags().BoolVar(&options.dryRun, "dry-run", false, "Validate locally and emit a no-network mutation plan")
		command.Flags().StringVar(&options.confirm, "confirm", "", "Short-lived confirmation token from --dry-run")
	}
	if operation.Paginated {
		command.Flags().IntVar(&options.page, "page", 1, "FreeAgent page number")
		command.Flags().IntVar(&options.perPage, "per-page", 25, "Records per page (normally 1-100; inspect capabilities for exceptions)")
		command.Flags().BoolVar(&options.all, "all", false, "Read pages until complete or a configured bound is reached")
		command.Flags().IntVar(&options.maxPages, "max-pages", 10, "Maximum pages when --all is set")
		command.Flags().IntVar(&options.maxItems, "max-items", 1000, "Maximum records when --all is set; later pages are consumed only in full")
		command.Flags().Int64Var(&options.maxBytes, "max-bytes", 64<<20, "Maximum cumulative response bytes when --all is set")
	}
	return command
}

// flagValues exists because pflag writes into pointers. It is populated from
// the command after parsing instead of retaining pointers in the catalogue.
func collectPathValues(cmd *cobra.Command, operation catalog.Operation) (map[string]string, error) {
	values := make(map[string]string, len(operation.PathParameters))
	for _, parameter := range operation.PathParameters {
		value, err := cmd.Flags().GetString(parameter.Flag)
		if err != nil {
			return nil, protocol.InvariantError(err)
		}
		if strings.TrimSpace(value) == "" {
			return nil, protocol.UsageError("--%s is required", parameter.Flag)
		}
		values[parameter.Name] = value
	}
	return values, nil
}

func (a *App) executeOperation(cmd *cobra.Command, state *executionState, operation catalog.Operation, options *operationOptions) error {
	profile, err := state.resolveProfile(cmd.Context())
	if err != nil {
		return err
	}
	if operation.PracticeOnly && state.global.subdomain != "" {
		return protocol.UsageError("--subdomain selects a client company and cannot be used with practice-level operations")
	}
	pathValues, err := collectPathValues(cmd, operation)
	if err != nil {
		return err
	}
	path, err := resolvePath(operation.Path, pathValues)
	if err != nil {
		return err
	}
	query, err := parseQuery(options.queries)
	if err != nil {
		return err
	}
	query, err = collectQueryValues(cmd, operation, query)
	if err != nil {
		return err
	}
	body, err := readRequestBody(a.deps.Stdin, options.input)
	if err != nil {
		return err
	}
	if operation.BodyRequired && len(body) == 0 {
		return protocol.UsageError("--input is required for %s", operation.ID)
	}
	if !operation.BodyAllowed && len(body) > 0 {
		return protocol.UsageError("%s does not accept --input", operation.ID)
	}
	if len(body) > 0 && len(operation.BodyRootKeys) > 0 {
		if err := validateBodyRootKeys(body, operation); err != nil {
			return err
		}
	}
	if !operation.Mutation() && (options.dryRun || options.confirm != "") {
		return protocol.UsageError("--dry-run and --confirm are only valid for mutations")
	}

	if operation.Paginated {
		return a.executePaginated(cmd, state, profile, operation, path, query, options)
	}
	request := APIRequest{Method: operation.Method, Path: path, Query: query, Body: body, Mutation: operation.Mutation(), PracticeSubdomain: state.global.subdomain, APIVersion: profile.APIVersion}
	return a.executeOne(cmd.Context(), state, profile, operation, request, options)
}

func (a *App) executeOne(ctx context.Context, state *executionState, profile Profile, operation catalog.Operation, request APIRequest, options *operationOptions) error {
	meta := profileMeta(profile, operation.ID, state.global.subdomain)
	// Assign request context before the network call so every failure envelope,
	// including an unknown mutation outcome, identifies the selected context.
	state.meta = meta
	var reconciliation *protocol.Reconciliation
	if operation.Mutation() {
		reconciliation = requestReconciliation(request)
		plan, err := protocol.NewMutationPlan(operation.ID, profile.Name, profile.Environment, state.global.subdomain, profile.APIVersion, request.Method, request.Path, request.Query, request.Body)
		if err != nil {
			return protocol.UsageError("the request body must contain one valid JSON value: %s", err)
		}
		token, err := protocol.ConfirmationTokenAt(plan, a.deps.Now())
		if err != nil {
			return protocol.InvariantError(err)
		}
		if options.dryRun {
			return state.emit(protocol.DryRunResult{Plan: plan, ConfirmationToken: token}, meta)
		}
		if state.global.readOnly || profile.ReadOnly {
			return protocol.SafetyError("the active read-only policy rejects %s", operation.ID)
		}
		if options.confirm == "" {
			return protocol.SafetyError("%s requires a current --confirm token from --dry-run", operation.ID)
		}
		if err := protocol.VerifyConfirmationTokenAt(plan, options.confirm, a.deps.Now(), protocol.DefaultConfirmationMaxAge); err != nil {
			return err
		}
	}

	response, err := a.callAPI(ctx, state, profile.Name, request)
	if err != nil {
		if operation.Mutation() {
			var commandErr *protocol.CommandError
			if errors.As(err, &commandErr) {
				if commandErr.Outcome == "unknown" || commandErr.Code == protocol.ExitOutcomeUnknown {
					commandErr.Reconciliation = reconciliation
				}
				return commandErr
			}
			return &protocol.CommandError{Code: protocol.ExitOutcomeUnknown, Kind: "mutation_outcome_unknown", Message: "the connection ended before the mutation outcome was known; inspect current FreeAgent state before any repeat", Outcome: "unknown", Reconciliation: reconciliation, Cause: err}
		}
		return protocol.AsCommandError(err)
	}
	if err := responseError(response, a.deps.Now()); err != nil {
		return err
	}
	data, err := responseData(response.Body, operation.ResponseKey, operation.OutputKind, state.global.format)
	if err != nil {
		if operation.Mutation() {
			return mutationAppliedOutputError(err)
		}
		return err
	}
	if err := state.emit(data, meta); err != nil {
		if operation.Mutation() {
			return mutationAppliedOutputError(err)
		}
		return err
	}
	return nil
}

func requestReconciliation(request APIRequest) *protocol.Reconciliation {
	queryDigest := sha256.Sum256([]byte(request.Query.Encode()))
	bodyDigest := sha256.Sum256(request.Body)
	return &protocol.Reconciliation{
		Method:      strings.ToUpper(request.Method),
		Path:        request.Path,
		QuerySHA256: fmt.Sprintf("%x", queryDigest),
		BodySHA256:  fmt.Sprintf("%x", bodyDigest),
	}
}

func (a *App) callAPI(parent context.Context, state *executionState, profile string, request APIRequest) (APIResponse, error) {
	if a.deps.API == nil {
		return APIResponse{}, unavailable("FreeAgent API")
	}
	requestContext, cancel := state.requestContext(parent)
	defer cancel()
	return a.deps.API.Do(requestContext, profile, request)
}

func (a *App) executePaginated(cmd *cobra.Command, state *executionState, profile Profile, operation catalog.Operation, path string, baseQuery url.Values, options *operationOptions) error {
	if options.page < 1 {
		return protocol.UsageError("--page must be at least 1")
	}
	maximumPerPage := operationMaxPerPage(operation, baseQuery)
	if options.perPage < 1 || options.perPage > maximumPerPage {
		return protocol.UsageError("--per-page must be between 1 and %d for this request", maximumPerPage)
	}
	if _, exists := baseQuery["page"]; exists {
		return protocol.UsageError("use --page instead of --query page=...")
	}
	if _, exists := baseQuery["per_page"]; exists {
		return protocol.UsageError("use --per-page instead of --query per_page=...")
	}
	if options.all && state.global.format == output.FormatRaw {
		return protocol.UsageError("--all cannot preserve a single upstream body in raw output; use json or jsonl")
	}
	if options.all {
		if !cmd.Flags().Changed("per-page") {
			options.perPage = 100
		}
		if options.maxPages < 1 || options.maxPages > 1000 {
			return protocol.UsageError("--max-pages must be between 1 and 1000")
		}
		if options.maxItems < 1 || options.maxItems > 100000 {
			return protocol.UsageError("--max-items must be between 1 and 100000")
		}
		if options.maxBytes < 1 || options.maxBytes > 1<<30 {
			return protocol.UsageError("--max-bytes must be between 1 and 1073741824")
		}
		if options.maxItems < options.perPage {
			options.perPage = options.maxItems
		}
	}

	page := options.page
	pagesRead := 0
	items := make([]any, 0)
	total := 0
	complete := false
	nextPage := page
	var bytesRead int64
	var singleBody []byte
	for {
		if options.all && pagesRead > 0 && len(items)+options.perPage > options.maxItems {
			nextPage = page
			break
		}
		query := cloneQuery(baseQuery)
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", strconv.Itoa(options.perPage))
		request := APIRequest{Method: "GET", Path: path, Query: query, Mutation: false, PracticeSubdomain: state.global.subdomain, APIVersion: profile.APIVersion}
		response, err := a.callAPI(cmd.Context(), state, profile.Name, request)
		if err != nil {
			return protocol.AsCommandError(err)
		}
		if err := responseError(response, a.deps.Now()); err != nil {
			return err
		}
		if options.all && bytesRead+int64(len(response.Body)) > options.maxBytes {
			nextPage = page
			break
		}
		singleBody = response.Body
		pageItems, err := responseItems(response.Body, operation.ResponseKey)
		if err != nil {
			return err
		}
		if len(pageItems) > options.perPage {
			return &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "unexpected_response", Message: "FreeAgent returned more records than the requested page size"}
		}
		if count, err := strconv.Atoi(response.Header.Get("X-Total-Count")); err == nil && count >= 0 {
			total = count
		}
		pagesRead++
		bytesRead += int64(len(response.Body))
		if !options.all {
			items = pageItems
			break
		}
		items = append(items, pageItems...)
		if len(pageItems) < options.perPage || total > 0 && len(items) >= total {
			complete = true
			break
		}
		if len(items) >= options.maxItems || pagesRead >= options.maxPages {
			nextPage = page + 1
			break
		}
		page++
		nextPage = page
	}

	meta := profileMeta(profile, operation.ID, state.global.subdomain)
	meta.Page = options.page
	meta.PerPage = options.perPage
	meta.TotalCount = total
	meta.Items = len(items)
	meta.BytesRead = bytesRead
	if !options.all {
		if state.global.format == output.FormatRaw {
			return state.emit(singleBody, meta)
		}
		return state.emit(items, meta)
	}
	meta.Complete = &complete
	if !complete {
		meta.NextPage = nextPage
	}
	if err := state.emit(items, meta); err != nil {
		return err
	}
	if !complete {
		return &protocol.CommandError{Code: protocol.ExitPartial, Kind: "partial_result", Message: "pagination stopped at the configured bound; use next_page to continue", Retryable: true, Outcome: "not_applied"}
	}
	return nil
}

func resolvePath(template string, values map[string]string) (string, error) {
	path := template
	for name, value := range values {
		if value == "" {
			return "", protocol.UsageError("the %s target is empty", name)
		}
		escaped := url.PathEscape(value)
		if strings.ContainsAny(value, "/\\") || unsafeEncodedPath(escaped) {
			return "", protocol.SafetyError("the %s target contains an unsafe path separator or dot segment", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", escaped)
	}
	if strings.ContainsAny(path, "{}") {
		return "", protocol.InvariantError(fmt.Errorf("unresolved path template %q", path))
	}
	return path, nil
}

func parseQuery(entries []string) (url.Values, error) {
	values := make(url.Values)
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		if !found || strings.TrimSpace(name) == "" {
			return nil, protocol.UsageError("--query values must use NAME=VALUE")
		}
		values.Add(name, value)
	}
	return values, nil
}

func collectQueryValues(cmd *cobra.Command, operation catalog.Operation, query url.Values) (url.Values, error) {
	result := cloneQuery(query)
	for _, parameter := range operation.QueryParameters {
		if cmd.Flags().Changed(parameter.Flag) {
			if _, exists := result[parameter.Name]; exists {
				return nil, protocol.UsageError("use either --%s or --query %s=..., not both", parameter.Flag, parameter.Name)
			}
			value, err := cmd.Flags().GetString(parameter.Flag)
			if err != nil {
				return nil, protocol.InvariantError(err)
			}
			result.Add(parameter.Name, value)
		}
		values := result[parameter.Name]
		if parameter.Required && len(values) == 0 {
			return nil, protocol.UsageError("--%s is required", parameter.Flag)
		}
		if len(values) > 1 {
			return nil, protocol.UsageError("query parameter %s must be supplied once", parameter.Name)
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return nil, protocol.UsageError("query parameter %s must not be empty", parameter.Name)
			}
			if err := validateQueryValue(parameter, value); err != nil {
				return nil, err
			}
		}
	}
	for _, constraint := range operation.QueryConstraints {
		switch constraint.Kind {
		case catalog.QueryConstraintExactlyOne:
			present := 0
			for _, name := range constraint.Parameters {
				if len(result[name]) > 0 {
					present++
				}
			}
			if present != 1 {
				return nil, protocol.UsageError("exactly one of the query parameters %s is required", strings.Join(constraint.Parameters, ", "))
			}
		default:
			return nil, protocol.InvariantError(fmt.Errorf("unsupported query constraint %q", constraint.Kind))
		}
	}
	return result, nil
}

func validateQueryValue(parameter catalog.Parameter, value string) error {
	switch parameter.Type {
	case catalog.TypeString:
		return nil
	case catalog.TypeInteger:
		if _, err := strconv.Atoi(value); err != nil {
			return protocol.UsageError("query parameter %s must be an integer", parameter.Name)
		}
	case catalog.TypeBoolean:
		if _, err := strconv.ParseBool(value); err != nil {
			return protocol.UsageError("query parameter %s must be a boolean", parameter.Name)
		}
	default:
		return protocol.InvariantError(fmt.Errorf("unsupported query parameter type %q", parameter.Type))
	}
	return nil
}

func validateBodyRootKeys(body []byte, operation catalog.Operation) error {
	value, err := decodeJSON(body)
	if err != nil {
		return protocol.UsageError("--input must contain one valid JSON value: %s", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return protocol.UsageError("--input for %s must be a JSON object", operation.ID)
	}
	for _, key := range operation.BodyRootKeys {
		if _, exists := object[key]; !exists {
			return protocol.UsageError("--input for %s must contain the top-level key %q", operation.ID, key)
		}
	}
	return nil
}

func operationMaxPerPage(operation catalog.Operation, query url.Values) int {
	const standardMaximum = 100
	if operation.MaxPerPage <= standardMaximum {
		return standardMaximum
	}
	if strings.EqualFold(query.Get("minimal_data"), "true") {
		return operation.MaxPerPage
	}
	return standardMaximum
}

func cloneQuery(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func readRequestBody(stdin io.Reader, source string) ([]byte, error) {
	if source == "" {
		return nil, nil
	}
	var reader io.Reader
	var closeReader io.Closer
	if source == "-" {
		reader = stdin
	} else {
		file, err := os.Open(source)
		if err != nil {
			return nil, &protocol.CommandError{Code: protocol.ExitUsage, Kind: "input_error", Message: "the JSON input file could not be opened", Cause: err}
		}
		reader = file
		closeReader = file
	}
	if closeReader != nil {
		defer func() { _ = closeReader.Close() }()
	}
	var buffer bytes.Buffer
	if _, err := io.CopyN(&buffer, reader, maxInputBytes+1); err != nil && err != io.EOF {
		return nil, &protocol.CommandError{Code: protocol.ExitUsage, Kind: "input_error", Message: "the JSON input could not be read", Cause: err}
	}
	if buffer.Len() > maxInputBytes {
		return nil, protocol.UsageError("the JSON input exceeds %d bytes", maxInputBytes)
	}
	return buffer.Bytes(), nil
}

func responseError(response APIResponse, now time.Time) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	requestID := response.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = response.Header.Get("X-Request-Id")
	}
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), now)
	details := redactedDetails(response.Body)
	return protocol.HTTPError(response.StatusCode, "FreeAgent rejected the request", requestID, retryAfter, details)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func redactedDetails(body []byte) json.RawMessage {
	value, err := decodeJSON(body)
	if err != nil {
		return json.RawMessage(`{"omitted":"non_json_error_body"}`)
	}
	redactValue(value)
	encoded, _ := json.Marshal(value)
	if len(encoded) > maxErrorBytes {
		return json.RawMessage(`{"omitted":"error_details_exceed_limit"}`)
	}
	return encoded
}

func redactValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") {
				typed[key] = "[REDACTED]"
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range typed {
			redactValue(child)
		}
	}
}

func responseData(body []byte, responseKey string, outputKind catalog.OutputKind, format output.Format) (any, error) {
	if format == output.FormatRaw {
		return body, nil
	}
	if len(body) == 0 {
		if outputKind == catalog.OutputNull {
			return nil, nil
		}
		return nil, &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "unexpected_response", Message: "FreeAgent returned an empty body for a catalogued response"}
	}
	value, err := decodeJSON(body)
	if err != nil {
		return nil, &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "unexpected_response", Message: "FreeAgent returned a non-JSON response; inspect it with --output raw", Cause: err}
	}
	if responseKey != "" {
		if object, ok := value.(map[string]any); ok {
			if selected, exists := object[responseKey]; exists {
				value = selected
			} else {
				return nil, &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "unexpected_response", Message: "FreeAgent returned JSON without the catalogued response field"}
			}
		} else {
			return nil, &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "unexpected_response", Message: "FreeAgent returned a non-object response for a catalogued response field"}
		}
	}
	if !matchesOutputKind(value, outputKind) {
		return nil, &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "unexpected_response", Message: "FreeAgent returned a JSON value with an unexpected catalogued type"}
	}
	return value, nil
}

func matchesOutputKind(value any, kind catalog.OutputKind) bool {
	switch kind {
	case "", catalog.OutputObject:
		_, ok := value.(map[string]any)
		return ok
	case catalog.OutputArray:
		_, ok := value.([]any)
		return ok
	case catalog.OutputString:
		_, ok := value.(string)
		return ok
	case catalog.OutputNull:
		return value == nil
	case catalog.OutputRaw:
		return true
	default:
		return false
	}
}

func mutationAppliedOutputError(cause error) *protocol.CommandError {
	return &protocol.CommandError{
		Code: protocol.ExitInvariant, Kind: "mutation_applied_output_error",
		Message:   "FreeAgent accepted the mutation, but its result could not be emitted; reconcile current state and do not repeat the mutation",
		Retryable: false, Outcome: "applied", Cause: cause,
	}
}

func responseItems(body []byte, responseKey string) ([]any, error) {
	data, err := responseData(body, responseKey, catalog.OutputArray, output.FormatJSON)
	if err != nil {
		return nil, err
	}
	items, ok := data.([]any)
	if !ok {
		return nil, &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "unexpected_response", Message: "the paginated FreeAgent response did not contain the expected array"}
	}
	return items, nil
}

func decodeJSON(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func humanCommand(value string) string { return strings.ReplaceAll(value, "-", " ") }
