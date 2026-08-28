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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

var fixedNow = time.Unix(1_800_000_000, 0).UTC()

type fakeProfiles struct {
	profile Profile
}

func (f *fakeProfiles) List(context.Context) ([]Profile, error) { return []Profile{f.profile}, nil }
func (f *fakeProfiles) Resolve(_ context.Context, name string) (Profile, error) {
	if name != "" && name != f.profile.Name {
		return Profile{}, errors.New("not found")
	}
	return f.profile, nil
}
func (f *fakeProfiles) Upsert(_ context.Context, input ProfileInput) (Profile, error) {
	f.profile = Profile{Name: input.Name, Environment: input.Environment, ClientID: input.ClientID, RedirectURI: input.RedirectURI, APIVersion: input.APIVersion, ReadOnly: input.ReadOnly}
	return f.profile, nil
}
func (f *fakeProfiles) SetDefault(context.Context, string) error { return nil }
func (f *fakeProfiles) Remove(context.Context, string) error     { return nil }

type fakeAPI struct {
	requests []APIRequest
	do       func(APIRequest) (APIResponse, error)
}

type fakeAuth struct {
	logoutProfiles []string
}

func (*fakeAuth) Login(context.Context, string, LoginOptions) (AuthStatus, error) {
	return AuthStatus{}, nil
}
func (*fakeAuth) Status(context.Context, string) (AuthStatus, error) { return AuthStatus{}, nil }
func (f *fakeAuth) Logout(_ context.Context, profile string) error {
	f.logoutProfiles = append(f.logoutProfiles, profile)
	return nil
}
func (*fakeAuth) SetClientSecret(context.Context, string, []byte) error { return nil }
func (*fakeAuth) DeleteClientSecret(context.Context, string) error      { return nil }

