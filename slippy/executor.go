package slippy

import (
	"context"
	"fmt"
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
func (c *Client) checkPipelineCompletion(ctx context.Context, correlationID string) (bool, SlipStatus, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return false, "", fmt.Errorf("%w: failed to load slip for completion check: %s", ErrSlipNotFound, err.Error())
	}

	// Check if prod_steady_state is completed
	if step, ok := slip.Steps["prod_steady_state"]; ok && step.Status == StepStatusCompleted {
		c.logger.Info(ctx, "Pipeline complete! Updating slip status to completed", map[string]interface{}{
			"correlation_id": correlationID,
		})
		if err := c.UpdateSlipStatus(ctx, correlationID, SlipStatusCompleted); err != nil {
			return true, SlipStatusCompleted, fmt.Errorf("%w: %s", ErrSlipStatusUpdateFailed, err.Error())
		}
		return true, SlipStatusCompleted, nil
	}

	// Check if any terminal step failed (pipeline failed)
	for stepName, step := range slip.Steps {
		if step.Status == StepStatusFailed || step.Status == StepStatusError ||
			step.Status == StepStatusAborted || step.Status == StepStatusTimeout {
			c.logger.Info(ctx, "Pipeline failed at step, updating slip status", map[string]interface{}{
				"correlation_id": correlationID,
				"step_name":      stepName,
			})
			if err := c.UpdateSlipStatus(ctx, correlationID, SlipStatusFailed); err != nil {
				return true, SlipStatusFailed, fmt.Errorf("%w: %s", ErrSlipStatusUpdateFailed, err.Error())
			}
			return true, SlipStatusFailed, nil
		}
	}

	if slip.Status == SlipStatusFailed {
		c.logger.Info(ctx, "No failing steps remain, reconciling slip status to in_progress", map[string]interface{}{
			"correlation_id": correlationID,
		})
		if err := c.UpdateSlipStatus(ctx, correlationID, SlipStatusInProgress); err != nil {
			return false, SlipStatusInProgress, fmt.Errorf("%w: %s", ErrSlipStatusUpdateFailed, err.Error())
		}
		return false, SlipStatusInProgress, nil
	}

	return false, slip.Status, nil
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
