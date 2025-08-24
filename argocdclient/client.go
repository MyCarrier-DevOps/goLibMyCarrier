package argocdclient

import (
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
