package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/timbrinded/freeagent-cli/internal/credential"
)

// TokenStore serializes a complete OAuth token into a secret store.
type TokenStore struct {
	Credentials credential.Store
}

func (s TokenStore) Load(ctx context.Context, profile string) (Token, error) {
	if s.Credentials == nil {
		return Token{}, errors.New("credential store is required")
	}
	encoded, err := s.Credentials.Get(ctx, profile, credential.KindOAuthToken)
	if err != nil {
		return Token{}, err
	}
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	var token Token
	if err := decoder.Decode(&token); err != nil {
		return Token{}, fmt.Errorf("decode stored OAuth token: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Token{}, errors.New("decode stored OAuth token: trailing JSON value")
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.Expiry.IsZero() {
		return Token{}, errors.New("stored OAuth token is incomplete")
	}
	return token, nil
}

func (s TokenStore) Save(ctx context.Context, profile string, token Token) error {
	if s.Credentials == nil {
		return errors.New("credential store is required")
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.Expiry.IsZero() {
		return errors.New("refusing to store an incomplete OAuth token")
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("encode OAuth token: %w", err)
	}
	return s.Credentials.Set(ctx, profile, credential.KindOAuthToken, string(encoded))
}

func (s TokenStore) Delete(ctx context.Context, profile string) error {
	if s.Credentials == nil {
		return errors.New("credential store is required")
	}
	return s.Credentials.Delete(ctx, profile, credential.KindOAuthToken)
}

// Status describes credential presence without exposing any credential value.
type Status struct {
	HasClientSecret bool      `json:"has_client_secret"`
	HasToken        bool      `json:"has_token"`
	NeedsRefresh    bool      `json:"needs_refresh"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}

// Inspect returns non-secret authentication state without refreshing it.
func Inspect(ctx context.Context, store credential.Store, profile string, now time.Time, skew time.Duration) (Status, error) {
	if store == nil {
		return Status{}, errors.New("credential store is required")
	}
	result := Status{}
	if _, err := store.Get(ctx, profile, credential.KindClientSecret); err == nil {
		result.HasClientSecret = true
	} else if !errors.Is(err, credential.ErrNotFound) {
		return Status{}, err
	}
	token, err := (TokenStore{Credentials: store}).Load(ctx, profile)
	if err == nil {
		result.HasToken = true
		result.ExpiresAt = token.Expiry
		result.NeedsRefresh = !token.ValidAt(now, skew)
	} else if !errors.Is(err, credential.ErrNotFound) {
		return Status{}, err
	}
	return result, nil
}
