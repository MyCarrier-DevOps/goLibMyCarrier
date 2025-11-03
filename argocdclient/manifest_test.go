package argocdclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/go-retryablehttp"
)

func TestGetManifests_SuccessWithRevision(t *testing.T) {
	// Create response with the correct ArgoCD API format: {"manifests": [...]}
	response := map[string]interface{}{
		"manifests": []string{
			"apiVersion: v1\nkind: Service",
			"apiVersion: apps/v1\nkind: Deployment",
		},
	}
	body, _ := json.Marshal(response)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Authorization header 'Bearer test-token', got '%s'", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type 'application/json', got '%s'", r.Header.Get("Content-Type"))
		}

		// Verify URL includes revision parameter
		expectedPath := "/api/v1/applications/test-app/manifests"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path '%s', got '%s'", expectedPath, r.URL.Path)
		}
		if r.URL.Query().Get("revision") != "abc123" {
			t.Errorf("expected revision 'abc123', got '%s'", r.URL.Query().Get("revision"))
		}

		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	result, err := client.GetManifests("abc123", "test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 manifests, got %d", len(result))
	}
	if result[0] != "apiVersion: v1\nkind: Service" {
		t.Errorf("unexpected manifest content: %s", result[0])
	}
}

func TestGetManifests_SuccessWithoutRevision(t *testing.T) {
	// Create response with the correct ArgoCD API format
	response := map[string]interface{}{
		"manifests": []string{
			"apiVersion: v1\nkind: ConfigMap",
		},
	}
	body, _ := json.Marshal(response)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify URL does not include revision parameter
		expectedPath := "/api/v1/applications/test-app/manifests"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path '%s', got '%s'", expectedPath, r.URL.Path)
		}
		if r.URL.Query().Get("revision") != "" {
			t.Errorf("expected no revision parameter, got '%s'", r.URL.Query().Get("revision"))
		}

		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	result, err := client.GetManifests("", "test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 manifest, got %d", len(result))
	}
}

func TestGetManifests_EmptyManifestsList(t *testing.T) {
	// Test when API returns empty manifests array
	response := map[string]interface{}{
		"manifests": []string{},
	}
	body, _ := json.Marshal(response)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	result, err := client.GetManifests("", "test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 manifests, got %d", len(result))
	}
}

func TestGetManifests_ClientError404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		if _, err := w.Write([]byte("application not found")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	_, err := client.GetManifests("", "nonexistent-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectedMsg := "client error 404: application not found"
	if err.Error() != expectedMsg {
		t.Errorf("expected error '%s', got '%s'", expectedMsg, err.Error())
	}
}

func TestGetManifests_ClientError401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		if _, err := w.Write([]byte("unauthorized")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "invalid-token"})
	_, err := client.GetManifests("", "test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expectedMsg := "client error 401: unauthorized"
	if err.Error() != expectedMsg {
		t.Errorf("expected error '%s', got '%s'", expectedMsg, err.Error())
	}
}

func TestGetManifests_ServerError500(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(500)
		if _, err := w.Write([]byte("internal server error")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	_, err := client.GetManifests("", "test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Verify retries happened (default RetryMax is 3, so 4 total attempts)
	if callCount != 4 {
		t.Errorf("expected 4 attempts due to retries, got %d", callCount)
	}
}

func TestGetManifests_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write([]byte("this is not valid json")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	_, err := client.GetManifests("", "test-app")
	if err == nil {
		t.Fatal("expected JSON unmarshal error, got nil")
	}
}

func TestGetManifests_MissingManifestsField(t *testing.T) {
	// Test when API returns object without "manifests" field
	response := map[string]interface{}{
		"data": []string{"some data"},
	}
	body, _ := json.Marshal(response)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	result, err := client.GetManifests("", "test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Should return empty slice when "manifests" field is missing
	if len(result) != 0 {
		t.Errorf("expected 0 manifests when field is missing, got %d", len(result))
	}
}

func TestGetManifests_WrongDataType(t *testing.T) {
	// Test when manifests field is not a string array
	response := map[string]interface{}{
		"manifests": map[string]string{
			"key": "value",
		},
	}
	body, _ := json.Marshal(response)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "test-token"})
	_, err := client.GetManifests("", "test-app")
	if err == nil {
		t.Fatal("expected unmarshal error for wrong data type, got nil")
	}
}

func TestGetManifests_Success(t *testing.T) {
	// Use the correct ArgoCD API format: {"manifests": [...]}
	response := map[string]interface{}{
		"manifests": []string{"manifest1", "manifest2"},
	}
	body, _ := json.Marshal(response)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	result, err := client.GetManifests("", "test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 manifests, got %d", len(result))
	}
}

func TestGetManifests_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		if _, err := w.Write([]byte("server error")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetManifests("", "test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetManifests_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write([]byte("not a json")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetManifests("", "test-app")
	if err == nil || err.Error() == "" {
		t.Fatal("expected JSON unmarshal error, got nil")
	}
}

func TestGetManifests_RequestCreationError(t *testing.T) {
	c := &Client{retryableClient: retryablehttp.NewClient(), baseUrl: "http://%%invalid-url", authToken: "token"}
	_, err := c.GetManifests("", "test-app")
	if err == nil {
		t.Fatal("expected error for invalid request creation, got nil")
	}
}
