package slippytest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// repaveSuccessorSlip builds the successor slip the Repave tests below replace a run with.
func repaveSuccessorSlip(id, repo, branch, sha string) *slippy.Slip {
	return &slippy.Slip{
		CorrelationID: id,
		Repository:    repo,
		Branch:        branch,
		CommitSHA:     sha,
		Status:        slippy.SlipStatusPending,
	}
}

func TestMockStore_Repave(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-superseded",
		Repository:    "test/repo",
		Branch:        "main",
		CommitSHA:     "commitdel",
		// Repave only ever removes an ended slip (D1.2's status guard); give the fixture a
		// realistic ended status rather than the zero value, which SlipStatus.IsLive()
		// treats as live.
		Status: slippy.SlipStatusCompleted,
	})
	parent := &slippy.AncestryEntry{CorrelationID: "parent-1", CommitSHA: "sha-parent"}

	successor := repaveSuccessorSlip("test-successor", "test/repo", "main", "commitdel")
	if err := store.Repave(ctx, "test-superseded", successor, parent); err != nil {
		t.Fatalf("Repave failed: %v", err)
	}

	if len(store.RepaveCalls) != 1 || store.RepaveCalls[0] != "test-superseded" {
		t.Errorf("expected Repave call for test-superseded, got %v", store.RepaveCalls)
	}
	if len(store.RepaveSuccessorCalls) != 1 || store.RepaveSuccessorCalls[0] != "test-successor" {
		t.Errorf("expected test-successor to be recorded, got %v", store.RepaveSuccessorCalls)
	}
	if len(store.RepaveParents) != 1 || store.RepaveParents[0] != parent {
		t.Errorf("expected the parent link to be recorded, got %v", store.RepaveParents)
	}

	// The superseded slip is gone...
	if _, err := store.Load(ctx, "test-superseded"); !errors.Is(err, slippy.ErrSlipNotFound) {
		t.Errorf("expected ErrSlipNotFound for the superseded run, got %v", err)
	}

	// ...and the successor took its place, including on the commit index.
	if _, err := store.Load(ctx, "test-successor"); err != nil {
		t.Errorf("expected the successor to exist after Repave, got %v", err)
	}
	got, err := store.LoadByCommit(ctx, "test/repo", "commitdel")
	if err != nil {
		t.Fatalf("expected the commit to resolve to the successor, got %v", err)
	}
	if got.CorrelationID != "test-successor" {
		t.Errorf("expected the commit index to point at the successor, got %s", got.CorrelationID)
	}
}

func TestMockStore_Repave_WithError(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "test-repave-err",
		Repository:    "test/repo",
		CommitSHA:     "commiterr",
		Status:        slippy.SlipStatusFailed,
	})

	testErr := errors.New("repave error")
	store.RepaveError = testErr

	successor := repaveSuccessorSlip("successor-err", "test/repo", "main", "commiterr")
	err := store.Repave(ctx, "test-repave-err", successor, nil)
	if !errors.Is(err, testErr) {
		t.Errorf("expected RepaveError, got %v", err)
	}

	// The call is recorded even when it fails, the superseded slip survives, and — the
	// point of atomicity — the successor was never created.
	if len(store.RepaveCalls) != 1 {
		t.Errorf("expected the failed call to still be recorded, got %v", store.RepaveCalls)
	}
	if _, loadErr := store.Load(ctx, "test-repave-err"); loadErr != nil {
		t.Errorf("superseded slip must survive a failed repave, got %v", loadErr)
	}
	if _, successorErr := store.Load(ctx, "successor-err"); !errors.Is(successorErr, slippy.ErrSlipNotFound) {
		t.Errorf("a failed repave must not create the successor, got %v", successorErr)
	}
}

