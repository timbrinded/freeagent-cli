package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timbrinded/freeagent-cli/internal/auth"
	"github.com/timbrinded/freeagent-cli/internal/cli"
	"github.com/timbrinded/freeagent-cli/internal/config"
	"github.com/timbrinded/freeagent-cli/internal/credential"
	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

func TestUpsertRequiresExplicitReplace(t *testing.T) {
	t.Parallel()
	repository := config.Repository{Path: filepath.Join(t.TempDir(), "config.json")}
	store := credential.NewMemoryStore()
	service := New(repository, store, "freeagent-cli/test")
	input := cli.ProfileInput{
		Name: "main", Environment: "sandbox", ClientID: "sandbox-client",
		RedirectURI: "http://127.0.0.1:18443/callback", APIVersion: "2024-10-01",
	}
	if _, err := service.Upsert(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "main", credential.KindClientSecret, "secret"); err != nil {
		t.Fatal(err)
	}
	replacement := input
	replacement.Environment = "production"
	replacement.ClientID = "production-client"
	if _, err := service.Upsert(context.Background(), replacement); err == nil {
		t.Fatal("existing profile was silently retargeted")
	}
	current, err := service.Resolve(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if current.Environment != "sandbox" || current.ClientID != "sandbox-client" {
		t.Fatalf("failed replacement changed profile: %+v", current)
	}
	replacement.Replace = true
	if _, err := service.Upsert(context.Background(), replacement); err == nil {
		t.Fatal("--replace changed OAuth identity while credentials remained")
	}
	nonIdentity := input
	nonIdentity.Replace = true
	nonIdentity.APIVersion = "2025-01-01"
	nonIdentity.ReadOnly = true
	updated, err := service.Upsert(context.Background(), nonIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Environment != "sandbox" || updated.ClientID != "sandbox-client" || updated.APIVersion != "2025-01-01" || !updated.ReadOnly {
		t.Fatalf("safe explicit replacement did not apply: %+v", updated)
	}
}

func TestRemovingDefaultProfileDoesNotSelectAnotherContext(t *testing.T) {
	t.Parallel()
	repository := config.Repository{Path: filepath.Join(t.TempDir(), "config.json")}
	service := New(repository, credential.NewMemoryStore(), "freeagent-cli/test")
	first := cli.ProfileInput{Name: "first", Environment: "sandbox", ClientID: "first", RedirectURI: "http://127.0.0.1:18443/callback", APIVersion: "2024-10-01"}
	second := cli.ProfileInput{Name: "second", Environment: "production", ClientID: "second", RedirectURI: "http://127.0.0.1:18444/callback", APIVersion: "2024-10-01"}
	if _, err := service.Upsert(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upsert(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if err := service.Remove(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	value, err := repository.Load()
	if err != nil {
		t.Fatal(err)
	}
	if value.DefaultProfile != "" {
		t.Fatalf("profile removal silently selected %q", value.DefaultProfile)
	}
}

func TestProfileTokenLockPathPrefixesWindowsDeviceNames(t *testing.T) {
	t.Parallel()
	service := New(
		config.Repository{Path: filepath.Join(t.TempDir(), "config.json")},
		credential.NewMemoryStore(),
		"freeagent-cli/test",
	)

	lockPath, err := service.profileTokenLockPath("con")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(lockPath), "profile-con.oauth.lock"; got != want {
		t.Fatalf("lock basename = %q, want %q", got, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type blockingCredentialDeleteStore struct {
	credential.Store
	deleteStarted chan struct{}
	deleteProceed chan struct{}
	once          sync.Once
}

func (s *blockingCredentialDeleteStore) Delete(ctx context.Context, profile string, kind credential.Kind) error {
	if kind == credential.KindClientSecret {
		s.once.Do(func() { close(s.deleteStarted) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.deleteProceed:
		}
	}
	return s.Store.Delete(ctx, profile, kind)
}

func configuredService(t *testing.T, redirectURI string, transport roundTripFunc) (*Services, *credential.MemoryStore) {
	t.Helper()
	store := credential.NewMemoryStore()
	service := New(config.Repository{Path: filepath.Join(t.TempDir(), "config.json")}, store, "freeagent-cli/test")
	service.Now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	service.HTTPClient = &http.Client{Transport: transport}
	_, err := service.Upsert(context.Background(), cli.ProfileInput{
		Name: "sandbox", Environment: "sandbox", ClientID: "client-id",
		RedirectURI: redirectURI, APIVersion: "2024-10-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "sandbox", credential.KindClientSecret, "client-secret"); err != nil {
		t.Fatal(err)
	}
	return service, store
}

func TestMutationServerFailureRemainsOutcomeUnknown(t *testing.T) {
	t.Parallel()
	service, store := configuredService(t, "http://127.0.0.1:18443/callback", func(*http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("X-Request-ID", "request-500-1")
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: header, Body: io.NopCloser(strings.NewReader(`{"error":"server"}`))}, nil
	})
	if err := (auth.TokenStore{Credentials: store}).Save(context.Background(), "sandbox", auth.Token{AccessToken: "access", RefreshToken: "refresh", Expiry: service.now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	_, err := service.Do(context.Background(), "sandbox", cli.APIRequest{Method: "POST", Path: "/v2/contacts", Body: []byte(`{"contact":{}}`), Mutation: true, APIVersion: "2024-10-01"})
	var commandErr *protocol.CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != protocol.ExitOutcomeUnknown || commandErr.Outcome != "unknown" || commandErr.RequestID != "request-500-1" {
		t.Fatalf("mutation 500 was not kept unknown: %#v", err)
	}
}

func TestMissingTokenIsAuthenticationFailure(t *testing.T) {
	t.Parallel()
	called := false
	service, _ := configuredService(t, "http://127.0.0.1:18443/callback", func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not call transport")
	})
	_, err := service.Do(context.Background(), "sandbox", cli.APIRequest{Method: "GET", Path: "/v2/company", APIVersion: "2024-10-01"})
	var commandErr *protocol.CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != protocol.ExitAuth || commandErr.Kind != "authentication_required" || called {
		t.Fatalf("missing token mapping: err=%#v transport_called=%t", err, called)
	}
}

func TestCLIRejectsPathSeparatorBeforeRuntimeTransport(t *testing.T) {
	t.Parallel()
	transportCalled := false
	service, _ := configuredService(t, "http://127.0.0.1:18443/callback", func(*http.Request) (*http.Response, error) {
		transportCalled = true
		return nil, errors.New("unsafe target reached the transport")
	})
	var stdout, stderr bytes.Buffer
	app := cli.New(cli.Dependencies{
		Profiles: service,
		API:      service,
		Stdin:    strings.NewReader(""),
		Stdout:   &stdout,
		Stderr:   &stderr,
		Now:      service.Now,
	})
	code := app.Execute(context.Background(), []string{"--agent", "--profile", "sandbox", "contacts", "get", "--id", "a/b"})
	if code != int(protocol.ExitSafety) || transportCalled || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"safety_policy"`) {
		t.Fatalf("code=%d transport_called=%t stdout=%s stderr=%s", code, transportCalled, stdout.String(), stderr.String())
	}
}

func TestManualLoginOpensAuthorizationBeforeDeferredHiddenCallback(t *testing.T) {
	t.Parallel()
	service, _ := configuredService(t, "https://app.example/callback", func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v2/token_endpoint" {
			t.Fatalf("unexpected HTTP request: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"access","refresh_token":"refresh","token_type":"bearer","expires_in":3600}`))}, nil
	})
	var opened string
	service.OpenURL = func(value string) error {
		opened = value
		return nil
	}
	var messages bytes.Buffer
	callbackRead := false
	status, err := service.Login(context.Background(), "sandbox", cli.LoginOptions{
		Manual: true, Messages: &messages,
		ReadManualCallback: func() ([]byte, error) {
			callbackRead = true
			if opened == "" {
				t.Fatal("callback was read before the authorization URL was opened")
			}
			authorization, parseErr := url.Parse(opened)
			if parseErr != nil {
				return nil, parseErr
			}
			callback := "https://app.example/callback?code=sensitive-code&state=" + url.QueryEscape(authorization.Query().Get("state"))
			return []byte(callback), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !callbackRead || !status.Authenticated || opened == "" {
		t.Fatalf("manual login did not complete: status=%+v opened=%q", status, opened)
	}
	if strings.Contains(messages.String(), "sensitive-code") {
		t.Fatalf("callback code was logged: %s", messages.String())
	}
}

func TestLoginSaveUsesRefreshTokenLock(t *testing.T) {
	t.Parallel()
	exchanged := make(chan struct{}, 1)
	service, store := configuredService(t, "https://app.example/callback", func(*http.Request) (*http.Response, error) {
		exchanged <- struct{}{}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"access","refresh_token":"refresh","token_type":"bearer","expires_in":3600}`))}, nil
	})
	var opened string
	service.OpenURL = func(value string) error { opened = value; return nil }
	lockPath, err := service.profileTokenLockPath("sandbox")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := (auth.FileLocker{Path: lockPath}).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, loginErr := service.Login(context.Background(), "sandbox", cli.LoginOptions{Manual: true, Messages: io.Discard, ReadManualCallback: func() ([]byte, error) {
			authorization, parseErr := url.Parse(opened)
			if parseErr != nil {
				return nil, parseErr
			}
			return []byte("https://app.example/callback?code=code&state=" + url.QueryEscape(authorization.Query().Get("state"))), nil
		}})
		done <- loginErr
	}()
	<-exchanged
	select {
	case err := <-done:
		t.Fatalf("login completed while refresh token lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := (auth.TokenStore{Credentials: store}).Load(context.Background(), "sandbox"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("token was saved outside the lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLoginDoesNotStoreTokenAfterProfileRemoval(t *testing.T) {
	t.Parallel()
	service, store := configuredService(t, "https://app.example/callback", func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"access","refresh_token":"refresh","token_type":"bearer","expires_in":3600}`))}, nil
	})
	var opened string
	service.OpenURL = func(value string) error { opened = value; return nil }
	callbackStarted := make(chan struct{})
	continueCallback := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, loginErr := service.Login(context.Background(), "sandbox", cli.LoginOptions{
			Manual:   true,
			Messages: io.Discard,
			ReadManualCallback: func() ([]byte, error) {
				close(callbackStarted)
				<-continueCallback
				authorization, parseErr := url.Parse(opened)
				if parseErr != nil {
					return nil, parseErr
				}
				return []byte("https://app.example/callback?code=code&state=" + url.QueryEscape(authorization.Query().Get("state"))), nil
			},
		})
		done <- loginErr
	}()
	<-callbackStarted
	if err := service.Logout(context.Background(), "sandbox"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteClientSecret(context.Background(), "sandbox"); err != nil {
		t.Fatal(err)
	}
	if err := service.Remove(context.Background(), "sandbox"); err != nil {
		t.Fatal(err)
	}
	close(continueCallback)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "token was not stored") {
		t.Fatalf("login after profile removal error = %v", err)
	}
	if _, err := (auth.TokenStore{Credentials: store}).Load(context.Background(), "sandbox"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("removed profile retained a token: %v", err)
	}
}

func TestRemovalLockSpansCredentialsAndConfiguration(t *testing.T) {
	t.Parallel()
	exchanged := make(chan struct{})
	service, store := configuredService(t, "https://app.example/callback", func(*http.Request) (*http.Response, error) {
		close(exchanged)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"access","refresh_token":"refresh","token_type":"bearer","expires_in":3600}`))}, nil
	})
	blockingStore := &blockingCredentialDeleteStore{
		Store:         store,
		deleteStarted: make(chan struct{}),
		deleteProceed: make(chan struct{}),
	}
	service.Credentials = blockingStore
	var opened string
	service.OpenURL = func(value string) error { opened = value; return nil }
	callbackStarted := make(chan struct{})
	continueCallback := make(chan struct{})
	loginDone := make(chan error, 1)
	go func() {
		_, loginErr := service.Login(context.Background(), "sandbox", cli.LoginOptions{
			Manual:   true,
			Messages: io.Discard,
			ReadManualCallback: func() ([]byte, error) {
				close(callbackStarted)
				<-continueCallback
				authorization, parseErr := url.Parse(opened)
				if parseErr != nil {
					return nil, parseErr
				}
				return []byte("https://app.example/callback?code=code&state=" + url.QueryEscape(authorization.Query().Get("state"))), nil
			},
		})
		loginDone <- loginErr
	}()
	<-callbackStarted
	removeDone := make(chan error, 1)
	go func() { removeDone <- service.Remove(context.Background(), "sandbox") }()
	<-blockingStore.deleteStarted
	close(continueCallback)
	<-exchanged
	select {
	case err := <-loginDone:
		t.Fatalf("login completed while removal held the profile lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(blockingStore.deleteProceed)
	if err := <-removeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-loginDone; err == nil || !strings.Contains(err.Error(), "token was not stored") {
		t.Fatalf("login during profile removal error = %v", err)
	}
	if _, err := (auth.TokenStore{Credentials: store}).Load(context.Background(), "sandbox"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("removed profile retained a token: %v", err)
	}
	if _, err := store.Get(context.Background(), "sandbox", credential.KindClientSecret); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("removed profile retained a client secret: %v", err)
	}
	if _, err := service.Resolve(context.Background(), "sandbox"); err == nil {
		t.Fatal("removed profile remained configured")
	}
}

func TestLogoutDeleteUsesRefreshTokenLock(t *testing.T) {
	t.Parallel()
	service, store := configuredService(t, "http://127.0.0.1:18443/callback", func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected request")
	})
	tokens := auth.TokenStore{Credentials: store}
	if err := tokens.Save(context.Background(), "sandbox", auth.Token{AccessToken: "access", RefreshToken: "refresh", Expiry: service.now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	lockPath, err := service.profileTokenLockPath("sandbox")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := (auth.FileLocker{Path: lockPath}).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- service.Logout(context.Background(), "sandbox") }()
	select {
	case err := <-done:
		t.Fatalf("logout completed while refresh token lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := tokens.Load(context.Background(), "sandbox"); err != nil {
		t.Fatalf("logout deleted token outside the lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Load(context.Background(), "sandbox"); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("logout did not delete token after lock release: %v", err)
	}
}

func TestProfileTokenLockPathRejectsTraversal(t *testing.T) {
	t.Parallel()
	service := New(config.Repository{Path: filepath.Join(t.TempDir(), "config.json")}, credential.NewMemoryStore(), "freeagent-cli/test")
	for _, name := range []string{"../escape", "a/b", `a\\b`} {
		if _, err := service.profileTokenLockPath(name); err == nil {
			t.Errorf("profileTokenLockPath(%q) accepted traversal", name)
		}
	}
}