func (f *fakeAPI) Do(_ context.Context, _ string, request APIRequest) (APIResponse, error) {
	f.requests = append(f.requests, request)
	if f.do != nil {
		return f.do(request)
	}
	return APIResponse{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{}`)}, nil
}

func sandboxProfile() Profile {
	return Profile{Name: "sandbox", Environment: "sandbox", BaseURL: "https://api.sandbox.freeagent.com/v2", APIVersion: "2024-10-01"}
}

func runCLI(t *testing.T, stdin string, profiles ProfileService, api APIService, args ...string) (int, []byte, []byte) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := New(Dependencies{Profiles: profiles, API: api, Build: BuildInfo{Version: "test", Go: "go-test"}, Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr, Now: func() time.Time { return fixedNow }})
	code := app.Execute(context.Background(), args)
	return code, stdout.Bytes(), stderr.Bytes()
}

func decodeEnvelope(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode envelope %q: %v", raw, err)
	}
	return value
}

func TestAgentFreeAgentOperationRequiresExplicitProfile(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api, "--agent", "contacts", "list")
	if code != int(protocol.ExitUsage) {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	if len(stdout) != 0 {
		t.Fatalf("error wrote stdout: %s", stdout)
	}
	errorEnvelope := decodeEnvelope(t, stderr)
	if errorEnvelope["protocol_version"] != "1" {
		t.Fatalf("missing protocol version: %s", stderr)
	}
	if len(api.requests) != 0 {
		t.Fatal("API was called without an explicit agent profile")
	}
}

func TestDryRunHasNoNetworkAndTokenAppliesExactMutation(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	dryAPI := &fakeAPI{}
	body := `{"contact":{"first_name":"Ada"}}`
	code, stdout, stderr := runCLI(t, body, profiles, dryAPI, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", code, stderr)
	}
	if len(dryAPI.requests) != 0 {
		t.Fatal("dry-run made a network request")
	}
	envelope := decodeEnvelope(t, stdout)
	data := envelope["data"].(map[string]any)
	token := data["confirmation_token"].(string)

	applyAPI := &fakeAPI{do: func(request APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusCreated, Header: make(http.Header), Body: []byte(`{"contact":{"url":"https://api.sandbox.freeagent.com/v2/contacts/1"}}`)}, nil
	}}
	code, _, stderr = runCLI(t, body, profiles, applyAPI, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--confirm", token)
	if code != 0 {
		t.Fatalf("apply exit=%d stderr=%s", code, stderr)
	}
	if len(applyAPI.requests) != 1 || !applyAPI.requests[0].Mutation || applyAPI.requests[0].Path != "/v2/contacts" {
		t.Fatalf("unexpected apply requests: %+v", applyAPI.requests)
	}
}

func TestReadOnlyProfileBlocksConfirmedMutation(t *testing.T) {
	t.Parallel()
	baseProfile := sandboxProfile()
	profiles := &fakeProfiles{profile: baseProfile}
	body := `{"contact":{"first_name":"Ada"}}`
	code, stdout, stderr := runCLI(t, body, profiles, &fakeAPI{}, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", code, stderr)
	}
	token := decodeEnvelope(t, stdout)["data"].(map[string]any)["confirmation_token"].(string)
	baseProfile.ReadOnly = true
	profiles.profile = baseProfile
	api := &fakeAPI{}
	code, stdout, stderr = runCLI(t, body, profiles, api, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--confirm", token)
	if code != int(protocol.ExitSafety) || len(stdout) != 0 || len(api.requests) != 0 {
		t.Fatalf("read-only result code=%d stdout=%s stderr=%s calls=%d", code, stdout, stderr, len(api.requests))
	}
}

func TestRawOutputPreservesUpstreamBinary(t *testing.T) {
	t.Parallel()
	want := []byte{0x25, 0x50, 0x44, 0x46, 0x00, 0xff}
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusOK, Header: make(http.Header), Body: want}, nil
	}}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api, "--profile", "sandbox", "--output", "raw", "api", "GET", "/v2/invoices/1/pdf")
	if code != 0 || !bytes.Equal(stdout, want) || len(stderr) != 0 {
		t.Fatalf("code=%d stdout=%v stderr=%s", code, stdout, stderr)
	}
}

func TestRawAPIRejectsAbsoluteAndTraversalPaths(t *testing.T) {
	t.Parallel()
	for _, unsafe := range []string{
		"https://evil.example/v2/company", "//evil.example/v2/company", "/v2/../token_endpoint",
		"/v2/%2e%2e/company", "/v2/%252e%252e/company", "/v2/a%252fb",
		"/v2/token_endpoint", "/v2/%74oken_endpoint", "/v2/%2574oken_endpoint",
		"/v2/approve_app", "/v2/%61pprove_app", "/v2/%2561pprove_app",
	} {
		api := &fakeAPI{}
		code, stdout, _ := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api, "--agent", "--profile", "sandbox", "api", "GET", unsafe)
		if code != int(protocol.ExitSafety) || len(stdout) != 0 || len(api.requests) != 0 {
			t.Errorf("path %q: code=%d calls=%d stdout=%s", unsafe, code, len(api.requests), stdout)
		}
	}
}

func TestBoundedPaginationReturnsPartialWithoutJSONLSummary(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{do: func(request APIRequest) (APIResponse, error) {
		page, _ := strconv.Atoi(request.Query.Get("page"))
		body := `{"contacts":[{"id":"` + strconv.Itoa(page*2-1) + `"},{"id":"` + strconv.Itoa(page*2) + `"}]}`
		return APIResponse{StatusCode: http.StatusOK, Header: http.Header{"X-Total-Count": {"10"}}, Body: []byte(body)}, nil
	}}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api,
		"--agent", "--profile", "sandbox", "--output", "jsonl", "contacts", "list", "--all", "--per-page", "2", "--max-pages", "2", "--max-items", "10")
	if code != int(protocol.ExitPartial) {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if len(api.requests) != 2 || strings.Contains(string(stdout), `"type":"summary"`) || strings.Count(string(stdout), `"type":"item"`) != 4 {
		t.Fatalf("calls=%d stdout=%s", len(api.requests), stdout)
	}
	errorEnvelope := decodeEnvelope(t, stderr)
	if errorEnvelope["error"].(map[string]any)["kind"] != "partial_result" {
		t.Fatalf("unexpected partial error: %s", stderr)
	}
}

func TestDocumentedRequiredQueryIsValidatedBeforeNetwork(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	missingAPI := &fakeAPI{}
	code, stdout, stderr := runCLI(t, "", profiles, missingAPI, "--agent", "--profile", "sandbox", "bank-transactions", "list")
	if code != int(protocol.ExitUsage) || len(stdout) != 0 || len(missingAPI.requests) != 0 {
		t.Fatalf("missing query code=%d calls=%d stdout=%s stderr=%s", code, len(missingAPI.requests), stdout, stderr)
	}

	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"bank_transactions":[]}`)}, nil
	}}
	account := "https://api.sandbox.freeagent.com/v2/bank_accounts/1"
	code, _, stderr = runCLI(t, "", profiles, api, "--agent", "--profile", "sandbox", "bank-transactions", "list", "--bank-account", account)
	if code != 0 || len(api.requests) != 1 || api.requests[0].Query.Get("bank_account") != account {
		t.Fatalf("declared query code=%d calls=%d stderr=%s request=%+v", code, len(api.requests), stderr, api.requests)
	}
}

