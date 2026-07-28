package teamsbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	// The one thing this module exists to do: ship the Adaptive Card with the
	// right content type so Teams renders it as a card, not raw JSON text.
	activity, _ := gotBody["activity"].(map[string]any)
	attachments, _ := activity["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("activity.attachments = %v, want exactly 1", activity["attachments"])
	}
	attachment, _ := attachments[0].(map[string]any)
	if attachment["contentType"] != AdaptiveCardContentType {
		t.Errorf(
			"activity.attachments[0].contentType = %v, want %q",
			attachment["contentType"],
			AdaptiveCardContentType,
		)
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

func TestSendToChannelEmptyBodySuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Some Bot Connector responses (e.g. 200/204) carry no body at all. That
		// must not be treated as a decode failure — the status already confirmed
		// the activity was accepted.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := staticClient(t, srv.URL)
	res, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	if err != nil {
		t.Fatalf("unexpected error for empty 2xx body: %v", err)
	}
	if res.ConversationID != "" || res.ActivityID != "" {
		t.Fatalf("got %+v, want zero SendResult", res)
	}
}

func TestSendToChannelOversizedBodyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// A JSON string value that never closes before the maxResponseBody cap:
		// the LimitReader cuts it off mid-token, forcing io.ErrUnexpectedEOF
		// rather than a clean io.EOF.
		_, _ = w.Write([]byte(`{"id":"`))
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBody+10)))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer srv.Close()

	c := staticClient(t, srv.URL)
	_, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	if err == nil || !errors.Is(err, ErrBadResponse) {
		t.Fatalf("want ErrBadResponse, got %v", err)
	}
}

func TestSendToChannelRedirectNotFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://example.invalid/other")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	c := staticClient(t, srv.URL)
	_, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d (redirect must not be followed)", apiErr.Status, http.StatusTemporaryRedirect)
	}
}

func TestSendToChannelAttachmentNoContent(t *testing.T) {
	c := staticClient(t, "http://unused.test")

	nilContent := Activity{
		Type:        "message",
		Attachments: []Attachment{{ContentType: AdaptiveCardContentType, Content: nil}},
	}
	if _, err := c.SendToChannel(context.Background(), "19:abc", "t", nilContent); err == nil {
		t.Error("expected error for nil attachment content")
	}

	nullContent := Activity{
		Type:        "message",
		Attachments: []Attachment{{ContentType: AdaptiveCardContentType, Content: json.RawMessage("null")}},
	}
	if _, err := c.SendToChannel(context.Background(), "19:abc", "t", nullContent); err == nil {
		t.Error("expected error for \"null\" attachment content")
	}
}

// countingTransport wraps an http.RoundTripper and counts invocations, letting
// tests prove a specific transport instance was actually used to make requests
// (not merely that some client somewhere succeeded).
type countingTransport struct {
	base  http.RoundTripper
	calls int
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return t.base.RoundTrip(req)
}

func TestWithHTTPClientOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"conv-2","activityId":"act-2"}`))
	}))
	defer srv.Close()

	cfg := &Config{AppID: "id", AppSecret: "s", TenantID: "t", ServiceURL: srv.URL}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok", TokenType: "Bearer"})

	custom := &countingTransport{base: http.DefaultTransport}
	c := NewClient(cfg, WithTokenSource(ts), WithHTTPClient(&http.Client{Transport: custom}))

	// Transport identity: the exact RoundTripper instance passed to
	// WithHTTPClient must be the one wired into the client, not a copy or a
	// fresh default. (We deliberately do not assert on Timeout — NewClient
	// normalizes it as part of hardening.)
	if c.httpClient.Transport != custom {
		t.Fatalf("client.httpClient.Transport = %v, want the injected transport %v", c.httpClient.Transport, custom)
	}

	res, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ConversationID != "conv-2" || res.ActivityID != "act-2" {
		t.Fatalf("got %+v", res)
	}
	if custom.calls != 1 {
		t.Fatalf(
			"injected transport calls = %d, want 1 (the injected transport must actually carry the request)",
			custom.calls,
		)
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

// TestNewClientServiceURLShapeError pins the behavior that NewClient (which has
// no error return) defers a malformed ServiceURL to first use: construction
// succeeds, but SendToChannel surfaces the shape-validation error instead of
// silently sending to a bad URL. NewClient itself must not reach for the https
// check (only ValidateServiceURLShape) — that stricter rule belongs to
// LoadConfig's validateConfig — so http:// httptest servers keep working
// elsewhere in this file.
func TestNewClientServiceURLShapeError(t *testing.T) {
	cfg := &Config{AppID: "id", AppSecret: "s", TenantID: "t", ServiceURL: "https://h/teams?x=1"}
	c := NewClient(cfg)

	_, err := c.SendToChannel(context.Background(), "19:abc", "t", TextActivity("hi"))
	if err == nil {
		t.Fatal("expected a ServiceURL shape validation error")
	}
	if !strings.Contains(err.Error(), "query or fragment") {
		t.Fatalf("err = %v, want the ValidateServiceURLShape query/fragment error", err)
	}
}

// recordingRoundTripper captures the single request it receives (URL and body)
// and returns a canned OAuth2 client-credentials token response, so tests can
// inspect exactly what newTokenSource sent to the Entra ID token endpoint
// without making a real network call.
type recordingRoundTripper struct {
	req  *http.Request
	body []byte
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.req = req
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		rt.body = b
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(
			bytes.NewReader([]byte(`{"access_token":"tok-123","token_type":"Bearer","expires_in":3600}`)),
		),
	}
	return resp, nil
}

// TestTokenSourceExchange pins the actual client-credentials exchange request:
// the token endpoint URL is single-tenant, and the form body carries exactly
// the fields the Bot Connector's Entra ID app registration expects.
func TestTokenSourceExchange(t *testing.T) {
	rt := &recordingRoundTripper{}
	cfg := &Config{
		AppID:      "app-id-1",
		AppSecret:  "app-secret-1",
		TenantID:   "tenant-xyz",
		ServiceURL: "https://unused.test/teams",
	}
	c := NewClient(cfg, WithHTTPClient(&http.Client{Transport: rt}))

	tok, err := c.tokenSource.Token()
	if err != nil {
		t.Fatalf("Token() unexpected error: %v", err)
	}
	if tok.AccessToken != "tok-123" {
		t.Fatalf("AccessToken = %q, want %q", tok.AccessToken, "tok-123")
	}

	if rt.req == nil {
		t.Fatal("expected the token endpoint to be called")
	}
	wantURL := "https://login.microsoftonline.com/tenant-xyz/oauth2/v2.0/token"
	if got := rt.req.URL.String(); got != wantURL {
		t.Errorf("token URL = %q, want %q", got, wantURL)
	}

	form, err := url.ParseQuery(string(rt.body))
	if err != nil {
		t.Fatalf("parse token request body: %v", err)
	}
	if got := form.Get("grant_type"); got != "client_credentials" {
		t.Errorf("grant_type = %q, want %q", got, "client_credentials")
	}
	if got := form.Get("client_id"); got != "app-id-1" {
		t.Errorf("client_id = %q, want %q", got, "app-id-1")
	}
	if got := form.Get("client_secret"); got != "app-secret-1" {
		t.Errorf("client_secret = %q, want %q", got, "app-secret-1")
	}
	if got := form.Get("scope"); got != "https://api.botframework.com/.default" {
		t.Errorf("scope = %q, want %q", got, "https://api.botframework.com/.default")
	}
}
