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
func (c *Client) StartStep(ctx context.Context, correlationID, stepName, componentName string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, stepName, componentName,
		StepStatusRunning, "step started")
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

// SkipStep marks a step as skipped.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) SkipStep(ctx context.Context, correlationID, stepName, componentName, reason string) error {
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
	entry := StateHistoryEntry{
		Step:      stepName,
		Component: componentName,
		Status:    status,
		Timestamp: time.Now(),
		Actor:     "slippy-library",
		Message:   message,
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
	// performed inside the store (updateAggregateStatusFromComponentStatesWithHistory)
	// do not re-enter this path, so they do not trigger pipeline completion from here.
	//
	// Known race window: checkPipelineCompletion calls UpdateSlipStatus which does a
	// full Load+store.Update row rewrite. A concurrent appendHistoryWithOverrides
	// landing between the Load and Update may lose a state_history entry (last-write-wins).
	// Step statuses are unaffected (hydrateSlip re-derives them from slip_component_states).
	// Follow-up: UpdateSlipStatusAtomic (INSERT SELECT override for status column only)
	// eliminates this race without a full row rewrite.
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
