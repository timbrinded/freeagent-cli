package freeagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type staticToken string

func (token staticToken) AccessToken(context.Context) (string, error) { return string(token), nil }

func newTestClient(t *testing.T, server *httptest.Server, mutate func(*Options)) *Client {
	t.Helper()
	base, err := url.Parse(server.URL + "/v2/")
	if err != nil {
		t.Fatal(err)
	}
	options := Options{
		BaseURL: base, HTTPClient: server.Client(), TokenSource: staticToken("access-token"),
		UserAgent: "freeagent-cli/test", APIVersion: "2024-10-01", ReadRetries: -1,
	}
	if mutate != nil {
		mutate(&options)
	}
	client, err := NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientSetsRequiredHeadersAndDoesNotLeakSubdomain(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var observed []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		observed = append(observed, request.Header.Clone())
		mu.Unlock()
		response.Header().Set("X-Request-ID", "request-1")
		_, _ = io.WriteString(response, `{}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	first, err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "invoices", PracticeSubdomain: "client-one"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Metadata.PracticeSubdomain != "client-one" || first.Metadata.RequestID != "request-1" {
		t.Fatalf("metadata = %#v", first.Metadata)
	}
	if _, err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "contacts"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 {
		t.Fatalf("request count = %d", len(observed))
	}
	for key, want := range map[string]string{
		"Authorization": "Bearer access-token",
		"Accept":        "application/json",
		"Content-Type":  "application/json",
		"User-Agent":    "freeagent-cli/test",
		"X-Api-Version": "2024-10-01",
	} {
		if got := observed[0].Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := observed[0].Get("X-Subdomain"); got != "client-one" {
		t.Errorf("first X-Subdomain = %q", got)
	}
	if got := observed[1].Get("X-Subdomain"); got != "" {
		t.Errorf("second request leaked X-Subdomain = %q", got)
	}
}

func TestClientRejectsUnsafePathsBeforeSending(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	client := newTestClient(t, server, nil)
	paths := []string{
		"https://attacker.invalid/steal",
		"//attacker.invalid/steal",
		"../steal",
		"%2e%2e/steal",
		"%252e%252e/steal",
		"invoices%252f1",
		"invoices%255c1",
		"invoices?secret=1",
		"/other/path",
		"invoices#fragment",
	}
	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			if _, err := client.Do(context.Background(), Request{Path: path}); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unsafe paths sent %d request(s)", got)
	}
}

func TestClientRejectsCrossOriginRedirectWithoutLeakingBearer(t *testing.T) {
	t.Parallel()
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		targetRequests.Add(1)
		if request.Header.Get("Authorization") != "" {
			t.Error("bearer token reached redirect target")
		}
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Redirect(response, &http.Request{}, target.URL+"/capture", http.StatusFound)
	}))
	defer origin.Close()
	client := newTestClient(t, origin, nil)
	_, err := client.Do(context.Background(), Request{Path: "invoices"})
	if err == nil {
		t.Fatal("expected redirect error")
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d request(s)", got)
	}
}

func TestReadRetriesHonorRetryAfter(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			response.Header().Set("Retry-After", "7")
			response.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(response, `{"error":"slow down"}`)
			return
		}
		_, _ = io.WriteString(response, `{"invoices":[]}`)
	}))
	defer server.Close()
	var delays []time.Duration
	client := newTestClient(t, server, func(options *Options) {
		options.ReadRetries = 1
		options.Sleep = func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		}
	})
	response, err := client.Do(context.Background(), Request{Path: "invoices"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Metadata.Attempts != 2 || requests.Load() != 2 {
		t.Fatalf("attempts = %d, requests = %d", response.Metadata.Attempts, requests.Load())
	}
	if len(delays) != 1 || delays[0] != 7*time.Second {
		t.Fatalf("delays = %v", delays)
	}
}

func TestMutationNeverRetriesHTTPFailure(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(response, `{"errors":["failed"]}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, func(options *Options) { options.ReadRetries = 5 })
	_, err := client.Do(context.Background(), Request{Method: http.MethodPost, Path: "invoices", JSONBody: []byte(`{"invoice":{}}`)})
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected OutcomeUnknownError, got %T: %v", err, err)
	}
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("mutation sent %d times", got)
	}
}

type traceErrorTransport struct {
	wroteHeaders bool
	written      bool
	writeErr     error
	calls        atomic.Int32
}

func (transport *traceErrorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	if transport.wroteHeaders {
		if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.WroteHeaders != nil {
			trace.WroteHeaders()
		}
	}
	if transport.written {
		if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.WroteRequest != nil {
			trace.WroteRequest(httptrace.WroteRequestInfo{Err: transport.writeErr})
		}
	}
	return nil, errors.New("connection reset")
}

