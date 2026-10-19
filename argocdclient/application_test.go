package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGetApplication_Success(t *testing.T) {
	appData := map[string]interface{}{"name": "test-app"}
	body, _ := json.Marshal(appData)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	result, err := client.GetApplication("test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result["name"] != "test-app" {
		t.Errorf("expected app name 'test-app', got %v", result["name"])
	}
}

func TestGetApplication_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		if _, err := w.Write([]byte("not found")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetApplication_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		if _, err := w.Write([]byte("internal server error")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The retryable client retries 5xx errors and eventually gives up
	if !contains(err.Error(), "giving up") {
		t.Errorf("expected error message to contain 'giving up', got: %v", err)
	}
}

func TestGetApplication_UnauthorizedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		if _, err := w.Write([]byte("unauthorized")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Verify error contains "client error"
	if !contains(err.Error(), "client error") {
		t.Errorf("expected error message to contain 'client error', got: %v", err)
	}
}

func TestGetApplication_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write([]byte("invalid json")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	// Verify error is about unmarshalling
	if !contains(err.Error(), "unmarshalling") {
		t.Errorf("expected error message to contain 'unmarshalling', got: %v", err)
	}
}

func TestGetApplication_HeadersSet(t *testing.T) {
	authToken := "test-token-12345"
	var capturedAuthHeader string
	var capturedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuthHeader = r.Header.Get("Authorization")
		capturedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(200)
		if _, err := w.Write([]byte(`{"name":"test"}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: authToken})
	_, err := client.GetApplication("test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedAuth := "Bearer " + authToken
	if capturedAuthHeader != expectedAuth {
		t.Errorf("expected Authorization header %q, got %q", expectedAuth, capturedAuthHeader)
	}

	if capturedContentType != "application/json" {
		t.Errorf("expected Content-Type header 'application/json', got %q", capturedContentType)
	}
}

func TestGetApplication_URLConstruction(t *testing.T) {
	appName := "my-test-app"
	var capturedURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(200)
		if _, err := w.Write([]byte(`{"name":"test"}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication(appName)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedPath := "/api/v1/applications/" + appName
	if !contains(capturedURL, expectedPath) {
		t.Errorf("expected URL to contain %q, got %q", expectedPath, capturedURL)
	}

	if !contains(capturedURL, "refresh=soft") {
		t.Errorf("expected URL to contain 'refresh=soft', got %q", capturedURL)
	}
}

func TestGetApplication_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write([]byte(`{}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	result, err := client.GetApplication("test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestGetApplication_ComplexResponse(t *testing.T) {
	complexData := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      "test-app",
			"namespace": "argocd",
		},
		"spec": map[string]interface{}{
			"source": map[string]interface{}{
				"repoURL": "https://github.com/test/repo",
			},
		},
		"status": map[string]interface{}{
			"sync": map[string]interface{}{
				"status": "Synced",
			},
			"health": map[string]interface{}{
				"status": "Healthy",
			},
		},
	}
	body, _ := json.Marshal(complexData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	result, err := client.GetApplication("test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify nested structure
	metadata, ok := result["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("expected metadata to be a map")
	}
	if metadata["name"] != "test-app" {
		t.Errorf("expected metadata name 'test-app', got %v", metadata["name"])
	}

	status, ok := result["status"].(map[string]interface{})
	if !ok {
		t.Fatal("expected status to be a map")
	}
	sync, ok := status["sync"].(map[string]interface{})
	if !ok {
		t.Fatal("expected sync to be a map")
	}
	if sync["status"] != "Synced" {
		t.Errorf("expected sync status 'Synced', got %v", sync["status"])
	}
}

func TestGetApplication_ForbiddenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		if _, err := w.Write([]byte("forbidden")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Verify it's a client error
	if !contains(err.Error(), "client error 403") {
		t.Errorf("expected error message to contain 'client error 403', got: %v", err)
	}
}

func TestGetApplication_BadRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		if _, err := w.Write([]byte("bad request")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Verify it's a client error
	if !contains(err.Error(), "client error 400") {
		t.Errorf("expected error message to contain 'client error 400', got: %v", err)
	}
}

func TestGetApplication_ServiceUnavailableError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		if _, err := w.Write([]byte("service unavailable")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The retryable client retries 5xx errors and eventually gives up
	if !contains(err.Error(), "giving up") {
		t.Errorf("expected error message to contain 'giving up', got: %v", err)
	}
}

func TestGetApplicationWithContext_Success(t *testing.T) {
	appData := map[string]interface{}{"name": "ctx-app"}
	body, _ := json.Marshal(appData)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	result, err := client.GetApplicationWithContext(context.Background(), "ctx-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result["name"] != "ctx-app" {
		t.Errorf("expected app name 'ctx-app', got %v", result["name"])
	}
}

func TestGetApplicationWithContext_Canceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write([]byte(`{"name":"x"}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	// Keep retries deterministic so ctx errors propagate quickly.
	client.retryableClient.RetryMax = 0

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetApplicationWithContext(ctx, "test-app")
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error chain to contain context.Canceled, got %v", err)
	}
}

func TestGetApplicationWithContext_DeadlineExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the ctx deadline.
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
		if _, err := w.Write([]byte(`{"name":"x"}`)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	client.retryableClient.RetryMax = 0

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := client.GetApplicationWithContext(ctx, "test-app")
	if err == nil {
		t.Fatal("expected error from expired deadline, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected error chain to contain context.DeadlineExceeded, got %v", err)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGetApplicationWithContext_ForbiddenIsTypedAPIError(t *testing.T) {
	const body = `{"code":7,"message":"permission denied"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplicationWithContext(context.Background(), "test-app")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", apiErr.StatusCode)
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Error("expected errors.Is(err, ErrPermissionDenied)")
	}
	if want := "client error 403: " + body; err.Error() != want {
		t.Errorf("expected error %q, got %q", want, err.Error())
	}
}

// applicationListServer answers GET /api/v1/applications with the given status and body and records
// every request it receives.
type applicationListServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

func newApplicationListServer(t *testing.T, status int, body string) *applicationListServer {
	t.Helper()
	s := &applicationListServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{
			Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.Query(), Auth: r.Header.Get("Authorization"),
		})
		s.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *applicationListServer) client() *Client {
	return NewClient(&Config{ServerUrl: s.URL, AuthToken: testToken})
}

func (s *applicationListServer) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

func TestListApplications_ReturnsSortedNames(t *testing.T) {
	srv := newApplicationListServer(t, http.StatusOK, `{"metadata":{"resourceVersion":"7"},"items":[
		{"metadata":{"name":"zeta-prod"}},
		{"metadata":{"name":"production-csp-prod-api"}},
		{"metadata":{"name":"alpha-prod"}}]}`)

	got, err := srv.client().ListApplications(context.Background())
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}

	want := []string{"alpha-prod", "production-csp-prod-api", "zeta-prod"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	reqs := srv.recorded()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != "/api/v1/applications" {
		t.Errorf("request = %s %s", reqs[0].Method, reqs[0].Path)
	}
	if reqs[0].Auth != "Bearer "+testToken {
		t.Errorf("Authorization = %q", reqs[0].Auth)
	}
	if fields := reqs[0].Query.Get("fields"); fields != "items.metadata.name" {
		t.Errorf("fields = %q, want items.metadata.name", fields)
	}
}

func TestListApplications_EmptyList(t *testing.T) {
	for _, body := range []string{`{"items":null}`, `{"items":[]}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			srv := newApplicationListServer(t, http.StatusOK, body)

			got, err := srv.client().ListApplications(context.Background())
			if err != nil {
				t.Fatalf("ListApplications: %v", err)
			}
			if got == nil || len(got) != 0 {
				t.Errorf("names = %#v, want an empty non-nil slice", got)
			}
		})
	}
}

func TestListApplications_Errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		wantIs error
	}{
		{"permission denied", http.StatusForbidden, `{"code":7,"message":"permission denied"}`, ErrPermissionDenied},
		{"unauthorized", http.StatusUnauthorized, `{"code":16,"message":"invalid session"}`, nil},
		{"malformed body", http.StatusOK, "not json", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newApplicationListServer(t, tt.status, tt.body)

			got, err := srv.client().ListApplications(context.Background())

			if err == nil {
				t.Fatalf("expected error, got %v", got)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("expected errors.Is(%v), got %v", tt.wantIs, err)
			}
			if !strings.Contains(err.Error(), "applications") {
				t.Errorf("error does not say what failed: %v", err)
			}
		})
	}
}

func TestListApplications_CancelledContext(t *testing.T) {
	srv := newApplicationListServer(t, http.StatusOK, `{"items":[]}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := srv.client().ListApplications(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
