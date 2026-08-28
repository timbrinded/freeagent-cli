package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/timbrinded/freeagent-cli/internal/credential"
)

func TestGenerateStateUsesCryptographicInput(t *testing.T) {
	t.Parallel()
	client := Client{Rand: strings.NewReader(strings.Repeat("a", 32))}
	state, err := client.GenerateState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 43 {
		t.Fatalf("state has %d characters, want 43", len(state))
	}
	if _, err := (&Client{Rand: strings.NewReader("short")}).GenerateState(); err == nil {
		t.Fatal("expected short random source to fail")
	}
}

func TestAuthorizationURLRequiresAndIncludesState(t *testing.T) {
	t.Parallel()
	client := Client{Endpoints: Endpoints{AuthorizationURL: "https://api.sandbox.freeagent.com/v2/approve_app", TokenURL: "https://api.sandbox.freeagent.com/v2/token_endpoint"}}
	credentials := Credentials{ClientID: "client", RedirectURI: "http://127.0.0.1:8484/callback"}
	if _, err := client.AuthorizationURL(credentials, ""); err == nil {
		t.Fatal("expected missing state error")
	}
	raw, err := client.AuthorizationURL(credentials, "state-value")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"response_type": "code",
		"client_id":     "client",
		"redirect_uri":  credentials.RedirectURI,
		"state":         "state-value",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestExchangeUsesBasicAuthAndFormEncoding(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s", request.Method)
		}
		if got := request.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", got)
		}
		username, password, ok := request.BasicAuth()
		if !ok || username != url.QueryEscape("client:id") || password != url.QueryEscape("secret value") {
			t.Errorf("Basic auth = %q, %q, %v", username, password, ok)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := request.Form.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q", got)
		}
		if got := request.Form.Get("code"); got != "short-code" {
			t.Errorf("code = %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"access_token":"access","token_type":"bearer","refresh_token":"refresh","expires_in":3600}`)
	}))
	defer server.Close()
	client := Client{Endpoints: Endpoints{AuthorizationURL: server.URL + "/approve", TokenURL: server.URL}, HTTPClient: server.Client(), Now: func() time.Time { return fixedNow }}
	token, err := client.Exchange(context.Background(), Credentials{
		ClientID: "client:id", ClientSecret: "secret value", RedirectURI: "http://127.0.0.1:8484/callback",
	}, "short-code")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || token.RefreshToken != "refresh" || !token.Expiry.Equal(fixedNow.Add(time.Hour)) {
		t.Fatalf("unexpected token: %#v", token)
	}
}

func TestRefreshRetainsOmittedRefreshToken(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := request.Form.Get("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh_token = %q", got)
		}
		_, _ = io.WriteString(response, `{"access_token":"new-access","token_type":"bearer","expires_in":"3600"}`)
	}))
	defer server.Close()
	client := Client{Endpoints: Endpoints{AuthorizationURL: server.URL + "/approve", TokenURL: server.URL}, HTTPClient: server.Client()}
	token, err := client.Refresh(context.Background(), Credentials{ClientID: "id", ClientSecret: "secret", RedirectURI: "http://127.0.0.1:8484/callback"}, "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if token.RefreshToken != "old-refresh" {
		t.Fatalf("refresh token = %q", token.RefreshToken)
	}
}

func TestTokenEndpointErrorIsStructured(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("X-Request-ID", "request-123")
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(response, `{"error":"invalid_grant","error_description":"expired code"}`)
	}))
	defer server.Close()
	client := Client{Endpoints: Endpoints{AuthorizationURL: server.URL + "/approve", TokenURL: server.URL}, HTTPClient: server.Client()}
	_, err := client.Exchange(context.Background(), Credentials{ClientID: "id", ClientSecret: "secret", RedirectURI: "http://127.0.0.1:8484/callback"}, "code")
	var oauthError *OAuthError
	if !errors.As(err, &oauthError) {
		t.Fatalf("expected OAuthError, got %T: %v", err, err)
	}
	if oauthError.StatusCode != 401 || oauthError.Code != "invalid_grant" || oauthError.RequestID != "request-123" || len(oauthError.Body) == 0 {
		t.Fatalf("unexpected OAuthError: %#v", oauthError)
	}
}