func TestExactlyOneDocumentedQueryIsRequired(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	body := `{"note":{"content":"test"}}`
	for _, extra := range [][]string{
		nil,
		{"--contact", "https://api.sandbox.freeagent.com/v2/contacts/1", "--project", "https://api.sandbox.freeagent.com/v2/projects/1"},
	} {
		api := &fakeAPI{}
		args := []string{"--agent", "--profile", "sandbox", "notes", "create", "--input", "-", "--dry-run"}
		args = append(args, extra...)
		code, stdout, _ := runCLI(t, body, profiles, api, args...)
		if code != int(protocol.ExitUsage) || len(stdout) != 0 || len(api.requests) != 0 {
			t.Errorf("extra=%v code=%d calls=%d stdout=%s", extra, code, len(api.requests), stdout)
		}
	}
	code, _, stderr := runCLI(t, body, profiles, &fakeAPI{}, "--agent", "--profile", "sandbox", "notes", "create", "--input", "-", "--contact", "https://api.sandbox.freeagent.com/v2/contacts/1", "--dry-run")
	if code != 0 {
		t.Fatalf("one contextual query failed: code=%d stderr=%s", code, stderr)
	}
}

func TestPracticeMinimalDataAllowsDocumentedPageSize(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	withoutMinimal := &fakeAPI{}
	code, _, _ := runCLI(t, "", profiles, withoutMinimal, "--agent", "--profile", "sandbox", "practice-clients", "list", "--per-page", "500")
	if code != int(protocol.ExitUsage) || len(withoutMinimal.requests) != 0 {
		t.Fatalf("page size without minimal_data code=%d calls=%d", code, len(withoutMinimal.requests))
	}
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"clients":[]}`)}, nil
	}}
	code, _, stderr := runCLI(t, "", profiles, api, "--agent", "--profile", "sandbox", "practice-clients", "list", "--minimal-data", "true", "--per-page", "500")
	if code != 0 || len(api.requests) != 1 || api.requests[0].Query.Get("minimal_data") != "true" {
		t.Fatalf("minimal page code=%d calls=%d stderr=%s", code, len(api.requests), stderr)
	}
}

func TestMissingCataloguedResponseFieldIsInvariant(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{}`)}, nil
	}}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api, "--agent", "--profile", "sandbox", "contacts", "get", "--id", "1")
	if code != int(protocol.ExitInvariant) || len(stdout) != 0 || !strings.Contains(string(stderr), `"kind":"unexpected_response"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestMaxItemsNeverCutsThroughFetchedPage(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{do: func(request APIRequest) (APIResponse, error) {
		items := make([]map[string]string, 100)
		for index := range items {
			items[index] = map[string]string{"id": strconv.Itoa(index + 1)}
		}
		body, err := json.Marshal(map[string]any{"contacts": items})
		if err != nil {
			t.Fatal(err)
		}
		return APIResponse{StatusCode: http.StatusOK, Header: http.Header{"X-Total-Count": {"300"}}, Body: body}, nil
	}}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api,
		"--agent", "--profile", "sandbox", "contacts", "list", "--all", "--per-page", "100", "--max-pages", "10", "--max-items", "150")
	if code != int(protocol.ExitPartial) {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if len(api.requests) != 1 {
		t.Fatalf("fetched a page that could not be emitted intact: %d requests", len(api.requests))
	}
	envelope := decodeEnvelope(t, stdout)
	if len(envelope["data"].([]any)) != 100 {
		t.Fatalf("unexpected item count: %s", stdout)
	}
	meta := envelope["meta"].(map[string]any)
	if meta["next_page"] != json.Number("2") {
		t.Fatalf("resume would skip a page: %s", stdout)
	}
}

func TestPaginationRejectsUpstreamPageLargerThanRequestedBound(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       []byte(`{"contacts":[{"id":"1"},{"id":"2"}]}`),
		}, nil
	}}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api,
		"--agent", "--profile", "sandbox", "contacts", "list", "--all", "--per-page", "1", "--max-items", "1")
	if code != int(protocol.ExitInvariant) || len(stdout) != 0 || !strings.Contains(string(stderr), `"kind":"unexpected_response"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestPaginationCumulativeByteBoundDoesNotConsumeNextPage(t *testing.T) {
	t.Parallel()
	body := []byte(`{"contacts":[{"id":"1"},{"id":"2"}]}`)
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusOK, Header: http.Header{"X-Total-Count": {"10"}}, Body: body}, nil
	}}
	maxBytes := strconv.Itoa(len(body) + 1)
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api,
		"--agent", "--profile", "sandbox", "contacts", "list", "--all", "--per-page", "2", "--max-pages", "10", "--max-items", "100", "--max-bytes", maxBytes)
	if code != int(protocol.ExitPartial) {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if len(api.requests) != 2 {
		t.Fatalf("expected the second page to be fetched then rejected intact, got %d calls", len(api.requests))
	}
	envelope := decodeEnvelope(t, stdout)
	if len(envelope["data"].([]any)) != 2 || envelope["meta"].(map[string]any)["next_page"] != json.Number("2") {
		t.Fatalf("byte bound consumed or skipped page 2: %s", stdout)
	}
}

