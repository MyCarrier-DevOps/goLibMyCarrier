package argocdclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/go-retryablehttp"
)

// mockRetryableClient implements HTTPClientInterface for testing
// It returns a custom response for each test

// ...existing code...

type mockRetryableClient struct {
	Response *http.Response
	Err      error
}

func (m *mockRetryableClient) Do(req *retryablehttp.Request) (*http.Response, error) {
	return m.Response, m.Err
}

func TestGetApplication_Success(t *testing.T) {
	appData := map[string]interface{}{"name": "test-app"}
	body, _ := json.Marshal(appData)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(body)
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
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetManifests_Success(t *testing.T) {
	manifests := []string{"manifest1", "manifest2"}
	body, _ := json.Marshal(manifests)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(body)
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
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetManifests("", "test-app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
func TestGetApplication_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("not a json"))
	}))
	defer server.Close()

	client := NewClient(&Config{ServerUrl: server.URL, AuthToken: "token"})
	_, err := client.GetApplication("test-app")
	if err == nil || err.Error() == "" {
		t.Fatal("expected JSON unmarshal error, got nil")
	}
}

func TestGetApplication_RequestCreationError(t *testing.T) {
	c := &Client{retryableClient: retryablehttp.NewClient(), baseUrl: "http://%%invalid-url", authToken: "token"}
	_, err := c.GetApplication("test-app")
	if err == nil {
		t.Fatal("expected error for invalid request creation, got nil")
	}
}

func TestGetManifests_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("not a json"))
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
