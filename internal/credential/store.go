// Package credential stores FreeAgent secrets outside the public configuration.
package credential

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

const DefaultService = "freeagent-cli"

// Kind distinguishes secrets stored for one profile.
type Kind string

const (
	KindClientSecret Kind = "client-secret"
	KindOAuthToken   Kind = "oauth-token"
)

var (
	ErrNotFound       = errors.New("credential not found")
	credentialSegment = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// Store is the minimum secret-storage contract used by the CLI.
type Store interface {
	Get(ctx context.Context, profile string, kind Kind) (string, error)
	Set(ctx context.Context, profile string, kind Kind, value string) error
	Delete(ctx context.Context, profile string, kind Kind) error
}

func account(profile string, kind Kind) (string, error) {
	if !credentialSegment.MatchString(profile) {
		return "", fmt.Errorf("invalid credential profile %q", profile)
	}
	if kind != KindClientSecret && kind != KindOAuthToken {
		return "", fmt.Errorf("invalid credential kind %q", kind)
	}
	return profile + "/" + string(kind), nil
}

// KeyringStore uses the operating system credential manager.
type KeyringStore struct {
	Service string
}

// NewKeyringStore returns a keyring store with a stable service name.
func NewKeyringStore(service string) *KeyringStore {
	if service == "" {
		service = DefaultService
	}
	return &KeyringStore{Service: service}
}

func (s *KeyringStore) service() string {
	if s.Service == "" {
		return DefaultService
	}
	return s.Service
}

func (s *KeyringStore) Get(ctx context.Context, profile string, kind Kind) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := account(profile, kind)
	if err != nil {
		return "", err
	}
	value, err := keyring.Get(s.service(), name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get credential from OS keyring: %w", err)
	}
	return value, nil
}

func (s *KeyringStore) Set(ctx context.Context, profile string, kind Kind, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value == "" {
		return errors.New("refusing to store an empty credential")
	}
	name, err := account(profile, kind)
	if err != nil {
		return err
	}
	if err := keyring.Set(s.service(), name, value); err != nil {
		return fmt.Errorf("set credential in OS keyring: %w", err)
	}
	return nil
}

func (s *KeyringStore) Delete(ctx context.Context, profile string, kind Kind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := account(profile, kind)
	if err != nil {
		return err
	}
	if err := keyring.Delete(s.service(), name); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete credential from OS keyring: %w", err)
	}
	return nil
}

// MemoryStore is a concurrency-safe Store for tests and embedded use.
type MemoryStore struct {
	mu     sync.RWMutex
	values map[string]string
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{values: make(map[string]string)}
}

func (s *MemoryStore) Get(ctx context.Context, profile string, kind Kind) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := account(profile, kind)
	if err != nil {
		return "", err
	}
	s.mu.RLock()
	value, ok := s.values[name]
	s.mu.RUnlock()
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) Set(ctx context.Context, profile string, kind Kind, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value == "" {
		return errors.New("refusing to store an empty credential")
	}
	name, err := account(profile, kind)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[name] = value
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, profile string, kind Kind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := account(profile, kind)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.values, name)
	s.mu.Unlock()
	return nil
}
