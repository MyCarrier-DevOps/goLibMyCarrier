package slippy

import (
	"context"
	"fmt"
	"time"
)

// PushOptions contains the information needed to create a slip from a push event.
type PushOptions struct {
	// CorrelationID links this slip to Kafka events
	CorrelationID string

	// Repository is the full repository name (owner/repo)
	Repository string

	// Branch is the git branch name
	Branch string

	// CommitSHA is the full git commit SHA
	CommitSHA string

	// Components defines the components to track
	Components []ComponentDefinition
}

// Validate checks that all required fields are present.
func (o PushOptions) Validate() error {
	if o.CorrelationID == "" {
		return fmt.Errorf("correlation_id is required")
	}
	if o.Repository == "" {
		return fmt.Errorf("repository is required")
	}
	if o.CommitSHA == "" {
		return fmt.Errorf("commit_sha is required")
	}
	return nil
}

// CreateSlipForPush creates a new routing slip for a git push event.
// If a slip already exists for this commit (retry scenario), it resets
// the push_parsed step and returns the existing slip.
func (c *Client) CreateSlipForPush(ctx context.Context, opts PushOptions) (*Slip, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid push options: %w", err)
	}

	c.logger.Info(ctx, "Creating routing slip", map[string]interface{}{
		"repository": opts.Repository,
		"commit":     shortSHA(opts.CommitSHA),
	})

	// Check for existing slip (retry detection)
	existingSlip, err := c.store.LoadByCommit(ctx, opts.Repository, opts.CommitSHA)
	if err == nil && existingSlip != nil {
		return c.handlePushRetry(ctx, existingSlip)
	}

	// Create new slip with full initialization
	slip := c.initializeSlipForPush(opts)

	if err := c.store.Create(ctx, slip); err != nil {
		return nil, fmt.Errorf("failed to create slip: %w", err)
	}

	c.logger.Info(ctx, "Created routing slip", map[string]interface{}{
		"correlation_id": slip.CorrelationID,
		"components":     len(opts.Components),
	})
	return slip, nil
}

// handlePushRetry resets a slip for retry processing.
func (c *Client) handlePushRetry(ctx context.Context, slip *Slip) (*Slip, error) {
	c.logger.Info(ctx, "Found existing slip for commit, handling retry", map[string]interface{}{
		"correlation_id": slip.CorrelationID,
		"commit":         shortSHA(slip.CommitSHA),
	})

	now := time.Now()
	entry := StateHistoryEntry{
		Step:      "push_parsed",
		Status:    StepStatusRunning,
		Timestamp: now,
		Actor:     "slippy-library",
		Message:   "retry detected, resetting push_parsed",
	}

	if err := c.store.UpdateStep(ctx, slip.CorrelationID, "push_parsed", "", StepStatusRunning); err != nil {
		return nil, fmt.Errorf("failed to reset push_parsed: %w", err)
	}

	if err := c.store.AppendHistory(ctx, slip.CorrelationID, entry); err != nil {
		c.logger.Error(ctx, "Failed to append history for retry", err, map[string]interface{}{
			"correlation_id": slip.CorrelationID,
		})
		// Non-fatal - continue
	}

	// Reload to get updated slip
	return c.store.Load(ctx, slip.CorrelationID)
}

// initializeSlipForPush creates a fully initialized slip for a push event.
func (c *Client) initializeSlipForPush(opts PushOptions) *Slip {
	now := time.Now()

	// Initialize components
	components := make([]Component, len(opts.Components))
	for i, def := range opts.Components {
		components[i] = Component{
			Name:           def.Name,
			DockerfilePath: def.DockerfilePath,
			BuildStatus:    StepStatusPending,
			UnitTestStatus: StepStatusPending,
		}
	}

	// Initialize all pipeline steps as pending (push_parsed starts as running)
	steps := map[string]Step{
		"push_parsed":           {Status: StepStatusRunning, StartedAt: &now},
		"builds_completed":      {Status: StepStatusPending},
		"unit_tests_completed":  {Status: StepStatusPending},
		"secret_scan_completed": {Status: StepStatusPending},
		"dev_deploy":            {Status: StepStatusPending},
		"dev_tests":             {Status: StepStatusPending},
		"preprod_deploy":        {Status: StepStatusPending},
		"preprod_tests":         {Status: StepStatusPending},
		"prod_release_created":  {Status: StepStatusPending},
		"prod_deploy":           {Status: StepStatusPending},
		"prod_tests":            {Status: StepStatusPending},
		"alert_gate":            {Status: StepStatusPending},
		"prod_steady_state":     {Status: StepStatusPending},
	}

	history := []StateHistoryEntry{
		{
			Step:      "push_parsed",
			Status:    StepStatusRunning,
			Timestamp: now,
			Actor:     "slippy-library",
			Message:   "processing push event",
		},
	}

	return &Slip{
		CorrelationID: opts.CorrelationID,
		Repository:    opts.Repository,
		Branch:        opts.Branch,
		CommitSHA:     opts.CommitSHA,
		CreatedAt:     now,
		UpdatedAt:     now,
		Status:        SlipStatusInProgress,
		Components:    components,
		Steps:         steps,
		StateHistory:  history,
	}
}
