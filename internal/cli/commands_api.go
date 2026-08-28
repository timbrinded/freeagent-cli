package cli

import (
	"net/url"
	pathpkg "path"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timbrinded/freeagent-cli/internal/catalog"
	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

func (a *App) newAPICommand(state *executionState) *cobra.Command {
	options := &operationOptions{}
	command := &cobra.Command{
		Use:   "api METHOD /v2/PATH",
		Short: "Make a guarded request to a documented relative FreeAgent API path",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			if method != "GET" && method != "POST" && method != "PUT" && method != "DELETE" {
				return protocol.UsageError("METHOD must be GET, POST, PUT, or DELETE")
			}
			parsed, err := safeRelativeAPIPath(args[1])
			if err != nil {
				return err
			}
			query, err := parseQuery(options.queries)
			if err != nil {
				return err
			}
			for key, values := range parsed.Query() {
				for _, value := range values {
					query.Add(key, value)
				}
			}
			body, err := readRequestBody(a.deps.Stdin, options.input)
			if err != nil {
				return err
			}
			mutation := method != "GET"
			if !mutation && len(body) > 0 {
				return protocol.UsageError("raw GET requests do not accept --input")
			}
			if !mutation && (options.dryRun || options.confirm != "") {
				return protocol.UsageError("--dry-run and --confirm are only valid for mutations")
			}
			profile, err := state.resolveProfile(cmd.Context())
			if err != nil {
				return err
			}
			risk := catalog.RiskRead
			if method == "DELETE" {
				risk = catalog.RiskDelete
			} else if mutation {
				risk = catalog.RiskWrite
			}
			operation := catalog.Operation{
				ID: "api." + strings.ToLower(method), Noun: "api", Verb: strings.ToLower(method),
				Summary: "Make a raw FreeAgent API request", Method: method, Path: parsed.Path,
				Risk: risk, BodyAllowed: method == "POST" || method == "PUT", PracticeSupported: true,
				DocumentationURL: "https://dev.freeagent.com/docs/introduction",
			}
			request := APIRequest{Method: method, Path: parsed.Path, Query: query, Body: body, Mutation: mutation, PracticeSubdomain: state.global.subdomain, APIVersion: profile.APIVersion}
			return a.executeOne(cmd.Context(), state, profile, operation, request, options)
		},
	}
	command.Flags().StringVar(&options.input, "input", "", "JSON request body file, or - for stdin")
	command.Flags().StringArrayVar(&options.queries, "query", nil, "Query parameter as NAME=VALUE (repeatable)")
	command.Flags().BoolVar(&options.dryRun, "dry-run", false, "Validate locally and emit a no-network mutation plan")
	command.Flags().StringVar(&options.confirm, "confirm", "", "Short-lived confirmation token from --dry-run")
	return command
}

func safeRelativeAPIPath(value string) (*url.URL, error) {
	if strings.HasPrefix(value, "//") {
		return nil, protocol.SafetyError("raw API paths must be relative to the configured FreeAgent origin")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, protocol.UsageError("invalid API path: %s", err)
	}
	if parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" {
		return nil, protocol.SafetyError("raw API paths must not contain an origin or credentials")
	}
	if parsed.Fragment != "" {
		return nil, protocol.UsageError("raw API paths must not contain a fragment")
	}
	if !strings.HasPrefix(parsed.Path, "/v2/") || reservedOAuthPath(parsed.EscapedPath()) {
		return nil, protocol.SafetyError("raw API paths must select a documented /v2 resource endpoint")
	}
	cleaned := pathpkg.Clean(parsed.Path)
	if cleaned != parsed.Path || unsafeEncodedPath(parsed.EscapedPath()) {
		return nil, protocol.SafetyError("raw API paths must not contain traversal segments")
	}
	parsed.Scheme = ""
	parsed.Host = ""
	return parsed, nil
}

func reservedOAuthPath(escaped string) bool {
	current := escaped
	for range 5 {
		decoded, err := url.PathUnescape(current)
		if err != nil {
			return false
		}
		if decoded == "/v2/token_endpoint" || decoded == "/v2/approve_app" {
			return true
		}
		if decoded == current {
			return false
		}
		current = decoded
	}
	return false
}

func unsafeEncodedPath(escaped string) bool {
	current := escaped
	for range 5 {
		lower := strings.ToLower(current)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(current, "\\") {
			return true
		}
		decoded, err := url.PathUnescape(current)
		if err != nil {
			return true
		}
		for _, segment := range strings.Split(decoded, "/") {
			if segment == "." || segment == ".." {
				return true
			}
		}
		if decoded == current {
			return false
		}
		current = decoded
	}
	return true
}
