package slippy

import (
	"context"
	"fmt"
	"time"
)

// CompleteStep marks a step as completed and records history.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) CompleteStep(ctx context.Context, correlationID, stepName, componentName string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, stepName, componentName,
		StepStatusCompleted, "step completed successfully")
}

// FailStep marks a step as failed and records history.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) FailStep(ctx context.Context, correlationID, stepName, componentName, reason string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, stepName, componentName,
		StepStatusFailed, reason)
}

// StartStep marks a step as running.
// The correlationID is the unique identifier for the routing slip.
//
// claimedBy names the run that starts the step (the CLI's workflow name). The store records it
// while the step is running, and a start that names none leaves the step unclaimed, so a reader
// deciding whether a running step belongs to a run reads an empty claimant as not its own. That
// read is not atomic with a later write: another run of the same slip can start the step between
// the read and the reader's close, and the close then lands on that run's step.
func (c *Client) StartStep(ctx context.Context, correlationID, stepName, componentName, claimedBy string) error {
	return c.updateStep(ctx, correlationID, stepName, componentName, StepStatusRunning, "step started", claimedBy)
}

// HoldStep marks a step as held waiting for prerequisites.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) HoldStep(ctx context.Context, correlationID, stepName, componentName, waitingFor string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, stepName, componentName,
		StepStatusHeld, fmt.Sprintf("waiting for: %s", waitingFor))
}

// AbortStep marks a step as aborted due to upstream failure.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) AbortStep(ctx context.Context, correlationID, stepName, componentName, reason string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, stepName, componentName,
		StepStatusAborted, reason)
}

// TimeoutStep marks a step as timed out.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) TimeoutStep(ctx context.Context, correlationID, stepName, componentName, reason string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, stepName, componentName,
		StepStatusTimeout, reason)
}

// skipGuardedSteps are the steps whose failed status SkipStep must never overwrite
// (STATE_MACHINE_V3 "Skip-writer contract"): the canary steps, and the two steps a D13 canary
// skip writes.
var skipGuardedSteps = map[string]struct{}{
	"prod_canary":           {},
	"preprod_rollback_test": {},
	"prod_deploy":           {},
	"prod_tests":            {},
}

// SkipStep marks a step as skipped.
// The correlationID is the unique identifier for the routing slip.
//
// A failed prod_canary, preprod_rollback_test, prod_deploy or prod_tests is never overwritten:
// skipped counts as success, so the skip would clear a primary failure. For a canary step it
// would unblock promotion past a failed canary; for prod_deploy or prod_tests (a D13 canary skip
// on a re-run whose deploy already failed) it would hide the failed deploy, and a later canary
// retry would return the slip to in_progress. In that case SkipStep returns
// ErrSkipOverFailedCanary and writes nothing. The check reads the slip first and is not atomic
// with the write. The skip writers (create-github-release step 0, rollout-watch) run outside the
// release mutex, so a failed status that a concurrent run of the same slip writes between the
// read and the write is overwritten.
func (c *Client) SkipStep(ctx context.Context, correlationID, stepName, componentName, reason string) error {
	if _, guarded := skipGuardedSteps[stepName]; guarded {
		slip, err := c.store.Load(ctx, correlationID)
		if err != nil {
			return NewStepError("skip", correlationID, stepName, componentName, err)
		}
		if slip.Steps[stepName].Status == StepStatusFailed {
			return NewStepError("skip", correlationID, stepName, componentName, ErrSkipOverFailedCanary)
		}
	}
	return c.UpdateStepWithStatus(ctx, correlationID, stepName, componentName,
		StepStatusSkipped, reason)
}

// UpdateStepWithStatus updates a step's status with a message and records history.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) UpdateStepWithStatus(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
	message string,
) error {
	return c.updateStep(ctx, correlationID, stepName, componentName, status, message, "")
}

