package slippy

import (
	"context"
	"fmt"
)

// Client is the main entry point for slippy operations.
// It provides methods for creating, loading, and managing routing slips.
//
// All slip operations use correlationID as the unique identifier.
// The correlationID is the single, canonical identifier for a routing slip
// throughout its entire lifecycle.
type Client struct {
	store  SlipStore
	github GitHubAPI
	config Config
	logger Logger
}

// NewClient creates a new slippy client with all dependencies.
// It validates the configuration and initializes the ClickHouse store and GitHub client.
func NewClient(config Config) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	if config.Logger == nil {
		config.Logger = NopLogger()
	}

	// Initialize ClickHouse store from config
	store, err := NewClickHouseStoreFromConfig(config.ClickHouseConfig, ClickHouseStoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// Initialize GitHub client for commit ancestry resolution
	githubClient, err := NewGitHubClient(config.GitHubConfig(), config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}

	return &Client{
		store:  store,
		github: githubClient,
		config: config,
		logger: config.Logger,
	}, nil
}

// NewClientWithDependencies creates a client with custom dependencies.
// This is primarily useful for testing with mock implementations.
func NewClientWithDependencies(store SlipStore, github GitHubAPI, config Config) *Client {
	if config.Logger == nil {
		config.Logger = NopLogger()
	}
	// Set defaults for unset config values
	if config.HoldTimeout == 0 {
		config.HoldTimeout = DefaultConfig().HoldTimeout
	}
	if config.PollInterval == 0 {
		config.PollInterval = DefaultConfig().PollInterval
	}
	if config.AncestryDepth == 0 {
		config.AncestryDepth = DefaultConfig().AncestryDepth
	}

	return &Client{
		store:  store,
		github: github,
		config: config,
		logger: config.Logger,
	}
}

// Load retrieves a slip by its correlation ID (the unique slip identifier).
func (c *Client) Load(ctx context.Context, correlationID string) (*Slip, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return nil, NewSlipError("load", correlationID, err)
	}
	return slip, nil
}

// LoadByCommit retrieves a slip by repository and commit SHA.
func (c *Client) LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error) {
	slip, err := c.store.LoadByCommit(ctx, repository, commitSHA)
	if err != nil {
		return nil, NewSlipError("load by commit", repository+"@"+shortSHA(commitSHA), err)
	}
	return slip, nil
}

// UpdateSlipStatus updates the overall slip status.
func (c *Client) UpdateSlipStatus(ctx context.Context, correlationID string, status SlipStatus) error {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return NewSlipError("update status", correlationID, err)
	}

	slip.Status = status
	if err := c.store.Update(ctx, slip); err != nil {
		return NewSlipError("update status", correlationID, err)
	}

	c.logger.Info(ctx, "Updated slip status", map[string]interface{}{
		"correlation_id": correlationID,
		"status":         string(status),
	})
	return nil
}

// Close releases resources held by the client.
func (c *Client) Close() error {
	if c.store != nil {
		return c.store.Close()
	}
	return nil
}

// Store returns the underlying SlipStore (useful for advanced operations).
func (c *Client) Store() SlipStore {
	return c.store
}

// GitHub returns the underlying GitHubAPI (useful for advanced operations).
func (c *Client) GitHub() GitHubAPI {
	return c.github
}

// Config returns the client configuration.
func (c *Client) Config() Config {
	return c.config
}
