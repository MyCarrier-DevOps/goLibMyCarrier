package slippy

import (
	"context"
	"testing"
	"time"
)

func TestAppendHistoryEntry(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	// Create a slip with initial history
	slip := &Slip{
		CorrelationID: "hist-001",
		StateHistory:  []StateHistoryEntry{},
	}
	store.Slips[slip.CorrelationID] = slip

	// Append a history entry
	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "build",
		Status:    StepStatusCompleted,
		Message:   "test message",
		Actor:     "unit-test",
	}
	err := client.AppendHistoryEntry(ctx, slip.CorrelationID, entry)
	if err != nil {
		t.Fatalf("AppendHistoryEntry() error = %v", err)
	}

	// Verify entry was appended
	updated := store.Slips[slip.CorrelationID]
	if len(updated.StateHistory) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(updated.StateHistory))
	}
	if updated.StateHistory[0].Message != "test message" {
		t.Errorf("expected message 'test message', got %q", updated.StateHistory[0].Message)
	}
}

func TestAppendHistoryEntry_StoreError(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	store.AppendHistoryError = ErrStoreConnection
	client := &Client{store: store, logger: newTestLogger()}

	entry := StateHistoryEntry{Step: "test", Status: StepStatusCompleted}
	err := client.AppendHistoryEntry(ctx, "hist-002", entry)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestRecordTransition(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	slip := &Slip{
		CorrelationID: "trans-001",
		Status:        SlipStatusInProgress,
		StateHistory:  []StateHistoryEntry{},
	}
	store.Slips[slip.CorrelationID] = slip

	err := client.RecordTransition(ctx, slip.CorrelationID, "build", "", StepStatusCompleted, "test transition")
	if err != nil {
		t.Fatalf("RecordTransition() error = %v", err)
	}

	// Verify history entry was recorded
	updated := store.Slips[slip.CorrelationID]
	if len(updated.StateHistory) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(updated.StateHistory))
	}
}

func TestGetHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	now := time.Now()
	slip := &Slip{
		CorrelationID: "get-hist-001",
		StateHistory: []StateHistoryEntry{
			{Timestamp: now.Add(-2 * time.Hour), Step: "push_parsed", Status: StepStatusCompleted, Message: "created"},
			{Timestamp: now.Add(-1 * time.Hour), Step: "build", Status: StepStatusRunning, Message: "step1 started"},
			{Timestamp: now, Step: "deploy", Status: StepStatusCompleted, Message: "completed"},
		},
	}
	store.Slips[slip.CorrelationID] = slip

	history, err := client.GetHistory(ctx, slip.CorrelationID)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 history entries, got %d", len(history))
	}
}

func TestGetHistory_SlipNotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	_, err := client.GetHistory(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestGetStepHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	slip := &Slip{
		CorrelationID: "step-hist-001",
		StateHistory: []StateHistoryEntry{
			{Step: "build", Status: StepStatusRunning, Message: "started"},
			{Step: "deploy", Status: StepStatusRunning, Message: "started"},
			{Step: "build", Status: StepStatusCompleted, Message: "completed"},
			{Step: "test", Status: StepStatusRunning, Message: "transition"},
		},
	}
	store.Slips[slip.CorrelationID] = slip

	history, err := client.GetStepHistory(ctx, slip.CorrelationID, "build")
	if err != nil {
		t.Fatalf("GetStepHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 entries for step 'build', got %d", len(history))
	}
}

func TestGetComponentHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	slip := &Slip{
		CorrelationID: "comp-hist-001",
		StateHistory: []StateHistoryEntry{
			{Step: "build", Component: "api", Status: StepStatusRunning, Message: "started"},
			{Step: "build", Component: "worker", Status: StepStatusRunning, Message: "started"},
			{Step: "build", Component: "api", Status: StepStatusCompleted, Message: "completed"},
		},
	}
	store.Slips[slip.CorrelationID] = slip

	history, err := client.GetComponentHistory(ctx, slip.CorrelationID, "api")
	if err != nil {
		t.Fatalf("GetComponentHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 entries for component 'api', got %d", len(history))
	}
}

