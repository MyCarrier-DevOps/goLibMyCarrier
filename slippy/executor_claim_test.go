package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkPipelineCompletion writes status; it never touches the claim. A rerun of a failed slip
// carries the old failed steps, so on its first completed step the pipeline still reads
// failed with nothing running — the exact shape in which a status-derived "quiescence"
// release fired mid-run (PR #87 review). Only a post-job release or a terminal write ends a
// claim.
func TestCheckPipelineCompletion_LeavesTheClaimAlone(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})
	store.AddSlip(&Slip{
		CorrelationID: "c", Repository: "o/r", Branch: "main", CommitSHA: "s", Status: SlipStatusFailed,
		Steps: map[string]Step{
			"builds":     {Status: StepStatusCompleted}, // the rerun's first step just finished
			"dev_deploy": {Status: StepStatusFailed},    // the failure the rerun was launched to clear
			"unit_tests": {Status: StepStatusPending},
		},
	})
	_, err := store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "slippy-cli/prejob", "rerun")
	require.NoError(t, err)

	completed, status, err := client.checkPipelineCompletion(ctx, "c")
	require.NoError(t, err)
	assert.False(t, completed)
	assert.Equal(t, SlipStatusFailed, status)

	got, err := store.Load(ctx, "c")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives the pipeline's own status writes")
	assert.Empty(t, store.ReleaseClaimCalls, "the library never releases from the completion check")
}