// updateStep is UpdateStepWithStatus with the claimant of a running write.
func (c *Client) updateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
	message, claimedBy string,
) error {
	entry := StateHistoryEntry{
		Step:      stepName,
		Component: componentName,
		Status:    status,
		Timestamp: time.Now(),
		Actor:     LibraryActor,
		Message:   message,
		ClaimedBy: claimedBy,
	}

	// Use the combined UpdateStepWithHistory to update step AND append history atomically.
	// This prevents the race condition where a separate AppendHistory call could reload
	// a stale slip and overwrite the step update.
	if err := c.store.UpdateStepWithHistory(ctx, correlationID, stepName, componentName, status, entry); err != nil {
		return NewStepError("update", correlationID, stepName, componentName, err)
	}

	c.logger.Info(ctx, "Updated step", map[string]interface{}{
		"step_name": stepName,
		"status":    string(status),
	})

	// After any terminal event on a pipeline-level step (componentName == ""),
	// re-evaluate the overall pipeline state. This propagates step failures to
	// slip.status = "failed" and recovers it back to "in_progress" when all
	// primary failures are resolved on rerun.
	//
	// Component-level events (componentName != "") are skipped here because this
	// completion check only runs for terminal updates that pass through
	// Client.UpdateStepWithStatus at the pipeline-step level. Aggregate rollups
	// performed inside the store (PostgresStore.recomputeAggregate) do not re-enter this
	// path, so they do not trigger pipeline completion from here.
	//
	// UpdateSlipStatus (store.UpdateSlipStatus) is a targeted status write with no Load+Update
	// round-trip: it sets status and updated_at, and on a terminal status also clears
	// claimed_from and appends a slip_released marker when a claim was held (see
	// SlipStore.UpdateSlipStatus). It never rewrites state_history from a snapshot, so it
	// cannot overwrite a concurrent state_history append.
	if status.IsTerminal() && componentName == "" {
		if _, _, checkErr := c.checkPipelineCompletion(ctx, correlationID); checkErr != nil {
			c.logger.Warn(ctx, "pipeline completion check failed (non-fatal)", map[string]interface{}{
				"correlation_id": correlationID,
				"step_name":      stepName,
				"status":         string(status),
				"error":          checkErr,
			})
		}
	}

	return nil
}

// UpdateComponentBuildStatus updates a component's build status.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) UpdateComponentBuildStatus(
	ctx context.Context,
	correlationID, componentName string,
	status StepStatus,
	message string,
) error {
	return c.UpdateStepWithStatus(ctx, correlationID, "build", componentName, status, message)
}

// UpdateComponentTestStatus updates a component's unit test status.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) UpdateComponentTestStatus(
	ctx context.Context,
	correlationID, componentName string,
	status StepStatus,
	message string,
) error {
	return c.UpdateStepWithStatus(ctx, correlationID, "unit_test", componentName, status, message)
}

// SetComponentImageTag records the container image tag for a component after a successful build.
// The image tag is stored in the component event log (slip_component_states), replacing the
// previous Read-Modify-Write pattern that was susceptible to concurrent lost updates.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) SetComponentImageTag(ctx context.Context, correlationID, componentName, imageTag string) error {
	if c.pipelineConfig == nil {
		return NewStepError("set image tag", correlationID, "build", componentName,
			fmt.Errorf("no pipeline config set"))
	}

	aggregateStepName := c.pipelineConfig.GetAggregateStep("build")
	if aggregateStepName == "" {
		return NewStepError("set image tag", correlationID, "build", componentName,
			fmt.Errorf("no step configured to aggregate 'build' components"))
	}

	if err := c.store.SetComponentImageTag(ctx, correlationID, "build", componentName, imageTag); err != nil {
		return NewStepError("set image tag", correlationID, "build", componentName, err)
	}

	c.logger.Info(ctx, "Set image tag for component", map[string]interface{}{
		"component_name": componentName,
		"image_tag":      imageTag,
	})
	return nil
}