func TestPartiallyWrittenMutationHasUnknownOutcome(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://api.sandbox.freeagent.com/v2/")
	transport := &traceErrorTransport{wroteHeaders: true, written: true, writeErr: errors.New("body write failed")}
	client, err := NewClient(Options{
		BaseURL: base, HTTPClient: &http.Client{Transport: transport}, TokenSource: staticToken("token"),
		UserAgent: "freeagent-cli/test", APIVersion: "2024-10-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), Request{Method: http.MethodPost, Path: "invoices", JSONBody: []byte(`{"invoice":{}}`)})
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected OutcomeUnknownError, got %T: %v", err, err)
	}
}

func TestWrittenMutationTransportFailureHasUnknownOutcome(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://api.sandbox.freeagent.com/v2/")
	transport := &traceErrorTransport{written: true}
	client, err := NewClient(Options{
		BaseURL: base, HTTPClient: &http.Client{Transport: transport}, TokenSource: staticToken("token"),
		UserAgent: "freeagent-cli/test", APIVersion: "2024-10-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), Request{Method: http.MethodDelete, Path: "invoices/1"})
	var unknown *OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected OutcomeUnknownError, got %T: %v", err, err)
	}
	if transport.calls.Load() != 1 {
		t.Fatalf("mutation attempted %d times", transport.calls.Load())
	}
}

func TestPreSendMutationFailureIsNotUnknown(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://api.sandbox.freeagent.com/v2/")
	transport := &traceErrorTransport{}
	client, err := NewClient(Options{
		BaseURL: base, HTTPClient: &http.Client{Transport: transport}, TokenSource: staticToken("token"),
		UserAgent: "freeagent-cli/test", APIVersion: "2024-10-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), Request{Method: http.MethodPost, Path: "contacts", JSONBody: []byte(`{}`)})
	var unknown *OutcomeUnknownError
	if errors.As(err, &unknown) {
		t.Fatalf("unexpected unknown outcome: %v", err)
	}
	var transportError *TransportError
	if !errors.As(err, &transportError) || transportError.Sent {
		t.Fatalf("expected unsent TransportError, got %T: %v", err, err)
	}
}

func TestAPIErrorPreservesBodyAndExtractsMessages(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("X-Request-ID", "request-2")
		response.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(response, `{"errors":{"contact":["is invalid"]}}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	_, err := client.Do(context.Background(), Request{Path: "/v2/contacts"})
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("expected APIError: %v", err)
	}
	if apiError.RequestID != "request-2" || len(apiError.Body) == 0 || len(apiError.Messages) != 1 || apiError.Messages[0] != "is invalid" {
		t.Fatalf("unexpected APIError: %#v", apiError)
	}
}

func TestPracticeSubdomainIsConservative(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(response, `{}`) }))
	defer server.Close()
	client := newTestClient(t, server, nil)
	for _, value := range []string{"UPPER", "two.labels", "-leading", "trailing-", "has_underscore"} {
		if _, err := client.Do(context.Background(), Request{Path: "company", PracticeSubdomain: value}); err == nil {
			t.Errorf("accepted practice subdomain %q", value)
		}
	}
}

func TestDefaultHTTPClientHasFiniteTimeout(t *testing.T) {
	t.Parallel()
	base, _ := url.Parse("https://api.sandbox.freeagent.com/v2/")
	client, err := NewClient(Options{BaseURL: base, TokenSource: staticToken("token"), UserAgent: "freeagent-cli/test", APIVersion: "2024-10-01"})
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient.Timeout <= 0 {
		t.Fatal("HTTP client has no timeout")
	}
}

func TestClientRejectsInvalidContextConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		base       string
		apiVersion string
	}{
		{"wrong base path", "https://api.sandbox.freeagent.com/", "2024-10-01"},
		{"invalid API date", "https://api.sandbox.freeagent.com/v2/", "2026-99-99"},
		{"remote HTTP", "http://api.sandbox.freeagent.com/v2/", "2024-10-01"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			base, err := url.Parse(test.base)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewClient(Options{BaseURL: base, TokenSource: staticToken("token"), UserAgent: "freeagent-cli/test", APIVersion: test.apiVersion}); err == nil {
				t.Fatal("expected configuration rejection")
			}
		})
	}
}

func TestAPIErrorMessageOrderingIsDeterministic(t *testing.T) {
	t.Parallel()
	got := extractMessages([]byte(`{"errors":{"z_field":"last","a_field":"first"}}`))
	if len(got) != 2 || got[0] != "first" || got[1] != "last" {
		t.Fatalf("messages = %v", got)
	}
}

func TestAPIErrorCollectsFallbackFieldsFromEachArrayItem(t *testing.T) {
	t.Parallel()
	got := extractMessages([]byte(`[{"field_b":"second"},{"field_a":"first"}]`))
	if len(got) != 2 || got[0] != "second" || got[1] != "first" {
		t.Fatalf("messages = %v", got)
	}
}
