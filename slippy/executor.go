package slippy

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// PreExecutionOptions configures pre-execution behavior.
type PreExecutionOptions struct {
	// Repository in owner/repo format
	Repository string

	// Branch name
	Branch string

	// Ref to resolve ancestry from (e.g., "HEAD", commit SHA)
	Ref string

	// ImageTag for fallback resolution (optional)
	ImageTag string

	// StepName is the current pipeline step
	StepName string

	// ComponentName is the component (if applicable)
	ComponentName string

	// Prerequisites is the list of prerequisite step names
	Prerequisites []string

	// HoldTimeout is the max time to wait for prerequisites
	HoldTimeout time.Duration

	// PollInterval is the interval between prerequisite checks
	PollInterval time.Duration
}

// PreExecutionResult contains the result of pre-execution.
type PreExecutionResult struct {
	// Outcome indicates what happened
	Outcome PreExecutionOutcome

	// CorrelationID is the unique identifier for the resolved routing slip.
	// This ID is used organization-wide to identify jobs across all systems.
	CorrelationID string

	// ResolvedBy indicates how the slip was found
	ResolvedBy string

	// MatchedCommit is the commit that matched during resolution
	MatchedCommit string

	// Message provides additional context
	Message string
}

// PreExecutionOutcome represents possible pre-execution outcomes.
type PreExecutionOutcome string

const (
	// PreExecutionOutcomeProceed indicates the step should proceed
	PreExecutionOutcomeProceed PreExecutionOutcome = "proceed"

	// PreExecutionOutcomeAbort indicates the step should abort
	PreExecutionOutcomeAbort PreExecutionOutcome = "abort"

	// PreExecutionOutcomeTimeout indicates a hold timeout occurred
	PreExecutionOutcomeTimeout PreExecutionOutcome = "timeout"

	// PreExecutionOutcomeNoSlip indicates no slip was found (shadow mode only)
	PreExecutionOutcomeNoSlip PreExecutionOutcome = "no_slip"
)

// RunPreExecution runs pre-execution logic: resolve slip, check prerequisites, hold if needed.
func (c *Client) RunPreExecution(ctx context.Context, opts PreExecutionOptions) (*PreExecutionResult, error) {
	c.logger.Info(ctx, "Pre-execution started", map[string]interface{}{
		"step_name":      opts.StepName,
		"component_name": opts.ComponentName,
	})

	// Step 1: Resolve the correct slip
	resolveResult, err := c.ResolveSlip(ctx, ResolveOptions{
		Repository:    opts.Repository,
		Branch:        opts.Branch,
		Ref:           opts.Ref,
		ImageTag:      opts.ImageTag,
		AncestryDepth: c.config.AncestryDepth,
	})

	if err != nil {
		if c.config.ShadowMode {
			c.logger.Warn(ctx, "[SHADOW] No slip found, would proceed anyway", map[string]interface{}{
				"error": err.Error(),
			})
			return &PreExecutionResult{
				Outcome: PreExecutionOutcomeNoSlip,
				Message: "shadow mode - no slip found but proceeding",
			}, nil
		}
		return nil, fmt.Errorf("failed to resolve slip: %w", err)
	}

	slip := resolveResult.Slip
	c.logger.Info(ctx, "Resolved slip", map[string]interface{}{
		"correlation_id": slip.CorrelationID,
		"resolved_by":    resolveResult.ResolvedBy,
		"commit":         shortSHA(resolveResult.MatchedCommit),
	})

	result := &PreExecutionResult{
		CorrelationID: slip.CorrelationID,
		ResolvedBy:    resolveResult.ResolvedBy,
		MatchedCommit: resolveResult.MatchedCommit,
	}

	// Step 2: Check prerequisites
	if len(opts.Prerequisites) > 0 {
		// Apply defaults
		timeout, pollInterval := c.applyHoldDefaults(opts.HoldTimeout, opts.PollInterval)

		holdResult, err := c.WaitForPrerequisitesWithResult(ctx, HoldOptions{
			CorrelationID: slip.CorrelationID,
			Prerequisites: opts.Prerequisites,
			ComponentName: opts.ComponentName,
			Timeout:       timeout,
			PollInterval:  pollInterval,
			StepName:      opts.StepName,
		})

		if err != nil {
			switch holdResult.Outcome {
			case HoldOutcomeProceeded:
				// This shouldn't happen with an error, but handle exhaustively
				result.Outcome = PreExecutionOutcomeAbort
				result.Message = err.Error()
				return result, err

			case HoldOutcomeTimeout:
				result.Outcome = PreExecutionOutcomeTimeout
				result.Message = holdResult.Message
				return result, err

			case HoldOutcomeFailed:
				result.Outcome = PreExecutionOutcomeAbort
				result.Message = fmt.Sprintf("prerequisites failed: %v", holdResult.FailedPrereqs)
				return result, err

			case HoldOutcomeCancelled:
				result.Outcome = PreExecutionOutcomeAbort
				result.Message = "operation cancelled"
				return result, err
			}
		}
	}

	// Step 3: Prerequisites satisfied, mark as running
	if err := c.StartStep(ctx, slip.CorrelationID, opts.StepName, opts.ComponentName); err != nil {
		// Return error - let caller decide if this should be blocking based on shadow mode
		return nil, fmt.Errorf("failed to start step %s: %w", opts.StepName, err)
	}

	result.Outcome = PreExecutionOutcomeProceed
	result.Message = "prerequisites satisfied, step started"
	c.logger.Info(ctx, "Pre-execution complete, proceeding with workflow execution", nil)

	return result, nil
}

