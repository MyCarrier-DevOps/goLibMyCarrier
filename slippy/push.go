package slippy

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// prNumberRegex matches PR references in commit messages.
// Handles: "Add feature (#42)", "Merge pull request #42 from ...", "(#42)"
var prNumberRegex = regexp.MustCompile(`(?:#|pull request #)(\d+)`)

// cherryPickRegex detects cherry-pick commits
var cherryPickRegex = regexp.MustCompile(`(?i)\b(cherry.pick|cherry-pick|picked from|backport)\b`)

// extractPRNumber extracts a pull request number from a commit message.
// extractPRNumber extracts the first PR number from a commit message.
// Returns 0 if no PR number is found.
// Handles common squash merge formats:
//   - "Add feature (#42)" - GitHub squash merge default
//   - "Merge pull request #42 from owner/branch" - GitHub merge commit
//
//nolint:unused // Kept for backwards compatibility, use extractAllPRNumbers for new code
func extractPRNumber(commitMessage string) int {
	matches := prNumberRegex.FindStringSubmatch(commitMessage)
	if len(matches) < 2 {
		return 0
	}
	prNum, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return prNum
}

// extractAllPRNumbers extracts all PR numbers from a commit message.
// Used for nested PR references (e.g., dev→main merge that mentions feature→dev PR).
func extractAllPRNumbers(commitMessage string) []int {
	matches := prNumberRegex.FindAllStringSubmatch(commitMessage, -1)
	if len(matches) == 0 {
		return nil
	}

	var prNumbers []int
	seen := make(map[int]bool)
	for _, match := range matches {
		if len(match) >= 2 {
			if prNum, err := strconv.Atoi(match[1]); err == nil {
				if !seen[prNum] {
					prNumbers = append(prNumbers, prNum)
					seen[prNum] = true
				}
			}
		}
	}
	return prNumbers
}

// isCherryPick detects if a commit message indicates a cherry-pick.
func isCherryPick(commitMessage string) bool {
	return cherryPickRegex.MatchString(commitMessage)
}

// isForceOrRewrite detects potential force push scenarios.
// This is heuristic-based: if no ancestry found despite having commits, might be force push.
func isForceOrRewrite(commitMessage string) bool {
	msg := strings.ToLower(commitMessage)
	return strings.Contains(msg, "force push") ||
		strings.Contains(msg, "rebase") ||
		strings.Contains(msg, "amend")
}

// normalizeRepository extracts the base repository path without fork prefixes.
// Handles: "user/repo" → "user/repo", "MyCarrier-DevOps/repo" → "MyCarrier-DevOps/repo"
// For fork detection, just returns as-is since we don't have enough context.
//
//nolint:unused // Reserved for future fork handling implementation
func normalizeRepository(repo string) string {
	// For now, return as-is. Future: could strip fork prefixes if we had org config
	return repo
}

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

	// CommitMessage is the commit message text (optional).
	// When provided, enables PR-based ancestry resolution for squash merges.
	// Squash merge commits typically contain the PR number (e.g., "Add feature (#42)")
	// which allows linking to the original feature branch slip.
	CommitMessage string

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
//
// For squash merges (when CommitMessage contains a PR reference like "#42"),
// if no ancestor is found via git history, falls back to PR-based lookup.
// This finds the original feature branch slip and marks it as "promoted" (not abandoned).
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

	// Detect potential edge cases that might break ancestry
	if opts.CommitMessage != "" {
		if isCherryPick(opts.CommitMessage) {
			c.logger.Warn(
				ctx,
				"Cherry-pick detected - ancestry may not link to original commit",
				map[string]interface{}{
					"commit":  shortSHA(opts.CommitSHA),
					"message": opts.CommitMessage,
				},
			)
		}
		if isForceOrRewrite(opts.CommitMessage) {
			c.logger.Warn(
				ctx,
				"Possible force push or rebase detected - ancestry chain may be broken",
				map[string]interface{}{
					"commit":  shortSHA(opts.CommitSHA),
					"message": opts.CommitMessage,
				},
			)
		}
	}

	// If no ancestors found via git history, try PR-based lookup for squash merges
	isSquashMerge := false
	if len(ancestorSlips) == 0 && opts.CommitMessage != "" {
		prSlip, found := c.findAncestorViaSquashMerge(ctx, owner, repo, opts)
		if found {
			ancestorSlips = []SlipWithCommit{prSlip}
			isSquashMerge = true
		}
	}

	if len(ancestorSlips) == 0 {
		return nil, nil
	}

	// Build ancestry chain and handle the ancestor slip
	// For squash merges: promote the feature branch slip
	// For regular pushes: abandon non-terminal ancestor slips
	var ancestry []AncestryEntry
	for i, ancestorSlip := range ancestorSlips {
		slip := ancestorSlip.Slip

		// Only the first (most recent) non-terminal slip needs status update
		// Earlier slips should already be in terminal states
		if i == 0 && !slip.Status.IsTerminal() {
			if isSquashMerge {
				// Squash merge: promote the feature branch slip (successful outcome)
				c.logger.Info(ctx, "Promoting feature branch slip via squash merge", map[string]interface{}{
					"promoted_id":     slip.CorrelationID,
					"promoted_commit": shortSHA(slip.CommitSHA),
					"promoted_status": string(slip.Status),
					"promoted_to":     opts.CorrelationID,
					"merge_commit":    shortSHA(opts.CommitSHA),
				})

				if err := c.PromoteSlip(ctx, slip.CorrelationID, opts.CorrelationID); err != nil {
					c.logger.Warn(ctx, "Failed to promote feature branch slip", map[string]interface{}{
						"error":          err.Error(),
						"correlation_id": slip.CorrelationID,
					})
					// Continue - don't fail slip creation due to promotion failure
				} else {
					// Update the local copy to reflect the promotion
					slip.Status = SlipStatusPromoted
				}
			} else {
				// Regular push: abandon superseded slip
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
		"commit":       shortSHA(opts.CommitSHA),
		"ancestors":    len(ancestry),
		"squash_merge": isSquashMerge,
	})

	return ancestry, nil
}

