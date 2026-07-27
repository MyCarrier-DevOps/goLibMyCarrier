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
	var gotPath, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
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
	cd, _ := gotBody["channelData"].(map[string]any)
	ch, _ := cd["channel"].(map[string]any)
	if ch["id"] != "19:abc@thread.tacv2" {
		t.Errorf("channel id not propagated: %v", gotBody["channelData"])
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