// PostExecutionOptions configures post-execution behavior.
type PostExecutionOptions struct {
	// CorrelationID is the unique identifier for the routing slip (from pre-execution result).
	// This ID is used organization-wide to identify jobs across all systems.
	CorrelationID string

	// StepName is the pipeline step that completed
	StepName string

	// ComponentName is the component (if applicable)
	ComponentName string

	// WorkflowSucceeded indicates if the workflow step succeeded
	WorkflowSucceeded bool

	// FailureMessage provides details if the workflow failed
	FailureMessage string
}

// PostExecutionResult contains the result of post-execution.
type PostExecutionResult struct {
	// StepStatus is the final status recorded
	StepStatus StepStatus

	// SlipStatus is the updated slip status (if changed)
	SlipStatus SlipStatus

	// SlipCompleted is true if the entire pipeline completed
	SlipCompleted bool
}

// RunPostExecution runs post-execution logic: update step status based on workflow result.
func (c *Client) RunPostExecution(ctx context.Context, opts PostExecutionOptions) (*PostExecutionResult, error) {
	c.logger.Info(ctx, "Post-execution started", map[string]interface{}{
		"step_name": opts.StepName,
		"success":   opts.WorkflowSucceeded,
	})

	result := &PostExecutionResult{}

	if opts.WorkflowSucceeded {
		result.StepStatus = StepStatusCompleted
		if err := c.CompleteStep(ctx, opts.CorrelationID, opts.StepName, opts.ComponentName); err != nil {
			return nil, err
		}
	} else {
		result.StepStatus = StepStatusFailed
		reason := opts.FailureMessage
		if reason == "" {
			reason = "workflow failed"
		}
		if err := c.FailStep(ctx, opts.CorrelationID, opts.StepName, opts.ComponentName, reason); err != nil {
			return nil, err
		}
	}

	// Check if pipeline is complete
	var completionErr error
	result.SlipCompleted, result.SlipStatus, completionErr = c.checkPipelineCompletion(ctx, opts.CorrelationID)
	if completionErr != nil {
		// Return the error - let caller decide how to log based on shadow mode
		// Don't log here to avoid duplicate logging when caller also logs
		return result, fmt.Errorf("post-execution completed but pipeline status update failed: %w", completionErr)
	}

	return result, nil
}

