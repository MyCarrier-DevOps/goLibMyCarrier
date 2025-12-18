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
func (c *Client) UpdateStepWithStatus(ctx context.Context, correlationID, stepName, componentName string, status StepStatus, message string) error {
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
		c.logger.Error(ctx, "Failed to append history", err, map[string]interface{}{
			"correlation_id": correlationID,
			"step_name":      stepName,
		})
		// Non-fatal - continue
	}

	c.logger.Info(ctx, "Updated step", map[string]interface{}{
		"step_name": stepName,
		"status":    string(status),
	})

	// Check if this affects aggregates (build/unit_test -> aggregate steps)
	if stepName == "build" || stepName == "unit_test" {
		if err := c.checkAndUpdateAggregates(ctx, correlationID, stepName); err != nil {
			c.logger.Error(ctx, "Failed to update aggregates", err, map[string]interface{}{
				"correlation_id": correlationID,
				"step_name":      stepName,
			})
			// Non-fatal
		}
	}

	return nil
}

// checkAndUpdateAggregates checks if all components have completed a step type
// and updates the corresponding aggregate step (builds_completed, unit_tests_completed).
func (c *Client) checkAndUpdateAggregates(ctx context.Context, correlationID, stepName string) error {
	aggregateMap := map[string]string{
		"build":     "builds_completed",
		"unit_test": "unit_tests_completed",
	}

	aggregateStep, ok := aggregateMap[stepName]
	if !ok {
		return nil
	}

	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return fmt.Errorf("failed to load slip for aggregate check: %w", err)
	}

	allCompleted := true
	anyFailed := false

	for _, comp := range slip.Components {
		var status StepStatus
		switch stepName {
		case "build":
			status = comp.BuildStatus
		case "unit_test":
			status = comp.UnitTestStatus
		}

		if !status.IsTerminal() {
			allCompleted = false
		}
		if status.IsFailure() {
			anyFailed = true
		}
	}

	if !allCompleted {
		c.logger.Debug(ctx, "Aggregate not yet complete, some components still running", map[string]interface{}{
			"aggregate_step": aggregateStep,
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
		"aggregate_step": aggregateStep,
		"status":         string(aggregateStatus),
	})
	return c.UpdateStepWithStatus(ctx, slip.CorrelationID, aggregateStep, "", aggregateStatus, message)
}

// UpdateComponentBuildStatus updates a component's build status.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) UpdateComponentBuildStatus(ctx context.Context, correlationID, componentName string, status StepStatus, message string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, "build", componentName, status, message)
}

// UpdateComponentTestStatus updates a component's unit test status.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) UpdateComponentTestStatus(ctx context.Context, correlationID, componentName string, status StepStatus, message string) error {
	return c.UpdateStepWithStatus(ctx, correlationID, "unit_test", componentName, status, message)
}

// SetComponentImageTag sets the image tag for a component after successful build.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) SetComponentImageTag(ctx context.Context, correlationID, componentName, imageTag string) error {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return NewSlipError("set image tag", correlationID, err)
	}

	// Find and update the component
	found := false
	for i := range slip.Components {
		if slip.Components[i].Name == componentName {
			slip.Components[i].ImageTag = imageTag
			found = true
			break
		}
	}

	if !found {
		return NewStepError("set image tag", correlationID, "build", componentName,
			fmt.Errorf("component not found: %s", componentName))
	}

	if err := c.store.Update(ctx, slip); err != nil {
		return NewSlipError("set image tag", correlationID, err)
	}

	c.logger.Info(ctx, "Set image tag for component", map[string]interface{}{
		"component_name": componentName,
		"image_tag":      imageTag,
	})
	return nil
}
