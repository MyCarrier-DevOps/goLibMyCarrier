package teamsbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// defaultTimeout bounds each HTTP call the client makes — the token fetch and
// the send — when no shorter context deadline applies.
const defaultTimeout = 30 * time.Second

// maxResponseBody caps the success-path read. SendResult is two short IDs.
const maxResponseBody = 1 << 20

// ErrBadResponse marks a 2xx whose body could not be used.
var ErrBadResponse = errors.New("teamsbot: decode response")

// Sender posts activities to Microsoft Teams via the Bot Connector. Client
// implements it; teamsbottest.MockSender is the test double.
type Sender interface {
	SendToChannel(ctx context.Context, channelID, tenantID string, act Activity) (SendResult, error)
}

// Client sends proactive messages to Teams channels through the Bot Connector
// REST API. It is safe for concurrent use by multiple goroutines.
type Client struct {
	serviceURL  string
	tokenSource oauth2.TokenSource
	httpClient  *http.Client
	// initErr is set at construction time when serviceURL fails shape
	// validation. NewClient has no error return, so the send methods surface it
	// on first use instead of the client silently sending to a bad URL.
	initErr error
}

// Option customizes a Client at construction time.
type Option func(*Client)

// WithHTTPClient supplies a custom *http.Client. A nil client is ignored.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithTokenSource overrides the OAuth2 token source. Used in tests to inject a
// static token, avoiding a real call to Entra ID. A nil source is ignored.
func WithTokenSource(ts oauth2.TokenSource) Option {
	return func(c *Client) {
		if ts != nil {
			c.tokenSource = ts
		}
	}
}

// NewClient builds a Client from a validated Config. It constructs a
// client-credentials token source from the config unless WithTokenSource
// overrides it. A nil cfg (or one with an empty ServiceURL) falls back to the
// global Bot Connector URL.
//
// Token fetches are bounded by the client's HTTP timeout (defaultTimeout,
// unless overridden via WithHTTPClient): the token source is built after
// options are applied, with that http.Client injected via oauth2.HTTPClient.
// Note the per-call ctx passed to SendToChannel governs only the send — it
// does not bound a cached token's background refresh, since oauth2 bakes in
// the context given at token-source construction time. This is an oauth2
// design limitation, not something this package can change.
func NewClient(cfg *Config, opts ...Option) *Client {
	su := ""
	if cfg != nil {
		su = cfg.ServiceURL
	}
	if strings.TrimSpace(su) == "" {
		su = defaultServiceURL
	}

	c := &Client{
		serviceURL: strings.TrimRight(strings.TrimSpace(su), "/"),
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
	if err := ValidateServiceURLShape(c.serviceURL); err != nil {
		c.initErr = err
	}
	for _, opt := range opts {
		opt(c)
	}
	// Harden whatever client we ended up with (default or WithHTTPClient) before
	// the token source captures it, so the token fetch inherits the same bounds.
	// Copied by value so we never mutate a client the caller may share.
	hardened := *c.httpClient
	if hardened.Timeout == 0 {
		hardened.Timeout = defaultTimeout
	}
	hardened.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	c.httpClient = &hardened
	if c.tokenSource == nil { // WithTokenSource may have set it
		if cfg == nil {
			cfg = &Config{}
		}
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, c.httpClient)
		c.tokenSource = newTokenSource(ctx, cfg)
	}
	return c
}

// NewClientFromEnv loads Config from the environment (see LoadConfig) and returns
// a Client. It returns the config error unchanged when required vars are missing.
func NewClientFromEnv(opts ...Option) (*Client, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	return NewClient(cfg, opts...), nil
}

// ThreadSender extends Sender with calls that edit a posted activity in place
// and reply in its thread. Client implements it; teamsbottest.MockSender is the
// test double. Sender itself is unchanged so existing implementations keep
// compiling.
type ThreadSender interface {
	Sender
	UpdateActivity(ctx context.Context, conversationID, activityID string, act Activity) error
	ReplyToActivity(ctx context.Context, conversationID, activityID string, act Activity) (SendResult, error)
}

var _ ThreadSender = (*Client)(nil)