func TestMockStore_Repave_MissingSupersededSlip_StillCreatesSuccessor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	// Repaving an unknown run is not an error (idempotent), matching PostgresStore — and
	// the successor is still created so a redelivery converges.
	successor := repaveSuccessorSlip("successor-ghost", "owner/repo", "main", "sha-ghost")
	if err := store.Repave(ctx, "never-existed", successor, nil); err != nil {
		t.Errorf("repaving a missing slip must not error, got %v", err)
	}
	if _, err := store.Load(ctx, "successor-ghost"); err != nil {
		t.Errorf("expected the successor to be created anyway, got %v", err)
	}
}

func TestMockStore_Repave_NilSuccessor_Rejected(t *testing.T) {
	store := NewMockStore()

	err := store.Repave(context.Background(), "whatever", nil, nil)
	if !errors.Is(err, slippy.ErrInvalidConfiguration) {
		t.Errorf("expected ErrInvalidConfiguration for a nil successor, got %v", err)
	}
}

// TestMockStore_Repave_SelfRepave_Rejected mirrors PostgresStore's self-repave rejection,
// which SlipStore.Repave states as a hard precondition ("newSlip.CorrelationID must differ
// from oldCorrelationID").
//
// This double is the exported, consumer-facing one, so a gap here is worse than a gap in an
// in-package mock: without the guard the double deletes and re-inserts the SAME map key,
// silently reproducing exactly the history destruction the real store now refuses — and a
// downstream test asserting "my caller never self-repaves" would pass against this mock while
// failing against Postgres. It is the same argument the live-status guard's own doc already
// makes about itself ("it is not decorative").
func TestMockStore_Repave_SelfRepave_Rejected(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-same",
		Repository:    "test/repo",
		Branch:        "main",
		CommitSHA:     "sha-self",
		Status:        slippy.SlipStatusCompleted,
		StateHistory:  []slippy.StateHistoryEntry{{Step: "builds", Actor: "ci"}},
	})

	successor := repaveSuccessorSlip("corr-same", "test/repo", "main", "sha-self")
	err := store.Repave(ctx, "corr-same", successor, nil)
	if !errors.Is(err, slippy.ErrInvalidConfiguration) {
		t.Errorf("expected ErrInvalidConfiguration for a self-repave, got %v", err)
	}

	// Rejected means untouched: the predecessor's history must survive intact, since
	// destroying it is the whole hazard the guard exists to prevent.
	stored, loadErr := store.Load(ctx, "corr-same")
	if loadErr != nil {
		t.Fatalf("the slip must be left in place after a rejected self-repave, got %v", loadErr)
	}
	if stored.Status != slippy.SlipStatusCompleted {
		t.Errorf("status must be untouched, got %q", stored.Status)
	}
	if len(stored.StateHistory) != 1 {
		t.Errorf("expected the predecessor's history to survive, got %d entries", len(stored.StateHistory))
	}
}

// TestMockStore_Repave_RecordsPredecessorOnSuccessor mirrors the state-history entry
// PostgresStore.Repave appends to the successor naming the run it replaced. Without it, a
// consumer writing "the successor records the run it replaced" gets a green test here and a
// false negative against the real store.
func TestMockStore_Repave_RecordsPredecessorOnSuccessor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-old",
		Repository:    "test/repo",
		Branch:        "main",
		CommitSHA:     "sha-hist",
		Status:        slippy.SlipStatusFailed,
	})

	successor := repaveSuccessorSlip("corr-new", "test/repo", "main", "sha-hist")
	if err := store.Repave(ctx, "corr-old", successor, nil); err != nil {
		t.Fatalf("Repave failed: %v", err)
	}

	stored, err := store.Load(ctx, "corr-new")
	if err != nil {
		t.Fatalf("expected the successor to exist, got %v", err)
	}
	var found bool
	for _, entry := range stored.StateHistory {
		if strings.Contains(entry.Message, "corr-old") {
			found = true
		}
	}
	if !found {
		t.Errorf("the successor must carry a history entry naming the run it replaced, got %+v",
			stored.StateHistory)
	}
}

