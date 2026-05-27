package pollyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// envAPIKey is the bearer token polly-api validates on every authenticated route.
const envAPIKey = "POLLY_API_KEY"

// Route prefixes. The base URL carries no path suffix (see ResolveAPIURL), so
// the Client appends the full versioned route, matching the polly CLI.
const (
	checkRunPathPrefix = "/v1/github/checkrun/workflow/"
	commentPath        = "/v1/github/comment"
)

// defaultTimeout bounds a single request when the caller does not supply an
// http.Client of their own or a context deadline.
const defaultTimeout = 30 * time.Second

// Client posts check-run and PR-comment payloads to polly-api. It is safe for
// concurrent use by multiple goroutines.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Option customizes a Client at construction time.
type Option func(*Client)

// WithAPIKey sets the bearer token sent in the Authorization header. When empty,
// the Authorization header is omitted (acceptable for in-cluster calls that rely
// on network policy rather than the token).
func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

// WithHTTPClient supplies a custom *http.Client (for shared transports, custom
// timeouts, or test servers). A nil client is ignored so the default is kept.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// NewClient returns a Client for the given polly-api base URL. The base URL is
// trimmed of surrounding whitespace and any trailing slash. Typically baseURL
// comes from ResolveAPIURL; see NewClientFromEnv for the env-driven shortcut.
func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewClientFromEnv resolves the base URL via ResolveAPIURL and reads the bearer
// token from POLLY_API_KEY. It returns the same errors as ResolveAPIURL, so a
// caller treating polly as optional can check
// errors.Is(err, ErrNotConfigured). Explicit options override the env-derived
// values.
func NewClientFromEnv(opts ...Option) (*Client, error) {
	base, err := ResolveAPIURL()
	if err != nil {
		return nil, err
	}
	merged := append([]Option{WithAPIKey(os.Getenv(envAPIKey))}, opts...)
	return NewClient(base, merged...), nil
}

// UpsertCheckRun posts a check-run payload to the workflow endpoint for the
// given workflow type (e.g. "build", "unittest", "offload", "custom").
//
// payload is any value that marshals to the JSON the endpoint expects —
// typically one of the request types in this package (BuildRequest,
// UnittestRequest, ...). Passing the workflow type as a string (rather than one
// method per type) keeps this client forward-compatible: a new polly-api
// workflow type works without a library bump.
//
// On a 2xx response it returns nil. On any non-2xx response it returns an
// *APIError carrying the status, code, message, and retry_after hint.
func (c *Client) UpsertCheckRun(ctx context.Context, workflowType string, payload any) error {
	wt := strings.TrimSpace(workflowType)
	if wt == "" {
		return fmt.Errorf("pollyapi: workflow type is required")
	}
	return c.postJSON(ctx, checkRunPathPrefix+wt, payload)
}

// Comment upserts a GitHub PR comment via polly-api. polly-api finds an existing
// comment by req.Marker and edits it, or creates a new one.
func (c *Client) Comment(ctx context.Context, req CommentRequest) error {
	return c.postJSON(ctx, commentPath, req)
}

// postJSON marshals payload, POSTs it to baseURL+path with JSON and (when set)
// bearer headers, and maps the response to nil (2xx) or an *APIError (non-2xx).
func (c *Client) postJSON(ctx context.Context, path string, payload any) error {
	if c.baseURL == "" {
		return fmt.Errorf("pollyapi: client base URL is empty")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("pollyapi: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pollyapi: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("pollyapi: post request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return parseAPIError(resp)
}
