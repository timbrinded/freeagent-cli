package protocol

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const confirmationPrefix = "fa1_"

const DefaultConfirmationMaxAge = 10 * time.Minute

// MutationPlan is both the dry-run output and the canonical input to the
// confirmation-token algorithm. Body is decoded JSON so insignificant spacing
// changes do not invalidate a token.
type MutationPlan struct {
	Command           string          `json:"command"`
	Profile           string          `json:"profile"`
	Environment       string          `json:"environment"`
	PracticeSubdomain string          `json:"practice_subdomain,omitempty"`
	APIVersion        string          `json:"api_version"`
	Method            string          `json:"method"`
	Path              string          `json:"path"`
	Query             json.RawMessage `json:"query"`
	Body              json.RawMessage `json:"body,omitempty"`
	ServerValidated   bool            `json:"server_validated"`
}

type DryRunResult struct {
	Plan              MutationPlan `json:"plan"`
	ConfirmationToken string       `json:"confirmation_token"`
}

func NewMutationPlan(command, profile, environment, practiceSubdomain, apiVersion, method, path string, query url.Values, body []byte) (MutationPlan, error) {
	canonicalQuery, err := canonicalQueryJSON(query)
	if err != nil {
		return MutationPlan{}, err
	}
	canonicalBody, err := canonicalJSON(body)
	if err != nil {
		return MutationPlan{}, fmt.Errorf("canonicalize request body: %w", err)
	}
	return MutationPlan{
		Command:           command,
		Profile:           profile,
		Environment:       environment,
		PracticeSubdomain: practiceSubdomain,
		APIVersion:        apiVersion,
		Method:            strings.ToUpper(method),
		Path:              path,
		Query:             canonicalQuery,
		Body:              canonicalBody,
		ServerValidated:   false,
	}, nil
}

func ConfirmationToken(plan MutationPlan) (string, error) {
	return ConfirmationTokenAt(plan, time.Now())
}

func ConfirmationTokenAt(plan MutationPlan, issuedAt time.Time) (string, error) {
	// ServerValidated is output metadata, not request identity.
	plan.ServerValidated = false
	payload, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode confirmation plan: %w", err)
	}
	timestamp := strconv.FormatInt(issuedAt.UTC().Unix(), 10)
	digest := sha256.Sum256(append(append([]byte(timestamp), 0), payload...))
	return confirmationPrefix + timestamp + "_" + base64.RawURLEncoding.EncodeToString(digest[:18]), nil
}

func VerifyConfirmationToken(plan MutationPlan, supplied string) error {
	return VerifyConfirmationTokenAt(plan, supplied, time.Now(), DefaultConfirmationMaxAge)
}

func VerifyConfirmationTokenAt(plan MutationPlan, supplied string, now time.Time, maxAge time.Duration) error {
	remainder := strings.TrimPrefix(supplied, confirmationPrefix)
	if remainder == supplied {
		return SafetyError("the confirmation token is invalid; run the command again with --dry-run")
	}
	timestampPart, digestPart, found := strings.Cut(remainder, "_")
	if !found || timestampPart == "" || digestPart == "" {
		return SafetyError("the confirmation token is invalid; run the command again with --dry-run")
	}
	issuedUnix, err := strconv.ParseInt(timestampPart, 10, 64)
	if err != nil {
		return SafetyError("the confirmation token is invalid; run the command again with --dry-run")
	}
	issuedAt := time.Unix(issuedUnix, 0).UTC()
	if maxAge <= 0 {
		maxAge = DefaultConfirmationMaxAge
	}
	age := now.UTC().Sub(issuedAt)
	if age < -time.Minute || age > maxAge {
		return SafetyError("the confirmation token has expired; run the command again with --dry-run")
	}
	expected, err := ConfirmationTokenAt(plan, issuedAt)
	if err != nil {
		return err
	}
	if len(supplied) != len(expected) || subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) != 1 {
		return SafetyError("the confirmation token does not match this request; run the command again with --dry-run")
	}
	return nil
}

func canonicalQueryJSON(values url.Values) (json.RawMessage, error) {
	canonical := make(map[string][]string, len(values))
	for key, vals := range values {
		canonical[key] = append([]string(nil), vals...)
	}
	encoded, err := json.Marshal(canonical)
	return encoded, err
}

func canonicalJSON(input []byte) (json.RawMessage, error) {
	if len(input) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("request body contains more than one JSON value")
		}
		return nil, fmt.Errorf("invalid trailing request body data: %w", err)
	}
	encoded, err := json.Marshal(value)
	return encoded, err
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return nil, fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return nil, fmt.Errorf("invalid JSON object")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return nil, fmt.Errorf("invalid JSON array")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}
