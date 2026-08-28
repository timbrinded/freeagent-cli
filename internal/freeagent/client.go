// Package freeagent provides the guarded HTTP boundary for the FreeAgent API.
package freeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	pathpkg "path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

const (
	defaultMaximumBodyBytes int64 = 16 << 20
	defaultReadRetries            = 2
	defaultHTTPTimeout            = 30 * time.Second
)

var (
	apiVersionPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// AccessTokenSource obtains a bearer token without exposing its refresh mechanics.
type AccessTokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

// SleepFunc makes retry waiting cancellable and testable.
type SleepFunc func(ctx context.Context, duration time.Duration) error

// Options configure one immutable API context.
type Options struct {
	BaseURL          *url.URL
	HTTPClient       *http.Client
	TokenSource      AccessTokenSource
	UserAgent        string
	APIVersion       string
	ReadRetries      int
	MaximumBodyBytes int64
	Sleep            SleepFunc
	Backoff          func(retry int) time.Duration
	Now              func() time.Time
}

// Client is safe for concurrent requests. It does not store per-request headers.
type Client struct {
	baseURL          *url.URL
	httpClient       *http.Client
	tokenSource      AccessTokenSource
	userAgent        string
	apiVersion       string
	readRetries      int
	maximumBodyBytes int64
	sleep            SleepFunc
	backoff          func(int) time.Duration
	now              func() time.Time
}

// Request is one API operation. Path must be relative to the fixed FreeAgent
// origin, or an absolute-path reference beginning with /v2.
type Request struct {
	Method            string
	Path              string
	Query             url.Values
	JSONBody          []byte
	PracticeSubdomain string
}

// Metadata identifies the exact API context used for a response.
type Metadata struct {
	Method            string `json:"method"`
	Path              string `json:"path"`
	Query             string `json:"query,omitempty"`
	APIVersion        string `json:"api_version"`
	PracticeSubdomain string `json:"practice_subdomain,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	Attempts          int    `json:"attempts"`
}

// Response is the bounded response body and selected transport metadata.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Metadata   Metadata
}

// APIError preserves FreeAgent's inconsistent error body while exposing stable fields.
type APIError struct {
	StatusCode int
	RequestID  string
	Messages   []string
	RetryAfter time.Duration
	Body       []byte
	Metadata   Metadata
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("FreeAgent API returned HTTP %d", e.StatusCode)
	if len(e.Messages) > 0 {
		message += ": " + strings.Join(e.Messages, "; ")
	}
	return message
}

// TransportError describes a request that did not produce an HTTP response.
type TransportError struct {
	Method   string
	Path     string
	Sent     bool
	Attempts int
	Err      error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("FreeAgent %s %s transport failed after %d attempt(s): %v", e.Method, e.Path, e.Attempts, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

// OutcomeUnknownError means a mutation was written to the network but no
// authoritative response arrived. Callers must reconcile state before a repeat.
type OutcomeUnknownError struct {
	Operation Metadata
	Err       error
}

func (e *OutcomeUnknownError) Error() string {
	return fmt.Sprintf("FreeAgent mutation outcome is unknown for %s %s: %v", e.Operation.Method, e.Operation.Path, e.Err)
}

func (e *OutcomeUnknownError) Unwrap() error { return e.Err }

// NewClient validates and freezes a transport context.
func NewClient(options Options) (*Client, error) {
	if options.BaseURL == nil || !options.BaseURL.IsAbs() || options.BaseURL.Host == "" || options.BaseURL.User != nil || options.BaseURL.Fragment != "" || options.BaseURL.RawQuery != "" {
		return nil, errors.New("a clean absolute FreeAgent base URL is required")
	}
	if options.BaseURL.Scheme != "https" && options.BaseURL.Scheme != "http" {
		return nil, errors.New("FreeAgent base URL must use HTTPS")
	}
	if options.BaseURL.Scheme == "http" {
		host := options.BaseURL.Hostname()
		if host != "127.0.0.1" && host != "::1" {
			return nil, errors.New("FreeAgent base URL must use HTTPS except for an IP-loopback test server")
		}
	}
	if options.BaseURL.RawPath != "" || pathpkg.Clean(options.BaseURL.Path) != "/v2" {
		return nil, errors.New("FreeAgent base URL path must be /v2")
	}
	if options.TokenSource == nil {
		return nil, errors.New("FreeAgent access token source is required")
	}
	if strings.TrimSpace(options.UserAgent) == "" || strings.ContainsAny(options.UserAgent, "\r\n") {
		return nil, errors.New("a safe non-empty User-Agent is required")
	}
	if !apiVersionPattern.MatchString(options.APIVersion) {
		return nil, errors.New("FreeAgent API version must use YYYY-MM-DD")
	}
	if _, err := time.Parse(time.DateOnly, options.APIVersion); err != nil {
		return nil, errors.New("FreeAgent API version must be a valid calendar date")
	}
	base := cloneURL(options.BaseURL)
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	underlying := options.HTTPClient
	if underlying == nil {
		underlying = http.DefaultClient
	}
	httpClient := *underlying
	if httpClient.Timeout <= 0 {
		httpClient.Timeout = defaultHTTPTimeout
	}
	originalRedirectPolicy := underlying.CheckRedirect
	httpClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if !sameOrigin(request.URL, base) {
			return errors.New("refusing cross-origin FreeAgent redirect")
		}
		if len(via) > 0 && !isReadOnlyMethod(via[0].Method) {
			return errors.New("refusing to redirect a FreeAgent mutation")
		}
		if originalRedirectPolicy != nil {
			return originalRedirectPolicy(request, via)
		}
		return nil
	}
	retries := options.ReadRetries
	if retries == 0 {
		retries = defaultReadRetries
	}
	if retries < -1 {
		return nil, errors.New("read retries must be -1 (disabled) or non-negative")
	}
	if retries == -1 {
		retries = 0
	}
	maximum := options.MaximumBodyBytes
	if maximum <= 0 {
		maximum = defaultMaximumBodyBytes
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	backoff := options.Backoff
	if backoff == nil {
		backoff = defaultBackoff
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		baseURL: base, httpClient: &httpClient, tokenSource: options.TokenSource,
		userAgent: options.UserAgent, apiVersion: options.APIVersion,
		readRetries: retries, maximumBodyBytes: maximum, sleep: sleep, backoff: backoff, now: now,
	}, nil
}

// Do sends one guarded API request.
func (c *Client) Do(ctx context.Context, operation Request) (*Response, error) {
	method := strings.ToUpper(strings.TrimSpace(operation.Method))
	if method == "" {
		method = http.MethodGet
	}
	endpoint, canonicalPath, err := c.resolvePath(operation.Path, operation.Query)
	if err != nil {
		return nil, err
	}
	if err := protocol.ValidatePracticeSubdomain(operation.PracticeSubdomain); err != nil {
		return nil, err
	}
	if len(operation.JSONBody) > 0 && !json.Valid(operation.JSONBody) {
		return nil, errors.New("request JSON body is invalid")
	}
	readOnly := isReadOnlyMethod(method)
	maximumAttempts := 1
	if readOnly {
		maximumAttempts += c.readRetries
	}
	metadata := Metadata{
		Method: method, Path: canonicalPath, APIVersion: c.apiVersion,
		PracticeSubdomain: operation.PracticeSubdomain, Query: endpoint.RawQuery,
	}
	var lastErr error
	for attempt := 1; attempt <= maximumAttempts; attempt++ {
		metadata.Attempts = attempt
		response, wroteRequest, requestErr := c.doOnce(ctx, method, endpoint, operation, metadata)
		if requestErr != nil {
			transportError := &TransportError{Method: method, Path: canonicalPath, Sent: wroteRequest, Attempts: attempt, Err: requestErr}
			if !readOnly {
				if wroteRequest {
					return nil, &OutcomeUnknownError{Operation: metadata, Err: transportError}
				}
				return nil, transportError
			}
			lastErr = transportError
			if attempt == maximumAttempts || ctx.Err() != nil {
				return nil, transportError
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return response, nil
		}
		apiError := newAPIError(response, c.now())
		lastErr = apiError
		if !readOnly && response.StatusCode >= 500 {
			return response, &OutcomeUnknownError{Operation: metadata, Err: apiError}
		}
		if !readOnly || !retryableStatus(response.StatusCode) || attempt == maximumAttempts {
			return response, apiError
		}
		delay := apiError.RetryAfter
		if delay <= 0 {
			delay = c.backoff(attempt)
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) doOnce(ctx context.Context, method string, endpoint *url.URL, operation Request, metadata Metadata) (*Response, bool, error) {
	token, err := c.tokenSource.AccessToken(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get FreeAgent access token: %w", err)
	}
	if token == "" {
		return nil, false, errors.New("FreeAgent access token source returned an empty token")
	}
	var body io.Reader
	if len(operation.JSONBody) > 0 {
		body = bytes.NewReader(operation.JSONBody)
	}
	var possiblySent atomic.Bool
	trace := &httptrace.ClientTrace{
		// A server can act as soon as it receives the request headers. Treat a
		// later body-write failure as an unknown mutation outcome even when
		// WroteRequest reports an error.
		WroteHeaders: func() { possiblySent.Store(true) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				possiblySent.Store(true)
			}
		},
	}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, endpoint.String(), body)
	if err != nil {
		return nil, false, fmt.Errorf("build FreeAgent request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("X-Api-Version", c.apiVersion)
	if operation.PracticeSubdomain != "" {
		request.Header.Set("X-Subdomain", operation.PracticeSubdomain)
	}
	httpResponse, err := c.httpClient.Do(request)
	if err != nil {
		return nil, possiblySent.Load(), err
	}
	defer func() { _ = httpResponse.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, c.maximumBodyBytes+1))
	if err != nil {
		return nil, possiblySent.Load(), fmt.Errorf("read FreeAgent response: %w", err)
	}
	if int64(len(responseBody)) > c.maximumBodyBytes {
		return nil, possiblySent.Load(), fmt.Errorf("FreeAgent response exceeds %d bytes", c.maximumBodyBytes)
	}
	metadata.RequestID = httpResponse.Header.Get("X-Request-ID")
	return &Response{
		StatusCode: httpResponse.StatusCode,
		Header:     httpResponse.Header.Clone(),
		Body:       responseBody,
		Metadata:   metadata,
	}, possiblySent.Load(), nil
}

func (c *Client) resolvePath(rawPath string, query url.Values) (*url.URL, string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return nil, "", errors.New("FreeAgent request path is required")
	}
	reference, err := url.Parse(rawPath)
	if err != nil {
		return nil, "", fmt.Errorf("parse FreeAgent request path: %w", err)
	}
	if reference.IsAbs() || reference.Host != "" || reference.User != nil || reference.Opaque != "" || reference.Fragment != "" || reference.RawQuery != "" || strings.HasPrefix(rawPath, "//") {
		return nil, "", errors.New("FreeAgent request path must be relative and must not contain a query or fragment")
	}
	if hasUnsafeEncodedPath(reference.EscapedPath()) {
		return nil, "", errors.New("FreeAgent request path contains unsafe encoded separators or dot segments")
	}
	var endpoint *url.URL
	if strings.HasPrefix(reference.Path, "/") {
		if reference.Path != "/v2" && !strings.HasPrefix(reference.Path, "/v2/") {
			return nil, "", errors.New("absolute-path FreeAgent references must begin with /v2")
		}
		endpoint = cloneURL(c.baseURL)
		endpoint.Path = reference.Path
		endpoint.RawPath = reference.RawPath
	} else {
		endpoint = c.baseURL.ResolveReference(reference)
	}
	if !sameOrigin(endpoint, c.baseURL) {
		return nil, "", errors.New("FreeAgent request path resolved outside the configured origin")
	}
	endpoint.RawQuery = cloneValues(query).Encode()
	return endpoint, endpoint.EscapedPath(), nil
}

func hasUnsafeEncodedPath(escaped string) bool {
	current := escaped
	for range 5 {
		lower := strings.ToLower(current)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(current, "\\") {
			return true
		}
		decoded, err := url.PathUnescape(current)
		if err != nil {
			return true
		}
		for _, segment := range strings.Split(decoded, "/") {
			if segment == "." || segment == ".." {
				return true
			}
		}
		if decoded == current {
			return false
		}
		current = decoded
	}
	return true
}

func newAPIError(response *Response, now time.Time) *APIError {
	return &APIError{
		StatusCode: response.StatusCode,
		RequestID:  response.Metadata.RequestID,
		Messages:   extractMessages(response.Body),
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), now),
		Body:       append([]byte(nil), response.Body...),
		Metadata:   response.Metadata,
	}
}

func extractMessages(body []byte) []string {
	var value any
	if json.Unmarshal(body, &value) != nil {
		message := strings.TrimSpace(string(body))
		if message == "" {
			return nil
		}
		return []string{message}
	}
	var result []string
	collectStrings(value, &result, 8)
	return result
}

func collectStrings(value any, result *[]string, remaining int) {
	if remaining <= 0 {
		return
	}
	switch typed := value.(type) {
	case string:
		if typed != "" {
			*result = append(*result, typed)
		}
	case []any:
		for _, item := range typed {
			collectStrings(item, result, remaining-len(*result))
		}
	case map[string]any:
		before := len(*result)
		preferred := []string{"error", "errors", "message", "description"}
		for _, key := range preferred {
			if item, ok := typed[key]; ok {
				collectStrings(item, result, remaining-len(*result))
			}
		}
		if len(*result) == before {
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				collectStrings(typed[key], result, remaining-len(*result))
			}
		}
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500
}

func isReadOnlyMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func sameOrigin(first, second *url.URL) bool {
	return strings.EqualFold(first.Scheme, second.Scheme) && strings.EqualFold(first.Host, second.Host)
}

func cloneURL(value *url.URL) *url.URL {
	clone := *value
	return &clone
}

func cloneValues(values url.Values) url.Values {
	result := make(url.Values, len(values))
	for key, items := range values {
		result[key] = append([]string(nil), items...)
	}
	return result
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func defaultBackoff(retry int) time.Duration {
	delay := 200 * time.Millisecond
	for index := 1; index < retry && delay < 2*time.Second; index++ {
		delay *= 2
	}
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	return delay
}
