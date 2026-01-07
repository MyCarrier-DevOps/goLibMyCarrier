package slippy

import (
	"context"
	"strings"
)

// CheckPrerequisites checks if all prerequisites are satisfied for a step.
// It returns a PrereqResult with details about each prerequisite's status.
func (c *Client) CheckPrerequisites(
	ctx context.Context,
	slip *Slip,
	prerequisites []string,
	componentName string,
) (PrereqResult, error) {
	result := PrereqResult{
		Status:           PrereqStatusCompleted,
		CompletedPrereqs: make([]string, 0),
		RunningPrereqs:   make([]string, 0),
		FailedPrereqs:    make([]string, 0),
	}

	for _, prereq := range prerequisites {
		prereq = strings.TrimSpace(prereq)
		if prereq == "" {
			continue
		}

		status := c.getPrereqStatus(ctx, slip, prereq, componentName)

		switch {
		case status.IsSuccess():
			result.CompletedPrereqs = append(result.CompletedPrereqs, prereq)
		case status.IsFailure():
			result.FailedPrereqs = append(result.FailedPrereqs, prereq)
			result.Status = PrereqStatusFailed
		default:
			// pending, running, held
			result.RunningPrereqs = append(result.RunningPrereqs, prereq)
			if result.Status != PrereqStatusFailed {
				result.Status = PrereqStatusRunning
			}
		}
	}

	return result, nil
}

// getPrereqStatus gets the status of a specific prerequisite.
// For component-specific steps, it looks up the component in the aggregate data.
func (c *Client) getPrereqStatus(ctx context.Context, slip *Slip, prereq, componentName string) StepStatus {
	// Check if it's a component-specific step within an aggregate
	if componentName != "" && c.pipelineConfig != nil {
		// Look up if this prereq is a component type for any aggregate step
		aggregateStep := c.pipelineConfig.GetAggregateStep(prereq)
		if aggregateStep != "" {
			columnName := pluralize(prereq)
			if componentData, ok := slip.Aggregates[columnName]; ok {
				for _, comp := range componentData {
					if comp.Component == componentName {
						return comp.Status
					}
				}
			}
			// Component not found in aggregate data - still pending
			return StepStatusPending
		}
	}

	// Check aggregate/pipeline steps
	if step, ok := slip.Steps[prereq]; ok {
		return step.Status
	}

	c.logger.Warn(ctx, "Unknown prerequisite", map[string]interface{}{
		"prerequisite": prereq,
	})
	return StepStatusPending
}

// AllPrerequisitesMet returns true if all prerequisites are completed successfully.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) AllPrerequisitesMet(
	ctx context.Context,
	correlationID string,
	prerequisites []string,
	componentName string,
) (bool, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return false, err
	}

	result, err := c.CheckPrerequisites(ctx, slip, prerequisites, componentName)
	if err != nil {
		return false, err
	}

	return result.Status == PrereqStatusCompleted, nil
}

// AnyPrerequisiteFailed returns true if any prerequisite has failed.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) AnyPrerequisiteFailed(
	ctx context.Context,
	correlationID string,
	prerequisites []string,
	componentName string,
) (anyFailed bool, failedPrereqs []string, err error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return false, nil, err
	}

	result, err := c.CheckPrerequisites(ctx, slip, prerequisites, componentName)
	if err != nil {
		return false, nil, err
	}

	return result.Status == PrereqStatusFailed, result.FailedPrereqs, nil
}

// GetPendingPrerequisites returns the list of prerequisites that are not yet completed.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) GetPendingPrerequisites(
	ctx context.Context,
	correlationID string,
	prerequisites []string,
	componentName string,
) ([]string, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return nil, err
	}

	result, err := c.CheckPrerequisites(ctx, slip, prerequisites, componentName)
	if err != nil {
		return nil, err
	}

	// Combine running and failed (anything not completed)
	pending := make([]string, 0, len(result.RunningPrereqs)+len(result.FailedPrereqs))
	pending = append(pending, result.RunningPrereqs...)
	pending = append(pending, result.FailedPrereqs...)

	return pending, nil
}
