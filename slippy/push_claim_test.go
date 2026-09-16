package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A claimant's run is in flight against a failed slip: the claim never writes status, so
// the row still reads failed and, before DEVOPS-367, the next same-commit push repaved it
// and every later write from the run hit ErrSlipNotFound. The push path now dedups on a
// set claimed_from before ancestor resolution, and the store's Repave guard refuses the
// row as a second line of defence (see the mock tests).
func TestClient_CreateSlipForPush_ClaimedSlipIsNotRepavedAfterAStepFailure(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: testPipelineConfig()})

	store.AddSlip(&Slip{
		CorrelationID: "corr-claimed",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-claimed",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	})
	_, err := store.ClaimSlip(ctx, "corr-claimed", []SlipStatus{SlipStatusFailed}, "slippy-cli/prejob", "rerun")
	require.NoError(t, err)

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-fresh",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-claimed",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-claimed", result.Slip.CorrelationID, "the push dedups onto the claimed run")
	assert.Empty(t, store.CreateCalls, "no fresh slip is created")
	assert.Empty(t, store.RepaveCalls, "the fast path decided before any repave attempt")

	_, err = store.Load(ctx, "corr-fresh")
	require.ErrorIs(t, err, ErrSlipNotFound, "a refused repave creates no successor")
	got, err := store.Load(ctx, "corr-claimed")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives the push")
	assert.Equal(t, SlipStatusFailed, got.Status, "and so does the status the run wrote")

	_, err = store.ReleaseClaim(ctx, "corr-claimed", "slippy-cli/postjob", "")
	require.NoError(t, err, "the dedup's push_parsed reset must not hold the claim")
}

// The claimed-slip dedup sits AFTER the empty-run guard, so a push that dispatches nothing —
// a branch create/recreate at an existing SHA — stays read-only on someone else's run. Before
// the reorder the claim branch ran first and took the componentless push through
// handlePushRetry, resetting push_parsed and appending history to a slip this push had no
// work for (PR #87 re-review).
func TestClient_CreateSlipForPush_ComponentlessPushOntoClaimedSlipIsReadOnly(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: testPipelineConfig()})

	store.AddSlip(&Slip{
		CorrelationID: "corr-claimed-run",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-branch-create",
		Status:        SlipStatusCompleted,
		Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
		StateHistory:  []StateHistoryEntry{},
	})
	_, err := store.ClaimSlip(ctx, "corr-claimed-run", nil, "slippy-cli/prejob", "rerun")
	require.NoError(t, err)
	historyBefore := len(store.Slips["corr-claimed-run"].StateHistory)

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-branch-create",
		Repository:    "owner/repo",
		Branch:        "feature/new-branch",
		CommitSHA:     "sha-branch-create",
		Components:    nil, // no work to dispatch
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-claimed-run", result.Slip.CorrelationID, "the guard hands back the existing run")
	assert.True(t, result.AncestryResolved)
	assert.Empty(t, store.RepaveCalls, "a claimed row is never repaved")
	assert.Empty(t, store.CreateCalls, "and no successor is created")
	assert.Empty(t, store.UpdateStepCalls, "read-only: no push_parsed reset on a push that dispatches nothing")
	assert.Len(t, store.Slips["corr-claimed-run"].StateHistory, historyBefore, "nor any history write")
	assert.Equal(t, SlipStatusCompleted, store.Slips["corr-claimed-run"].ClaimedFrom, "the claim survives")
}

// handleDuplicateSlipBackstop applies the same claimed-slip dedup as the main path, at the
// same point in the same order, so a lost insert race against a claimed conflicting row
// deduplicates rather than destroying a run someone else owns (PR #87 re-review). Mirrors the
// B5 empty-run-guard backstop test's shape.
func TestClient_CreateSlipForPush_DuplicateBackstopDedupsOntoClaimedConflictingSlip(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: testPipelineConfig()})

	conflicting := &Slip{
		CorrelationID: "corr-conflict-claimed",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-backstop-claimed",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
		ClaimedFrom:   SlipStatusFailed,
	}
	store.SeedOnCreate["corr-caller-claimed"] = conflicting
	store.CreateErrorOnce["corr-caller-claimed"] = ErrDuplicateSlip

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-caller-claimed",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-backstop-claimed",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-conflict-claimed", result.Slip.CorrelationID, "the backstop dedups onto the claimed run")
	assert.Empty(t, store.RepaveCalls, "a claimed conflicting row is never repaved")
	assert.Len(t, store.CreateCalls, 1, "exactly one Create attempt: no retry after the dedup")
	got, err := store.Load(ctx, "corr-conflict-claimed")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives the race")
}
