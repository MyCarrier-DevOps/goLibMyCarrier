package slippytest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

func TestMockStore_Create(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	slip := &slippy.Slip{
		CorrelationID: "test-123",
		Repository:    "test/repo",
		Branch:        "main",
		CommitSHA:     "abc123",
		Status:        slippy.SlipStatusPending,
	}

	err := store.Create(ctx, slip)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if len(store.CreateCalls) != 1 {
		t.Errorf("expected 1 create call, got %d", len(store.CreateCalls))
	}

	// Verify slip was stored
	loaded, err := store.Load(ctx, "test-123")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.CorrelationID != "test-123" {
		t.Errorf("expected correlation ID test-123, got %s", loaded.CorrelationID)
	}
}

func TestMockStore_Create_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("create error")
	store.CreateError = testErr

	slip := &slippy.Slip{CorrelationID: "test"}
	err := store.Create(ctx, slip)

	if err != testErr {
		t.Errorf("expected CreateError, got %v", err)
	}
}

func TestMockStore_Create_WithErrorFor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("specific error")
	store.CreateErrorFor["test-specific"] = testErr

	slip := &slippy.Slip{CorrelationID: "test-specific"}
	err := store.Create(ctx, slip)

	if err != testErr {
		t.Errorf("expected CreateErrorFor error, got %v", err)
	}
}

func TestMockStore_Load(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	// Add a slip directly
	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-456",
		Repository:    "test/repo",
	})

	slip, err := store.Load(ctx, "test-456")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if slip.CorrelationID != "test-456" {
		t.Errorf("expected correlation ID test-456, got %s", slip.CorrelationID)
	}

	if len(store.LoadCalls) != 1 {
		t.Errorf("expected 1 load call, got %d", len(store.LoadCalls))
	}
}

func TestMockStore_Load_NotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	_, err := store.Load(ctx, "nonexistent")
	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_LoadByCommit(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-789",
		Repository:    "test/repo",
		CommitSHA:     "commit123",
	})

	slip, err := store.LoadByCommit(ctx, "test/repo", "commit123")
	if err != nil {
		t.Fatalf("LoadByCommit failed: %v", err)
	}
	if slip.CorrelationID != "test-789" {
		t.Errorf("expected correlation ID test-789, got %s", slip.CorrelationID)
	}
}

func TestMockStore_LoadByCommit_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("load by commit error")
	store.LoadByCommitError = testErr

	_, err := store.LoadByCommit(ctx, "test/repo", "abc123")
	if err != testErr {
		t.Errorf("expected LoadByCommitError, got %v", err)
	}
}

func TestMockStore_LoadByCommit_NotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	_, err := store.LoadByCommit(ctx, "test/repo", "nonexistent")
	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_Update(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-update",
		Status:        slippy.SlipStatusPending,
	})

	slip, _ := store.Load(ctx, "test-update")
	slip.Status = slippy.SlipStatusCompleted

	err := store.Update(ctx, slip)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if len(store.UpdateCalls) != 1 {
		t.Errorf("expected 1 update call, got %d", len(store.UpdateCalls))
	}
}

func TestMockStore_Update_NotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	slip := &slippy.Slip{CorrelationID: "nonexistent"}
	err := store.Update(ctx, slip)

	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_Update_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("update error")
	store.UpdateError = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "test"})
	slip, _ := store.Load(ctx, "test")

	err := store.Update(ctx, slip)
	if err != testErr {
		t.Errorf("expected UpdateError, got %v", err)
	}
}

func TestMockStore_UpdateStep(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-step",
		Steps:         map[string]slippy.Step{},
	})

	err := store.UpdateStep(ctx, "test-step", "build", "", slippy.StepStatusCompleted)
	if err != nil {
		t.Fatalf("UpdateStep failed: %v", err)
	}

	if len(store.UpdateStepCalls) != 1 {
		t.Errorf("expected 1 UpdateStep call, got %d", len(store.UpdateStepCalls))
	}
}

func TestMockStore_UpdateStep_NotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	err := store.UpdateStep(ctx, "nonexistent", "build", "", slippy.StepStatusCompleted)
	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_UpdateStep_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("update step error")
	store.UpdateStepError = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "test"})

	err := store.UpdateStep(ctx, "test", "build", "", slippy.StepStatusCompleted)
	if err != testErr {
		t.Errorf("expected UpdateStepError, got %v", err)
	}
}

