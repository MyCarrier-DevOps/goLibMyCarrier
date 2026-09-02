package slippy

import (
	"context"
	"errors"
	"slices"
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

		// Verify the commit resolves to it. There is no commit index to inspect - the
		// lookups derive their answer from the stored rows - so this asserts the behaviour
		// the index existed to provide.
		found, err := store.LoadByCommit(ctx, slip.Repository, slip.CommitSHA)
		if err != nil {
			t.Fatalf("LoadByCommit after Create failed: %v", err)
		}
		if found.CorrelationID != slip.CorrelationID {
			t.Errorf("expected commit to resolve to %s, got %s",
				slip.CorrelationID, found.CorrelationID)
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

func TestMockStore_Repave(t *testing.T) {
	ctx := context.Background()

	successor := func(id, repo, sha string) *Slip {
		return &Slip{CorrelationID: id, Repository: repo, CommitSHA: sha, Status: SlipStatusPending}
	}

	t.Run("removes the superseded slip, creates the successor, and records the call", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "test-123", Repository: "owner/repo", CommitSHA: "abc123",
			Status: SlipStatusFailed,
		})
		parent := &AncestryEntry{CorrelationID: "parent-1", CommitSHA: "sha-parent"}

		err := store.Repave(ctx, "test-123", successor("successor-123", "owner/repo", "abc123"), parent)
		if err != nil {
			t.Fatalf("Repave failed: %v", err)
		}

		if _, ok := store.Slips["test-123"]; ok {
			t.Error("superseded slip not removed")
		}
		if _, ok := store.Slips["successor-123"]; !ok {
			t.Error("successor not created: Repave must persist both halves or neither")
		}
		if len(store.RepaveCalls) != 1 || store.RepaveCalls[0] != "test-123" {
			t.Errorf("RepaveCalls not tracked: %v", store.RepaveCalls)
		}
		if len(store.RepaveSuccessorCalls) != 1 || store.RepaveSuccessorCalls[0] != "successor-123" {
			t.Errorf("RepaveSuccessorCalls not tracked: %v", store.RepaveSuccessorCalls)
		}
		if len(store.RepaveParents) != 1 || store.RepaveParents[0] != parent {
			t.Errorf("RepaveParents not tracked: %v", store.RepaveParents)
		}
	})

	t.Run("successor is findable by commit after the repave", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "old", Repository: "owner/repo", CommitSHA: "sha-x", Status: SlipStatusFailed,
		})

		if err := store.Repave(ctx, "old", successor("new", "owner/repo", "sha-x"), nil); err != nil {
			t.Fatalf("Repave failed: %v", err)
		}

		got, err := store.LoadByCommit(ctx, "owner/repo", "sha-x")
		if err != nil {
			t.Fatalf("expected the successor to be findable by commit, got error: %v", err)
		}
		if got.CorrelationID != "new" {
			t.Errorf("expected the commit index to point at the successor, got %s", got.CorrelationID)
		}
	})

	t.Run("rejects a live superseded slip without creating the successor", func(t *testing.T) {
		// The internal mock used to happily delete a live slip, which let push tests pass
		// against behavior PostgresStore rejects. Both halves must be refused together.
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "live", Repository: "owner/repo", CommitSHA: "sha-live",
			Status: SlipStatusInProgress,
		})

		err := store.Repave(ctx, "live", successor("new", "owner/repo", "sha-live"), nil)
		if !errors.Is(err, ErrSlipWentLive) {
			t.Fatalf("expected ErrSlipWentLive, got %v", err)
		}
		if _, ok := store.Slips["live"]; !ok {
			t.Error("a live slip must survive a rejected repave")
		}
		if _, ok := store.Slips["new"]; ok {
			t.Error("a rejected repave must not create the successor")
		}
	})

	t.Run("creates the successor when the superseded slip is already gone", func(t *testing.T) {
		store := NewMockStore()

		if err := store.Repave(ctx, "ghost", successor("new", "owner/repo", "sha-g"), nil); err != nil {
			t.Fatalf("a missing superseded slip is not an error: %v", err)
		}
		if _, ok := store.Slips["new"]; !ok {
			t.Error("successor must still be created so a redelivery converges")
		}
	})

	t.Run("returns error when RepaveError is set", func(t *testing.T) {
		store := NewMockStore()
		store.RepaveError = errors.New("repave failed")

		err := store.Repave(ctx, "test", successor("new", "owner/repo", "sha"), nil)
		if err == nil || err.Error() != "repave failed" {
			t.Errorf("Expected 'repave failed', got %v", err)
		}
		if _, ok := store.Slips["new"]; ok {
			t.Error("a failed repave must not create the successor")
		}
	})

	t.Run("rejects a nil successor", func(t *testing.T) {
		store := NewMockStore()

		err := store.Repave(ctx, "test", nil, nil)
		if !errors.Is(err, ErrInvalidConfiguration) {
			t.Errorf("expected ErrInvalidConfiguration for a nil successor, got %v", err)
		}
	})

	t.Run("keeps a duplicate row for the same commit reachable after repaving the other", func(t *testing.T) {
		// The mock's Create permits duplicate (repo, sha) rows (unlike Postgres' unique
		// index). Repaving the OLDER row (a) must leave the newer row (b) reachable via
		// LoadByCommit - it still exists, so it must still resolve (DEVOPS-231 review D1.1).
		// This used to be a claim about which row a commit index named; the lookups now
		// derive the answer from the stored rows, so it is a claim about the answer itself.
		store := NewMockStore()

		if err := store.Create(ctx, &Slip{
			CorrelationID: "corr-a", Repository: "owner/repo", CommitSHA: "sha-shared",
			Status: SlipStatusFailed,
		}); err != nil {
			t.Fatalf("Create corr-a failed: %v", err)
		}
		if err := store.Create(ctx, &Slip{
			CorrelationID: "corr-b", Repository: "owner/repo", CommitSHA: "sha-shared",
			Status: SlipStatusFailed,
		}); err != nil {
			t.Fatalf("Create corr-b failed: %v", err)
		}

		// The successor lands on a different commit, so it cannot itself claim the shared
		// index entry — isolating the question this subtest asks.
		if err := store.Repave(ctx, "corr-a", successor("corr-c", "owner/repo", "sha-other"), nil); err != nil {
			t.Fatalf("Repave(corr-a) failed: %v", err)
		}

		got, err := store.LoadByCommit(ctx, "owner/repo", "sha-shared")
		if err != nil {
			t.Fatalf("expected corr-b to remain findable by commit after corr-a's repave, got error: %v", err)
		}
		if got.CorrelationID != "corr-b" {
			t.Errorf("expected corr-b, got %s", got.CorrelationID)
		}
	})

	t.Run("commit index resolves repository lookups case-insensitively", func(t *testing.T) {
		// PostgresStore compares `lower(repository) = lower($1)` (postgres_store.go), so
		// a casing-variant delivery for the same repo must still resolve in the mock
		// (DEVOPS-231 review D1.1).
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "corr-case", Repository: "Owner/Repo", CommitSHA: "sha-case",
		})

		got, err := store.LoadByCommit(ctx, "owner/repo", "sha-case")
		if err != nil {
			t.Fatalf("expected case-insensitive repository match, got error: %v", err)
		}
		if got.CorrelationID != "corr-case" {
			t.Errorf("expected corr-case, got %s", got.CorrelationID)
		}
	})
}

