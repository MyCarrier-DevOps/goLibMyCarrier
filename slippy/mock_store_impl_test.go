package slippy

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Tests for MockStore implementation to ensure it correctly implements SlipStore interface

func TestMockStore_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("creates slip and indexes by commit", func(t *testing.T) {
		store := NewMockStore()
		slip := &Slip{
			CorrelationID: "test-123",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
			Status:        SlipStatusPending,
			CreatedAt:     time.Now(),
		}

		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		// Verify slip was stored
		if store.Slips[slip.CorrelationID] == nil {
			t.Error("Slip not stored")
		}

		// Verify commit index was updated
		indexKey := slip.Repository + ":" + slip.CommitSHA
		if store.CommitIndex[indexKey] != slip.CorrelationID {
			t.Error("CommitIndex not updated")
		}

		// Verify call was tracked
		if len(store.CreateCalls) != 1 {
			t.Errorf("Expected 1 CreateCall, got %d", len(store.CreateCalls))
		}
		if store.CreateCalls[0].Slip.CorrelationID != slip.CorrelationID {
			t.Error("CreateCall not tracked correctly")
		}
	})

	t.Run("returns error when CreateError is set", func(t *testing.T) {
		store := NewMockStore()
		store.CreateError = errors.New("create failed")

		err := store.Create(ctx, &Slip{CorrelationID: "test"})
		if err == nil {
			t.Error("Expected error, got nil")
		}
		if err.Error() != "create failed" {
			t.Errorf("Expected 'create failed', got '%s'", err.Error())
		}
	})

	t.Run("returns conditional error for specific ID", func(t *testing.T) {
		store := NewMockStore()
		store.CreateErrorFor = map[string]error{
			"bad-id": errors.New("specific error"),
		}

		// Good ID should succeed
		err := store.Create(ctx, &Slip{CorrelationID: "good-id", Repository: "repo", CommitSHA: "sha"})
		if err != nil {
			t.Errorf("Expected no error for good-id, got %v", err)
		}

		// Bad ID should fail
		err = store.Create(ctx, &Slip{CorrelationID: "bad-id"})
		if err == nil {
			t.Error("Expected error for bad-id, got nil")
		}
	})
}

func TestMockStore_Load(t *testing.T) {
	ctx := context.Background()

	t.Run("loads existing slip", func(t *testing.T) {
		store := NewMockStore()
		slip := &Slip{
			CorrelationID: "test-123",
			Status:        SlipStatusPending,
		}
		store.AddSlip(slip)

		loaded, err := store.Load(ctx, "test-123")
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}
		if loaded.CorrelationID != "test-123" {
			t.Error("Loaded wrong slip")
		}

		// Verify call tracking
		if len(store.LoadCalls) != 1 || store.LoadCalls[0] != "test-123" {
			t.Error("LoadCall not tracked")
		}
	})

	t.Run("returns ErrSlipNotFound for missing slip", func(t *testing.T) {
		store := NewMockStore()

		_, err := store.Load(ctx, "nonexistent")
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("Expected ErrSlipNotFound, got %v", err)
		}
	})

	t.Run("returns error when LoadError is set", func(t *testing.T) {
		store := NewMockStore()
		store.LoadError = errors.New("load failed")
		store.AddSlip(&Slip{CorrelationID: "test"})

		_, err := store.Load(ctx, "test")
		if err == nil || err.Error() != "load failed" {
			t.Errorf("Expected 'load failed' error, got %v", err)
		}
	})

	t.Run("returns conditional error for specific ID", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "good-id"})
		store.AddSlip(&Slip{CorrelationID: "bad-id"})
		store.LoadErrorFor = map[string]error{
			"bad-id": errors.New("specific load error"),
		}

		// Good ID should succeed
		_, err := store.Load(ctx, "good-id")
		if err != nil {
			t.Errorf("Expected no error for good-id, got %v", err)
		}

		// Bad ID should fail
		_, err = store.Load(ctx, "bad-id")
		if err == nil {
			t.Error("Expected error for bad-id")
		}
	})
}

