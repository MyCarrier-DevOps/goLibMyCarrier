package teamsbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// defaultTimeout bounds a single request when no context deadline applies.
const defaultTimeout = 30 * time.Second

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
// overrides it.
func NewClient(cfg *Config, opts ...Option) *Client {
	c := &Client{
		serviceURL:  strings.TrimRight(strings.TrimSpace(cfg.ServiceURL), "/"),
		tokenSource: newTokenSource(context.Background(), cfg),
		httpClient:  &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
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

// SendToChannel posts act to the Teams channel identified by channelID within
// tenantID, creating a new conversation (thread) with the activity embedded. It
// returns the resulting conversation and activity IDs. On any non-2xx response
// it returns an *APIError.
func (c *Client) SendToChannel(ctx context.Context, channelID, tenantID string, act Activity) (SendResult, error) {
	var zero SendResult
	if strings.TrimSpace(channelID) == "" {
		return zero, fmt.Errorf("teamsbot: channelID is required")
	}
	if strings.TrimSpace(tenantID) == "" {
		return zero, fmt.Errorf("teamsbot: tenantID is required")
	}

	params := conversationParameters{
		IsGroup: true,
		ChannelData: channelData{
			Channel: channelInfo{ID: channelID},
			Tenant:  tenantInfo{ID: tenantID},
		},
		Activity: act,
	}
	body, err := json.Marshal(params)
	if err != nil {
		return zero, fmt.Errorf("teamsbot: marshal conversation: %w", err)
	}

	token, err := c.tokenSource.Token()
	if err != nil {
		return zero, fmt.Errorf("teamsbot: acquire token: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.serviceURL+"/v3/conversations",
		bytes.NewReader(body),
	)
	if err != nil {
		return zero, fmt.Errorf("teamsbot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	token.SetAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("teamsbot: send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, parseAPIError(resp)
	}

	var result SendResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, fmt.Errorf("teamsbot: decode response: %w", err)
	}
	return result, nil
}
