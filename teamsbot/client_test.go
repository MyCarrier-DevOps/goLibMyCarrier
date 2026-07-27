package teamsbot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func staticClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	cfg := &Config{AppID: "id", AppSecret: "s", TenantID: "t", ServiceURL: serverURL}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token", TokenType: "Bearer"})
	return NewClient(cfg, WithTokenSource(ts))
}

func TestSendToChannelSuccess(t *testing.T) {
	var gotPath, gotAuth, gotMethod, gotContentType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"conv-1","activityId":"act-1"}`))
	}))
	defer srv.Close()

	c := staticClient(t, srv.URL)
	card := json.RawMessage(`{"type":"AdaptiveCard"}`)
	res, err := c.SendToChannel(context.Background(), "19:abc@thread.tacv2", "tenant-1", AdaptiveCardActivity(card))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ConversationID != "conv-1" || res.ActivityID != "act-1" {
		t.Fatalf("got %+v", res)
	}
	if gotPath != "/v3/conversations" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
	if gotBody["isGroup"] != true {
		t.Errorf("isGroup = %v, want true", gotBody["isGroup"])
	}
	cd, _ := gotBody["channelData"].(map[string]any)
	ch, _ := cd["channel"].(map[string]any)
	if ch["id"] != "19:abc@thread.tacv2" {
		t.Errorf("channel id not propagated: %v", gotBody["channelData"])
	}
	tenant, _ := cd["tenant"].(map[string]any)
	if tenant["id"] != "tenant-1" {
		t.Errorf("tenant id not propagated: %v", cd["tenant"])
	}
}

func TestSendToChannelAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"nope"}}`))
	}))
	defer srv.Close()

	c := staticClient(t, srv.URL)
	_, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	var apiErr *APIError
	if err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("want APIError, got %v", err)
	}
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("errors.As failed: %v", err)
	}
}

func TestSendToChannelValidation(t *testing.T) {
	c := staticClient(t, "http://unused.test")
	if _, err := c.SendToChannel(context.Background(), "", "t", TextActivity("x")); err == nil {
		t.Error("expected error for empty channelID")
	}
	if _, err := c.SendToChannel(context.Background(), "19:abc", "", TextActivity("x")); err == nil {
		t.Error("expected error for empty tenantID")
	}
}

func TestNewClientFromEnvSuccess(t *testing.T) {
	t.Setenv("TEAMS_BOT_APP_ID", "app")
	t.Setenv("TEAMS_BOT_APP_SECRET", "secret")
	t.Setenv("TEAMS_BOT_TENANT_ID", "tenant")

	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientFromEnvMissingEnv(t *testing.T) {
	t.Setenv("TEAMS_BOT_APP_ID", "")
	t.Setenv("TEAMS_BOT_APP_SECRET", "")
	t.Setenv("TEAMS_BOT_TENANT_ID", "")

	if _, err := NewClientFromEnv(); err == nil {
		t.Fatal("expected error for missing required env vars")
	}
}

// errTokenSource is a minimal oauth2.TokenSource whose Token() always fails, used
// to exercise the token-acquisition error branch of SendToChannel.
type errTokenSource struct{}

func (errTokenSource) Token() (*oauth2.Token, error) {
	return nil, errors.New("boom: token endpoint unreachable")
}

func TestSendToChannelTokenError(t *testing.T) {
	cfg := &Config{AppID: "id", AppSecret: "s", TenantID: "t", ServiceURL: "http://unused.test"}
	c := NewClient(cfg, WithTokenSource(errTokenSource{}))

	_, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	if err == nil || !strings.Contains(err.Error(), "acquire token") {
		t.Fatalf("want token acquisition error, got %v", err)
	}
}

func TestSendToChannelDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := staticClient(t, srv.URL)
	_, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestWithHTTPClientOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"conv-2","activityId":"act-2"}`))
	}))
	defer srv.Close()

	cfg := &Config{AppID: "id", AppSecret: "s", TenantID: "t", ServiceURL: srv.URL}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok", TokenType: "Bearer"})

	c := NewClient(cfg, WithTokenSource(ts), WithHTTPClient(&http.Client{}))
	res, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ConversationID != "conv-2" || res.ActivityID != "act-2" {
		t.Fatalf("got %+v", res)
	}

	// WithHTTPClient(nil) must be ignored, leaving the default client usable.
	c2 := NewClient(cfg, WithTokenSource(ts), WithHTTPClient(nil))
	if _, err := c2.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi")); err != nil {
		t.Fatalf("unexpected error with nil WithHTTPClient override: %v", err)
	}
}

func TestNewClientNilConfig(t *testing.T) {
	c := NewClient(nil)
	if c == nil {
		t.Fatal("expected non-nil client for NewClient(nil)")
	}
	if c.serviceURL != strings.TrimRight(defaultServiceURL, "/") {
		t.Errorf("serviceURL = %q, want default %q", c.serviceURL, defaultServiceURL)
	}
	if c.tokenSource == nil {
		t.Error("expected a non-nil default token source")
	}
	if c.httpClient == nil || c.httpClient.Timeout != defaultTimeout {
		t.Errorf("httpClient = %+v, want Timeout %v", c.httpClient, defaultTimeout)
	}
}

func TestSendToChannelServiceURLTrim(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"conv-3","activityId":"act-3"}`))
	}))
	defer srv.Close()

	cfg := &Config{AppID: "id", AppSecret: "s", TenantID: "t", ServiceURL: srv.URL + "/"}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok", TokenType: "Bearer"})
	c := NewClient(cfg, WithTokenSource(ts))

	if _, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v3/conversations" {
		t.Fatalf("path = %q, want %q (no double slash)", gotPath, "/v3/conversations")
	}
}
