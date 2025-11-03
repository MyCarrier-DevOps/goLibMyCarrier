package argocdclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