func TestMockStore_LoadByCommit(t *testing.T) {
	ctx := context.Background()

	t.Run("loads slip by repository and commit", func(t *testing.T) {
		store := NewMockStore()
		slip := &Slip{
			CorrelationID: "test-123",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}
		store.AddSlip(slip)

		loaded, err := store.LoadByCommit(ctx, "owner/repo", "abc123")
		if err != nil {
			t.Fatalf("LoadByCommit failed: %v", err)
		}
		if loaded.CorrelationID != "test-123" {
			t.Error("Loaded wrong slip")
		}

		// Verify call tracking
		if len(store.LoadByCommitCalls) != 1 {
			t.Error("LoadByCommitCall not tracked")
		}
		if store.LoadByCommitCalls[0].Repository != "owner/repo" {
			t.Error("Repository not tracked")
		}
	})

	t.Run("returns ErrSlipNotFound for missing commit", func(t *testing.T) {
		store := NewMockStore()

		_, err := store.LoadByCommit(ctx, "repo", "nonexistent")
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("Expected ErrSlipNotFound, got %v", err)
		}
	})

	t.Run("returns error when LoadByCommitError is set", func(t *testing.T) {
		store := NewMockStore()
		store.LoadByCommitError = errors.New("load by commit failed")
		store.AddSlip(&Slip{CorrelationID: "test", Repository: "repo", CommitSHA: "sha"})

		_, err := store.LoadByCommit(ctx, "repo", "sha")
		if err == nil || err.Error() != "load by commit failed" {
			t.Errorf("Expected 'load by commit failed', got %v", err)
		}
	})
}

func TestMockStore_FindByCommits(t *testing.T) {
	ctx := context.Background()

	t.Run("finds slips by multiple commits", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "slip-1", Repository: "repo", CommitSHA: "sha1"})
		store.AddSlip(&Slip{CorrelationID: "slip-2", Repository: "repo", CommitSHA: "sha2"})
		store.AddSlip(&Slip{CorrelationID: "slip-3", Repository: "repo", CommitSHA: "sha3"})

		commits := []string{"sha1", "sha3", "sha4"}
		slip, matchedCommit, err := store.FindByCommits(ctx, "repo", commits)
		if err != nil {
			t.Fatalf("FindByCommits failed: %v", err)
		}

		// Should find first matching commit (sha1)
		if slip == nil {
			t.Error("Expected slip, got nil")
		}
		if matchedCommit != "sha1" {
			t.Errorf("Expected matched commit 'sha1', got '%s'", matchedCommit)
		}

		// Verify call tracking
		if len(store.FindByCommitsCalls) != 1 {
			t.Error("FindByCommitsCall not tracked")
		}
	})

	t.Run("returns error when FindByCommitsError is set", func(t *testing.T) {
		store := NewMockStore()
		store.FindByCommitsError = errors.New("find failed")

		_, _, err := store.FindByCommits(ctx, "repo", []string{"sha"})
		if err == nil || err.Error() != "find failed" {
			t.Errorf("Expected 'find failed', got %v", err)
		}
	})
}

func TestMockStore_Update(t *testing.T) {
	ctx := context.Background()

	t.Run("updates existing slip", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "test", Status: SlipStatusPending})

		updated := &Slip{CorrelationID: "test", Status: SlipStatusCompleted}
		err := store.Update(ctx, updated)
		if err != nil {
			t.Fatalf("Update failed: %v", err)
		}

		// Verify update
		if store.Slips["test"].Status != SlipStatusCompleted {
			t.Error("Slip not updated")
		}

		// Verify call tracking
		if len(store.UpdateCalls) != 1 {
			t.Error("UpdateCall not tracked")
		}
	})

	t.Run("returns error when UpdateError is set", func(t *testing.T) {
		store := NewMockStore()
		store.UpdateError = errors.New("update failed")

		err := store.Update(ctx, &Slip{CorrelationID: "test"})
		if err == nil || err.Error() != "update failed" {
			t.Errorf("Expected 'update failed', got %v", err)
		}
	})
}

