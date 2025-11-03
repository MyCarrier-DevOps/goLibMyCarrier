package argocdclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetArgoApplicationResourceTree_Success(t *testing.T) {
	resourceTree := map[string]interface{}{
		"nodes": []interface{}{
			map[string]interface{}{
				"kind": "Application",
				"name": "test-app",
				"health": map[string]interface{}{
					"status": "Healthy",
				},
			},
		},
	}
	body, _ := json.Marshal(resourceTree)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check request headers
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("expected Authorization header 'Bearer token', got '%s'", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type 'application/json', got '%s'", r.Header.Get("Content-Type"))
		}
		// Check URL path
		expectedPath := "/api/v1/applications/test-app/resource-tree"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path '%s', got '%s'", expectedPath, r.URL.Path)
		}
		w.WriteHeader(200)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	result, err := client.GetArgoApplicationResourceTree("test-app")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	nodes, ok := result["nodes"].([]interface{})
	if !ok {
		t.Fatal("expected nodes to be an array")
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(nodes))
	}
}

func TestGetArgoApplicationResourceTree_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		if _, err := w.Write([]byte("application not found")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetArgoApplicationResourceTree("nonexistent-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "client error 404: application not found" {
		t.Errorf("expected specific client error message, got: %v", err)
	}
}

func TestGetArgoApplicationResourceTree_ServerErrorWithRetry(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(500)
			if _, err := w.Write([]byte("server error")); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		} else {
			// Success on third attempt
			resourceTree := map[string]interface{}{"status": "success"}
			body, _ := json.Marshal(resourceTree)
			w.WriteHeader(200)
			if _, err := w.Write(body); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	result, err := client.GetArgoApplicationResourceTree("test-app")
	if err != nil {
		t.Fatalf("expected no error after retries, got %v", err)
	}
	if result["status"] != "success" {
		t.Errorf("expected status 'success', got %v", result["status"])
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (2 retries), got %d", callCount)
	}
}

func TestGetArgoApplicationResourceTree_MaxRetriesExceeded(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(500)
		if _, err := w.Write([]byte("persistent server error")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetArgoApplicationResourceTree("test-app")
	if err == nil {
		t.Fatal("expected error after max retries, got nil")
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (max retries), got %d", callCount)
	}
	expectedErrMsg := "failed after 3 attempts"
	if err.Error()[:len(expectedErrMsg)] != expectedErrMsg {
		t.Errorf("expected error to start with '%s', got: %v", expectedErrMsg, err)
	}
}

func TestGetArgoApplicationResourceTree_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		if _, err := w.Write([]byte("not valid json")); err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetArgoApplicationResourceTree("test-app")
	if err == nil {
		t.Fatal("expected JSON unmarshal error, got nil")
	}
	expectedErrMsg := "failed after 3 attempts, last error: error unmarshalling ArgoCD resource tree data"
	if len(err.Error()) < len(expectedErrMsg) || err.Error()[:len(expectedErrMsg)] != expectedErrMsg {
		t.Errorf("expected error to start with '%s', got: %v", expectedErrMsg, err)
	}
}

func TestGetArgoApplicationResourceTree_RequestCreationError(t *testing.T) {
	c := &Client{baseUrl: "http://%%invalid-url", authToken: "token"}
	_, err := c.GetArgoApplicationResourceTree("test-app")
	if err == nil {
		t.Fatal("expected error for invalid request creation, got nil")
	}
}
