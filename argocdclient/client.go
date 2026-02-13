package argocdclient

import (
	"fmt"
	"io"
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

// doGET performs an authenticated GET request against the ArgoCD API.
// It sets Authorization and Content-Type headers, handles retries via the
// retryable HTTP client, and classifies 4xx vs 5xx errors.
// Returns the raw response body on success.
func (c *Client) doGET(url string) ([]byte, error) {
	req, err := retryablehttp.NewRequest("GET", url, nil)
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

	// Handle 4xx client errors (these weren't retried)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading body: %w", err)
		}
		return nil, fmt.Errorf("client error %d: %s", resp.StatusCode, string(body))
	}

	// Handle any remaining 5xx errors that exhausted retries
	if resp.StatusCode >= 500 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading body: %w", err)
		}
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	return body, nil
}