func TestMockStore_UpdateStep_WithErrorFor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("specific step error")
	store.UpdateStepErrorFor["test-specific"] = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "test-specific"})

	err := store.UpdateStep(ctx, "test-specific", "build", "", slippy.StepStatusCompleted)
	if err != testErr {
		t.Errorf("expected UpdateStepErrorFor error, got %v", err)
	}
}

func TestMockStore_UpdateStep_NilSteps(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	// Add slip with nil Steps map
	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-nil-steps",
	})

	err := store.UpdateStep(ctx, "test-nil-steps", "build", "", slippy.StepStatusCompleted)
	if err != nil {
		t.Fatalf("UpdateStep with nil steps failed: %v", err)
	}
}

func TestMockStore_UpdateComponentStatus(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-comp",
		Aggregates: map[string][]slippy.ComponentStepData{
			"builds": {
				{Component: "api", Status: slippy.StepStatusPending},
			},
		},
	})

	err := store.UpdateComponentStatus(ctx, "test-comp", "api", "build", slippy.StepStatusCompleted)
	if err != nil {
		t.Fatalf("UpdateComponentStatus failed: %v", err)
	}

	if len(store.UpdateComponentCalls) != 1 {
		t.Errorf("expected 1 UpdateComponentStatus call, got %d", len(store.UpdateComponentCalls))
	}

	// Verify the status was updated
	slip, _ := store.Load(ctx, "test-comp")
	if slip.Aggregates["builds"][0].Status != slippy.StepStatusCompleted {
		t.Errorf("expected status completed, got %s", slip.Aggregates["builds"][0].Status)
	}
}

func TestMockStore_UpdateComponentStatus_NotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	err := store.UpdateComponentStatus(ctx, "nonexistent", "api", "builds", slippy.StepStatusCompleted)
	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_UpdateComponentStatus_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("update component error")
	store.UpdateComponentError = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "test"})

	err := store.UpdateComponentStatus(ctx, "test", "api", "builds", slippy.StepStatusCompleted)
	if err != testErr {
		t.Errorf("expected UpdateComponentError, got %v", err)
	}
}

func TestMockStore_UpdateComponentStatus_WithErrorFor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("specific component error")
	store.UpdateComponentErrorFor["test-specific"] = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "test-specific"})

	err := store.UpdateComponentStatus(ctx, "test-specific", "api", "builds", slippy.StepStatusCompleted)
	if err != testErr {
		t.Errorf("expected UpdateComponentErrorFor error, got %v", err)
	}
}

func TestMockStore_UpdateComponentStatus_ComponentNotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test",
		Aggregates: map[string][]slippy.ComponentStepData{
			"builds": {
				{Component: "other", Status: slippy.StepStatusPending},
			},
		},
	})

	// Should return nil even if component not found
	err := store.UpdateComponentStatus(ctx, "test", "nonexistent", "build", slippy.StepStatusCompleted)
	if err != nil {
		t.Errorf("expected nil error for missing component, got %v", err)
	}
}

func TestMockStore_AppendHistory(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-history",
		StateHistory:  []slippy.StateHistoryEntry{},
	})

	entry := slippy.StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "build",
		Status:    slippy.StepStatusCompleted,
	}

	err := store.AppendHistory(ctx, "test-history", entry)
	if err != nil {
		t.Fatalf("AppendHistory failed: %v", err)
	}

	if len(store.AppendHistoryCalls) != 1 {
		t.Errorf("expected 1 AppendHistory call, got %d", len(store.AppendHistoryCalls))
	}
}

func TestMockStore_AppendHistory_NotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	entry := slippy.StateHistoryEntry{Timestamp: time.Now()}
	err := store.AppendHistory(ctx, "nonexistent", entry)

	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_AppendHistory_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("append history error")
	store.AppendHistoryError = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "test"})

	entry := slippy.StateHistoryEntry{Timestamp: time.Now()}
	err := store.AppendHistory(ctx, "test", entry)

	if err != testErr {
		t.Errorf("expected AppendHistoryError, got %v", err)
	}
}

func TestMockStore_AppendHistory_WithErrorFor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("specific history error")
	store.AppendHistoryErrorFor["test-specific"] = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "test-specific"})

	entry := slippy.StateHistoryEntry{Timestamp: time.Now()}
	err := store.AppendHistory(ctx, "test-specific", entry)

	if err != testErr {
		t.Errorf("expected AppendHistoryErrorFor error, got %v", err)
	}
}

func TestMockStore_FindByCommits(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-find",
		Repository:    "test/repo",
		CommitSHA:     "abc123",
	})

	slip, matchedCommit, err := store.FindByCommits(ctx, "test/repo", []string{"abc123", "def456"})
	if err != nil {
		t.Fatalf("FindByCommits failed: %v", err)
	}
	if slip == nil {
		t.Fatal("expected slip, got nil")
	}
	if matchedCommit != "abc123" {
		t.Errorf("expected matched commit abc123, got %s", matchedCommit)
	}
}

