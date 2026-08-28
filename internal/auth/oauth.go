// Package auth implements FreeAgent's OAuth authorization-code flow and
// coordinated rotating-token storage.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultMaximumResponseBytes int64 = 1 << 20
const defaultHTTPTimeout = 30 * time.Second
const maximumTokenLifetime = 365 * 24 * time.Hour

var (
	ErrStateMismatch = errors.New("OAuth state does not match")
	ErrAccessDenied  = errors.New("OAuth authorization was denied")
)

// Endpoints are the two FreeAgent OAuth endpoints for one environment.
type Endpoints struct {
	AuthorizationURL string
	TokenURL         string
}

// EndpointsForBase constructs FreeAgent's fixed OAuth paths on one validated origin.
func EndpointsForBase(base *url.URL) (Endpoints, error) {
	if base == nil || !base.IsAbs() || base.Host == "" || base.User != nil || base.Fragment != "" || base.RawQuery != "" {
		return Endpoints{}, errors.New("a clean absolute OAuth base URL is required")
	}
	if !secureOrLoopbackHTTP(base.Scheme, base.Hostname()) {
		return Endpoints{}, errors.New("OAuth base URL must use HTTPS except for an IP-loopback test server")
	}
	origin := &url.URL{Scheme: base.Scheme, Host: base.Host}
	return Endpoints{
		AuthorizationURL: origin.ResolveReference(&url.URL{Path: "/v2/approve_app"}).String(),
		TokenURL:         origin.ResolveReference(&url.URL{Path: "/v2/token_endpoint"}).String(),
	}, nil
}

func secureOrLoopbackHTTP(scheme, hostname string) bool {
	return scheme == "https" || scheme == "http" && (hostname == "127.0.0.1" || hostname == "::1")
}

// Credentials are public and secret OAuth client settings for one operation.
type Credentials struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// Token is the credential returned by FreeAgent. It is stored as one unit so a
// rotated refresh token cannot be separated from its access token.
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// ValidAt reports whether the token can safely be used without a refresh.
func (t Token) ValidAt(now time.Time, skew time.Duration) bool {
	return t.AccessToken != "" && !t.Expiry.IsZero() && now.Add(skew).Before(t.Expiry)
}

// OAuthError is a structured token endpoint or authorization response error.
type OAuthError struct {
	StatusCode  int
	Code        string
	Description string
	RequestID   string
	Body        []byte
}

func (e *OAuthError) Error() string {
	message := "FreeAgent OAuth request failed"
	if e.Code != "" {
		message += ": " + e.Code
	}
	if e.Description != "" {
		message += ": " + e.Description
	}
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.StatusCode)
	}
	return message
}

// Client performs FreeAgent OAuth requests. All mutable dependencies are injectable.
type Client struct {
	Endpoints        Endpoints
	HTTPClient       *http.Client
	Rand             io.Reader
	Now              func() time.Time
	MaximumBodyBytes int64
	UserAgent        string
}

func (c *Client) httpClient() *http.Client {
	underlying := c.HTTPClient
	if underlying == nil {
		underlying = http.DefaultClient
	}
	result := *underlying
	if result.Timeout <= 0 {
		result.Timeout = defaultHTTPTimeout
	}
	tokenEndpoint, err := url.Parse(c.Endpoints.TokenURL)
	if err != nil {
		return &result
	}
	originalPolicy := underlying.CheckRedirect
	result.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if !sameOAuthOrigin(request.URL, tokenEndpoint) {
			return errors.New("refusing cross-origin OAuth redirect")
		}
		if len(via) > 0 && via[0].Method != http.MethodGet && via[0].Method != http.MethodHead {
			return errors.New("refusing to redirect an OAuth token request")
		}
		if originalPolicy != nil {
			return originalPolicy(request, via)
		}
		return nil
	}
	return &result
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) random() io.Reader {
	if c.Rand != nil {
		return c.Rand
	}
	return rand.Reader
}