func TestRequestCarriesExplicitVersionSubdomainAndEscapedTarget(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"contact":{"id":"1"}}`)}, nil
	}}
	code, _, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api,
		"--profile", "sandbox", "--api-version", "2026-10-01", "--subdomain", "client-a", "--output", "json", "contacts", "get", "--id", "a b")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	request := api.requests[0]
	if request.APIVersion != "2026-10-01" || request.PracticeSubdomain != "client-a" || request.Path != "/v2/contacts/a%20b" {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestTargetIDsRejectPathSeparatorsBeforeAPI(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"a/b", `a\b`, "..", "%2F"} {
		api := &fakeAPI{}
		code, stdout, _ := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api, "--agent", "--profile", "sandbox", "contacts", "get", "--id", id)
		if code != int(protocol.ExitSafety) || len(stdout) != 0 || len(api.requests) != 0 {
			t.Errorf("id=%q code=%d calls=%d stdout=%s", id, code, len(api.requests), stdout)
		}
	}
}

func TestMutationUnknownOutcomeUsesDedicatedExit(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	body := `{"invoice":{"reference":"x"}}`
	common := []string{"--agent", "--profile", "sandbox", "--subdomain", "client-a", "--api-version", "2026-10-01", "invoices", "create", "--query", "view=all", "--input", "-"}
	code, stdout, _ := runCLI(t, body, profiles, &fakeAPI{}, append(common, "--dry-run")...)
	if code != 0 {
		t.Fatal("dry-run failed")
	}
	token := decodeEnvelope(t, stdout)["data"].(map[string]any)["confirmation_token"].(string)
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{}, &protocol.CommandError{Code: protocol.ExitOutcomeUnknown, Kind: "mutation_outcome_unknown", Message: "unknown", RequestID: "request-unknown-1", Outcome: "unknown"}
	}}
	code, stdout, stderr := runCLI(t, body, profiles, api, append(common, "--confirm", token)...)
	if code != int(protocol.ExitOutcomeUnknown) || len(stdout) != 0 || !strings.Contains(string(stderr), `"outcome":"unknown"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	envelope := decodeEnvelope(t, stderr)
	errorDetail := envelope["error"].(map[string]any)
	if errorDetail["request_id"] != "request-unknown-1" {
		t.Fatalf("unknown outcome lost request ID: %s", stderr)
	}
	reconciliation := errorDetail["reconciliation"].(map[string]any)
	queryDigest := sha256.Sum256([]byte("view=all"))
	bodyDigest := sha256.Sum256([]byte(body))
	if reconciliation["method"] != "POST" || reconciliation["path"] != "/v2/invoices" || reconciliation["query_sha256"] != fmt.Sprintf("%x", queryDigest) || reconciliation["body_sha256"] != fmt.Sprintf("%x", bodyDigest) {
		t.Fatalf("unknown outcome lost exact request reconciliation data: %s", stderr)
	}
	if _, exists := reconciliation["body"]; exists || strings.Contains(string(stderr), `"reference":"x"`) || strings.Contains(string(stderr), "view=all") {
		t.Fatalf("unknown outcome exposed request body or query values: %s", stderr)
	}
	meta := envelope["meta"].(map[string]any)
	if meta["command"] != "invoices.create" || meta["profile"] != "sandbox" || meta["environment"] != "sandbox" || meta["practice_subdomain"] != "client-a" || meta["api_version"] != "2026-10-01" {
		t.Fatalf("unknown outcome lost execution context: %s", stderr)
	}
}