func TestOAuthTokenRedirectCannotCrossOrigin(t *testing.T) {
	t.Parallel()
	var targetRequests int
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		targetRequests++
		if request.Header.Get("Authorization") != "" {
			t.Error("OAuth Basic credential reached redirect target")
		}
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Location", target.URL+"/capture")
		response.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	client := Client{
		Endpoints:  Endpoints{AuthorizationURL: origin.URL + "/approve", TokenURL: origin.URL + "/token"},
		HTTPClient: origin.Client(),
	}
	_, err := client.Exchange(context.Background(), Credentials{ClientID: "id", ClientSecret: "secret", RedirectURI: "http://127.0.0.1:8484/callback"}, "code")
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
	if targetRequests != 0 {
		t.Fatalf("redirect target received %d request(s)", targetRequests)
	}
}

func TestOAuthClientHasFiniteTimeoutAndUserAgent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("User-Agent"); got != "freeagent-cli/test" {
			t.Errorf("User-Agent = %q", got)
		}
		_, _ = io.WriteString(response, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
	}))
	defer server.Close()
	client := Client{
		Endpoints:  Endpoints{AuthorizationURL: server.URL + "/approve", TokenURL: server.URL},
		HTTPClient: server.Client(), UserAgent: "freeagent-cli/test",
	}
	if client.httpClient().Timeout <= 0 {
		t.Fatal("OAuth HTTP client has no timeout")
	}
	if _, err := client.Exchange(context.Background(), Credentials{ClientID: "id", ClientSecret: "secret", RedirectURI: "http://127.0.0.1:8484/callback"}, "code"); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthEndpointsMustShareOrigin(t *testing.T) {
	t.Parallel()
	client := Client{Endpoints: Endpoints{
		AuthorizationURL: "https://api.sandbox.freeagent.com/v2/approve_app",
		TokenURL:         "https://attacker.invalid/v2/token_endpoint",
	}}
	_, err := client.AuthorizationURL(Credentials{ClientID: "id", RedirectURI: "http://127.0.0.1:8484/callback"}, "state")
	if err == nil {
		t.Fatal("expected mismatched OAuth origin error")
	}
}

func TestEndpointsForBaseUsesFixedPaths(t *testing.T) {
	t.Parallel()
	base, err := url.Parse("https://api.sandbox.freeagent.com/v2/")
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := EndpointsForBase(base)
	if err != nil {
		t.Fatal(err)
	}
	if endpoints.AuthorizationURL != "https://api.sandbox.freeagent.com/v2/approve_app" || endpoints.TokenURL != "https://api.sandbox.freeagent.com/v2/token_endpoint" {
		t.Fatalf("endpoints = %#v", endpoints)
	}
	insecure, _ := url.Parse("http://api.sandbox.freeagent.com/v2/")
	if _, err := EndpointsForBase(insecure); err == nil {
		t.Fatal("expected remote HTTP origin rejection")
	}
}

func TestParseCallbackURL(t *testing.T) {
	t.Parallel()
	registered := "http://127.0.0.1:8484/oauth/callback"
	code, err := ParseCallbackURL(registered+"?code=code-1&state=state-1", registered, "state-1")
	if err != nil || code != "code-1" {
		t.Fatalf("ParseCallbackURL = %q, %v", code, err)
	}
	tests := []struct {
		name     string
		callback string
		wantErr  error
	}{
		{"wrong port", "http://127.0.0.1:8485/oauth/callback?code=x&state=state-1", nil},
		{"wrong path", "http://127.0.0.1:8484/other?code=x&state=state-1", nil},
		{"wrong state", registered + "?code=x&state=other", ErrStateMismatch},
		{"missing state", registered + "?code=x", nil},
		{"duplicate state", registered + "?code=x&state=state-1&state=state-1", nil},
		{"duplicate code", registered + "?code=x&code=y&state=state-1", nil},
		{"denied", registered + "?error=access_denied&state=state-1", ErrAccessDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, gotErr := ParseCallbackURL(test.callback, registered, "state-1")
			if gotErr == nil {
				t.Fatal("expected error")
			}
			if test.wantErr != nil && !errors.Is(gotErr, test.wantErr) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", gotErr, test.wantErr)
			}
		})
	}
}

func TestTokenStoreRejectsUnknownAndIncompleteData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := credential.NewMemoryStore()
	tokens := TokenStore{Credentials: store}
	if err := store.Set(ctx, "work", credential.KindOAuthToken, `{"access_token":"a","refresh_token":"r","expiry":"2026-08-28T13:00:00Z","secret_extra":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Load(ctx, "work"); err == nil {
		t.Fatal("expected unknown field error")
	}
	if err := store.Set(ctx, "work", credential.KindOAuthToken, `{"access_token":"a"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Load(ctx, "work"); err == nil {
		t.Fatal("expected incomplete token error")
	}
}
