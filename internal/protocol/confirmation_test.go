package protocol

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestConfirmationTokenBindsPlanAndExpires(t *testing.T) {
	t.Parallel()
	issued := time.Unix(1_800_000_000, 0).UTC()
	plan, err := NewMutationPlan("invoices.update", "prod", "production", "client-a", "2024-10-01", "PUT", "/v2/invoices/42", url.Values{"view": {"all"}}, []byte(`{"invoice":{"total":"10.00"}}`))
	if err != nil {
		t.Fatal(err)
	}
	token, err := ConfirmationTokenAt(plan, issued)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "fa1_1800000000_") {
		t.Fatalf("unexpected token format: %s", token)
	}
	if err := VerifyConfirmationTokenAt(plan, token, issued.Add(9*time.Minute), DefaultConfirmationMaxAge); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	changed := plan
	changed.PracticeSubdomain = "client-b"
	if err := VerifyConfirmationTokenAt(changed, token, issued.Add(time.Minute), DefaultConfirmationMaxAge); err == nil {
		t.Fatal("token did not bind the practice subdomain")
	}
	if err := VerifyConfirmationTokenAt(plan, token, issued.Add(11*time.Minute), DefaultConfirmationMaxAge); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired token, got %v", err)
	}
}

func TestConfirmationTokenBindsRepeatedQueryValueOrder(t *testing.T) {
	t.Parallel()
	issued := time.Unix(1_800_000_000, 0).UTC()
	first, err := NewMutationPlan("api.post", "sandbox", "sandbox", "", "2024-10-01", "POST", "/v2/example", url.Values{"value": {"first", "second"}}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	token, err := ConfirmationTokenAt(first, issued)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := NewMutationPlan("api.post", "sandbox", "sandbox", "", "2024-10-01", "POST", "/v2/example", url.Values{"value": {"second", "first"}}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyConfirmationTokenAt(reversed, token, issued.Add(time.Minute), DefaultConfirmationMaxAge); err == nil {
		t.Fatal("token accepted a request with reordered repeated query values")
	}
}

func TestConfirmationTokenAcceptsUnderscoreInBase64Digest(t *testing.T) {
	t.Parallel()
	plan, err := NewMutationPlan("api.post", "sandbox", "sandbox", "", "2024-10-01", "POST", "/v2/example", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var issued time.Time
	var token string
	for unix := int64(1_800_000_000); unix < 1_800_010_000; unix++ {
		issued = time.Unix(unix, 0).UTC()
		token, err = ConfirmationTokenAt(plan, issued)
		if err != nil {
			t.Fatal(err)
		}
		_, digest, _ := strings.Cut(strings.TrimPrefix(token, "fa1_"), "_")
		if strings.Contains(digest, "_") {
			break
		}
		token = ""
	}
	if token == "" {
		t.Fatal("test could not find deterministic digest containing underscore")
	}
	if err := VerifyConfirmationTokenAt(plan, token, issued.Add(time.Minute), DefaultConfirmationMaxAge); err != nil {
		t.Fatalf("valid base64url digest containing underscore was rejected: %v", err)
	}
}

func TestMutationPlanRejectsTrailingJSON(t *testing.T) {
	t.Parallel()
	_, err := NewMutationPlan("contacts.create", "sandbox", "sandbox", "", "2024-10-01", "POST", "/v2/contacts", nil, []byte(`{} {}`))
	if err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("expected trailing JSON error, got %v", err)
	}
}

func TestMutationPlanRejectsDuplicateJSONKeysRecursively(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"x":1,"x":2}`, `{"outer":{"x":1,"x":2}}`, `[{"x":1,"x":2}]`} {
		_, err := NewMutationPlan("api.post", "sandbox", "sandbox", "", "2024-10-01", "POST", "/v2/example", nil, []byte(body))
		if err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
			t.Errorf("body %s: expected duplicate-key error, got %v", body, err)
		}
	}
}

func TestHTTPErrorExitMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		code   ExitCode
		kind   string
	}{
		{http.StatusUnauthorized, ExitAuth, "authentication_required"},
		{http.StatusForbidden, ExitSafety, "permission_denied"},
		{http.StatusUnprocessableEntity, ExitRejected, "api_rejected"},
		{http.StatusTooManyRequests, ExitRateLimited, "rate_limited"},
		{http.StatusServiceUnavailable, ExitTransient, "upstream_unavailable"},
	}
	for _, test := range tests {
		err := HTTPError(test.status, "rejected", "request-1", 2*time.Second, json.RawMessage(`{"error":"x"}`))
		if err.Code != test.code || err.Kind != test.kind {
			t.Errorf("status %d: got code=%d kind=%s", test.status, err.Code, err.Kind)
		}
	}
}

func TestAsCommandErrorDoesNotExposeUnknownError(t *testing.T) {
	t.Parallel()
	err := AsCommandError(assertError("secret refresh_token=do-not-print"))
	if strings.Contains(err.Error(), "do-not-print") {
		t.Fatalf("public error exposed cause: %s", err)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