func TestGetRecentHistory(t *testing.T) {
	tests := []struct {
		name          string
		historyCount  int
		limit         int
		expectedCount int
	}{
		{
			name:          "limit less than history",
			historyCount:  10,
			limit:         5,
			expectedCount: 5,
		},
		{
			name:          "limit greater than history",
			historyCount:  3,
			limit:         10,
			expectedCount: 3,
		},
		{
			name:          "limit equals history",
			historyCount:  5,
			limit:         5,
			expectedCount: 5,
		},
		{
			name:          "zero limit returns all",
			historyCount:  7,
			limit:         0,
			expectedCount: 7,
		},
		{
			name:          "negative limit returns all",
			historyCount:  4,
			limit:         -1,
			expectedCount: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewMockStore()
			client := &Client{store: store, logger: newTestLogger()}

			// Create slip with history entries
			history := make([]StateHistoryEntry, tt.historyCount)
			for i := 0; i < tt.historyCount; i++ {
				history[i] = StateHistoryEntry{
					Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
					Step:      "test",
					Status:    StepStatusCompleted,
					Message:   "entry",
				}
			}

			slip := &Slip{
				CorrelationID: "recent-" + tt.name,
				StateHistory:  history,
			}
			store.Slips[slip.CorrelationID] = slip

			result, err := client.GetRecentHistory(ctx, slip.CorrelationID, tt.limit)
			if err != nil {
				t.Fatalf("GetRecentHistory() error = %v", err)
			}
			if len(result) != tt.expectedCount {
				t.Errorf("expected %d entries, got %d", tt.expectedCount, len(result))
			}
		})
	}
}

func TestGetHistorySince(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	now := time.Now()
	slip := &Slip{
		CorrelationID: "since-001",
		StateHistory: []StateHistoryEntry{
			{Timestamp: now.Add(-3 * time.Hour), Step: "old", Status: StepStatusCompleted, Message: "old1"},
			{Timestamp: now.Add(-2 * time.Hour), Step: "old", Status: StepStatusCompleted, Message: "old2"},
			{Timestamp: now.Add(-30 * time.Minute), Step: "recent", Status: StepStatusCompleted, Message: "recent1"},
			{Timestamp: now.Add(-10 * time.Minute), Step: "recent", Status: StepStatusCompleted, Message: "recent2"},
		},
	}
	store.Slips[slip.CorrelationID] = slip

	// Get entries since 1 hour ago
	since := now.Add(-1 * time.Hour)
	history, err := client.GetHistorySince(ctx, slip.CorrelationID, since)
	if err != nil {
		t.Fatalf("GetHistorySince() error = %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 entries since 1 hour ago, got %d", len(history))
	}
}

func TestGetHistorySince_NoMatches(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := &Client{store: store, logger: newTestLogger()}

	now := time.Now()
	slip := &Slip{
		CorrelationID: "since-empty",
		StateHistory: []StateHistoryEntry{
			{Timestamp: now.Add(-2 * time.Hour), Step: "old", Status: StepStatusCompleted, Message: "old"},
		},
	}
	store.Slips[slip.CorrelationID] = slip

	// Get entries since now - should return empty
	history, err := client.GetHistorySince(ctx, slip.CorrelationID, now)
	if err != nil {
		t.Fatalf("GetHistorySince() error = %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 entries, got %d", len(history))
	}
}

func TestGetHistorySince_LoadError(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	store.LoadError = ErrStoreConnection
	client := &Client{store: store, logger: newTestLogger()}

	_, err := client.GetHistorySince(ctx, "test-id", time.Now())
	if err == nil {
		t.Error("expected error, got nil")
	}
}