// TestMockStore_Repave_MissingSupersededSlip_RecordsNoPredecessor pins the other half: the
// marker is written only when a predecessor was actually removed, matching the real store's
// removedOld gate. Inventing one for a repave that replaced nothing would be a false record.
func TestMockStore_Repave_MissingSupersededSlip_RecordsNoPredecessor(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	successor := repaveSuccessorSlip("corr-new", "test/repo", "main", "sha-ghost")
	if err := store.Repave(ctx, "corr-absent", successor, nil); err != nil {
		t.Fatalf("Repave failed: %v", err)
	}

	stored, err := store.Load(ctx, "corr-new")
	if err != nil {
		t.Fatalf("expected the successor to exist, got %v", err)
	}
	for _, entry := range stored.StateHistory {
		if strings.Contains(entry.Message, "repaved") {
			t.Errorf("no predecessor was removed, so no repave marker may be written, got %+v", entry)
		}
	}
}

func TestMockStore_Repave_EndedSlip_Replaces(t *testing.T) {
	// PostgresStore's Repave only ever removes a row whose status is "ended"
	// (interfaces.go). An ended slip must be replaced with no error (DEVOPS-231 review
	// D1.2).
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-ended",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-ended",
		Status:        slippy.SlipStatusCompleted,
	})

	successor := repaveSuccessorSlip("corr-successor", "owner/repo", "main", "sha-ended")
	if err := store.Repave(ctx, "corr-ended", successor, nil); err != nil {
		t.Fatalf("expected ended slip to be replaced cleanly, got %v", err)
	}
	if _, err := store.Load(ctx, "corr-ended"); !errors.Is(err, slippy.ErrSlipNotFound) {
		t.Errorf("expected ended slip to be gone, got %v", err)
	}
	if _, err := store.Load(ctx, "corr-successor"); err != nil {
		t.Errorf("expected the successor to exist, got %v", err)
	}
}

func TestMockStore_Repave_LiveSlip_ReturnsErrSlipWentLive(t *testing.T) {
	// The exported MockStore asserts `var _ slippy.SlipStore = (*MockStore)(nil)`, so a
	// downstream consumer's went-live handling must be exercisable against it. Without
	// this guard, the mock would let a caller destroy a pending/in_progress slip - the
	// exact operation PostgresStore refuses (DEVOPS-231 review D1.2).
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-live",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-live",
		Status:        slippy.SlipStatusInProgress,
	})

	successor := repaveSuccessorSlip("corr-successor", "owner/repo", "main", "sha-live")
	err := store.Repave(ctx, "corr-live", successor, nil)
	if !errors.Is(err, slippy.ErrSlipWentLive) {
		t.Fatalf("expected ErrSlipWentLive for a live slip, got %v", err)
	}

	// The live slip must survive the rejected repave, and no successor may appear.
	if _, loadErr := store.Load(ctx, "corr-live"); loadErr != nil {
		t.Errorf("live slip must survive a rejected repave, got %v", loadErr)
	}
	if _, byCommitErr := store.LoadByCommit(ctx, "owner/repo", "sha-live"); byCommitErr != nil {
		t.Errorf("live slip's commit index entry must survive a rejected repave, got %v", byCommitErr)
	}
	if _, successorErr := store.Load(ctx, "corr-successor"); !errors.Is(successorErr, slippy.ErrSlipNotFound) {
		t.Errorf("a rejected repave must not create the successor, got %v", successorErr)
	}
}