func TestCancellationPreservesDefinitiveMutationOutcome(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	body := `{"contact":{"first_name":"Ada"}}`
	code, stdout, stderr := runCLI(t, body, profiles, &fakeAPI{}, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run code=%d stderr=%s", code, stderr)
	}
	token := decodeEnvelope(t, stdout)["data"].(map[string]any)["confirmation_token"].(string)
	for _, test := range []struct {
		name    string
		outcome string
		code    protocol.ExitCode
	}{
		{name: "applied", outcome: "applied", code: protocol.ExitInvariant},
		{name: "unknown", outcome: "unknown", code: protocol.ExitTransient},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
				return APIResponse{}, &protocol.CommandError{Code: test.code, Kind: "definitive_test", Message: "definitive", Outcome: test.outcome}
			}}
			var stderr bytes.Buffer
			app := New(Dependencies{Profiles: profiles, API: api, Stdin: strings.NewReader(body), Stdout: io.Discard, Stderr: &stderr, Now: func() time.Time { return fixedNow }})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			got := app.Execute(ctx, []string{"--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--confirm", token})
			if got != int(test.code) || !strings.Contains(stderr.String(), `"outcome":"`+test.outcome+`"`) || strings.Contains(stderr.String(), `"kind":"interrupted"`) {
				t.Fatalf("code=%d stderr=%s", got, stderr.String())
			}
		})
	}
}

