package teamsbot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// maxErrorBody caps how much of an error response body we read, so a misbehaving
// or oversized error response can't force an unbounded allocation.
const maxErrorBody = 4096

// APIError is the structured error returned by Client methods for any non-2xx
// Bot Connector response. Match it with errors.As:
//
//	var apiErr *teamsbot.APIError
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
		return fmt.Sprintf("teamsbot: bot connector %d [%s]: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("teamsbot: bot connector %d: %s", e.Status, e.Message)
}

// botError is the Bot Connector error envelope: {"error":{"code","message"}}.
type botError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseAPIError builds an *APIError from a non-2xx response. It tolerates the
// JSON envelope, plain-text bodies, and empty bodies, and recovers Retry-After
// from the header for 429s.
func parseAPIError(resp *http.Response) *APIError {
	apiErr := &APIError{Status: resp.StatusCode}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody)) //nolint:errcheck // best-effort read

	var w botError
	if json.Unmarshal(raw, &w) == nil {
		apiErr.Code = w.Error.Code
		apiErr.Message = w.Error.Message
	}
	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(raw))
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && secs > 0 {
		apiErr.RetryAfter = secs
	}
	return apiErr
}
