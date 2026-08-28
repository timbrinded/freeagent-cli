// Package runtime connects the command protocol to local configuration,
// credential, OAuth, and FreeAgent transport packages.
package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/timbrinded/freeagent-cli/internal/auth"
	"github.com/timbrinded/freeagent-cli/internal/cli"
	"github.com/timbrinded/freeagent-cli/internal/config"
	"github.com/timbrinded/freeagent-cli/internal/credential"
	"github.com/timbrinded/freeagent-cli/internal/freeagent"
	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

const maximumManualCallbackBytes = 64 << 10

type Services struct {
	Repository  config.Repository
	Credentials credential.Store
	HTTPClient  *http.Client
	UserAgent   string
	Now         func() time.Time
	OpenURL     func(string) error

	mu sync.Mutex
}

func New(repository config.Repository, credentials credential.Store, userAgent string) *Services {
	return &Services{Repository: repository, Credentials: credentials, UserAgent: userAgent, Now: time.Now, OpenURL: openURL}
}

func (s *Services) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Services) load() (config.Config, error) {
	value, err := s.Repository.Load()
	if errors.Is(err, os.ErrNotExist) {
		return config.New(), nil
	}
	return value, err
}

func (s *Services) List(_ context.Context) ([]cli.Profile, error) {
	value, err := s.load()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(value.Profiles))
	for name := range value.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	profiles := make([]cli.Profile, 0, len(names))
	for _, name := range names {
		profile, err := publicProfile(name, value.Profiles[name], name == value.DefaultProfile)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (s *Services) Resolve(_ context.Context, requested string) (cli.Profile, error) {
	value, err := s.load()
	if err != nil {
		return cli.Profile{}, err
	}
	name, profile, err := value.ResolveProfile(requested)
	if err != nil {
		return cli.Profile{}, err
	}
	return publicProfile(name, profile, name == value.DefaultProfile)
}

func (s *Services) Upsert(ctx context.Context, input cli.ProfileInput) (cli.Profile, error) {
	var result cli.Profile
	err := s.updateConfig(ctx, func(value *config.Config) error {
		existing, exists := value.Profiles[input.Name]
		if exists && !input.Replace {
			return fmt.Errorf("profile %q already exists; pass --replace to change its non-identity settings", input.Name)
		}
		profile := config.Profile{Environment: config.Environment(input.Environment), ClientID: input.ClientID, RedirectURI: input.RedirectURI, APIVersion: input.APIVersion, ReadOnly: input.ReadOnly}
		if err := profile.Validate(); err != nil {
			return err
		}
		if exists && (existing.Environment != profile.Environment || existing.ClientID != profile.ClientID || existing.RedirectURI != profile.RedirectURI) {
			return fmt.Errorf("profile %q OAuth identity cannot be replaced; remove the profile and its credentials, then add it again", input.Name)
		}
		value.Profiles[input.Name] = profile
		if value.DefaultProfile == "" {
			value.DefaultProfile = input.Name
		}
		var err error
		result, err = publicProfile(input.Name, profile, value.DefaultProfile == input.Name)
		return err
	})
	return result, err
}

func (s *Services) SetDefault(ctx context.Context, name string) error {
	return s.updateConfig(ctx, func(value *config.Config) error {
		if _, exists := value.Profiles[name]; !exists {
			return fmt.Errorf("profile %q does not exist", name)
		}
		value.DefaultProfile = name
		return nil
	})
}

func (s *Services) Remove(ctx context.Context, name string) error {
	if err := config.ValidateProfileName(name); err != nil {
		return err
	}
	return s.withProfileTokenLock(ctx, name, func() error {
		if _, err := s.currentConfiguredProfile(name); err != nil {
			return err
		}
		if err := (auth.TokenStore{Credentials: s.Credentials}).Delete(ctx, name); err != nil {
			return fmt.Errorf("delete stored OAuth token: %w", err)
		}
		if err := s.Credentials.Delete(ctx, name, credential.KindClientSecret); err != nil {
			return fmt.Errorf("delete stored client secret: %w", err)
		}
		return s.updateConfig(ctx, func(value *config.Config) error {
			if _, exists := value.Profiles[name]; !exists {
				return fmt.Errorf("profile %q does not exist", name)
			}
			delete(value.Profiles, name)
			if value.DefaultProfile == name {
				value.DefaultProfile = ""
			}
			return nil
		})
	})
}

func (s *Services) updateConfig(ctx context.Context, update func(*config.Config) error) (resultErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	locker := auth.FileLocker{Path: s.Repository.Path + ".lock"}
	unlock, err := locker.Lock(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := unlock(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	value, err := s.load()
	if err != nil {
		return err
	}
	if err := update(&value); err != nil {
		return err
	}
	return s.Repository.Save(value)
}

func publicProfile(name string, profile config.Profile, isDefault bool) (cli.Profile, error) {
	base, err := profile.BaseURL()
	if err != nil {
		return cli.Profile{}, err
	}
	return cli.Profile{
		Name: name, Environment: string(profile.Environment), BaseURL: strings.TrimSuffix(base.String(), "/"),
		ClientID: profile.ClientID, RedirectURI: profile.RedirectURI, APIVersion: profile.APIVersion,
		ReadOnly: profile.ReadOnly, Default: isDefault,
	}, nil
}

func (s *Services) SetClientSecret(ctx context.Context, profile string, secret []byte) error {
	if s.Credentials == nil {
		return errors.New("credential store is not configured")
	}
	resolved, err := s.Resolve(ctx, profile)
	if err != nil {
		return err
	}
	return s.withProfileTokenLock(ctx, resolved.Name, func() error {
		if _, err := s.currentConfiguredProfile(resolved.Name); err != nil {
			return err
		}
		return s.Credentials.Set(ctx, resolved.Name, credential.KindClientSecret, string(secret))
	})
}

func (s *Services) DeleteClientSecret(ctx context.Context, profile string) error {
	if s.Credentials == nil {
		return errors.New("credential store is not configured")
	}
	return s.Credentials.Delete(ctx, profile, credential.KindClientSecret)
}

func (s *Services) Status(ctx context.Context, profileName string) (cli.AuthStatus, error) {
	profile, err := s.Resolve(ctx, profileName)
	if err != nil {
		return cli.AuthStatus{}, err
	}
	status, err := auth.Inspect(ctx, s.Credentials, profile.Name, s.now(), 30*time.Second)
	if err != nil {
		return cli.AuthStatus{}, err
	}
	result := cli.AuthStatus{Profile: profile.Name, Authenticated: status.HasToken, HasClientSecret: status.HasClientSecret, HasToken: status.HasToken, NeedsRefresh: status.NeedsRefresh}
	if !status.ExpiresAt.IsZero() {
		result.ExpiresAt = status.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return result, nil
}

func (s *Services) Logout(ctx context.Context, profile string) error {
	return s.withProfileTokenLock(ctx, profile, func() error {
		return (auth.TokenStore{Credentials: s.Credentials}).Delete(ctx, profile)
	})
}

func (s *Services) Login(ctx context.Context, profileName string, options cli.LoginOptions) (cli.AuthStatus, error) {
	public, configured, oauthClient, err := s.oauthClient(profileName)
	if err != nil {
		return cli.AuthStatus{}, err
	}
	clientSecret, err := s.Credentials.Get(ctx, public.Name, credential.KindClientSecret)
	if err != nil {
		return cli.AuthStatus{}, err
	}
	credentials := auth.Credentials{ClientID: configured.ClientID, ClientSecret: clientSecret, RedirectURI: configured.RedirectURI}
	state, err := oauthClient.GenerateState()
	if err != nil {
		return cli.AuthStatus{}, err
	}
	authorizationURL, err := oauthClient.AuthorizationURL(credentials, state)
	if err != nil {
		return cli.AuthStatus{}, err
	}
	if options.Messages == nil {
		options.Messages = io.Discard
	}
	var code string
	if options.Manual {
		if err := s.openOrPrintAuthorization(options.Messages, authorizationURL, options.NoBrowser); err != nil {
			return cli.AuthStatus{}, err
		}
		var callback string
		if options.ReadManualCallback != nil {
			callbackBytes, readErr := options.ReadManualCallback()
			if readErr != nil {
				return cli.AuthStatus{}, readErr
			}
			defer zero(callbackBytes)
			if len(callbackBytes) > maximumManualCallbackBytes {
				return cli.AuthStatus{}, errors.New("callback URL is too large")
			}
			callback = strings.TrimSpace(string(callbackBytes))
			if callback == "" {
				return cli.AuthStatus{}, errors.New("callback URL is empty")
			}
		} else {
			callback, err = readBoundedLine(options.Input, maximumManualCallbackBytes)
			if err != nil {
				return cli.AuthStatus{}, err
			}
		}
		code, err = auth.ParseCallbackURL(callback, configured.RedirectURI, state)
		if err != nil {
			return cli.AuthStatus{}, err
		}
	} else {
		listener, err := auth.ListenLoopback(configured.RedirectURI, state)
		if err != nil {
			return cli.AuthStatus{}, fmt.Errorf("start OAuth callback listener; use --manual for a non-loopback redirect: %w", err)
		}
		defer func() { _ = listener.Close() }()
		if err = s.openOrPrintAuthorization(options.Messages, authorizationURL, options.NoBrowser); err != nil {
			return cli.AuthStatus{}, err
		}
		code, err = listener.Wait(ctx)
		if err != nil {
			return cli.AuthStatus{}, err
		}
	}
	token, err := oauthClient.Exchange(ctx, credentials, code)
	if err != nil {
		return cli.AuthStatus{}, err
	}
	if err := s.withProfileTokenLock(ctx, public.Name, func() error {
		current, err := s.currentConfiguredProfile(public.Name)
		if err != nil {
			return fmt.Errorf("profile changed during OAuth authorization; token was not stored: %w", err)
		}
		if !sameOAuthIdentity(configured, current) {
			return errors.New("profile OAuth identity changed during authorization; token was not stored")
		}
		return (auth.TokenStore{Credentials: s.Credentials}).Save(ctx, public.Name, token)
	}); err != nil {
		return cli.AuthStatus{}, err
	}
	return cli.AuthStatus{Profile: public.Name, Authenticated: true, HasClientSecret: true, HasToken: true, NeedsRefresh: false, ExpiresAt: token.Expiry.UTC().Format(time.RFC3339)}, nil
}

func (s *Services) currentConfiguredProfile(name string) (config.Profile, error) {
	value, err := s.load()
	if err != nil {
		return config.Profile{}, err
	}
	_, profile, err := value.ResolveProfile(name)
	return profile, err
}

func sameOAuthIdentity(left, right config.Profile) bool {
	return left.Environment == right.Environment &&
		left.ClientID == right.ClientID &&
		left.RedirectURI == right.RedirectURI
}

func (s *Services) profileTokenLockPath(profile string) (string, error) {
	if err := config.ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(s.Repository.Path), "locks", "profile-"+profile+".oauth.lock"), nil
}

func (s *Services) withProfileTokenLock(ctx context.Context, profile string, action func() error) (resultErr error) {
	lockPath, err := s.profileTokenLockPath(profile)
	if err != nil {
		return err
	}
	unlock, err := (auth.FileLocker{Path: lockPath}).Lock(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := unlock(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	return action()
}

func (s *Services) openOrPrintAuthorization(messages io.Writer, authorizationURL string, noBrowser bool) error {
	if noBrowser {
		_, err := fmt.Fprintf(messages, "Open this FreeAgent authorization URL:\n%s\n", authorizationURL)
		return err
	}
	opener := s.OpenURL
	if opener == nil {
		opener = openURL
	}
	if err := opener(authorizationURL); err == nil {
		return nil
	}
	_, err := fmt.Fprintf(messages, "The browser could not be opened. Open this FreeAgent authorization URL:\n%s\n", authorizationURL)
	return err
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func readBoundedLine(reader io.Reader, maximum int64) (string, error) {
	if reader == nil {
		return "", errors.New("callback input is required")
	}
	buffered := bufio.NewReader(io.LimitReader(reader, maximum+1))
	line, err := buffered.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if int64(len(line)) > maximum {
		return "", errors.New("callback URL is too large")
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("callback URL is empty")
	}
	return line, nil
}

func (s *Services) oauthClient(profileName string) (cli.Profile, config.Profile, *auth.Client, error) {
	value, err := s.load()
	if err != nil {
		return cli.Profile{}, config.Profile{}, nil, err
	}
	name, profile, err := value.ResolveProfile(profileName)
	if err != nil {
		return cli.Profile{}, config.Profile{}, nil, err
	}
	public, err := publicProfile(name, profile, name == value.DefaultProfile)
	if err != nil {
		return cli.Profile{}, config.Profile{}, nil, err
	}
	base, err := profile.OAuthBaseURL()
	if err != nil {
		return cli.Profile{}, config.Profile{}, nil, err
	}
	endpoints, err := auth.EndpointsForBase(base)
	if err != nil {
		return cli.Profile{}, config.Profile{}, nil, err
	}
	return public, profile, &auth.Client{Endpoints: endpoints, HTTPClient: s.HTTPClient, Now: s.Now, UserAgent: s.UserAgent}, nil
}

func (s *Services) Do(ctx context.Context, profileName string, request cli.APIRequest) (cli.APIResponse, error) {
	public, configured, oauthClient, err := s.oauthClient(profileName)
	if err != nil {
		return cli.APIResponse{}, err
	}
	base, err := configured.BaseURL()
	if err != nil {
		return cli.APIResponse{}, err
	}
	tokenStore := auth.TokenStore{Credentials: s.Credentials}
	lockPath, err := s.profileTokenLockPath(public.Name)
	if err != nil {
		return cli.APIResponse{}, err
	}
	manager := &auth.Manager{
		Profile: public.Name, ClientID: configured.ClientID, RedirectURI: configured.RedirectURI,
		Credentials: s.Credentials, Tokens: tokenStore, OAuth: oauthClient,
		Locker: auth.FileLocker{Path: lockPath},
		Now:    s.Now,
	}
	apiVersion := request.APIVersion
	if apiVersion == "" {
		apiVersion = configured.APIVersion
	}
	client, err := freeagent.NewClient(freeagent.Options{
		BaseURL: base, HTTPClient: s.HTTPClient, TokenSource: manager,
		UserAgent: s.UserAgent, APIVersion: apiVersion, Now: s.Now,
	})
	if err != nil {
		return cli.APIResponse{}, err
	}
	response, err := client.Do(ctx, freeagent.Request{
		Method: request.Method, Path: request.Path, Query: request.Query, JSONBody: request.Body,
		PracticeSubdomain: request.PracticeSubdomain,
	})
	var unknown *freeagent.OutcomeUnknownError
	if errors.As(err, &unknown) {
		var apiError *freeagent.APIError
		requestID := ""
		if errors.As(err, &apiError) {
			requestID = apiError.RequestID
		}
		if requestID == "" && response != nil {
			requestID = response.Metadata.RequestID
			if requestID == "" {
				requestID = response.Header.Get("X-Request-ID")
			}
		}
		return cli.APIResponse{}, &protocol.CommandError{
			Code: protocol.ExitOutcomeUnknown, Kind: "mutation_outcome_unknown",
			Message:   "the mutation was sent but FreeAgent did not return an authoritative result; inspect current state before any repeat",
			RequestID: requestID, Outcome: "unknown", Cause: err,
		}
	}
	if response != nil {
		converted := cli.APIResponse{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: append([]byte(nil), response.Body...)}
		var apiError *freeagent.APIError
		if err == nil || errors.As(err, &apiError) {
			return converted, nil
		}
	}
	if errors.Is(err, credential.ErrNotFound) {
		return cli.APIResponse{}, &protocol.CommandError{Code: protocol.ExitAuth, Kind: "authentication_required", Message: "the profile has no stored OAuth token; run auth login", Outcome: "not_applied", Cause: err}
	}
	var oauthError *auth.OAuthError
	if errors.As(err, &oauthError) {
		return cli.APIResponse{}, &protocol.CommandError{Code: protocol.ExitAuth, Kind: "authentication_required", Message: "the stored FreeAgent authorization is no longer usable; run auth login", Outcome: "not_applied", Cause: err}
	}
	var transport *freeagent.TransportError
	if errors.As(err, &transport) {
		return cli.APIResponse{}, &protocol.CommandError{Code: protocol.ExitTransient, Kind: "transport_error", Message: "the request did not reach an authoritative FreeAgent response", Retryable: true, Outcome: "not_applied", Cause: err}
	}
	return cli.APIResponse{}, err
}

func openURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("refusing to open an invalid authorization URL")
	}
	var command *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		command = exec.Command("open", value)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", value)
	default:
		command = exec.Command("xdg-open", value)
	}
	return command.Run()
}

var (
	_ cli.ProfileService = (*Services)(nil)
	_ cli.AuthService    = (*Services)(nil)
	_ cli.APIService     = (*Services)(nil)
)
