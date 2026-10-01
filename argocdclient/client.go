package argocdclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// Client represents an ArgoCD client with retryable HTTP capabilities
type Client struct {
	retryableClient *retryablehttp.Client
	baseUrl         string
	authToken       string
}

// NewClient creates a new ArgoCD client with retryable HTTP configuration
func NewClient(config *Config) *Client {
	retryClient := retryablehttp.NewClient()

	// Configure retry parameters
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 1 * time.Second
	retryClient.RetryWaitMax = 4 * time.Second
	retryClient.Backoff = retryablehttp.DefaultBackoff

	// Use default retry policy (retries on 5xx and network errors)
	retryClient.CheckRetry = retryablehttp.DefaultRetryPolicy

	// Disable default logging to avoid noise
	retryClient.Logger = nil

	return &Client{
		retryableClient: retryClient,
		baseUrl:         config.ServerUrl,
		authToken:       config.AuthToken,
	}
}

// applicationURL returns the API URL of the Application appName, path-escaped.
func (c *Client) applicationURL(appName string) string {
	return fmt.Sprintf("%s/api/v1/applications/%s", c.baseUrl, url.PathEscape(appName))
}

// doGET performs an authenticated GET request against the ArgoCD API.
// It sets Authorization and Content-Type headers, handles retries via the
// retryable HTTP client, and returns *APIError for the statuses it does not retry:
// every 4xx except 429, plus 501. A 429, or a status of 500 or above other than
// 501, that is still returned after the retries are exhausted surfaces as the retry
// client's untyped "giving up after N attempt(s)" error instead.
// Returns the raw response body on success.
//
// The request honors ctx cancellation/deadline. ctx must be non-nil; callers
// should pass context.Background() explicitly if no deadline/cancellation is
// desired. Internal helper; signature change is intentional and not part of the
// public API.
func (c *Client) doGET(ctx context.Context, apiURL string) ([]byte, error) {
	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.retryableClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Intentionally ignore close error; response body already processed
			_ = closeErr
		}
	}()

	return readResponse(resp)
}

// doPOST performs an authenticated JSON POST against the ArgoCD API.
// The request is sent exactly once through the underlying http.Client and is
// never retried: ArgoCD resource actions are not idempotent (a retried resume
// can clear the next pause step), so a transport failure or 5xx must surface to
// the caller instead of being replayed.
// Returns the raw response body on success and *APIError for statuses of 400 and above.
func (c *Client) doPOST(ctx context.Context, apiURL string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.retryableClient.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	return readResponse(resp)
}

// readResponse reads the body of resp and returns it for 2xx/3xx statuses.
// Any status of 400 or above yields an *APIError carrying the raw body, so
// callers can match it with errors.Is/errors.As.
func readResponse(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			return nil, fmt.Errorf("error reading body of %d response: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	return body, nil
}