// checkPipelineCompletion checks if the entire pipeline is complete and updates slip status.
// Returns (completed, status, error). The error is returned rather than swallowed to allow
// callers (and shadow mode settings) to decide how to handle failures.
//
// Step failures are classified as:
//   - Primary failures (failed, error, timeout): the step itself encountered an issue.
//   - Cascade failures (aborted): the step was killed because an upstream step failed.
//
// When a previously-failed slip has no remaining primary failures, cascade-aborted steps
// are reset to pending so they can re-run, and the slip status is reconciled to in_progress.
// On every pass that does not complete the slip, prod_deploy and prod_tests skipped for a canary
// reason (decision D13) are reset to pending once prod_canary has succeeded, whatever the slip
// status and whether or not another step has a primary failure (scenario H, see
// canarySkipsToReset).
func (c *Client) checkPipelineCompletion(ctx context.Context, correlationID string) (bool, SlipStatus, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return false, "", fmt.Errorf("%w: failed to load slip for completion check: %s", ErrSlipNotFound, err.Error())
	}

	// Terminal-immutable statuses per STATE_MACHINE_V3.md §Pipeline termination states:
	// completed, abandoned, and promoted are all pipeline-terminal and must bypass
	// checkPipelineCompletion entirely to prevent late step events from overwriting
	// the slip status back to "failed".
	switch slip.Status {
	case SlipStatusCompleted:
		return true, SlipStatusCompleted, nil
	case SlipStatusAbandoned:
		return false, SlipStatusAbandoned, nil
	case SlipStatusPromoted:
		return false, SlipStatusPromoted, nil
	case SlipStatusPending, SlipStatusInProgress, SlipStatusFailed, SlipStatusCompensating, SlipStatusCompensated:
		// These statuses proceed to normal pipeline-completion evaluation below.
	}

	// Categorize step failures into primary (step itself failed) vs cascade (aborted by upstream).
	// Primary failures indicate real problems that need resolution.
	// Cascade failures are collateral damage from an upstream primary failure.
	var primaryFailures []string
	var cascadeFailures []string
	for stepName, step := range slip.Steps {
		switch step.Status {
		case StepStatusFailed, StepStatusError, StepStatusTimeout:
			primaryFailures = append(primaryFailures, stepName)
		case StepStatusAborted:
			cascadeFailures = append(cascadeFailures, stepName)
		case StepStatusPending, StepStatusHeld, StepStatusRunning, StepStatusCompleted, StepStatusSkipped:
			// Non-failure statuses — no action needed
		}
	}

	// D13 canary skips that a successful prod_canary has made stale (scenario H) are reset to
	// pending on every pass that does not complete the slip, whatever the slip status and whatever
	// else has failed. The slip can already have recovered to in_progress while the retried canary
	// was still running, and a step no prod step depends on (dev_tests, say) can be failed when the
	// canary completes; tying the reset to either branch below would leave the skips satisfying
	// prod_steady_state's prerequisites. The reset only writes skipped -> pending, and pending never
	// satisfies a prerequisite, so it is safe beside a primary failure.
	resets := make(map[string]string)
	for stepName, reason := range canarySkipsToReset(slip) {
		resets[stepName] = "reset: canary retried after " + reason
	}

	// If any step has a primary failure, the pipeline is in a failed state.
	// Return completed=false because Failed is non-terminal — the pipeline can
	// recover if the failed steps are retried and succeed.
	if len(primaryFailures) > 0 {
		c.logger.Info(ctx, "Pipeline has primary step failure(s), updating slip status", map[string]interface{}{
			"correlation_id":   correlationID,
			"primary_failures": primaryFailures,
			"cascade_failures": cascadeFailures,
			"canary_resets":    resets,
		})
		// A canary reset that fails here is logged by resetStepsToPending; the conditions still
		// hold, so the next terminal step event retries it.
		c.resetStepsToPending(ctx, correlationID, resets)
		if err := c.UpdateSlipStatus(ctx, correlationID, SlipStatusFailed); err != nil {
			return false, SlipStatusFailed, fmt.Errorf("%w: %s", ErrSlipStatusUpdateFailed, err.Error())
		}
		return false, SlipStatusFailed, nil
	}

	// Mark pipeline completed only when steady-state is complete and there are
	// no remaining primary failures.
	if step, ok := slip.Steps["prod_steady_state"]; ok && step.Status == StepStatusCompleted {
		c.logger.Info(ctx, "Pipeline complete! Updating slip status to completed", map[string]interface{}{
			"correlation_id": correlationID,
		})
		if err := c.UpdateSlipStatus(ctx, correlationID, SlipStatusCompleted); err != nil {
			return true, SlipStatusCompleted, fmt.Errorf("%w: %s", ErrSlipStatusUpdateFailed, err.Error())
		}
		return true, SlipStatusCompleted, nil
	}

	// If the slip was previously failed, reconcile: reset cascade-aborted steps to pending so
	// they can re-run, then move the slip back to in_progress.
	if slip.Status == SlipStatusFailed {
		c.logger.Info(ctx, "No primary failures remain, reconciling slip status to in_progress", map[string]interface{}{
			"correlation_id": correlationID,
			"cascade_resets": cascadeFailures,
			"canary_resets":  resets,
		})

		// Reset cascade-aborted steps to pending so downstream steps can be
		// re-evaluated by the prerequisite system on their next pre-execution.
		// If any reset fails, keep the slip as Failed so the next call retries
		// rather than leaving orphaned aborted steps on an in_progress slip.
		for _, stepName := range cascadeFailures {
			resets[stepName] = "reset: upstream failure resolved"
		}
		resetFailures := c.resetStepsToPending(ctx, correlationID, resets)

		// If any cascade resets failed, keep the slip as Failed so the next
		// checkPipelineCompletion call retries the resets. Moving to InProgress
		// with orphaned aborted steps would leave them permanently stuck.
		if len(resetFailures) > 0 {
			c.logger.Warn(ctx, "Keeping slip as failed due to cascade reset failures", map[string]interface{}{
				"correlation_id": correlationID,
				"reset_failures": resetFailures,
			})
			return false, SlipStatusFailed, nil
		}

		if err := c.UpdateSlipStatus(ctx, correlationID, SlipStatusInProgress); err != nil {
			return false, SlipStatusInProgress, fmt.Errorf("%w: %s", ErrSlipStatusUpdateFailed, err.Error())
		}
		return false, SlipStatusInProgress, nil
	}

	// A canary reset that fails here is logged by resetStepsToPending; the conditions still hold,
	// so the next terminal step event retries it.
	if len(resets) > 0 {
		c.logger.Info(ctx, "Resetting canary skips after a successful canary retry", map[string]interface{}{
			"correlation_id": correlationID,
			"canary_resets":  resets,
		})
		c.resetStepsToPending(ctx, correlationID, resets)
	}

	return false, slip.Status, nil
}