func TestMockStore_Repave_WentLiveHook_MutatesStatusBeforeReturningError(t *testing.T) {
	// Mirrors slippy's internal MockStore.RepaveWentLiveStatus: simulates the slip
	// transitioning to live in the window between the caller's repave decision and the
	// Repave call itself, so a subsequent reload observes the new state rather than
	// the stale decision-time snapshot (DEVOPS-231 review D1.2, review finding B1).
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-went-live",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-went-live",
		Status:        slippy.SlipStatusFailed, // ended at decision time
	})
	store.RepaveError = slippy.ErrSlipWentLive
	store.RepaveWentLiveStatus = map[string]slippy.SlipStatus{"corr-went-live": slippy.SlipStatusInProgress}

	successor := repaveSuccessorSlip("corr-successor", "owner/repo", "main", "sha-went-live")
	err := store.Repave(ctx, "corr-went-live", successor, nil)
	if !errors.Is(err, slippy.ErrSlipWentLive) {
		t.Fatalf("expected ErrSlipWentLive, got %v", err)
	}

	reloaded, loadErr := store.Load(ctx, "corr-went-live")
	if loadErr != nil {
		t.Fatalf("expected the slip to survive, got %v", loadErr)
	}
	if reloaded.Status != slippy.SlipStatusInProgress {
		t.Errorf("expected the hook to mutate status to in_progress, got %s", reloaded.Status)
	}
	if len(store.RepaveWentLiveStatus) != 0 {
		t.Error("expected the one-shot hook entry to be cleared after firing")
	}
}

func TestMockStore_Repave_KeepsSiblingRowFindableWhenSuccessorMovesCommit(t *testing.T) {
	// Create permits duplicate (repo, sha) rows, so superseding an OLDER ended row must leave
	// the commit resolving to the still-live sibling (DEVOPS-231 review D1.1). The successor
	// lands on a DIFFERENT commit here, which is what separates this from
	// TestMockStore_Repave_LeavesOtherRowsForTheSameCommitAlone: nothing the repave writes can
	// compete for sha-shared, so the only way corr-b stops resolving is if Repave removed it.
	store := NewMockStore()
	ctx := context.Background()

	if err := store.Create(ctx, &slippy.Slip{
		CorrelationID: "corr-a",
		Repository:    "owner/repo",
		CommitSHA:     "sha-shared",
		Status:        slippy.SlipStatusCompleted,
	}); err != nil {
		t.Fatalf("Create corr-a failed: %v", err)
	}
	if err := store.Create(ctx, &slippy.Slip{
		CorrelationID: "corr-b",
		Repository:    "owner/repo",
		CommitSHA:     "sha-shared",
		Status:        slippy.SlipStatusInProgress,
	}); err != nil {
		t.Fatalf("Create corr-b failed: %v", err)
	}

	// The successor lands on a different commit, isolating the question this test asks.
	successor := repaveSuccessorSlip("corr-c", "owner/repo", "main", "sha-other")
	if err := store.Repave(ctx, "corr-a", successor, nil); err != nil {
		t.Fatalf("Repave(corr-a) failed: %v", err)
	}

	got, err := store.LoadByCommit(ctx, "owner/repo", "sha-shared")
	if err != nil {
		t.Fatalf("expected corr-b to remain findable by commit after corr-a's repave, got error: %v", err)
	}
	if got.CorrelationID != "corr-b" {
		t.Errorf("expected corr-b, got %s", got.CorrelationID)
	}
}

