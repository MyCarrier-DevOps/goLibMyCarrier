package slippy

import (
	"context"
	"fmt"
	"strings"
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
//
// This function also resolves the commit ancestry chain via GitHub,
// finds any existing slips for ancestor commits, and ensures they are
// in a terminal state (abandoning non-terminal slips that are being superseded).
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

	// Resolve ancestry chain and abandon superseded slips
	ancestry, err := c.resolveAndAbandonAncestors(ctx, opts)
	if err != nil {
		// Log but don't fail - ancestry is informational
		c.logger.Warn(ctx, "Failed to resolve ancestry", map[string]interface{}{
			"error": err.Error(),
		})
		ancestry = nil
	}

	// Create new slip with full initialization including ancestry
	slip := c.initializeSlipForPush(opts, ancestry)

	if err := c.store.Create(ctx, slip); err != nil {
		return nil, fmt.Errorf("failed to create slip: %w", err)
	}

	c.logger.Info(ctx, "Created routing slip", map[string]interface{}{
		"correlation_id": slip.CorrelationID,
		"components":     len(opts.Components),
		"ancestors":      len(ancestry),
	})
	return slip, nil
}

// resolveAndAbandonAncestors fetches commit ancestry from GitHub,
// finds any existing slips for those commits, abandons non-terminal ones,
// and returns the ancestry chain for recording on the new slip.
//
// This uses progressive depth searching: starts with AncestryDepth (default 25),
// and if no ancestor slip is found, expands to AncestryMaxDepth (default 100).
// This handles cases where pushes contain many commits or there are gaps between slips.
func (c *Client) resolveAndAbandonAncestors(ctx context.Context, opts PushOptions) ([]AncestryEntry, error) {
	// Parse owner/repo for GitHub API
	parts := strings.SplitN(opts.Repository, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository format: %s", opts.Repository)
	}
	owner, repo := parts[0], parts[1]

	// Progressive depth search: start with initial depth, expand if no ancestor found
	ancestorSlips, err := c.findAncestorSlipsWithProgressiveDepth(ctx, owner, repo, opts)
	if err != nil {
		return nil, err
	}

	if len(ancestorSlips) == 0 {
		return nil, nil
	}

	// Build ancestry chain and abandon non-terminal slips
	// We only process the first (most recent) ancestor slip for abandonment,
	// but inherit its ancestry chain to maintain full lineage history.
	var ancestry []AncestryEntry
	for i, ancestorSlip := range ancestorSlips {
		slip := ancestorSlip.Slip

		// Only the first (most recent) non-terminal slip needs abandonment
		// Earlier slips should already be in terminal states
		if i == 0 && !slip.Status.IsTerminal() {
			c.logger.Info(ctx, "Abandoning superseded slip", map[string]interface{}{
				"superseded_id":      slip.CorrelationID,
				"superseded_commit":  shortSHA(slip.CommitSHA),
				"superseded_status":  string(slip.Status),
				"superseding_commit": shortSHA(opts.CommitSHA),
			})

			if err := c.AbandonSlip(ctx, slip.CorrelationID, opts.CorrelationID); err != nil {
				c.logger.Warn(ctx, "Failed to abandon superseded slip", map[string]interface{}{
					"error":          err.Error(),
					"correlation_id": slip.CorrelationID,
				})
				// Continue - don't fail slip creation due to abandonment failure
			} else {
				// Update the local copy to reflect the abandonment
				slip.Status = SlipStatusAbandoned
			}
		}

		// Find the failed step if status is failed
		var failedStep string
		if slip.Status == SlipStatusFailed {
			for stepName, step := range slip.Steps {
				if step.Status == StepStatusFailed {
					failedStep = stepName
					break
				}
			}
		}

		ancestry = append(ancestry, AncestryEntry{
			CorrelationID: slip.CorrelationID,
			CommitSHA:     slip.CommitSHA,
			Status:        slip.Status,
			FailedStep:    failedStep,
			CreatedAt:     slip.CreatedAt,
		})

		// Inherit ancestor's ancestry chain (only from the first/most recent ancestor)
		// This ensures we maintain full lineage even when commits exceed AncestryDepth
		if i == 0 && len(slip.Ancestry) > 0 {
			c.logger.Debug(ctx, "Inheriting ancestry from parent slip", map[string]interface{}{
				"parent_id":         slip.CorrelationID,
				"inherited_entries": len(slip.Ancestry),
			})
			ancestry = append(ancestry, slip.Ancestry...)
		}
	}

	c.logger.Info(ctx, "Resolved ancestry chain", map[string]interface{}{
		"commit":    shortSHA(opts.CommitSHA),
		"ancestors": len(ancestry),
	})

	return ancestry, nil
}