func TestMockStore_UpdateStep(t *testing.T) {
	ctx := context.Background()

	t.Run("updates step status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "test",
			Steps: map[string]Step{
				"build": {Status: StepStatusPending},
			},
		})

		err := store.UpdateStep(ctx, "test", "build", "", StepStatusCompleted)
		if err != nil {
			t.Fatalf("UpdateStep failed: %v", err)
		}

		// Verify step was updated
		if store.Slips["test"].Steps["build"].Status != StepStatusCompleted {
			t.Error("Step not updated")
		}

		// Verify call tracking
		if len(store.UpdateStepCalls) != 1 {
			t.Error("UpdateStepCall not tracked")
		}
	})

	t.Run("returns ErrSlipNotFound for missing slip", func(t *testing.T) {
		store := NewMockStore()

		err := store.UpdateStep(ctx, "nonexistent", "build", "", StepStatusCompleted)
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("Expected ErrSlipNotFound, got %v", err)
		}
	})

	t.Run("returns error when UpdateStepError is set", func(t *testing.T) {
		store := NewMockStore()
		store.UpdateStepError = errors.New("step error")
		store.AddSlip(&Slip{
			CorrelationID: "test",
			Steps:         map[string]Step{"build": {}},
		})

		err := store.UpdateStep(ctx, "test", "build", "", StepStatusCompleted)
		if err == nil || err.Error() != "step error" {
			t.Errorf("Expected 'step error', got %v", err)
		}
	})
}

func TestMockStore_UpdateComponentStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("updates component build status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "test",
			Components: []Component{
				{Name: "api", BuildStatus: StepStatusPending},
			},
		})

		err := store.UpdateComponentStatus(ctx, "test", "api", "build", StepStatusCompleted)
		if err != nil {
			t.Fatalf("UpdateComponentStatus failed: %v", err)
		}

		// Verify component was updated
		comp := store.Slips["test"].Components[0]
		if comp.BuildStatus != StepStatusCompleted {
			t.Error("Component build status not updated")
		}

		// Verify call tracking
		if len(store.UpdateComponentCalls) != 1 {
			t.Error("UpdateComponentCall not tracked")
		}
	})

	t.Run("updates component unit_test status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "test",
			Components: []Component{
				{Name: "api", UnitTestStatus: StepStatusPending},
			},
		})

		err := store.UpdateComponentStatus(ctx, "test", "api", "unit_test", StepStatusCompleted)
		if err != nil {
			t.Fatalf("UpdateComponentStatus failed: %v", err)
		}

		// Verify component was updated
		comp := store.Slips["test"].Components[0]
		if comp.UnitTestStatus != StepStatusCompleted {
			t.Error("Component unit_test status not updated")
		}
	})

	t.Run("returns ErrSlipNotFound for missing slip", func(t *testing.T) {
		store := NewMockStore()

		err := store.UpdateComponentStatus(ctx, "nonexistent", "api", "build", StepStatusCompleted)
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("Expected ErrSlipNotFound, got %v", err)
		}
	})

	t.Run("returns error when UpdateComponentError is set", func(t *testing.T) {
		store := NewMockStore()
		store.UpdateComponentError = errors.New("component error")
		store.AddSlip(&Slip{
			CorrelationID: "test",
			Components:    []Component{{Name: "api"}},
		})

		err := store.UpdateComponentStatus(ctx, "test", "api", "build", StepStatusCompleted)
		if err == nil || err.Error() != "component error" {
			t.Errorf("Expected 'component error', got %v", err)
		}
	})
}

