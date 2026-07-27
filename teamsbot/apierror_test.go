package teamsbot

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func respWith(t *testing.T, status int, body, retryAfter string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	if retryAfter != "" {
		rec.Header().Set("Retry-After", retryAfter)
	}
	rec.WriteHeader(status)
	_, _ = rec.WriteString(body)
	return rec.Result()
}

func TestParseAPIError(t *testing.T) {
	t.Run("bot connector envelope", func(t *testing.T) {
		resp := respWith(t, http.StatusForbidden,
			`{"error":{"code":"BotDisabledByAdmin","message":"blocked"}}`, "")
		e := parseAPIError(resp)
		if e.Status != http.StatusForbidden || e.Code != "BotDisabledByAdmin" || e.Message != "blocked" {
			t.Fatalf("got %+v", e)
		}
	})
	t.Run("plain text body falls back to message", func(t *testing.T) {
		e := parseAPIError(respWith(t, http.StatusBadGateway, "upstream boom", ""))
		if e.Message != "upstream boom" {
			t.Fatalf("message = %q", e.Message)
		}
	})
	t.Run("empty body falls back to status text", func(t *testing.T) {
		e := parseAPIError(respWith(t, http.StatusInternalServerError, "", ""))
		if e.Message != http.StatusText(http.StatusInternalServerError) {
			t.Fatalf("message = %q", e.Message)
		}
	})
	t.Run("retry-after header", func(t *testing.T) {
		e := parseAPIError(respWith(t, http.StatusTooManyRequests, "", "12"))
		if e.RetryAfter != 12 {
			t.Fatalf("retryAfter = %d", e.RetryAfter)
		}
	})
}

func TestAPIErrorErrorString(t *testing.T) {
	withCode := &APIError{Status: 403, Code: "X", Message: "m"}
	if withCode.Error() == "" {
		t.Fatal("empty error string")
	}
	var target *APIError
	if !errors.As(error(withCode), &target) {
		t.Fatal("errors.As should match *APIError")
	}
}