// findAncestorViaSquashMerge attempts to find an ancestor slip by parsing
// a PR number from the commit message and looking up the PR's head commit.
// This handles squash merge scenarios where git ancestry is broken.
// Supports nested PR references for multi-stage merges (feature→dev→main).
// Uses the same progressive depth ancestry search starting from the PR head.
// Returns the slip and true if found, nil and false otherwise.
func (c *Client) findAncestorViaSquashMerge(
	ctx context.Context,
	owner, repo string,
	opts PushOptions,
) (SlipWithCommit, bool) {
	// Try all PR numbers found in commit message (supports nested merges)
	prNumbers := extractAllPRNumbers(opts.CommitMessage)
	if len(prNumbers) == 0 {
		return SlipWithCommit{}, false
	}

	c.logger.Debug(ctx, "Detected potential squash merge, looking up PRs", map[string]interface{}{
		"pr_numbers": prNumbers,
		"commit":     shortSHA(opts.CommitSHA),
	})

	// Try each PR number until we find a slip
	for _, prNumber := range prNumbers {
		// Get the PR's original head commit
		prHeadCommit, err := c.github.GetPRHeadCommit(ctx, owner, repo, prNumber)
		if err != nil {
			c.logger.Debug(ctx, "Failed to get PR head commit, trying next", map[string]interface{}{
				"error":     err.Error(),
				"pr_number": prNumber,
			})
			continue
		}

		c.logger.Debug(ctx, "Starting ancestry search from PR head commit", map[string]interface{}{
			"pr_number":   prNumber,
			"head_commit": shortSHA(prHeadCommit),
		})

		// Search for slips starting from the PR head commit and walking back its ancestry
		// Note: we want to INCLUDE the PR head commit itself in the search, since that's
		// where a slip might exist (unlike normal ancestry where we skip the merge commit)
		ancestorSlips, err := c.findSlipsInPRBranchHistory(ctx, owner, repo, opts.Repository, prHeadCommit)
		if err != nil {
			c.logger.Debug(ctx, "Failed to search PR head ancestry, trying next", map[string]interface{}{
				"error":       err.Error(),
				"pr_number":   prNumber,
				"head_commit": shortSHA(prHeadCommit),
			})
			continue
		}

		if len(ancestorSlips) > 0 {
			// Found a slip via this PR
			prSlip := ancestorSlips[0]
			c.logger.Info(ctx, "Found feature branch slip via squash merge PR ancestry", map[string]interface{}{
				"pr_number":   prNumber,
				"pr_head":     shortSHA(prHeadCommit),
				"slip_commit": shortSHA(prSlip.MatchedCommit),
				"slip_id":     prSlip.Slip.CorrelationID,
				"slip_status": string(prSlip.Slip.Status),
			})
			return prSlip, true
		}
	}

	c.logger.Debug(ctx, "No slips found for any PR references", map[string]interface{}{
		"pr_numbers": prNumbers,
		"commit":     shortSHA(opts.CommitSHA),
	})
	return SlipWithCommit{}, false
}

// findSlipsInPRBranchHistory searches for slips in a PR branch's commit history,
// starting from (and including) the PR head commit. Unlike findAncestorSlipsWithProgressiveDepth,
// this includes the starting commit in the search since the PR head itself may have a slip.
func (c *Client) findSlipsInPRBranchHistory(
	ctx context.Context,
	owner, repo, repository, headCommit string,
) ([]SlipWithCommit, error) {
	// Define search depths: initial, then max if needed
	depths := []int{c.config.AncestryDepth}
	if c.config.AncestryMaxDepth > c.config.AncestryDepth {
		depths = append(depths, c.config.AncestryMaxDepth)
	}

	for i, depth := range depths {
		isRetry := i > 0

		if isRetry {
			c.logger.Debug(ctx, "Expanding PR branch history search depth", map[string]interface{}{
				"head_commit":    shortSHA(headCommit),
				"previous_depth": depths[i-1],
				"new_depth":      depth,
			})
		}

		// Get commit ancestry from GitHub
		commits, err := c.github.GetCommitAncestry(ctx, owner, repo, headCommit, depth)
		if err != nil {
			return nil, fmt.Errorf("failed to get commit ancestry: %w", err)
		}

		// Unlike ancestor search, we INCLUDE the head commit in PR branch search
		// since that's where a slip is most likely to exist

		if len(commits) == 0 {
			c.logger.Debug(ctx, "No commits found in PR branch history", map[string]interface{}{
				"head_commit": shortSHA(headCommit),
				"depth":       depth,
			})
			return nil, nil
		}

		// Find all slips matching commits in PR branch history
		slips, err := c.store.FindAllByCommits(ctx, repository, commits)
		if err != nil {
			return nil, fmt.Errorf("failed to find slips by commits: %w", err)
		}

		if len(slips) > 0 {
			c.logger.Debug(ctx, "Found slips in PR branch history", map[string]interface{}{
				"head_commit": shortSHA(headCommit),
				"slip_count":  len(slips),
				"depth":       depth,
			})
			return slips, nil
		}

		// No slips found at this depth; if max depth not reached, try again
		if !isRetry && c.config.AncestryMaxDepth > c.config.AncestryDepth {
			continue
		}

		// Either first attempt with no retry configured, or final retry
		break
	}

	return nil, nil
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
