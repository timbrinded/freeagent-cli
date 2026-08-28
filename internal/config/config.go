// Package config loads and stores non-secret CLI configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// CurrentVersion is the only configuration schema version understood by this release.
	CurrentVersion = 1
	// DefaultAPIVersion is the deliberately pinned default for new profiles. It
	// opts in to the documented multi-attachment bank-explanation contract.
	DefaultAPIVersion = "2024-10-01"

	productionBaseURL = "https://api.freeagent.com/v2/"
	sandboxBaseURL    = "https://api.sandbox.freeagent.com/v2/"
)

var (
	profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	apiVersionPattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ValidateProfileName rejects names that cannot be used as configuration keys,
// keyring accounts, or lock-file path segments.
func ValidateProfileName(name string) error {
	if !profileNamePattern.MatchString(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	return nil
}

// Environment selects one of FreeAgent's fixed API origins.
type Environment string

const (
	EnvironmentSandbox    Environment = "sandbox"
	EnvironmentProduction Environment = "production"
)

// Profile contains public OAuth and API settings. Secrets are deliberately not
// part of this type and must be held by the credential package.
type Profile struct {
	Environment Environment `json:"environment"`
	ClientID    string      `json:"client_id"`
	RedirectURI string      `json:"redirect_uri"`
	APIVersion  string      `json:"api_version"`
	ReadOnly    bool        `json:"read_only,omitempty"`
}

// BaseURL returns the immutable FreeAgent API origin for this profile.
func (p Profile) BaseURL() (*url.URL, error) {
	var raw string
	switch p.Environment {
	case EnvironmentSandbox:
		raw = sandboxBaseURL
	case EnvironmentProduction:
		raw = productionBaseURL
	default:
		return nil, fmt.Errorf("unsupported FreeAgent environment %q", p.Environment)
	}

	return url.Parse(raw)
}

// OAuthBaseURL returns the immutable FreeAgent origin used by OAuth endpoints.
func (p Profile) OAuthBaseURL() (*url.URL, error) {
	base, err := p.BaseURL()
	if err != nil {
		return nil, err
	}
	base.Path = "/"
	return base, nil
}

// Validate checks all public settings without reading any credentials.
func (p Profile) Validate() error {
	if _, err := p.BaseURL(); err != nil {
		return err
	}
	if strings.TrimSpace(p.ClientID) == "" {
		return errors.New("client_id is required")
	}
	redirect, err := url.Parse(p.RedirectURI)
	if err != nil {
		return fmt.Errorf("parse redirect_uri: %w", err)
	}
	if err := validateRedirectURI(redirect); err != nil {
		return err
	}
	if !apiVersionPattern.MatchString(p.APIVersion) {
		return errors.New("api_version must use YYYY-MM-DD")
	}
	if _, err := time.Parse(time.DateOnly, p.APIVersion); err != nil {
		return errors.New("api_version must be a valid calendar date")
	}
	return nil
}

func validateRedirectURI(redirect *url.URL) error {
	if redirect == nil || redirect.Scheme == "" || redirect.Host == "" {
		return errors.New("redirect_uri must be an absolute URL")
	}
	if redirect.User != nil || redirect.Fragment != "" || redirect.RawQuery != "" || redirect.RawPath != "" {
		return errors.New("redirect_uri must not contain user information, a query, a fragment, or an encoded path")
	}
	if redirect.Path == "" {
		return errors.New("redirect_uri must contain an explicit callback path")
	}
	if redirect.Scheme != "https" {
		host := strings.ToLower(redirect.Hostname())
		if redirect.Scheme != "http" || (host != "127.0.0.1" && host != "[::1]" && host != "::1") {
			return errors.New("redirect_uri must use HTTPS, except for an IP loopback HTTP callback")
		}
	}
	return nil
}

// Config is the versioned, non-secret configuration file.
type Config struct {
	Version        int                `json:"version"`
	DefaultProfile string             `json:"default_profile,omitempty"`
	Profiles       map[string]Profile `json:"profiles"`
}

// New returns an empty configuration using the current schema.
func New() Config {
	return Config{Version: CurrentVersion, Profiles: make(map[string]Profile)}
}

// Validate rejects unsupported schemas, invalid profile names and invalid settings.
func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported configuration version %d (want %d)", c.Version, CurrentVersion)
	}
	if c.Profiles == nil {
		return errors.New("profiles must be an object")
	}
	for name, profile := range c.Profiles {
		if err := ValidateProfileName(name); err != nil {
			return err
		}
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
	}
	if c.DefaultProfile != "" {
		if _, ok := c.Profiles[c.DefaultProfile]; !ok {
			return fmt.Errorf("default profile %q does not exist", c.DefaultProfile)
		}
	}
	return nil
}

// ResolveProfile returns an explicitly named profile, or the configured default.
func (c Config) ResolveProfile(name string) (string, Profile, error) {
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		return "", Profile{}, errors.New("a profile is required and no default_profile is configured")
	}
	if err := ValidateProfileName(name); err != nil {
		return "", Profile{}, err
	}
	profile, ok := c.Profiles[name]
	if !ok {
		return "", Profile{}, fmt.Errorf("profile %q does not exist", name)
	}
	return name, profile, nil
}

// Repository stores a configuration at Path.
type Repository struct {
	Path string
}

// DefaultPath returns the platform-specific user configuration path.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, "freeagent", "config.json"), nil
}

// Load reads and strictly decodes the configuration.
func (r Repository) Load() (Config, error) {
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result Config
	if err := decoder.Decode(&result); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("decode configuration: trailing JSON value")
	}
	if err := result.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate configuration: %w", err)
	}
	return result, nil
}

// Save validates and atomically replaces the configuration file.
func (r Repository) Save(value Config) error {
	if r.Path == "" {
		return errors.New("configuration path is required")
	}
	if err := value.Validate(); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(r.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect temporary configuration: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary configuration: %w", err)
	}
	if err := replaceFile(temporaryName, r.Path); err != nil {
		return fmt.Errorf("replace configuration: %w", err)
	}
	committed = true
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync configuration directory: %w", err)
	}
	return nil
}