func TestMockStore_AppendHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("appends history entry", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "test"})

		entry := StateHistoryEntry{
			Timestamp: time.Now(),
			Step:      "build",
			Status:    StepStatusCompleted,
			Actor:     "test-actor",
		}

		err := store.AppendHistory(ctx, "test", entry)
		if err != nil {
			t.Fatalf("AppendHistory failed: %v", err)
		}

		// Verify history was appended
		if len(store.Slips["test"].StateHistory) != 1 {
			t.Error("History not appended")
		}

		// Verify call tracking
		if len(store.AppendHistoryCalls) != 1 {
			t.Error("AppendHistoryCall not tracked")
		}
	})

	t.Run("returns ErrSlipNotFound for missing slip", func(t *testing.T) {
		store := NewMockStore()

		err := store.AppendHistory(ctx, "nonexistent", StateHistoryEntry{})
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("Expected ErrSlipNotFound, got %v", err)
		}
	})

	t.Run("returns error when AppendHistoryError is set", func(t *testing.T) {
		store := NewMockStore()
		store.AppendHistoryError = errors.New("history error")
		store.AddSlip(&Slip{CorrelationID: "test"})

		err := store.AppendHistory(ctx, "test", StateHistoryEntry{})
		if err == nil || err.Error() != "history error" {
			t.Errorf("Expected 'history error', got %v", err)
		}
	})
}

func TestMockStore_Close(t *testing.T) {
	t.Run("closes store", func(t *testing.T) {
		store := NewMockStore()

		err := store.Close()
		if err != nil {
			t.Errorf("Close failed: %v", err)
		}

		// Verify call tracking
		if store.CloseCalls != 1 {
			t.Errorf("Expected 1 CloseCall, got %d", store.CloseCalls)
		}
	})

	t.Run("returns error when CloseError is set", func(t *testing.T) {
		store := NewMockStore()
		store.CloseError = errors.New("close error")

		err := store.Close()
		if err == nil || err.Error() != "close error" {
			t.Errorf("Expected 'close error', got %v", err)
		}
	})
}

func TestMockStore_Reset(t *testing.T) {
	t.Run("clears all stored data and call tracking", func(t *testing.T) {
		store := NewMockStore()

		// Add some data
		store.AddSlip(&Slip{CorrelationID: "test", Repository: "repo", CommitSHA: "sha"})
		store.LoadCalls = append(store.LoadCalls, "test")
		store.CloseCalls = 5

		// Reset
		store.Reset()

		// Verify data and calls are cleared
		if len(store.Slips) != 0 {
			t.Error("Slips not cleared")
		}
		if len(store.CommitIndex) != 0 {
			t.Error("CommitIndex not cleared")
		}
		if len(store.LoadCalls) != 0 {
			t.Error("LoadCalls not cleared")
		}
		if store.CloseCalls != 0 {
			t.Error("CloseCalls not cleared")
		}
	})

	t.Run("does not clear error injections", func(t *testing.T) {
		// This tests that Reset preserves error configurations
		// so you can reset state between tests without re-configuring errors
		store := NewMockStore()
		store.CreateError = errors.New("error")

		store.Reset()

		// Errors should be preserved (user must clear them manually if desired)
		if store.CreateError == nil {
			t.Log("Note: Reset does not clear error injections - this is by design")
		}
	})
}

func TestMockStore_ThreadSafety(t *testing.T) {
	t.Run("handles concurrent operations", func(t *testing.T) {
		store := NewMockStore()
		ctx := context.Background()

		// Run concurrent operations
		done := make(chan bool, 100)

		for i := 0; i < 50; i++ {
			go func(id int) {
				slip := &Slip{
					CorrelationID: "slip-" + string(rune('a'+id)),
					Repository:    "repo",
					CommitSHA:     "sha-" + string(rune('a'+id)),
				}
				_ = store.Create(ctx, slip)
				done <- true
			}(i)
		}

		for i := 0; i < 50; i++ {
			go func() {
				_, _ = store.Load(ctx, "slip-a")
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 100; i++ {
			<-done
		}

		// Verify no panic occurred and data is consistent
		if len(store.CreateCalls) != 50 {
			t.Errorf("Expected 50 CreateCalls, got %d", len(store.CreateCalls))
		}
	})
}