// GenerateState creates a mandatory, unpredictable OAuth state value.
func (c *Client) GenerateState() (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(c.random(), buffer); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// AuthorizationURL builds the URL that must be opened by the approving human.
func (c *Client) AuthorizationURL(credentials Credentials, state string) (string, error) {
	if err := validateCredentials(credentials, false); err != nil {
		return "", err
	}
	if state == "" {
		return "", errors.New("OAuth state is required")
	}
	endpoint, _, err := c.validatedEndpoints()
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", credentials.ClientID)
	query.Set("redirect_uri", credentials.RedirectURI)
	query.Set("state", state)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func sameOAuthOrigin(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

func (c *Client) validatedEndpoints() (*url.URL, *url.URL, error) {
	authorization, authorizationErr := url.Parse(c.Endpoints.AuthorizationURL)
	token, tokenErr := url.Parse(c.Endpoints.TokenURL)
	if authorizationErr != nil || authorization == nil || !authorization.IsAbs() || authorization.Host == "" {
		return nil, nil, errors.New("valid absolute authorization endpoint is required")
	}
	if tokenErr != nil || token == nil || !token.IsAbs() || token.Host == "" {
		return nil, nil, errors.New("valid absolute token endpoint is required")
	}
	if authorization.User != nil || authorization.Fragment != "" || authorization.RawQuery != "" || token.User != nil || token.Fragment != "" || token.RawQuery != "" {
		return nil, nil, errors.New("OAuth endpoints must not contain user information, queries, or fragments")
	}
	if !sameOAuthOrigin(authorization, token) {
		return nil, nil, errors.New("authorization and token endpoints must use the same origin")
	}
	if !secureOrLoopbackHTTP(authorization.Scheme, authorization.Hostname()) {
		return nil, nil, errors.New("OAuth endpoints must use HTTPS except for an IP-loopback test server")
	}
	return authorization, token, nil
}

// Exchange exchanges a short-lived authorization code using HTTP Basic client authentication.
func (c *Client) Exchange(ctx context.Context, credentials Credentials, code string) (Token, error) {
	if strings.TrimSpace(code) == "" {
		return Token{}, errors.New("authorization code is required")
	}
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {credentials.RedirectURI},
	}
	return c.requestToken(ctx, credentials, form, "")
}

// Refresh exchanges and rotates a refresh token using HTTP Basic client authentication.
func (c *Client) Refresh(ctx context.Context, credentials Credentials, refreshToken string) (Token, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return Token{}, errors.New("refresh token is required")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	token, err := c.requestToken(ctx, credentials, form, refreshToken)
	if err != nil {
		return Token{}, err
	}
	if token.RefreshToken == "" {
		// OAuth permits an unchanged refresh token to be omitted. FreeAgent
		// normally rotates it, but retaining the old token is safer than erasing it.
		token.RefreshToken = refreshToken
	}
	return token, nil
}

func validateCredentials(credentials Credentials, requireSecret bool) error {
	if strings.TrimSpace(credentials.ClientID) == "" {
		return errors.New("OAuth client ID is required")
	}
	if requireSecret && credentials.ClientSecret == "" {
		return errors.New("OAuth client secret is required")
	}
	redirect, err := url.Parse(credentials.RedirectURI)
	if err != nil || !redirect.IsAbs() || redirect.Host == "" || redirect.Fragment != "" || redirect.RawQuery != "" || redirect.RawPath != "" || redirect.User != nil || redirect.Path == "" {
		return errors.New("valid absolute OAuth redirect URI is required")
	}
	if !secureOrLoopbackHTTP(redirect.Scheme, redirect.Hostname()) {
		return errors.New("OAuth redirect URI must use HTTPS except for an IP loopback callback")
	}
	return nil
}

func (c *Client) requestToken(ctx context.Context, credentials Credentials, form url.Values, priorRefreshToken string) (Token, error) {
	if err := validateCredentials(credentials, true); err != nil {
		return Token{}, err
	}
	_, endpoint, err := c.validatedEndpoints()
	if err != nil {
		return Token{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("build token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	userAgent := c.UserAgent
	if userAgent == "" {
		userAgent = "freeagent-cli"
	}
	if strings.ContainsAny(userAgent, "\r\n") {
		return Token{}, errors.New("OAuth User-Agent contains a newline")
	}
	request.Header.Set("User-Agent", userAgent)
	// RFC 6749 section 2.3.1 requires form encoding before Basic encoding.
	request.SetBasicAuth(url.QueryEscape(credentials.ClientID), url.QueryEscape(credentials.ClientSecret))
	response, err := c.httpClient().Do(request)
	if err != nil {
		return Token{}, fmt.Errorf("send token request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	limit := c.MaximumBodyBytes
	if limit <= 0 {
		limit = defaultMaximumResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return Token{}, fmt.Errorf("read token response: %w", err)
	}
	if int64(len(body)) > limit {
		return Token{}, fmt.Errorf("token response exceeds %d bytes", limit)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Token{}, decodeOAuthError(response, body)
	}
	var wire struct {
		AccessToken      string          `json:"access_token"`
		TokenType        string          `json:"token_type"`
		RefreshToken     string          `json:"refresh_token"`
		ExpiresIn        json.RawMessage `json:"expires_in"`
		Error            string          `json:"error"`
		ErrorDescription string          `json:"error_description"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&wire); err != nil {
		return Token{}, fmt.Errorf("decode token response: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Token{}, errors.New("decode token response: trailing JSON value")
	}
	if wire.Error != "" {
		return Token{}, &OAuthError{StatusCode: response.StatusCode, Code: wire.Error, Description: wire.ErrorDescription, RequestID: response.Header.Get("X-Request-ID"), Body: append([]byte(nil), body...)}
	}
	if wire.AccessToken == "" {
		return Token{}, errors.New("token response does not contain access_token")
	}
	seconds, err := parseExpiresIn(wire.ExpiresIn)
	if err != nil {
		return Token{}, err
	}
	if wire.RefreshToken == "" {
		wire.RefreshToken = priorRefreshToken
	}
	return Token{
		AccessToken:  wire.AccessToken,
		TokenType:    wire.TokenType,
		RefreshToken: wire.RefreshToken,
		Expiry:       c.now().Add(time.Duration(seconds) * time.Second),
	}, nil
}

func parseExpiresIn(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, errors.New("token response does not contain expires_in")
	}
	var number json.Number
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, fmt.Errorf("decode expires_in: %w", err)
		}
		number = json.Number(value)
	} else {
		number = json.Number(string(raw))
	}
	seconds, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || seconds <= 0 || seconds > int64(maximumTokenLifetime/time.Second) {
		return 0, errors.New("token response contains invalid expires_in")
	}
	return seconds, nil
}

func decodeOAuthError(response *http.Response, body []byte) error {
	result := &OAuthError{
		StatusCode: response.StatusCode,
		RequestID:  response.Header.Get("X-Request-ID"),
		Body:       append([]byte(nil), body...),
	}
	var wire struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Message          string `json:"message"`
	}
	if json.Unmarshal(body, &wire) == nil {
		result.Code = wire.Error
		result.Description = wire.ErrorDescription
		if result.Description == "" {
			result.Description = wire.Message
		}
	}
	return result
}

// ParseCallbackURL validates the exact registered redirect target and mandatory state.
// It is also the manual-flow parser: callers pass the complete redirected URL.
func ParseCallbackURL(callbackURL, registeredRedirectURI, expectedState string) (string, error) {
	if expectedState == "" {
		return "", errors.New("expected OAuth state is required")
	}
	registered, err := url.Parse(registeredRedirectURI)
	if err != nil || !registered.IsAbs() || registered.Host == "" || registered.Path == "" || registered.RawPath != "" || registered.RawQuery != "" || registered.Fragment != "" || registered.User != nil {
		return "", errors.New("valid registered redirect URI is required")
	}
	callback, err := url.Parse(callbackURL)
	if err != nil || !callback.IsAbs() {
		return "", errors.New("callback must be a complete absolute URL")
	}
	if callback.Scheme != registered.Scheme || callback.Host != registered.Host || callback.EscapedPath() != registered.EscapedPath() || callback.User != nil || callback.Fragment != "" {
		return "", errors.New("callback URL does not match the registered redirect URI")
	}
	query := callback.Query()
	if len(query["state"]) != 1 {
		return "", errors.New("callback must contain exactly one state value")
	}
	if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(expectedState)) != 1 {
		return "", ErrStateMismatch
	}
	if len(query["error"]) > 1 || len(query["error_description"]) > 1 {
		return "", errors.New("callback contains duplicate OAuth error values")
	}
	if oauthCode := query.Get("error"); oauthCode != "" {
		description := query.Get("error_description")
		if oauthCode == "access_denied" {
			return "", fmt.Errorf("%w: %s", ErrAccessDenied, description)
		}
		return "", &OAuthError{Code: oauthCode, Description: description}
	}
	if len(query["code"]) != 1 || query.Get("code") == "" {
		return "", errors.New("callback must contain exactly one authorization code")
	}
	return query.Get("code"), nil
}
