package pollyapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// maxErrorBody caps how much of an error response body we read, mirroring the
// polly CLI's 4 KiB limit, so a misbehaving or oversized error response can't
// force an unbounded allocation.
const maxErrorBody = 4096

// APIError is the structured error returned by Client methods for any non-2xx
// polly-api response. It mirrors polly-api's internal APIError: HTTP status, a
// machine-readable code, a human-readable message, and an optional retry_after
// hint (seconds) for rate-limited (429) responses.
//
// Callers can branch on it with errors.As:
//
//	var apiErr *pollyapi.APIError
//	if errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests {
//	    time.Sleep(time.Duration(apiErr.RetryAfter) * time.Second)
//	}
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter int
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("polly-api %d [%s]: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("polly-api %d: %s", e.Status, e.Message)
}

// wireError is a superset of the two error body shapes polly-api emits:
//   - the {error, code, retry_after} envelope (auth middleware, 401)
//   - Huma's application/problem+json ({title, status, detail}) for handler errors
//
// Decoding into one struct lets a single code path handle both.
type wireError struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	RetryAfter int    `json:"retry_after"`
	Detail     string `json:"detail"`
	Title      string `json:"title"`
	Status     int    `json:"status"`
}

// parseAPIError builds an *APIError from a non-2xx response. It is tolerant of
// both error body shapes and of bodies that are not JSON at all (e.g. a proxy
// returning plain text), and falls back to the Retry-After header and the
// standard status text when the body is unhelpful.
func parseAPIError(resp *http.Response) *APIError {
	apiErr := &APIError{Status: resp.StatusCode}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody)) //nolint:errcheck // best-effort read

	var w wireError
	if json.Unmarshal(raw, &w) == nil {
		apiErr.Code = w.Code
		apiErr.RetryAfter = w.RetryAfter
		apiErr.Message = firstNonEmpty(w.Error, w.Detail, w.Title)
	}

	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(raw))
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}

	// Huma's problem+json drops the provider's retry_after; recover it from the
	// Retry-After header when the body did not carry one.
	if apiErr.RetryAfter == 0 {
		if secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && secs > 0 {
			apiErr.RetryAfter = secs
		}
	}

	return apiErr
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
