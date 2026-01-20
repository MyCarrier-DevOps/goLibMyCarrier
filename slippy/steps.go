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

	if err := c.store.UpdateStep(ctx, correlationID, stepName, componentName, status); err != nil {
		return NewStepError("update", correlationID, stepName, componentName, err)
	}

	if err := c.store.AppendHistory(ctx, correlationID, entry); err != nil {
		// Return the error - callers/shadow mode should decide if this is blocking
		// The step update succeeded, but the audit trail is incomplete
		return fmt.Errorf("%w: step %s updated to %s but history append failed: %s",
			ErrHistoryAppendFailed, stepName, status, err.Error())
	}

	c.logger.Info(ctx, "Updated step", map[string]interface{}{
		"step_name": stepName,
		"status":    string(status),
	})

	// Check if this is a component step that affects an aggregate
	if componentName != "" && c.pipelineConfig != nil {
		aggregateStep := c.pipelineConfig.GetAggregateStep(stepName)
		if aggregateStep != "" {
			if err := c.checkAndUpdateAggregate(ctx, correlationID, stepName, aggregateStep); err != nil {
				// Return the error - aggregate status is important for pipeline flow
				return fmt.Errorf("step %s updated but aggregate %s update failed: %w",
					stepName, aggregateStep, err)
			}
		}
	}

	return nil
}

// checkAndUpdateAggregate checks if all components have completed a step type
// and updates the corresponding aggregate step.
// This is config-driven: the stepName is a component type (e.g., "build")
// and aggregateStepName is the parent aggregate (e.g., "builds_completed").
func (c *Client) checkAndUpdateAggregate(ctx context.Context, correlationID, stepName, aggregateStepName string) error {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return fmt.Errorf("failed to load slip for aggregate check: %w", err)
	}

	// Get component data from the aggregate column.
	// The aggregate JSON column is named after the aggregate step (e.g., "builds_completed")
	columnName := aggregateStepName
	componentData, ok := slip.Aggregates[columnName]
	if !ok || len(componentData) == 0 {
		c.logger.Debug(ctx, "No component data found for aggregate", map[string]interface{}{
			"aggregate_step": aggregateStepName,
			"column_name":    columnName,
		})
		return nil
	}

	allCompleted := true
	anyFailed := false

	for _, comp := range componentData {
		if !comp.Status.IsTerminal() {
			allCompleted = false
		}
		if comp.Status.IsFailure() {
			anyFailed = true
		}
	}

	if !allCompleted {
		c.logger.Debug(ctx, "Aggregate not yet complete, some components still running", map[string]interface{}{
			"aggregate_step": aggregateStepName,
		})
		return nil
	}

	var aggregateStatus StepStatus
	var message string
	if anyFailed {
		aggregateStatus = StepStatusFailed
		message = "one or more components failed"
	} else {
		aggregateStatus = StepStatusCompleted
		message = "all components completed successfully"
	}

	c.logger.Info(ctx, "Updating aggregate", map[string]interface{}{
		"aggregate_step": aggregateStepName,
		"status":         string(aggregateStatus),
	})
	return c.UpdateStepWithStatus(ctx, slip.CorrelationID, aggregateStepName, "", aggregateStatus, message)
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

// SetComponentImageTag sets the image tag for a component after successful build.
// The correlationID is the unique identifier for the routing slip.
// The image tag is stored in the component's metadata within the builds aggregate column.
func (c *Client) SetComponentImageTag(ctx context.Context, correlationID, componentName, imageTag string) error {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return NewSlipError("set image tag", correlationID, err)
	}

	// Find the builds aggregate column
	columnName := "builds" // Default pluralized form of "build"
	componentData, ok := slip.Aggregates[columnName]
	if !ok {
		return NewStepError("set image tag", correlationID, "build", componentName,
			fmt.Errorf("builds aggregate not found"))
	}

	// Find and update the component
	found := false
	for i := range componentData {
		if componentData[i].Component == componentName {
			componentData[i].ImageTag = imageTag
			found = true
			break
		}
	}

	if !found {
		return NewStepError("set image tag", correlationID, "build", componentName,
			fmt.Errorf("component not found: %s", componentName))
	}

	slip.Aggregates[columnName] = componentData

	if err := c.store.Update(ctx, slip); err != nil {
		return NewSlipError("set image tag", correlationID, err)
	}

	c.logger.Info(ctx, "Set image tag for component", map[string]interface{}{
		"component_name": componentName,
		"image_tag":      imageTag,
	})
	return nil
}