// SendToChannel posts act to the Teams channel identified by channelID within
// tenantID, creating a new conversation (thread) with the activity embedded. It
// returns the resulting conversation and activity IDs. On any non-2xx response
// it returns an *APIError.
func (c *Client) SendToChannel(ctx context.Context, channelID, tenantID string, act Activity) (SendResult, error) {
	var zero SendResult
	if c.initErr != nil {
		return zero, c.initErr
	}
	if strings.TrimSpace(channelID) == "" {
		return zero, fmt.Errorf("teamsbot: channelID is required")
	}
	if strings.TrimSpace(tenantID) == "" {
		return zero, fmt.Errorf("teamsbot: tenantID is required")
	}

	if err := validateAttachments(act.Attachments); err != nil {
		return zero, err
	}
	// A new conversation has no activity to edit or reply to.
	act.ID, act.ReplyToID = "", ""
	params := conversationParameters{
		IsGroup: true,
		ChannelData: channelData{
			Channel: channelInfo{ID: channelID},
			Tenant:  tenantInfo{ID: tenantID},
		},
		Activity: act,
	}
	var result SendResult
	if err := c.do(ctx, http.MethodPost, "/v3/conversations", "conversation", params, &result); err != nil {
		return zero, err
	}
	return result, nil
}

// UpdateActivity replaces the activity activityID in conversation
// conversationID with act, editing the posted message in place. A 2xx response
// is success and its body is ignored. On any non-2xx response it returns an
// *APIError.
func (c *Client) UpdateActivity(ctx context.Context, conversationID, activityID string, act Activity) error {
	if c.initErr != nil {
		return c.initErr
	}
	path, err := activityPath(conversationID, activityID)
	if err != nil {
		return err
	}
	if err := validateAttachments(act.Attachments); err != nil {
		return err
	}
	act.ID, act.ReplyToID = activityID, ""
	return c.do(ctx, http.MethodPut, path, "activity", act, nil)
}

// ReplyToActivity posts act as a reply to activity activityID in conversation
// conversationID. It returns the conversation ID and the ID of the new reply
// activity. ActivityID is "" when the 2xx response has an empty body. On any
// non-2xx response it returns an *APIError.
func (c *Client) ReplyToActivity(
	ctx context.Context,
	conversationID, activityID string,
	act Activity,
) (SendResult, error) {
	var zero SendResult
	if c.initErr != nil {
		return zero, c.initErr
	}
	path, err := activityPath(conversationID, activityID)
	if err != nil {
		return zero, err
	}
	if err := validateAttachments(act.Attachments); err != nil {
		return zero, err
	}
	act.ID, act.ReplyToID = "", activityID
	// The reply response carries the new activity's ID in "id". SendResult's
	// "id" tag is the conversation ID, so decode into a private struct.
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, path, "activity", act, &resp); err != nil {
		return zero, err
	}
	return SendResult{ConversationID: conversationID, ActivityID: resp.ID}, nil
}

// activityPath builds the Bot Connector path of one activity. Each ID is
// escaped as a path segment: Teams conversation IDs contain ";" and "@".
func activityPath(conversationID, activityID string) (string, error) {
	if strings.TrimSpace(conversationID) == "" {
		return "", fmt.Errorf("teamsbot: conversationID is required")
	}
	if strings.TrimSpace(activityID) == "" {
		return "", fmt.Errorf("teamsbot: activityID is required")
	}
	return "/v3/conversations/" + url.PathEscape(conversationID) +
		"/activities/" + url.PathEscape(activityID), nil
}

// validateAttachments rejects an attachment with no content, which Teams would
// render as an empty card.
func validateAttachments(atts []Attachment) error {
	for i := range atts {
		if content := bytes.TrimSpace(atts[i].Content); len(content) == 0 || string(content) == "null" {
			return fmt.Errorf("teamsbot: attachment %d has no content", i)
		}
	}
	return nil
}

// do sends one JSON request to the Bot Connector and decodes a 2xx body into
// out. A nil out ignores the body. An empty 2xx body is success. Non-2xx
// responses become an *APIError. path must already be escaped. what names the
// payload in the marshal error text ("conversation", "activity").
func (c *Client) do(
	ctx context.Context,
	method, path, what string,
	payload any,
	out any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("teamsbot: marshal %s: %w", what, err)
	}

	token, err := c.tokenSource.Token()
	if err != nil {
		return fmt.Errorf("teamsbot: acquire token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.serviceURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("teamsbot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	token.SetAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("teamsbot: send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp)
	}
	if out == nil {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody)) //nolint:errcheck // best-effort drain
		return nil
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		switch {
		case errors.Is(err, io.EOF):
			// 200/204 with an empty body: the status already said the activity
			// was accepted, so there is nothing to decode and nothing failed.
			return nil
		case errors.Is(err, io.ErrUnexpectedEOF):
			return fmt.Errorf("%w: body exceeds %d bytes", ErrBadResponse, maxResponseBody)
		default:
			return fmt.Errorf("%w: %w", ErrBadResponse, err)
		}
	}
	return nil
}
