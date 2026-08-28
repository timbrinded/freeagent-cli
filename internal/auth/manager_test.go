package auth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timbrinded/freeagent-cli/internal/credential"
)

func TestManagerRefreshesOnceAcrossConcurrentCallers(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		refreshes.Add(1)
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("refresh_token") != "refresh-old" {
			t.Errorf("refresh token = %q", request.Form.Get("refresh_token"))
		}
		_, _ = io.WriteString(response, `{"access_token":"access-new","refresh_token":"refresh-new","token_type":"bearer","expires_in":3600}`)
	}))
	defer server.Close()
	secrets := credential.NewMemoryStore()
	if err := secrets.Set(context.Background(), "work", credential.KindClientSecret, "client-secret"); err != nil {
		t.Fatal(err)
	}
	tokens := TokenStore{Credentials: secrets}
	if err := tokens.Save(context.Background(), "work", Token{
		AccessToken: "access-old", RefreshToken: "refresh-old", TokenType: "bearer", Expiry: fixedNow.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		Profile: "work", ClientID: "client-id", RedirectURI: "http://127.0.0.1:8484/callback",
		Credentials: secrets, Tokens: tokens,
		OAuth:  &Client{Endpoints: Endpoints{AuthorizationURL: server.URL + "/approve", TokenURL: server.URL}, HTTPClient: server.Client(), Now: func() time.Time { return fixedNow }},
		Locker: FileLocker{Path: filepath.Join(t.TempDir(), "work.lock")},
		Now:    func() time.Time { return fixedNow },
	}
	const callers = 24
	var wait sync.WaitGroup
	results := make(chan string, callers)
	errorsFound := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := manager.AccessToken(context.Background())
			if err != nil {
				errorsFound <- err
				return
			}
			results <- value
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("AccessToken: %v", err)
	}
	for result := range results {
		if result != "access-new" {
			t.Errorf("AccessToken = %q", result)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("refresh endpoint called %d times, want 1", got)
	}
	stored, err := tokens.Load(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "refresh-new" {
		t.Fatalf("stored refresh token = %q", stored.RefreshToken)
	}
}

func TestInspectDoesNotExposeOrRefreshCredentials(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := credential.NewMemoryStore()
	if err := store.Set(context.Background(), "work", credential.KindClientSecret, "client-secret"); err != nil {
		t.Fatal(err)
	}
	if err := (TokenStore{Credentials: store}).Save(context.Background(), "work", Token{
		AccessToken: "access-secret", RefreshToken: "refresh-secret", Expiry: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(context.Background(), store, "work", now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !status.HasClientSecret || !status.HasToken || !status.NeedsRefresh || status.ExpiresAt.IsZero() {
		t.Fatalf("status = %#v", status)
	}
}

func TestLoopbackIgnoresWrongStateThenAcceptsExactCallback(t *testing.T) {
	t.Parallel()
	registered := reserveLoopbackURL(t, "/oauth/callback")
	listener, err := ListenLoopback(registered, "correct-state")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	response, err := http.Get(registered + "?code=wrong&state=wrong-state")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong-state status = %d", response.StatusCode)
	}
	response, err = http.Get(registered + "?code=right-code&state=correct-state")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	code, err := listener.Wait(ctx)
	if err != nil || code != "right-code" {
		t.Fatalf("Wait = %q, %v", code, err)
	}
}

func TestLoopbackRequiresIPAndFixedPort(t *testing.T) {
	t.Parallel()
	for _, redirect := range []string{
		"http://localhost:8484/callback",
		"http://127.0.0.1/callback",
		"https://127.0.0.1:8484/callback",
		"http://127.0.0.1:8484/callback?query=1",
	} {
		if listener, err := ListenLoopback(redirect, "state"); err == nil {
			_ = listener.Close()
			t.Errorf("accepted %q", redirect)
		}
	}
}

func reserveLoopbackURL(t *testing.T, path string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return (&url.URL{Scheme: "http", Host: address, Path: path}).String()
}

type cancellationLocker struct{}

func (cancellationLocker) Lock(ctx context.Context) (func() error, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestManagerHonorsLockCancellation(t *testing.T) {
	t.Parallel()
	store := credential.NewMemoryStore()
	tokens := TokenStore{Credentials: store}
	if err := tokens.Save(context.Background(), "work", Token{
		AccessToken: "old", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		Profile: "work", ClientID: "id", RedirectURI: "http://127.0.0.1:8484/callback",
		Credentials: store, Tokens: tokens, OAuth: &Client{}, Locker: cancellationLocker{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.AccessToken(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AccessToken error = %v", err)
	}
}