func TestMockStore_LoadByCommit_CaseInsensitiveRepository(t *testing.T) {
	// PostgresStore compares `lower(repository) = lower($1)` (postgres_store.go), so a
	// casing-variant delivery for the same repo must still resolve in the mock
	// (DEVOPS-231 review D1.1).
	store := NewMockStore()
	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-case",
		Repository:    "Owner/Repo",
		CommitSHA:     "sha-case",
	})

	got, err := store.LoadByCommit(context.Background(), "owner/repo", "sha-case")
	if err != nil {
		t.Fatalf("expected case-insensitive repository match, got error: %v", err)
	}
	if got.CorrelationID != "corr-case" {
		t.Errorf("expected corr-case, got %s", got.CorrelationID)
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

// TestMockStore_Reset_ClearsRepaveState pins Reset's own contract — "clears all stored data
// and call tracking" — against the state DEVOPS-231 added.
//
// This is published API, so a gap here is a bug a consumer hits rather than one we hit: a
// test that Resets between scenarios would keep the previous scenario's repave records, so
// len(RepaveCalls) assertions pass or fail on the wrong calls. The one-shot hook matters more
// than the counters — an unspent RepaveWentLiveStatus entry surviving Reset would mutate a
// later scenario's slip, in a store that looks clean.
func TestMockStore_Reset_ClearsRepaveState(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-old", Repository: "test/repo", Branch: "main",
		CommitSHA: "sha-reset", Status: slippy.SlipStatusCompleted,
	})
	require.NoError(t, store.Repave(ctx, "corr-old",
		repaveSuccessorSlip("corr-new", "test/repo", "main", "sha-reset"),
		&slippy.AncestryEntry{CorrelationID: "parent-1"}))
	store.RepaveWentLiveStatus["corr-unspent"] = slippy.SlipStatusInProgress

	require.NotEmpty(t, store.RepaveCalls, "precondition: the call was recorded")

	store.Reset()

	assert.Empty(t, store.RepaveCalls, "RepaveCalls must not survive Reset")
	assert.Empty(t, store.RepaveSuccessorCalls, "RepaveSuccessorCalls must not survive Reset")
	assert.Empty(t, store.RepaveParents, "RepaveParents must not survive Reset")
	assert.Empty(t, store.RepaveWentLiveStatus,
		"an unspent one-shot hook must not leak into the next scenario")
	assert.Empty(t, store.Slips, "stored slips must not survive Reset")

	// Slips is the only commit-lookup state there is, so an empty map is the whole claim -
	// asserted through the lookup as well, since that is what a consumer actually calls.
	_, err := store.LoadByCommit(ctx, "test/repo", "sha-reset")
	assert.ErrorIs(t, err, slippy.ErrSlipNotFound, "the commit must not resolve after Reset")
}

// TestMockStore_Repave_LeavesOtherRowsForTheSameCommitAlone pins that Repave touches only the
// row it supersedes, even when another row shares the commit.
//
// This replaces a pair of tests that asserted which correlation ID a commit index pointed at
// after a repave. There is no index any more - the commit lookups derive their answer from the
// stored rows - so the index-stealing hazard those tests guarded is unrepresentable, and the
// claim worth keeping is the one about blast radius: a repave must not delete, hide or mutate a
// sibling row.
//
// Note what is deliberately NOT asserted: that the still-live sibling always wins the lookup.
// With two live rows the store orders on updated_at, and real Postgres breaks an exact tie
// arbitrarily - so this seeds distinct timestamps and asserts the ordering that follows from
// them, rather than a guarantee neither store makes.
func TestMockStore_Repave_LeavesOtherRowsForTheSameCommitAlone(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	newest := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	// Ended slip A and live slip C share a commit - a duplicate-row shape Phase A can hold.
	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-A", Repository: "test/repo", Branch: "main",
		CommitSHA: "sha-shared", Status: slippy.SlipStatusCompleted,
		UpdatedAt: newest.Add(-2 * time.Hour),
	})
	require.NoError(t, store.Create(ctx, &slippy.Slip{
		CorrelationID: "corr-C", Repository: "test/repo", Branch: "main",
		CommitSHA: "sha-shared", Status: slippy.SlipStatusInProgress,
		UpdatedAt: newest,
	}))

	successor := repaveSuccessorSlip("corr-B", "test/repo", "main", "sha-shared")
	successor.UpdatedAt = newest.Add(-1 * time.Hour)
	require.NoError(t, store.Repave(ctx, "corr-A", successor, nil))

	// The superseded row is gone and the successor exists.
	_, err := store.Load(ctx, "corr-A")
	assert.ErrorIs(t, err, slippy.ErrSlipNotFound, "the superseded row must be removed")
	_, err = store.Load(ctx, "corr-B")
	assert.NoError(t, err, "the successor must be stored")

	// The sibling is untouched - still present, still live, status not rewritten.
	sibling, err := store.Load(ctx, "corr-C")
	require.NoError(t, err, "a repave must not remove a sibling row for the same commit")
	assert.Equal(t, slippy.SlipStatusInProgress, sibling.Status,
		"a repave must not mutate a sibling row for the same commit")

	// Both survivors are live, so updated_at decides: corr-C is the newer of the two.
	got, err := store.LoadByCommit(ctx, "test/repo", "sha-shared")
	require.NoError(t, err)
	assert.Equal(t, "corr-C", got.CorrelationID,
		"the newer of two live rows wins the commit lookup")
}

