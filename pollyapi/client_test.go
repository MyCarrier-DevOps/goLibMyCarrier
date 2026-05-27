package pollyapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// capture records what the test server received.
type capture struct {
	path        string
	method      string
	contentType string
	authz       string
	body        []byte
}

// newServer returns a server that records the request into cap and replies with
// the given status (no body), plus its base URL.
func newServer(t *testing.T, cap *capture, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.path = r.URL.Path
		cap.method = r.Method
		cap.contentType = r.Header.Get("Content-Type")
		cap.authz = r.Header.Get("Authorization")
		cap.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestNewClient_TrimsBaseURL(t *testing.T) {
	t.Parallel()
	c := NewClient("  http://example.com/  ")
	if c.baseURL != "http://example.com" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, "http://example.com")
	}
}

func TestUpsertCheckRun_Success(t *testing.T) {
	t.Parallel()
	var cap capture
	url := newServer(t, &cap, http.StatusNoContent)

	c := NewClient(url, WithAPIKey("secret"))
	req := BuildRequest{
		BaseCheckRun: BaseCheckRun{Owner: "o", Repo: "r", SHA: "s", RunID: "w"},
		Tag:          "t",
		GrafanaURL:   "u",
	}
	if err := c.UpsertCheckRun(context.Background(), "build", req); err != nil {
		t.Fatalf("UpsertCheckRun: %v", err)
	}

	if cap.path != "/v1/github/checkrun/workflow/build" {
		t.Errorf("path = %q", cap.path)
	}
	if cap.method != http.MethodPost {
		t.Errorf("method = %q", cap.method)
	}
	if cap.contentType != "application/json" {
		t.Errorf("content-type = %q", cap.contentType)
	}
	if cap.authz != "Bearer secret" {
		t.Errorf("authz = %q", cap.authz)
	}

	var got BuildRequest
	if err := json.Unmarshal(cap.body, &got); err != nil {
		t.Fatalf("server received non-JSON body: %v", err)
	}
	if got.Tag != "t" || got.Owner != "o" {
		t.Errorf("server received unexpected body: %+v", got)
	}
}

func TestUpsertCheckRun_NoAPIKeyOmitsAuthHeader(t *testing.T) {
	t.Parallel()
	var cap capture
	url := newServer(t, &cap, http.StatusNoContent)

	c := NewClient(url)
	if err := c.UpsertCheckRun(context.Background(), "custom", CustomRequest{Name: "X"}); err != nil {
		t.Fatalf("UpsertCheckRun: %v", err)
	}
	if cap.authz != "" {
		t.Errorf("expected no Authorization header, got %q", cap.authz)
	}
}

func TestUpsertCheckRun_EmptyWorkflowType(t *testing.T) {
	t.Parallel()
	c := NewClient("http://example.com")
	if err := c.UpsertCheckRun(context.Background(), "   ", struct{}{}); err == nil {
		t.Error("expected error for empty workflow type")
	}
}

func TestComment_Success(t *testing.T) {
	t.Parallel()
	var cap capture
	url := newServer(t, &cap, http.StatusNoContent)

	c := NewClient(url, WithAPIKey("k"))
	req := CommentRequest{Owner: "o", Repo: "r", PR: 7, Marker: "<!-- m -->", Body: "hi"}
	if err := c.Comment(context.Background(), req); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if cap.path != "/v1/github/comment" {
		t.Errorf("path = %q", cap.path)
	}
	var got CommentRequest
	if err := json.Unmarshal(cap.body, &got); err != nil || got.PR != 7 {
		t.Errorf("unexpected body: %s (err=%v)", cap.body, err)
	}
}

func TestWithHTTPClient_NilIgnored(t *testing.T) {
	t.Parallel()
	c := NewClient("http://example.com", WithHTTPClient(nil))
	if c.httpClient == nil {
		t.Error("nil http client should have been ignored, leaving the default")
	}
}

func TestWithHTTPClient_Custom(t *testing.T) {
	t.Parallel()
	custom := &http.Client{}
	c := NewClient("http://example.com", WithHTTPClient(custom))
	if c.httpClient != custom {
		t.Error("custom http client was not applied")
	}
}