// findAncestorSlipsWithProgressiveDepth searches for ancestor slips using progressive depth.
// It starts with AncestryDepth and expands to AncestryMaxDepth if no ancestors are found.
// This handles cases where many commits occur between slip creations (e.g., large pushes).
func (c *Client) findAncestorSlipsWithProgressiveDepth(
	ctx context.Context,
	owner, repo string,
	opts PushOptions,
) ([]SlipWithCommit, error) {
	// Define search depths: initial, then max if needed
	depths := []int{c.config.AncestryDepth}
	if c.config.AncestryMaxDepth > c.config.AncestryDepth {
		depths = append(depths, c.config.AncestryMaxDepth)
	}

	for i, depth := range depths {
		isRetry := i > 0

		if isRetry {
			c.logger.Debug(ctx, "Expanding ancestry search depth", map[string]interface{}{
				"commit":         shortSHA(opts.CommitSHA),
				"previous_depth": depths[i-1],
				"new_depth":      depth,
			})
		}

		// Get commit ancestry from GitHub
		commits, err := c.github.GetCommitAncestry(ctx, owner, repo, opts.CommitSHA, depth)
		if err != nil {
			return nil, fmt.Errorf("failed to get commit ancestry: %w", err)
		}

		// Skip the first commit if it's the current one (we're looking for ancestors)
		if len(commits) > 0 && commits[0] == opts.CommitSHA {
			commits = commits[1:]
		}

		if len(commits) == 0 {
			c.logger.Debug(ctx, "No ancestor commits found", map[string]interface{}{
				"commit": shortSHA(opts.CommitSHA),
				"depth":  depth,
			})
			return nil, nil // No point retrying if there are no commits at all
		}

		// Find all slips matching ancestor commits
		ancestorSlips, err := c.store.FindAllByCommits(ctx, opts.Repository, commits)
		if err != nil {
			return nil, fmt.Errorf("failed to find ancestor slips: %w", err)
		}

		if len(ancestorSlips) > 0 {
			c.logger.Debug(ctx, "Found ancestor slips", map[string]interface{}{
				"commit":            shortSHA(opts.CommitSHA),
				"ancestors_checked": len(commits),
				"ancestors_found":   len(ancestorSlips),
				"depth_used":        depth,
			})
			return ancestorSlips, nil
		}

		// No ancestors found at this depth
		c.logger.Debug(ctx, "No ancestor slips found at depth", map[string]interface{}{
			"commit":            shortSHA(opts.CommitSHA),
			"ancestors_checked": len(commits),
			"depth":             depth,
		})
	}

	return nil, nil
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
// Steps are initialized from the pipeline configuration rather than hardcoded.
// The ancestry parameter records any ancestor slips in the commit lineage.
func (c *Client) initializeSlipForPush(opts PushOptions, ancestry []AncestryEntry) *Slip {
	now := time.Now()

	// Initialize all pipeline steps from config as pending
	// The first step (typically push_parsed) starts as running
	steps := make(map[string]Step)
	aggregates := make(map[string][]ComponentStepData)
	var firstStep string

	if c.pipelineConfig != nil {
		for i, stepConfig := range c.pipelineConfig.Steps {
			step := Step{Status: StepStatusPending}

			// First step starts as running
			if i == 0 {
				firstStep = stepConfig.Name
				step.Status = StepStatusRunning
				step.StartedAt = &now
			}

			steps[stepConfig.Name] = step

			// Initialize aggregate columns with component data
			if stepConfig.Aggregates != "" {
				columnName := pluralize(stepConfig.Aggregates)
				componentData := make([]ComponentStepData, len(opts.Components))
				for j, def := range opts.Components {
					componentData[j] = ComponentStepData{
						Component: def.Name,
						Status:    StepStatusPending,
					}
				}
				aggregates[columnName] = componentData
			}
		}
	} else {
		// Fallback to default first step if no config (for backward compatibility)
		firstStep = "push_parsed"
		steps["push_parsed"] = Step{Status: StepStatusRunning, StartedAt: &now}
	}

	history := []StateHistoryEntry{
		{
			Step:      firstStep,
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
		Steps:         steps,
		Aggregates:    aggregates,
		StateHistory:  history,
		Ancestry:      ancestry,
	}
}
