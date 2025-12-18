package slippy

import (
	"context"
	"time"
)

// AppendHistoryEntry appends a state history entry to a slip.
// This is a convenience method for recording state transitions.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) AppendHistoryEntry(ctx context.Context, correlationID string, entry StateHistoryEntry) error {
	// Ensure timestamp is set
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Set default actor if not provided
	if entry.Actor == "" {
		entry.Actor = "slippy-library"
	}

	if err := c.store.AppendHistory(ctx, correlationID, entry); err != nil {
		return NewSlipError("append history", correlationID, err)
	}

	c.logger.Debug(ctx, "Appended history entry", map[string]interface{}{
		"step":   entry.Step,
		"status": string(entry.Status),
	})
	return nil
}

// RecordTransition records a state transition with the given parameters.
// This is a convenience method for common state changes.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) RecordTransition(ctx context.Context, correlationID, stepName, componentName string, status StepStatus, message string) error {
	entry := StateHistoryEntry{
		Step:      stepName,
		Component: componentName,
		Status:    status,
		Timestamp: time.Now(),
		Actor:     "slippy-library",
		Message:   message,
	}

	return c.AppendHistoryEntry(ctx, correlationID, entry)
}

// GetHistory retrieves the state history for a slip.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) GetHistory(ctx context.Context, correlationID string) ([]StateHistoryEntry, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return nil, NewSlipError("get history", correlationID, err)
	}
	return slip.StateHistory, nil
}

// GetStepHistory retrieves history entries for a specific step.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) GetStepHistory(ctx context.Context, correlationID, stepName string) ([]StateHistoryEntry, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return nil, NewSlipError("get step history", correlationID, err)
	}

	var entries []StateHistoryEntry
	for _, entry := range slip.StateHistory {
		if entry.Step == stepName {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// GetComponentHistory retrieves history entries for a specific component.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) GetComponentHistory(ctx context.Context, correlationID, componentName string) ([]StateHistoryEntry, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return nil, NewSlipError("get component history", correlationID, err)
	}

	var entries []StateHistoryEntry
	for _, entry := range slip.StateHistory {
		if entry.Component == componentName {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// GetRecentHistory retrieves the most recent N history entries.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) GetRecentHistory(ctx context.Context, correlationID string, limit int) ([]StateHistoryEntry, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return nil, NewSlipError("get recent history", correlationID, err)
	}

	if limit <= 0 || limit >= len(slip.StateHistory) {
		return slip.StateHistory, nil
	}

	// Return the most recent entries
	start := len(slip.StateHistory) - limit
	return slip.StateHistory[start:], nil
}

// GetHistorySince retrieves history entries since a given timestamp.
// The correlationID is the unique identifier for the routing slip.
func (c *Client) GetHistorySince(ctx context.Context, correlationID string, since time.Time) ([]StateHistoryEntry, error) {
	slip, err := c.store.Load(ctx, correlationID)
	if err != nil {
		return nil, NewSlipError("get history since", correlationID, err)
	}

	var entries []StateHistoryEntry
	for _, entry := range slip.StateHistory {
		if entry.Timestamp.After(since) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}