func TestMockStore_FindByCommits_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("find by commits error")
	store.FindByCommitsError = testErr

	_, _, err := store.FindByCommits(ctx, "test/repo", []string{"abc123"})
	if err != testErr {
		t.Errorf("expected FindByCommitsError, got %v", err)
	}
}

func TestMockStore_FindByCommits_NotFound(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	_, _, err := store.FindByCommits(ctx, "test/repo", []string{"nonexistent"})
	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_FindAllByCommits(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-1",
		Repository:    "test/repo",
		CommitSHA:     "abc123",
	})
	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-2",
		Repository:    "test/repo",
		CommitSHA:     "def456",
	})

	results, err := store.FindAllByCommits(ctx, "test/repo", []string{"abc123", "def456", "ghi789"})
	if err != nil {
		t.Fatalf("FindAllByCommits failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	if len(store.FindAllByCommitsCalls) != 1 {
		t.Errorf("expected 1 FindAllByCommits call, got %d", len(store.FindAllByCommitsCalls))
	}
}

func TestMockStore_FindAllByCommits_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("find all error")
	store.FindAllByCommitsError = testErr

	_, err := store.FindAllByCommits(ctx, "test/repo", []string{"abc123"})
	if err != testErr {
		t.Errorf("expected FindAllByCommitsError, got %v", err)
	}
}

func TestMockStore_Close(t *testing.T) {
	store := NewMockStore()

	err := store.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if store.CloseCalls != 1 {
		t.Errorf("expected 1 close call, got %d", store.CloseCalls)
	}
}

func TestMockStore_Close_WithError(t *testing.T) {
	store := NewMockStore()
	testErr := errors.New("close error")
	store.CloseError = testErr

	err := store.Close()
	if err != testErr {
		t.Errorf("expected CloseError, got %v", err)
	}
}

func TestMockStore_Reset(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{CorrelationID: "test-1"})
	store.AddSlip(&slippy.Slip{CorrelationID: "test-2"})
	_, _ = store.Load(ctx, "test-1")

	store.Reset()

	if len(store.LoadCalls) != 0 {
		t.Errorf("expected 0 load calls after reset, got %d", len(store.LoadCalls))
	}

	// Verify slips were cleared
	if len(store.Slips) != 0 {
		t.Errorf("expected 0 slips after reset, got %d", len(store.Slips))
	}
}

func TestMockStore_ErrorInjection(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	// Test global error injection
	store.LoadError = slippy.ErrSlipNotFound

	_, err := store.Load(ctx, "any-id")
	if err != slippy.ErrSlipNotFound {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

func TestMockStore_LoadErrorFor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	testErr := errors.New("specific load error")
	store.LoadErrorFor["specific-id"] = testErr

	store.AddSlip(&slippy.Slip{CorrelationID: "specific-id"})

	_, err := store.Load(ctx, "specific-id")
	if err != testErr {
		t.Errorf("expected LoadErrorFor error, got %v", err)
	}
}

func TestDeepCopySlip(t *testing.T) {
	original := &slippy.Slip{
		CorrelationID: "test-copy",
		Repository:    "test/repo",
		Steps: map[string]slippy.Step{
			"build": {Status: slippy.StepStatusCompleted},
		},
		Aggregates: map[string][]slippy.ComponentStepData{
			"builds": {{Component: "api", Status: slippy.StepStatusCompleted}},
		},
		StateHistory: []slippy.StateHistoryEntry{
			{Step: "build", Status: slippy.StepStatusCompleted},
		},
	}

	cpy := DeepCopySlip(original)

	if cpy.CorrelationID != original.CorrelationID {
		t.Error("copy should have same correlation ID")
	}

	// Modify copy and verify original is unchanged
	cpy.Repository = "modified"
	if original.Repository == "modified" {
		t.Error("modifying copy should not affect original")
	}

	// Test nil input
	nilCopy := DeepCopySlip(nil)
	if nilCopy != nil {
		t.Error("DeepCopySlip(nil) should return nil")
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"build", "builds"},
		{"test", "tests"},
		{"builds", "buildses"},
	}

	for _, tt := range tests {
		result := pluralize(tt.input)
		if result != tt.expected {
			t.Errorf("pluralize(%s) = %s, expected %s", tt.input, result, tt.expected)
		}
	}
}