func TestMalformedMutationSuccessIsAppliedOutputError(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	body := `{"contact":{"first_name":"Ada"}}`
	code, stdout, _ := runCLI(t, body, profiles, &fakeAPI{}, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--dry-run")
	if code != 0 {
		t.Fatal("dry-run failed")
	}
	token := decodeEnvelope(t, stdout)["data"].(map[string]any)["confirmation_token"].(string)
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusCreated, Header: make(http.Header), Body: []byte(`not-json`)}, nil
	}}
	code, stdout, stderr := runCLI(t, body, profiles, api, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--confirm", token)
	if code != int(protocol.ExitInvariant) || len(stdout) != 0 || !strings.Contains(string(stderr), `"kind":"mutation_applied_output_error"`) || !strings.Contains(string(stderr), `"outcome":"applied"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestMutationStdoutFailureIsAppliedOutputError(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	body := `{"contact":{"first_name":"Ada"}}`
	code, stdout, _ := runCLI(t, body, profiles, &fakeAPI{}, "--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--dry-run")
	if code != 0 {
		t.Fatal("dry-run failed")
	}
	token := decodeEnvelope(t, stdout)["data"].(map[string]any)["confirmation_token"].(string)
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusCreated, Header: make(http.Header), Body: []byte(`{"contact":{"id":"1"}}`)}, nil
	}}
	var stderr bytes.Buffer
	app := New(Dependencies{Profiles: profiles, API: api, Build: BuildInfo{Version: "test"}, Stdin: strings.NewReader(body), Stdout: failingWriter{}, Stderr: &stderr, Now: func() time.Time { return fixedNow }})
	code = app.Execute(context.Background(), []string{"--agent", "--profile", "sandbox", "contacts", "create", "--input", "-", "--confirm", token})
	if code != int(protocol.ExitInvariant) || !strings.Contains(stderr.String(), `"kind":"mutation_applied_output_error"`) || !strings.Contains(stderr.String(), `"outcome":"applied"`) {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestPermissionDeniedUsesSafetyExit(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{do: func(APIRequest) (APIResponse, error) {
		return APIResponse{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: []byte(`{"errors":{"access":"denied"}}`)}, nil
	}}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, api, "--agent", "--profile", "sandbox", "company", "get")
	if code != int(protocol.ExitSafety) || len(stdout) != 0 || !strings.Contains(string(stderr), `"kind":"permission_denied"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestOversizeErrorRedactionNeverLeaksSecretFields(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(map[string]any{"access_token": "must-not-leak", "padding": strings.Repeat("x", maxErrorBytes+1024)})
	if err != nil {
		t.Fatal(err)
	}
	details := redactedDetails(body)
	if bytes.Contains(details, []byte("must-not-leak")) || bytes.Contains(details, []byte(strings.Repeat("x", 100))) {
		t.Fatalf("oversize details leaked body content: %s", details)
	}
	if !bytes.Contains(details, []byte("error_details_exceed_limit")) {
		t.Fatalf("oversize details did not use safe marker: %s", details)
	}
}

func TestErrorDetailsRejectTrailingJSON(t *testing.T) {
	t.Parallel()
	details := redactedDetails([]byte(`{"error":"first"} {"access_token":"must-not-leak"}`))
	if string(details) != `{"omitted":"non_json_error_body"}` || bytes.Contains(details, []byte("must-not-leak")) {
		t.Fatalf("trailing error data was accepted or exposed: %s", details)
	}
}

func TestArgumentAndMissingTargetErrorsUseUsageExit(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--agent", "version", "extra"}, {"--agent", "--profile", "sandbox", "contacts", "get"}} {
		code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, &fakeAPI{}, args...)
		if code != int(protocol.ExitUsage) || len(stdout) != 0 || !strings.Contains(string(stderr), `"kind":"invalid_input"`) {
			t.Errorf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
}

func TestUnknownCommandsAndInvalidFlagsUseUsageExit(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--agent", "does-not-exist"}, {"--agent", "contacts", "does-not-exist"}, {"--agent", "--not-a-flag"}} {
		code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, &fakeAPI{}, args...)
		if code != int(protocol.ExitUsage) || len(stdout) != 0 || !strings.Contains(string(stderr), `"kind":"invalid_input"`) {
			t.Errorf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
}

func TestAgentAndNoInputRequireACommandVerb(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--agent"}, {"--agent", "contacts"}, {"--no-input", "--output", "json"}, {"--no-input", "--output", "json", "contacts"}} {
		code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, &fakeAPI{}, args...)
		if code != int(protocol.ExitUsage) || len(stdout) != 0 || !strings.Contains(string(stderr), `"kind":"invalid_input"`) {
			t.Errorf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
}

func TestInvalidCalendarAPIVersionFailsBeforeDryRun(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	code, stdout, stderr := runCLI(t, `{"contact":{}}`, &fakeProfiles{profile: sandboxProfile()}, api,
		"--agent", "--profile", "sandbox", "--api-version", "2026-99-99", "contacts", "create", "--input", "-", "--dry-run")
	if code != int(protocol.ExitUsage) || len(stdout) != 0 || len(api.requests) != 0 || !strings.Contains(string(stderr), "valid calendar date") {
		t.Fatalf("code=%d stdout=%s stderr=%s calls=%d", code, stdout, stderr, len(api.requests))
	}
}

func TestInvalidPracticeSubdomainFailsBeforeDryRun(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	code, stdout, stderr := runCLI(t, `{"contact":{}}`, &fakeProfiles{profile: sandboxProfile()}, api,
		"--agent", "--profile", "sandbox", "--subdomain", "Bad.Context", "contacts", "create", "--input", "-", "--dry-run")
	if code != int(protocol.ExitUsage) || len(stdout) != 0 || len(api.requests) != 0 || !strings.Contains(string(stderr), "lowercase DNS label") {
		t.Fatalf("code=%d stdout=%s stderr=%s calls=%d", code, stdout, stderr, len(api.requests))
	}
}

func TestInputFileIsReadWithoutChangingIt(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "contact.json")
	body := []byte(`{"contact":{"first_name":"Ada"}}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "", &fakeProfiles{profile: sandboxProfile()}, &fakeAPI{}, "--agent", "--profile", "sandbox", "contacts", "create", "--input", path, "--dry-run")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, body) {
		t.Fatal("input file changed")
	}
}

func TestAuthLogoutRequiresExactInteractiveProfileOrForce(t *testing.T) {
	t.Parallel()
	profiles := &fakeProfiles{profile: sandboxProfile()}
	for _, test := range []struct {
		name      string
		stdin     string
		args      []string
		wantCode  int
		wantCalls int
	}{
		{"wrong interactive confirmation", "other\n", []string{"--profile", "sandbox", "auth", "logout"}, int(protocol.ExitSafety), 0},
		{"exact interactive confirmation", "sandbox\n", []string{"--profile", "sandbox", "auth", "logout"}, 0, 1},
		{"forced non-interactive", "", []string{"--no-input", "--profile", "sandbox", "auth", "logout", "--force", "--output", "json"}, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := &fakeAuth{}
			var stdout, stderr bytes.Buffer
			app := New(Dependencies{Profiles: profiles, Auth: auth, Stdin: strings.NewReader(test.stdin), Stdout: &stdout, Stderr: &stderr, Now: func() time.Time { return fixedNow }})
			code := app.Execute(context.Background(), test.args)
			if code != test.wantCode || len(auth.logoutProfiles) != test.wantCalls {
				t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, len(auth.logoutProfiles), stdout.String(), stderr.String())
			}
		})
	}
}

func TestProfileRemoveValidatesTargetBeforeCredentialEffects(t *testing.T) {
	t.Parallel()
	auth := &fakeAuth{}
	var stdout, stderr bytes.Buffer
	app := New(Dependencies{
		Profiles: &fakeProfiles{profile: sandboxProfile()}, Auth: auth,
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Now: func() time.Time { return fixedNow },
	})
	code := app.Execute(context.Background(), []string{"--agent", "profile", "remove", "../escape", "--force"})
	if code != int(protocol.ExitAuth) || len(auth.logoutProfiles) != 0 || len(stdout.Bytes()) != 0 {
		t.Fatalf("code=%d logout=%v stdout=%s stderr=%s", code, auth.logoutProfiles, stdout.Bytes(), stderr.Bytes())
	}
}
