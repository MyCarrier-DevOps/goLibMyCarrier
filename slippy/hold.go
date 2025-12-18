package slippy

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// HoldOptions configures the hold/wait behavior.
type HoldOptions struct {
	// CorrelationID is the unique identifier for the routing slip.
	// This is the slip to check prerequisites on.
	CorrelationID string

	// Prerequisites is the list of prerequisite step names
	Prerequisites []string

	// ComponentName is the component context (if applicable)
	ComponentName string

	// Timeout is the maximum time to wait (defaults to config.HoldTimeout)
	Timeout time.Duration

	// PollInterval is the interval between checks (defaults to config.PollInterval)
	PollInterval time.Duration

	// StepName is the step that is being held (for status updates)
	StepName string
}

// WaitForPrerequisites waits for prerequisites with hold/timeout logic.
// Returns nil if prerequisites are satisfied, error if failed or timeout.
func (c *Client) WaitForPrerequisites(ctx context.Context, opts HoldOptions) error {
	// Apply defaults from config
	if opts.Timeout == 0 {
		opts.Timeout = c.config.HoldTimeout
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = c.config.PollInterval
	}

	if len(opts.Prerequisites) == 0 {
		return nil
	}

	c.logger.Info(ctx, "Waiting for prerequisites", map[string]interface{}{
		"prerequisites": opts.Prerequisites,
		"timeout":       opts.Timeout.String(),
	})

	holdStart := time.Now()
	holdDeadline := holdStart.Add(opts.Timeout)

	// Set initial held status if step name provided
	if opts.StepName != "" {
		waitingFor := strings.Join(opts.Prerequisites, ", ")
		if err := c.HoldStep(ctx, opts.CorrelationID, opts.StepName, opts.ComponentName, waitingFor); err != nil {
			c.logger.Warn(ctx, "Failed to set held status", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: context cancelled while waiting for prerequisites", ErrContextCancelled)
		default:
		}

		slip, err := c.store.Load(ctx, opts.CorrelationID)
		if err != nil {
			return fmt.Errorf("failed to load slip: %w", err)
		}

		result, err := c.CheckPrerequisites(ctx, slip, opts.Prerequisites, opts.ComponentName)
		if err != nil {
			return fmt.Errorf("failed to check prerequisites: %w", err)
		}

		switch result.Status {
		case PrereqStatusCompleted:
			c.logger.Info(ctx, "All prerequisites completed", map[string]interface{}{
				"completed_prereqs": result.CompletedPrereqs,
			})
			return nil

		case PrereqStatusFailed:
			c.logger.Error(ctx, "Prerequisites failed", nil, map[string]interface{}{
				"failed_prereqs": result.FailedPrereqs,
			})
			return fmt.Errorf("%w: %v", ErrPrerequisiteFailed, result.FailedPrereqs)

		case PrereqStatusRunning:
			// Check timeout
			if time.Now().After(holdDeadline) {
				timeoutMsg := fmt.Sprintf("hold timeout exceeded after %v, still waiting for: %v",
					opts.Timeout, result.RunningPrereqs)
				c.logger.Error(ctx, "Hold timeout exceeded", nil, map[string]interface{}{
					"timeout":         opts.Timeout.String(),
					"running_prereqs": result.RunningPrereqs,
				})

				// Update step to timeout status
				if opts.StepName != "" {
					_ = c.TimeoutStep(ctx, opts.CorrelationID, opts.StepName, opts.ComponentName, timeoutMsg)
				}

				return fmt.Errorf("%w: %s", ErrHoldTimeout, timeoutMsg)
			}

			// Log progress
			elapsed := time.Since(holdStart).Round(time.Second)
			remaining := opts.Timeout - elapsed
			c.logger.Info(ctx, "Waiting for prerequisites", map[string]interface{}{
				"running_prereqs": result.RunningPrereqs,
				"elapsed":         elapsed.String(),
				"remaining":       remaining.String(),
			})

			// Wait for next poll
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: context cancelled during wait", ErrContextCancelled)
			case <-time.After(opts.PollInterval):
				// Continue to next iteration
			}
		}
	}
}

// HoldResult represents the result of a hold operation.
type HoldResult struct {
	// Outcome indicates what happened
	Outcome HoldOutcome

	// WaitedFor is how long we waited
	WaitedFor time.Duration

	// FailedPrereqs lists any prerequisites that failed (if outcome is Failed)
	FailedPrereqs []string

	// Message provides additional context
	Message string
}

// HoldOutcome represents the possible outcomes of a hold operation.
type HoldOutcome string

const (
	// HoldOutcomeProceeded indicates prerequisites were met
	HoldOutcomeProceeded HoldOutcome = "proceeded"

	// HoldOutcomeFailed indicates prerequisites failed
	HoldOutcomeFailed HoldOutcome = "failed"

	// HoldOutcomeTimeout indicates the hold timed out
	HoldOutcomeTimeout HoldOutcome = "timeout"

	// HoldOutcomeCancelled indicates the operation was cancelled
	HoldOutcomeCancelled HoldOutcome = "cancelled"
)

// WaitForPrerequisitesWithResult waits for prerequisites and returns detailed result.
func (c *Client) WaitForPrerequisitesWithResult(ctx context.Context, opts HoldOptions) (*HoldResult, error) {
	startTime := time.Now()

	err := c.WaitForPrerequisites(ctx, opts)

	result := &HoldResult{
		WaitedFor: time.Since(startTime),
	}

	if err == nil {
		result.Outcome = HoldOutcomeProceeded
		result.Message = "all prerequisites completed"
		return result, nil
	}

	// Determine outcome from error
	switch {
	case isTimeoutError(err):
		result.Outcome = HoldOutcomeTimeout
		result.Message = err.Error()
		return result, err

	case isPrereqFailedError(err):
		result.Outcome = HoldOutcomeFailed
		result.Message = err.Error()
		// Try to get failed prereqs from the slip
		if slip, loadErr := c.store.Load(ctx, opts.CorrelationID); loadErr == nil {
			prereqResult, _ := c.CheckPrerequisites(ctx, slip, opts.Prerequisites, opts.ComponentName)
			result.FailedPrereqs = prereqResult.FailedPrereqs
		}
		return result, err

	case isContextError(err):
		result.Outcome = HoldOutcomeCancelled
		result.Message = "operation cancelled"
		return result, err

	default:
		result.Outcome = HoldOutcomeFailed
		result.Message = err.Error()
		return result, err
	}
}

// isTimeoutError checks if the error is a timeout error.
func isTimeoutError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "timeout")
}

// isPrereqFailedError checks if the error is a prerequisite failure.
func isPrereqFailedError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "prerequisite")
}

// isContextError checks if the error is a context cancellation.
func isContextError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cancelled")
}
