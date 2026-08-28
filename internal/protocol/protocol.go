// Package protocol defines the stable process contract exposed by the CLI.
// Keep these types independent from Cobra and the FreeAgent transport so that
// callers can rely on them across implementation changes.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const Version = "1"

// ExitCode is part of the public process contract. Do not renumber existing
// values after a stable release.
type ExitCode int

const (
	ExitSuccess        ExitCode = 0
	ExitUsage          ExitCode = 2
	ExitAuth           ExitCode = 3
	ExitSafety         ExitCode = 4
	ExitRejected       ExitCode = 5
	ExitRateLimited    ExitCode = 6
	ExitTransient      ExitCode = 7
	ExitOutcomeUnknown ExitCode = 8
	ExitPartial        ExitCode = 9
	ExitInvariant      ExitCode = 10
	ExitInterrupted    ExitCode = 130
)

// Meta describes the execution context without exposing credentials.
type Meta struct {
	Command           string `json:"command,omitempty"`
	Profile           string `json:"profile,omitempty"`
	Environment       string `json:"environment,omitempty"`
	PracticeSubdomain string `json:"practice_subdomain,omitempty"`
	APIVersion        string `json:"api_version,omitempty"`
	Page              int    `json:"page,omitempty"`
	PerPage           int    `json:"per_page,omitempty"`
	TotalCount        int    `json:"total_count,omitempty"`
	Items             int    `json:"items,omitempty"`
	BytesRead         int64  `json:"bytes_read,omitempty"`
	Complete          *bool  `json:"complete,omitempty"`
	NextPage          int    `json:"next_page,omitempty"`
}

type SuccessEnvelope struct {
	ProtocolVersion string `json:"protocol_version"`
	OK              bool   `json:"ok"`
	Data            any    `json:"data"`
	Meta            Meta   `json:"meta,omitempty"`
}

// Reconciliation identifies the exact request whose mutation result must be
// checked before an agent takes another action. Query values and the request
// body are never exposed; their SHA-256 values digest the exact wire data.
type Reconciliation struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	QuerySHA256 string `json:"query_sha256"`
	BodySHA256  string `json:"body_sha256"`
}

type ErrorDetail struct {
	Kind              string          `json:"kind"`
	Message           string          `json:"message"`
	HTTPStatus        int             `json:"http_status,omitempty"`
	RequestID         string          `json:"request_id,omitempty"`
	Retryable         bool            `json:"retryable"`
	RetryAfterSeconds int             `json:"retry_after_seconds,omitempty"`
	Outcome           string          `json:"outcome,omitempty"`
	Details           json.RawMessage `json:"details,omitempty"`
	Reconciliation    *Reconciliation `json:"reconciliation,omitempty"`
}

type ErrorEnvelope struct {
	ProtocolVersion string      `json:"protocol_version"`
	OK              bool        `json:"ok"`
	Error           ErrorDetail `json:"error"`
	Meta            Meta        `json:"meta,omitempty"`
}

// CommandError is the common error exchanged between command handlers and the
// root executor. Cause is never serialized. Details must already be redacted.
type CommandError struct {
	Code       ExitCode
	Kind       string
	Message    string
	HTTPStatus int
	RequestID  string
	Retryable  bool
	RetryAfter time.Duration
	Outcome    string
	Details    json.RawMessage
	// Reconciliation is set only when the caller must inspect FreeAgent state
	// before deciding whether it is safe to repeat a mutation.
	Reconciliation *Reconciliation
	Cause          error
	// Silent means that a structured result was already emitted. It is used for
	// partial JSONL output, where stdout contains items and stderr gets no second
	// copy of the failure.
	Silent bool
}

func (e *CommandError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Kind
}

func (e *CommandError) Unwrap() error { return e.Cause }

func (e *CommandError) Detail() ErrorDetail {
	retryAfter := 0
	if e.RetryAfter > 0 {
		retryAfter = int(e.RetryAfter.Round(time.Second) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
	}
	return ErrorDetail{
		Kind:              defaultString(e.Kind, "command_failed"),
		Message:           e.Error(),
		HTTPStatus:        e.HTTPStatus,
		RequestID:         e.RequestID,
		Retryable:         e.Retryable,
		RetryAfterSeconds: retryAfter,
		Outcome:           e.Outcome,
		Details:           e.Details,
		Reconciliation:    e.Reconciliation,
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func UsageError(format string, args ...any) *CommandError {
	return &CommandError{Code: ExitUsage, Kind: "invalid_input", Message: fmt.Sprintf(format, args...)}
}

func SafetyError(format string, args ...any) *CommandError {
	return &CommandError{Code: ExitSafety, Kind: "safety_policy", Message: fmt.Sprintf(format, args...), Outcome: "not_applied"}
}

func InvariantError(err error) *CommandError {
	return &CommandError{Code: ExitInvariant, Kind: "internal_error", Message: "the command could not complete", Cause: err}
}

// AsCommandError converts unknown implementation errors to a stable transient
// failure. Adapters should return a more specific CommandError when possible.
func AsCommandError(err error) *CommandError {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		return commandErr
	}
	return &CommandError{
		Code:      ExitTransient,
		Kind:      "transport_error",
		Message:   "the command could not communicate with FreeAgent",
		Retryable: true,
		Outcome:   "not_applied",
		Cause:     err,
	}
}

// HTTPError maps an upstream status to the public exit classes.
func HTTPError(status int, message, requestID string, retryAfter time.Duration, details json.RawMessage) *CommandError {
	err := &CommandError{
		Code:       ExitRejected,
		Kind:       "api_rejected",
		Message:    message,
		HTTPStatus: status,
		RequestID:  requestID,
		Outcome:    "not_applied",
		Details:    details,
	}
	switch {
	case status == http.StatusUnauthorized:
		err.Code = ExitAuth
		err.Kind = "authentication_required"
	case status == http.StatusForbidden:
		err.Code = ExitSafety
		err.Kind = "permission_denied"
	case status == http.StatusTooManyRequests:
		err.Code = ExitRateLimited
		err.Kind = "rate_limited"
		err.Retryable = true
		err.RetryAfter = retryAfter
	case status >= 500:
		err.Code = ExitTransient
		err.Kind = "upstream_unavailable"
		err.Retryable = true
	}
	return err
}
