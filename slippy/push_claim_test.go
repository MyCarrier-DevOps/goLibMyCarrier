package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A claimant's run that hits one step failure has failed written over its claim's
// in_progress by checkPipelineCompletion. Before Repave's DELETE was claim-aware, that
// reopened the repave window for the rest of the run: the next same-commit push saw an
// ended status, replaced the row, and every later write from the run hit ErrSlipNotFound
// (PR #87 review, critical). The store now refuses the DELETE while claimed_from is set and
// the push path's existing went-live fallback dedups onto the run instead of replacing it.
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
	// One step of the rerun failed: the executor writes failed over the claim's in_progress.
	require.NoError(t, store.UpdateSlipStatus(ctx, "corr-claimed", SlipStatusFailed))

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
	assert.Equal(t, []string{"corr-claimed"}, store.RepaveCalls, "the repave was attempted and the store refused it")

	_, err = store.Load(ctx, "corr-fresh")
	require.ErrorIs(t, err, ErrSlipNotFound, "a refused repave creates no successor")
	got, err := store.Load(ctx, "corr-claimed")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives the push")
	assert.Equal(t, SlipStatusFailed, got.Status, "and so does the status the run wrote")
}
