package argocdclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testErrorServer creates a mock server that returns the specified status code and message
func testErrorServer(t *testing.T, statusCode int, message string, requestCount *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requestCount++
		w.WriteHeader(statusCode)
		if _, err := w.Write([]byte(message)); err != nil {
			t.Errorf("Failed to write error response: %v", err)
		}
	}))
}

// testGetArgoApplicationError is a helper function to test error scenarios
func testGetArgoApplicationError(t *testing.T,
	statusCode int,
	message, appName, expectedError string,
	expectedRequests int,
) {
	requestCount := 0
	server := testErrorServer(t, statusCode, message, &requestCount)
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   appName,
		Revision:  "main",
	}

	result, err := GetArgoApplication("main", config)

	if err == nil {
		t.Fatal("Expected error for " + expectedError)
	}

	if !strings.Contains(err.Error(), expectedError) {
		t.Errorf("Expected error containing '%s', got %v", expectedError, err)
	}

	if requestCount != expectedRequests {
		t.Errorf("Expected %d requests, got %d", expectedRequests, requestCount)
	}

	if result != nil {
		t.Error("Expected nil result for error case")
	}
}

func TestGetArgoApplication_Success(t *testing.T) {
	// Mock response data
	mockApp := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-app",
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
		},
	}

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		expectedPath := "/api/v1/applications/test-app"
		if !strings.HasPrefix(r.URL.Path, expectedPath) {
			t.Errorf("Expected path to start with %s, got %s", expectedPath, r.URL.Path)
		}

		// Verify headers
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-token" {
			t.Errorf("Expected Authorization header 'Bearer test-token', got '%s'", authHeader)
		}

		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Expected Content-Type header 'application/json', got '%s'", contentType)
		}

		// Return mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockApp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	// Create config
	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	// Test the function
	result, err := GetArgoApplication("main", config)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify result
	if result == nil {
		t.Fatal("Expected result, got nil")
	}

	metadata, ok := result["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected metadata to be a map")
	}

	name, ok := metadata["name"].(string)
	if !ok || name != "test-app" {
		t.Errorf("Expected app name 'test-app', got '%v'", name)
	}
}

func TestGetArgoApplication_ServerError_WithRetry(t *testing.T) {
	requestCount := 0

	// Create mock server that fails first two attempts, succeeds on third
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte("Internal Server Error")); err != nil {
				t.Errorf("Failed to write error response: %v", err)
			}
			return
		}

		// Success on third attempt
		mockApp := map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test-app",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockApp); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	start := time.Now()
	result, err := GetArgoApplication("main", config)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Expected no error after retries, got %v", err)
	}

	if requestCount != 3 {
		t.Errorf("Expected 3 requests (2 retries), got %d", requestCount)
	}

	// Should take at least 3 seconds due to exponential backoff (1s + 2s)
	if duration < 3*time.Second {
		t.Errorf("Expected at least 3 seconds due to backoff, got %v", duration)
	}

	if result == nil {
		t.Fatal("Expected result after successful retry")
	}
}

func TestGetArgoApplication_ClientError_NoRetry(t *testing.T) {
	testGetArgoApplicationError(t,
		http.StatusNotFound,
		"Application not found",
		"nonexistent-app",
		"client error 404",
		1,
	)
}

func TestGetArgoApplication_MaxRetriesExceeded(t *testing.T) {
	testGetArgoApplicationError(t,
		http.StatusInternalServerError,
		"Internal Server Error",
		"test-app",
		"failed after 3 attempts",
		3,
	)
}

func TestGetManifests_Success(t *testing.T) {
	// Mock manifests response
	mockManifests := []string{
		"apiVersion: v1\nkind: Service\nmetadata:\n  name: test-service",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: test-deployment",
	}

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		expectedPath := "/api/v1/applications/test-app/manifests"
		if !strings.HasPrefix(r.URL.Path, expectedPath) {
			t.Errorf("Expected path to start with %s, got %s", expectedPath, r.URL.Path)
		}

		// Verify headers
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-token" {
			t.Errorf("Expected Authorization header 'Bearer test-token', got '%s'", authHeader)
		}

		// Return mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockManifests); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	// Create config
	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	// Test the function
	result, err := GetManifests("main", config)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify result
	if len(result) != 2 {
		t.Errorf("Expected 2 manifests, got %d", len(result))
	}

	if !strings.Contains(result[0], "kind: Service") {
		t.Error("Expected first manifest to contain 'kind: Service'")
	}

	if !strings.Contains(result[1], "kind: Deployment") {
		t.Error("Expected second manifest to contain 'kind: Deployment'")
	}
}

func TestGetManifests_WithRevision(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify revision parameter
		revision := r.URL.Query().Get("revision")
		if revision != "abc123" {
			t.Errorf("Expected revision 'abc123', got '%s'", revision)
		}

		mockManifests := []string{"test-manifest"}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockManifests); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	result, err := GetManifests("abc123", config)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(result) != 1 {
		t.Errorf("Expected 1 manifest, got %d", len(result))
	}
}

func TestGetManifests_EmptyRevision(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no revision parameter when empty string is passed
		if r.URL.RawQuery != "" {
			t.Errorf("Expected no query parameters, got '%s'", r.URL.RawQuery)
		}

		mockManifests := []string{"test-manifest"}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockManifests); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	result, err := GetManifests("", config)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(result) != 1 {
		t.Errorf("Expected 1 manifest, got %d", len(result))
	}
}

func TestGetManifests_ServerError_WithRetry(t *testing.T) {
	requestCount := 0

	// Create mock server that fails first attempt, succeeds on second
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Success on second attempt
		mockManifests := []string{"test-manifest"}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockManifests); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	result, err := GetManifests("main", config)

	if err != nil {
		t.Fatalf("Expected no error after retry, got %v", err)
	}

	if requestCount != 2 {
		t.Errorf("Expected 2 requests (1 retry), got %d", requestCount)
	}

	if len(result) != 1 {
		t.Errorf("Expected 1 manifest, got %d", len(result))
	}
}

func TestGetManifests_InvalidJSON(t *testing.T) {
	// Create mock server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("invalid json")); err != nil {
			t.Errorf("Failed to write invalid json: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	result, err := GetManifests("main", config)

	if err == nil {
		t.Fatal("Expected error for invalid JSON")
	}

	if !strings.Contains(err.Error(), "error unmarshalling") {
		t.Errorf("Expected unmarshalling error, got %v", err)
	}

	if result != nil {
		t.Error("Expected nil result for error case")
	}
}

// Benchmark tests
func BenchmarkGetArgoApplication(b *testing.B) {
	mockApp := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-app",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockApp); err != nil {
			b.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	b.ResetTimer()
	for range b.N {
		_, err := GetArgoApplication("main", config)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}

func BenchmarkGetManifests(b *testing.B) {
	mockManifests := []string{
		"apiVersion: v1\nkind: Service",
		"apiVersion: apps/v1\nkind: Deployment",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockManifests); err != nil {
			b.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	config := &Config{
		ServerUrl: server.URL,
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	b.ResetTimer()
	for range b.N {
		_, err := GetManifests("main", config)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}
