package teamsbot

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	t.Run("teams thread-blocked shape", func(t *testing.T) {
		// The message carries irregular internal whitespace (multiple spaces and
		// a newline) so the strings.Join(strings.Fields(...)) collapse in
		// parseAPIError is actually exercised — a message with only single
		// spaces would pass through unchanged and never prove the collapse ran.
		resp := respWith(t, http.StatusForbidden, `{"errorCode":209,"message":"Thread   is\nlocked."}`, "")
		e := parseAPIError(resp)
		if e.Code != "209" {
			t.Fatalf("code = %q, want %q", e.Code, "209")
		}
		if e.Message != "Thread is locked." {
			t.Fatalf("message = %q, want %q (internal whitespace must collapse)", e.Message, "Thread is locked.")
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

func TestAPIErrorErrorStringNoCode(t *testing.T) {
	noCode := &APIError{Status: 500, Message: "boom"}
	got := noCode.Error()
	if !strings.Contains(got, "500") || !strings.Contains(got, "boom") {
		t.Fatalf("error string = %q, want it to contain status and message", got)
	}
	if strings.Contains(got, "[") {
		t.Fatalf("error string = %q, want no bracketed code when Code is empty", got)
	}
}