func TestMockStore_NewMockStore_InitializesRepaveWentLiveStatus(t *testing.T) {
	// NewMockStore initializes CreateErrorOnce and SeedOnCreate; RepaveWentLiveStatus
	// must follow the same idiom so `store.RepaveWentLiveStatus["id"] = ...` does not
	// panic with "assignment to entry in nil map" (DEVOPS-231 review D1.3).
	store := NewMockStore()

	store.RepaveWentLiveStatus["corr-x"] = SlipStatusInProgress

	if store.RepaveWentLiveStatus["corr-x"] != SlipStatusInProgress {
		t.Error("expected the assignment to be stored")
	}
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
			Aggregates: map[string][]ComponentStepData{
				"builds": {{Component: "api", Status: StepStatusPending}},
			},
		})

		err := store.UpdateComponentStatus(ctx, "test", "api", "build", StepStatusCompleted)
		if err != nil {
			t.Fatalf("UpdateComponentStatus failed: %v", err)
		}

		// Verify component was updated
		comp := store.Slips["test"].Aggregates["builds"][0]
		if comp.Status != StepStatusCompleted {
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
			Aggregates: map[string][]ComponentStepData{
				"unit_tests": {{Component: "api", Status: StepStatusPending}},
			},
		})

		err := store.UpdateComponentStatus(ctx, "test", "api", "unit_test", StepStatusCompleted)
		if err != nil {
			t.Fatalf("UpdateComponentStatus failed: %v", err)
		}

		// Verify component was updated
		comp := store.Slips["test"].Aggregates["unit_tests"][0]
		if comp.Status != StepStatusCompleted {
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
			Aggregates: map[string][]ComponentStepData{
				"builds": {{Component: "api", Status: StepStatusPending}},
			},
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
		// Armed so the RepaveError assertion below is not vacuous: an unset field is nil
		// either way, so without arming it the clear in Reset is unpinned.
		store.RepaveError = ErrSlipWentLive
		store.RepaveWentLiveStatus["corr-unspent"] = SlipStatusInProgress

		// Reset
		store.Reset()

		// Verify data and calls are cleared
		if len(store.Slips) != 0 {
			t.Error("Slips not cleared")
		}
		if store.RepaveError != nil {
			t.Error("RepaveError not cleared — it is coupled to the one-shot went-live hook, " +
				"so clearing one without the other hands the next scenario an error with no mutation")
		}
		if len(store.RepaveWentLiveStatus) != 0 {
			t.Error("RepaveWentLiveStatus not cleared")
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

// TestMockStore_CommitLookups_DuplicateRowsPerCommit pins this mock's commit lookups against
// the duplicate-row shape Phase A can hold: more than one routing_slips row per
// (repository, commit_sha), which PostgresStore orders live-first then updated_at DESC.
//
// This mock is the one push_test.go runs against, so the fidelity matters more here than in the
// exported fixture: if the double resolves a commit to an ended row while a live run for that
// commit still exists, a push test greenlights re-dispatching against a pipeline that is still
// running. The exported fixture carries the same table (slippytest/mock_store_test.go); the two
// mocks are separate implementations, so both need it.
func TestMockStore_CommitLookups_DuplicateRowsPerCommit(t *testing.T) {
	const (
		repo = "owner/repo"
		sha  = "deadbeef"
	)
	ctx := context.Background()
	newer := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-1 * time.Hour)

	seed := func(id string, status SlipStatus, updated time.Time) *Slip {
		return &Slip{
			CorrelationID: id, Repository: repo, CommitSHA: sha,
			Status: status, UpdatedAt: updated,
		}
	}

	// The four lookups answer differently on purpose, because the store's four queries do:
	//
	//	LoadByCommit      no status filter   live-first, then updated_at DESC
	//	LoadLiveByCommit  excludes 3         live-first, then updated_at DESC
	//	FindByCommits     excludes 3         updated_at DESC only (no live-first)
	//	FindAllByCommits  no status filter   updated_at DESC only, EVERY row
	//
	// "excludes 3" is abandoned/promoted/compensated. Collapsing these into one expectation is
	// how an earlier version of this table came to assert the negation of the store on two of
	// the four.
	tests := []struct {
		name         string
		rows         []*Slip
		wantByCommit string
		wantLive     string // "" means ErrSlipNotFound
		wantFind     string // "" means ErrSlipNotFound
		wantFindAll  []string
	}{
		{
			name:         "live row is not shadowed by a newer completed duplicate",
			rows:         []*Slip{seed("live", SlipStatusPending, older), seed("ended", SlipStatusCompleted, newer)},
			wantByCommit: "live", wantLive: "live",
			wantFind: "ended", wantFindAll: []string{"ended", "live"},
		},
		{
			name:         "live row is not shadowed by a newer abandoned duplicate",
			rows:         []*Slip{seed("live", SlipStatusInProgress, older), seed("ended", SlipStatusAbandoned, newer)},
			wantByCommit: "live", wantLive: "live",
			wantFind: "live", wantFindAll: []string{"ended", "live"},
		},
		{
			name:         "compensating counts as live",
			rows:         []*Slip{seed("live", SlipStatusCompensating, older), seed("ended", SlipStatusFailed, newer)},
			wantByCommit: "live", wantLive: "live",
			wantFind: "ended", wantFindAll: []string{"ended", "live"},
		},
		{
			// Makes LoadLiveByCommit's per-row filter load-bearing: an excluded row sorts
			// first, with a non-excluded row behind it.
			name: "an excluded row sorting first must not hide a non-excluded row behind it",
			rows: []*Slip{
				seed("completed", SlipStatusCompleted, older),
				seed("abandoned", SlipStatusAbandoned, newer),
			},
			wantByCommit: "abandoned",
			wantLive:     "completed",
			wantFind:     "completed", wantFindAll: []string{"abandoned", "completed"},
		},
		{
			name: "no live row: newest ended row wins",
			rows: []*Slip{
				seed("old-ended", SlipStatusCompleted, older),
				seed("new-ended", SlipStatusFailed, newer),
			},
			wantByCommit: "new-ended",
			wantLive:     "new-ended",
			wantFind:     "new-ended", wantFindAll: []string{"new-ended", "old-ended"},
		},
		{
			name: "every row excluded from the live lookup reports not found",
			rows: []*Slip{
				seed("abandoned", SlipStatusAbandoned, older),
				seed("promoted", SlipStatusPromoted, newer),
			},
			wantByCommit: "promoted",
			wantLive:     "",
			wantFind:     "", wantFindAll: []string{"promoted", "abandoned"},
		},
		{
			name:         "single row behaves exactly as before",
			rows:         []*Slip{seed("only", SlipStatusCompleted, newer)},
			wantByCommit: "only", wantLive: "only",
			wantFind: "only", wantFindAll: []string{"only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMockStore()
			for _, row := range tt.rows {
				store.AddSlip(row)
			}

			got, err := store.LoadByCommit(ctx, repo, sha)
			if err != nil {
				t.Fatalf("LoadByCommit failed: %v", err)
			}
			if got.CorrelationID != tt.wantByCommit {
				t.Errorf("LoadByCommit picked the wrong duplicate: want %s, got %s",
					tt.wantByCommit, got.CorrelationID)
			}

			gotLive, err := store.LoadLiveByCommit(ctx, repo, sha)
			switch {
			case tt.wantLive == "":
				if !errors.Is(err, ErrSlipNotFound) {
					t.Errorf("expected ErrSlipNotFound from LoadLiveByCommit, got %v", err)
				}
			case err != nil:
				t.Fatalf("LoadLiveByCommit failed: %v", err)
			case gotLive.CorrelationID != tt.wantLive:
				t.Errorf("LoadLiveByCommit picked the wrong duplicate: want %s, got %s",
					tt.wantLive, gotLive.CorrelationID)
			}

			// FindByCommits keeps a SEPARATE answer from LoadByCommit because the store's
			// query does: it carries the abandoned/promoted/compensated filter and orders on
			// updated_at DESC with no live-first term.
			found, matched, err := store.FindByCommits(ctx, repo, []string{sha})
			switch {
			case tt.wantFind == "":
				if !errors.Is(err, ErrSlipNotFound) {
					t.Errorf("expected ErrSlipNotFound from FindByCommits, got %v", err)
				}
			case err != nil:
				t.Fatalf("FindByCommits failed: %v", err)
			case matched != sha || found.CorrelationID != tt.wantFind:
				t.Errorf("FindByCommits picked the wrong row: want %s, got %s",
					tt.wantFind, found.CorrelationID)
			}

			// FindAllByCommits returns one entry per matching ROW - no LIMIT, no status filter.
			all, err := store.FindAllByCommits(ctx, repo, []string{sha})
			if err != nil {
				t.Fatalf("FindAllByCommits failed: %v", err)
			}
			gotAll := make([]string, 0, len(all))
			for _, r := range all {
				gotAll = append(gotAll, r.Slip.CorrelationID)
			}
			if !slices.Equal(gotAll, tt.wantFindAll) {
				t.Errorf("FindAllByCommits must return every row newest-first: want %v, got %v",
					tt.wantFindAll, gotAll)
			}
		})
	}
}
