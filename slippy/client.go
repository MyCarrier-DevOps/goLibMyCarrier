package slippy

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Client is the main entry point for slippy operations.
// It provides methods for creating, loading, and managing routing slips.
//
// All slip operations use correlationID as the unique identifier.
// The correlationID is the single, canonical identifier for a routing slip
// throughout its entire lifecycle.
type Client struct {
	store          SlipStore
	github         GitHubAPI
	config         Config
	pipelineConfig *PipelineConfig
	logger         Logger
}

// NewClient creates a new slippy client with all dependencies.
// It validates the configuration and initializes the ClickHouse store and GitHub client.
// The pipeline configuration must be set in the Config.
func NewClient(config Config) (*Client, error) {
	ctx := context.Background()
	startTime := time.Now()

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	if config.Logger == nil {
		config.Logger = NopLogger()
	}

	// Initialize ClickHouse store from config (migrations run based on pipeline config)
	storeStart := time.Now()
	config.Logger.Info(ctx, "Creating ClickHouse store...", nil)
	store, err := NewClickHouseStoreFromConfig(config.ClickHouseConfig, ClickHouseStoreOptions{
		PipelineConfig: config.PipelineConfig,
		Database:       config.Database,
		Logger:         config.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}
	config.Logger.Info(ctx, "ClickHouse store created", map[string]interface{}{
		"store_create_ms": time.Since(storeStart).Milliseconds(),
	})

	// Initialize GitHub client for commit ancestry resolution
	githubStart := time.Now()
	githubClient, err := NewGitHubClient(config.GitHubConfig(), config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}
	config.Logger.Info(ctx, "GitHub client created", map[string]interface{}{
		"github_create_ms": time.Since(githubStart).Milliseconds(),
		"total_client_ms":  time.Since(startTime).Milliseconds(),
	})

	return &Client{
		store:          store,
		github:         githubClient,
		config:         config,
		pipelineConfig: config.PipelineConfig,
		logger:         config.Logger,
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
	if config.Database == "" {
		config.Database = DefaultConfig().Database
	}

	return &Client{
		store:          store,
		github:         github,
		config:         config,
		pipelineConfig: config.PipelineConfig,
		logger:         config.Logger,
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

// AbandonSlip marks a slip as abandoned, indicating it was superseded by a newer slip.
// This should only be called on slips that are not already in a terminal state.
// Returns an error if the slip is already terminal.
// Uses exponential backoff with jitter to handle concurrent modifications gracefully.
func (c *Client) AbandonSlip(ctx context.Context, correlationID, supersededBy string) error {
	// Start tracing span
	ctx, span := StartSpan(ctx, "AbandonSlip", correlationID)
	defer span.End()
	span.SetAttributes(attribute.String("slippy.superseded_by", supersededBy))

	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return NewSlipError("abandon", correlationID, err)
	}

	if c.checkTerminalStatus(ctx, slip, "abandon") {
		return nil
	}

	slip.Status = SlipStatusAbandoned

	// Use ForceUpdate to skip the optimistic locking check that would cause retries
	// during high concurrency. The atomic versioning in insertRow still ensures
	// correct, incrementing versions for VersionedCollapsingMergeTree.
	if err := c.store.ForceUpdate(ctx, slip); err != nil {
		return NewSlipError("abandon", correlationID, err)
	}

	c.logger.Info(ctx, "Abandoned slip", map[string]interface{}{
		"correlation_id": correlationID,
		"superseded_by":  supersededBy,
	})
	return nil
}

// PromoteSlip marks a slip as promoted, indicating its code was promoted to another branch
// via a PR merge (typically squash merge). Unlike abandon, this is a successful outcome -
// the slip's work continues in the new slip on the target branch.
// The promotedTo parameter records the correlation ID of the new slip for bidirectional linking.
func (c *Client) PromoteSlip(ctx context.Context, correlationID, promotedTo string) error {
	// Start tracing span
	ctx, span := StartSpan(ctx, "PromoteSlip", correlationID)
	defer span.End()
	span.SetAttributes(attribute.String("slippy.promoted_to", promotedTo))

	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return NewSlipError("promote", correlationID, err)
	}

	if c.checkTerminalStatus(ctx, slip, "promote") {
		return nil
	}

	slip.Status = SlipStatusPromoted
	slip.PromotedTo = promotedTo

	// Use ForceUpdate to skip the optimistic locking check that would cause retries
	// during high concurrency. The atomic versioning in insertRow still ensures
	// correct, incrementing versions for VersionedCollapsingMergeTree.
	if err := c.store.ForceUpdate(ctx, slip); err != nil {
		return NewSlipError("promote", correlationID, err)
	}

	c.logger.Info(ctx, "Promoted slip", map[string]interface{}{
		"correlation_id": correlationID,
		"promoted_to":    promotedTo,
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

// PipelineConfig returns the pipeline configuration.
func (c *Client) PipelineConfig() *PipelineConfig {
	return c.pipelineConfig
}

// applyHoldDefaults applies default values for timeout and poll interval if not set.
// This centralizes the defaulting logic used across WaitForPrerequisites and RunPreExecution.
func (c *Client) applyHoldDefaults(
	timeout, pollInterval time.Duration,
) (appliedTimeout, appliedPollInterval time.Duration) {
	appliedTimeout = timeout
	if appliedTimeout == 0 {
		appliedTimeout = c.config.HoldTimeout
	}
	appliedPollInterval = pollInterval
	if appliedPollInterval == 0 {
		appliedPollInterval = c.config.PollInterval
	}
	return appliedTimeout, appliedPollInterval
}

// checkTerminalStatus checks if a slip is already in a terminal state.
// If terminal, logs a message and returns true. Otherwise returns false.
// This centralizes the terminal check logic used in AbandonSlip and PromoteSlip.
func (c *Client) checkTerminalStatus(ctx context.Context, slip *Slip, operation string) bool {
	if slip.Status.IsTerminal() {
		c.logger.Info(ctx, fmt.Sprintf("Slip already terminal, skipping %s", operation), map[string]interface{}{
			"correlation_id": slip.CorrelationID,
			"status":         string(slip.Status),
		})
		return true
	}
	return false
}