func TestPostJSON_ErrorShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		status         int
		contentType    string
		body           string
		retryAfterHdr  string
		wantCode       string
		wantMessage    string
		wantRetryAfter int
	}{
		{
			name:        "envelope shape (auth 401)",
			status:      http.StatusUnauthorized,
			contentType: "application/json",
			body:        `{"error":"missing or invalid bearer token","code":"UNAUTHORIZED"}`,
			wantCode:    "UNAUTHORIZED",
			wantMessage: "missing or invalid bearer token",
		},
		{
			name:           "envelope with retry_after",
			status:         http.StatusTooManyRequests,
			contentType:    "application/json",
			body:           `{"error":"rate limited","code":"RATE_LIMITED","retry_after":42}`,
			wantCode:       "RATE_LIMITED",
			wantMessage:    "rate limited",
			wantRetryAfter: 42,
		},
		{
			name:        "huma problem+json shape (handler error)",
			status:      http.StatusBadGateway,
			contentType: "application/problem+json",
			body:        `{"title":"Bad Gateway","status":502,"detail":"github unavailable: boom"}`,
			wantMessage: "github unavailable: boom",
		},
		{
			name:           "problem+json plus Retry-After header",
			status:         http.StatusTooManyRequests,
			contentType:    "application/problem+json",
			body:           `{"title":"Too Many Requests","status":429,"detail":"slow down"}`,
			retryAfterHdr:  "17",
			wantMessage:    "slow down",
			wantRetryAfter: 17,
		},
		{
			name:        "plain text body",
			status:      http.StatusBadGateway,
			contentType: "text/plain",
			body:        "upstream exploded",
			wantMessage: "upstream exploded",
		},
		{
			name:        "empty body falls back to status text",
			status:      http.StatusServiceUnavailable,
			contentType: "text/plain",
			body:        "",
			wantMessage: http.StatusText(http.StatusServiceUnavailable),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				if tc.retryAfterHdr != "" {
					w.Header().Set("Retry-After", tc.retryAfterHdr)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			c := NewClient(srv.URL)
			err := c.Comment(context.Background(), CommentRequest{})
			if err == nil {
				t.Fatal("expected error")
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error is not *APIError: %v", err)
			}
			if apiErr.Status != tc.status {
				t.Errorf("Status = %d, want %d", apiErr.Status, tc.status)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.wantCode)
			}
			if apiErr.Message != tc.wantMessage {
				t.Errorf("Message = %q, want %q", apiErr.Message, tc.wantMessage)
			}
			if apiErr.RetryAfter != tc.wantRetryAfter {
				t.Errorf("RetryAfter = %d, want %d", apiErr.RetryAfter, tc.wantRetryAfter)
			}
			if apiErr.Error() == "" {
				t.Error("APIError.Error() returned empty string")
			}
		})
	}
}

func TestPostJSON_EmptyBaseURL(t *testing.T) {
	t.Parallel()
	c := NewClient("")
	if err := c.Comment(context.Background(), CommentRequest{}); err == nil {
		t.Error("expected error for empty base URL")
	}
}

func TestPostJSON_MarshalError(t *testing.T) {
	t.Parallel()
	c := NewClient("http://example.com")
	// A channel cannot be marshalled to JSON.
	if err := c.UpsertCheckRun(context.Background(), "build", make(chan int)); err == nil {
		t.Error("expected marshal error")
	}
}

func TestPostJSON_BadURL(t *testing.T) {
	t.Parallel()
	c := NewClient("://bad-url")
	if err := c.Comment(context.Background(), CommentRequest{}); err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestPostJSON_TransportError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // close immediately so the connection is refused

	c := NewClient(url)
	if err := c.Comment(context.Background(), CommentRequest{}); err == nil {
		t.Error("expected transport error against closed server")
	}
}

func TestNewClientFromEnv(t *testing.T) {
	var cap capture
	url := newServer(t, &cap, http.StatusNoContent)

	saveEnv(t, envAPIURL)
	saveEnv(t, envK8sNamespace)
	saveEnv(t, envAPIKey)
	t.Setenv(envAPIURL, url)
	t.Setenv(envAPIKey, "env-key")

	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("NewClientFromEnv: %v", err)
	}
	if err := c.Comment(context.Background(), CommentRequest{}); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if cap.authz != "Bearer env-key" {
		t.Errorf("authz = %q, want %q", cap.authz, "Bearer env-key")
	}
}

func TestNewClientFromEnv_NotConfigured(t *testing.T) {
	saveEnv(t, envAPIURL)
	saveEnv(t, envK8sNamespace)
	os.Unsetenv(envAPIURL)
	os.Unsetenv(envK8sNamespace)

	_, err := NewClientFromEnv()
	if !errors.Is(err, ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestNewClientFromEnv_OptionOverridesEnvKey(t *testing.T) {
	var cap capture
	url := newServer(t, &cap, http.StatusNoContent)

	saveEnv(t, envAPIURL)
	saveEnv(t, envK8sNamespace)
	saveEnv(t, envAPIKey)
	t.Setenv(envAPIURL, url)
	t.Setenv(envAPIKey, "env-key")

	c, err := NewClientFromEnv(WithAPIKey("explicit-key"))
	if err != nil {
		t.Fatalf("NewClientFromEnv: %v", err)
	}
	if err := c.Comment(context.Background(), CommentRequest{}); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if cap.authz != "Bearer explicit-key" {
		t.Errorf("authz = %q, want explicit key to override env key", cap.authz)
	}
}
