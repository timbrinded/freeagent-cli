package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"
)

type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go_version"`
}

type Profile struct {
	Name        string `json:"name"`
	Environment string `json:"environment"`
	BaseURL     string `json:"base_url"`
	ClientID    string `json:"client_id,omitempty"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	APIVersion  string `json:"api_version"`
	ReadOnly    bool   `json:"read_only"`
	Default     bool   `json:"default"`
}

type ProfileInput struct {
	Name        string
	Environment string
	ClientID    string
	RedirectURI string
	APIVersion  string
	ReadOnly    bool
	Replace     bool
}

type ProfileService interface {
	List(context.Context) ([]Profile, error)
	Resolve(context.Context, string) (Profile, error)
	Upsert(context.Context, ProfileInput) (Profile, error)
	SetDefault(context.Context, string) error
	Remove(context.Context, string) error
}

type AuthStatus struct {
	Profile         string `json:"profile"`
	Authenticated   bool   `json:"authenticated"`
	HasClientSecret bool   `json:"has_client_secret"`
	HasToken        bool   `json:"has_token"`
	NeedsRefresh    bool   `json:"needs_refresh"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	UserURL         string `json:"user_url,omitempty"`
}

type LoginOptions struct {
	NoBrowser          bool
	Manual             bool
	Input              io.Reader
	Messages           io.Writer
	ReadManualCallback func() ([]byte, error)
}

type AuthService interface {
	Login(context.Context, string, LoginOptions) (AuthStatus, error)
	Status(context.Context, string) (AuthStatus, error)
	Logout(context.Context, string) error
	SetClientSecret(context.Context, string, []byte) error
	DeleteClientSecret(context.Context, string) error
}

type APIRequest struct {
	Method            string
	Path              string
	Query             url.Values
	Body              []byte
	Mutation          bool
	PracticeSubdomain string
	APIVersion        string
}

type APIResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// APIService accepts only relative /v2 paths. Implementations must reject
// absolute URLs and cross-origin redirects before adding authorization.
type APIService interface {
	Do(context.Context, string, APIRequest) (APIResponse, error)
}
