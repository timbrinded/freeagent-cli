package auth

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/timbrinded/freeagent-cli/internal/credential"
)

// Locker serializes refresh-token rotation across processes.
type Locker interface {
	Lock(ctx context.Context) (unlock func() error, err error)
}

// FileLocker obtains an advisory file lock. Each Lock call uses a distinct flock
// handle so concurrent goroutines and concurrent processes follow one path.
type FileLocker struct {
	Path       string
	RetryDelay time.Duration
}

func (l FileLocker) Lock(ctx context.Context) (func() error, error) {
	if l.Path == "" {
		return nil, errors.New("lock path is required")
	}
	delay := l.RetryDelay
	if delay <= 0 {
		delay = 50 * time.Millisecond
	}
	if err := ensureLockDirectory(l.Path); err != nil {
		return nil, err
	}
	fileLock := flock.New(l.Path)
	locked, err := fileLock.TryLockContext(ctx, delay)
	if err != nil {
		return nil, fmt.Errorf("acquire OAuth refresh lock: %w", err)
	}
	if !locked {
		return nil, errors.New("OAuth refresh lock was not acquired")
	}
	return fileLock.Unlock, nil
}

var ensureLockDirectory = func(path string) error {
	return makePrivateDirectory(filepath.Dir(path))
}

// Manager supplies valid bearer tokens and safely persists rotated refresh tokens.
type Manager struct {
	Profile     string
	ClientID    string
	RedirectURI string
	Credentials credential.Store
	Tokens      TokenStore
	OAuth       *Client
	Locker      Locker
	Now         func() time.Time
	RefreshSkew time.Duration
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) skew() time.Duration {
	if m.RefreshSkew > 0 {
		return m.RefreshSkew
	}
	return 30 * time.Second
}

// AccessToken returns a usable bearer token. An expired token is reloaded after
// the cross-process lock is acquired, then refreshed and saved before use.
func (m *Manager) AccessToken(ctx context.Context) (accessToken string, resultErr error) {
	if err := m.validate(); err != nil {
		return "", err
	}
	token, err := m.Tokens.Load(ctx, m.Profile)
	if err != nil {
		return "", fmt.Errorf("load OAuth token: %w", err)
	}
	if token.ValidAt(m.now(), m.skew()) {
		return token.AccessToken, nil
	}

	unlock, err := m.Locker.Lock(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := unlock(); err != nil && resultErr == nil {
			accessToken = ""
			resultErr = fmt.Errorf("release OAuth refresh lock: %w", err)
		}
	}()

	// Another process may have refreshed while this process waited.
	token, err = m.Tokens.Load(ctx, m.Profile)
	if err != nil {
		return "", fmt.Errorf("reload OAuth token after lock: %w", err)
	}
	if token.ValidAt(m.now(), m.skew()) {
		return token.AccessToken, nil
	}
	clientSecret, err := m.Credentials.Get(ctx, m.Profile, credential.KindClientSecret)
	if err != nil {
		return "", fmt.Errorf("load OAuth client secret: %w", err)
	}
	refreshed, err := m.OAuth.Refresh(ctx, Credentials{
		ClientID:     m.ClientID,
		ClientSecret: clientSecret,
		RedirectURI:  m.RedirectURI,
	}, token.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh OAuth token: %w", err)
	}
	if err := m.Tokens.Save(ctx, m.Profile, refreshed); err != nil {
		// Do not return a rotated access token if its matching refresh token was
		// not persisted. That would leave subsequent processes in an unsafe state.
		return "", fmt.Errorf("persist rotated OAuth token: %w", err)
	}
	return refreshed.AccessToken, nil
}

func (m *Manager) validate() error {
	if m.Profile == "" || m.ClientID == "" || m.RedirectURI == "" {
		return errors.New("OAuth manager profile, client ID, and redirect URI are required")
	}
	if m.Credentials == nil || m.Tokens.Credentials == nil || m.OAuth == nil || m.Locker == nil {
		return errors.New("OAuth manager dependencies are incomplete")
	}
	return nil
}