// TestMockStore_Repave_SuccessorBecomesTheCommitsSlip is the ordinary case: with no sibling
// row, the commit resolves to the successor afterwards. Without this the test above could pass
// against a Repave that stored the successor somewhere the lookups never read.
func TestMockStore_Repave_SuccessorBecomesTheCommitsSlip(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()

	store.AddSlip(&slippy.Slip{
		CorrelationID: "corr-old", Repository: "test/repo", Branch: "main",
		CommitSHA: "sha-only", Status: slippy.SlipStatusFailed,
	})
	require.NoError(t, store.Repave(ctx, "corr-old",
		repaveSuccessorSlip("corr-new", "test/repo", "main", "sha-only"), nil))

	got, err := store.LoadByCommit(ctx, "test/repo", "sha-only")
	require.NoError(t, err)
	assert.Equal(t, "corr-new", got.CorrelationID,
		"the commit must resolve to the successor once the superseded row is gone")

	live, err := store.LoadLiveByCommit(ctx, "test/repo", "sha-only")
	require.NoError(t, err)
	assert.Equal(t, "corr-new", live.CorrelationID, "the successor is live and must be visible")
}

// TestMockStore_CommitLookups_DuplicateRowsPerCommit pins the double's commit lookups against
// the shape Phase A can genuinely hold: more than one routing_slips row per
// (repository, commit_sha). PostgresStore orders those live-first then updated_at DESC, and
// LoadLiveByCommit applies its status filter per row rather than to an already-chosen row.
// A double that returns the newest row instead reports the wrong answer confidently, which is
// worse for a consumer than reporting none — so these cases are the fixture's contract.
func TestMockStore_CommitLookups_DuplicateRowsPerCommit(t *testing.T) {
	const (
		repo = "owner/repo"
		sha  = "deadbeef"
	)
	newer := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-1 * time.Hour)

	seed := func(correlationID, repository string, status slippy.SlipStatus, updated time.Time) *slippy.Slip {
		return &slippy.Slip{
			CorrelationID: correlationID,
			Repository:    repository,
			CommitSHA:     sha,
			Status:        status,
			UpdatedAt:     updated,
		}
	}

	tests := []struct {
		name string
		// rows are seeded in slice order, so a fixture that keeps a last-writer-wins index
		// resolves the commit to the final entry.
		rows         []*slippy.Slip
		queryRepo    string
		wantByCommit string // correlation ID, or "" for ErrSlipNotFound
		wantLive     string
	}{
		{
			name: "live row is not shadowed by a newer completed duplicate",
			rows: []*slippy.Slip{
				seed("live", repo, slippy.SlipStatusPending, older),
				seed("ended", repo, slippy.SlipStatusCompleted, newer),
			},
			queryRepo:    repo,
			wantByCommit: "live",
			wantLive:     "live",
		},
		{
			name: "live row is not shadowed by a newer abandoned duplicate",
			rows: []*slippy.Slip{
				seed("live", repo, slippy.SlipStatusInProgress, older),
				seed("ended", repo, slippy.SlipStatusAbandoned, newer),
			},
			queryRepo:    repo,
			wantByCommit: "live",
			wantLive:     "live",
		},
		{
			name: "compensating counts as live",
			rows: []*slippy.Slip{
				seed("live", repo, slippy.SlipStatusCompensating, older),
				seed("ended", repo, slippy.SlipStatusFailed, newer),
			},
			queryRepo:    repo,
			wantByCommit: "live",
			wantLive:     "live",
		},
		{
			name: "no live row: newest ended row wins, and failed stays visible to live lookup",
			rows: []*slippy.Slip{
				seed("old-ended", repo, slippy.SlipStatusCompleted, older),
				seed("new-ended", repo, slippy.SlipStatusFailed, newer),
			},
			queryRepo:    repo,
			wantByCommit: "new-ended",
			wantLive:     "new-ended",
		},
		{
			// The case that makes LoadLiveByCommit's per-row filter load-bearing: an EXCLUDED
			// row sorts first (no live row, and it has the newest updated_at) with a
			// non-excluded row behind it. Filtering an already-chosen row reports not found
			// here, hiding a completed slip the store would have returned.
			name: "an excluded row sorting first must not hide a non-excluded row behind it",
			rows: []*slippy.Slip{
				seed("completed", repo, slippy.SlipStatusCompleted, older),
				seed("abandoned", repo, slippy.SlipStatusAbandoned, newer),
			},
			queryRepo:    repo,
			wantByCommit: "abandoned",
			wantLive:     "completed",
		},
		{
			name: "every row excluded from the live lookup reports not found",
			rows: []*slippy.Slip{
				seed("abandoned", repo, slippy.SlipStatusAbandoned, older),
				seed("promoted", repo, slippy.SlipStatusPromoted, newer),
			},
			queryRepo:    repo,
			wantByCommit: "promoted",
			wantLive:     "",
		},
		{
			name: "two live rows: newest updated_at wins",
			rows: []*slippy.Slip{
				seed("older-live", repo, slippy.SlipStatusPending, older),
				seed("newer-live", repo, slippy.SlipStatusInProgress, newer),
			},
			queryRepo:    repo,
			wantByCommit: "newer-live",
			wantLive:     "newer-live",
		},
		{
			name: "repository match stays case-insensitive across duplicates",
			rows: []*slippy.Slip{
				seed("live", "Owner/Repo", slippy.SlipStatusPending, older),
				seed("ended", "OWNER/REPO", slippy.SlipStatusCompleted, newer),
			},
			queryRepo:    "owner/repo",
			wantByCommit: "live",
			wantLive:     "live",
		},
		{
			name: "single row behaves exactly as before",
			rows: []*slippy.Slip{
				seed("only", repo, slippy.SlipStatusCompleted, newer),
			},
			queryRepo:    repo,
			wantByCommit: "only",
			wantLive:     "only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMockStore()
			for _, row := range tt.rows {
				store.AddSlip(row)
			}
			ctx := context.Background()

			got, err := store.LoadByCommit(ctx, tt.queryRepo, sha)
			if tt.wantByCommit == "" {
				require.ErrorIs(t, err, slippy.ErrSlipNotFound)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantByCommit, got.CorrelationID, "LoadByCommit picked the wrong duplicate")
			}

			gotLive, err := store.LoadLiveByCommit(ctx, tt.queryRepo, sha)
			if tt.wantLive == "" {
				require.ErrorIs(t, err, slippy.ErrSlipNotFound)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantLive, gotLive.CorrelationID, "LoadLiveByCommit picked the wrong duplicate")
			}

			// FindByCommits and FindAllByCommits resolve a commit to a slip too, so they must
			// agree with LoadByCommit rather than keeping a second, differently-ordered answer.
			found, matched, err := store.FindByCommits(ctx, tt.queryRepo, []string{sha})
			require.NoError(t, err)
			require.NotNil(t, found)
			assert.Equal(t, sha, matched)
			assert.Equal(t, tt.wantByCommit, found.CorrelationID, "FindByCommits disagreed with LoadByCommit")

			all, err := store.FindAllByCommits(ctx, tt.queryRepo, []string{sha})
			require.NoError(t, err)
			require.Len(t, all, 1, "FindAllByCommits returns one slip per matched commit")
			assert.Equal(t, tt.wantByCommit, all[0].Slip.CorrelationID,
				"FindAllByCommits disagreed with LoadByCommit")
		})
	}
}