// resetStepsToPending writes each step back to pending with its reset message and returns the
// steps whose write failed (each failure is logged).
func (c *Client) resetStepsToPending(ctx context.Context, correlationID string, resets map[string]string) []string {
	var failures []string
	for stepName, message := range resets {
		if err := c.UpdateStepWithStatus(ctx, correlationID, stepName, "", StepStatusPending, message); err != nil {
			failures = append(failures, stepName)
			c.logger.Warn(ctx, "Failed to reset step to pending", map[string]interface{}{
				"correlation_id": correlationID,
				"step_name":      stepName,
				"error":          err.Error(),
			})
		}
	}
	return failures
}

// canarySkipReasons are the reasons the canary writers record when they skip prod_deploy and
// prod_tests after a failed canary (decision D13): rollout-watch for its Aborted, Timeout,
// SyncFailed and Error outcomes, create-github-release's canary-not-deployed for a refusal before
// the watch. A skip with any other reason (a non-canary repo, create-github-release step 0) is not
// a canary skip.
var canarySkipReasons = map[string]struct{}{
	"canary_abort":                  {},
	"timeout":                       {},
	"sync_failed":                   {},
	"watch_lost":                    {},
	"deploy_failed":                 {},
	"release_deadline_insufficient": {},
}

// canarySkippableSteps are the steps a D13 canary skip writes.
var canarySkippableSteps = []string{"prod_deploy", "prod_tests"}

// canarySkipsToReset returns the D13 canary skips that a successful prod_canary has made stale,
// mapped to the reason each was skipped with: a step in canarySkippableSteps whose status is
// skipped and whose latest pipeline-level history entry is that skip, with a canarySkipReasons
// reason. It returns nothing while prod_canary is not a success, so a skip written beside a canary
// failure that has not landed yet (or never lands) is kept.
func canarySkipsToReset(slip *Slip) map[string]string {
	canary, ok := slip.Steps["prod_canary"]
	if !ok || !canary.Status.IsSuccess() {
		return nil
	}
	resets := make(map[string]string)
	for _, stepName := range canarySkippableSteps {
		if slip.Steps[stepName].Status != StepStatusSkipped {
			continue
		}
		entry, found := latestStepEntry(slip.StateHistory, stepName)
		if !found || entry.Status != StepStatusSkipped {
			continue
		}
		if _, canaryReason := canarySkipReasons[entry.Message]; canaryReason {
			resets[stepName] = entry.Message
		}
	}
	return resets
}

// latestStepEntry returns the last pipeline-level (componentless) history entry for stepName, in
// append order.
func latestStepEntry(history []StateHistoryEntry, stepName string) (StateHistoryEntry, bool) {
	for _, entry := range slices.Backward(history) {
		if entry.Step == stepName && entry.Component == "" {
			return entry, true
		}
	}
	return StateHistoryEntry{}, false
}

// ParsePrerequisites parses a comma-separated string of prerequisites.
func ParsePrerequisites(prereqStr string) []string {
	if prereqStr == "" {
		return nil
	}
	parts := strings.Split(prereqStr, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
