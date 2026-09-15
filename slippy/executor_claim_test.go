package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failed is not terminal, so the status write keeps the claim while sibling steps may still
// be executing. Once nothing is running the run is over with a failure, and a claim held past
// that would refuse every same-commit retrigger (repave) until someone released it — the flow
// that reruns a failed pipeline. checkPipelineCompletion releases it at that point.
func TestCheckPipelineCompletion_ReleasesTheClaimOnceAFailedPipelineIsQuiescent(t *testing.T) {
	ctx := context.Background()
	newClaimed := func(t *testing.T, steps map[string]Step, aggs map[string][]ComponentStepData) (*MockStore, *Client) {
		t.Helper()
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})
		store.AddSlip(&Slip{
			CorrelationID: "c", Repository: "o/r", Branch: "main", CommitSHA: "s",
			Status: SlipStatusFailed, Steps: steps, Aggregates: aggs,
		})
		_, err := store.ClaimSlip(ctx, "c", nil, "slippy-cli/prejob", "rerun")
		require.NoError(t, err)
		return store, client
	}

	t.Run("nothing running: failed is written and the claim released", func(t *testing.T) {
		store, client := newClaimed(t, map[string]Step{
			"builds":     {Status: StepStatusFailed},
			"unit_tests": {Status: StepStatusPending},
		}, nil)
		completed, status, err := client.checkPipelineCompletion(ctx, "c")
		require.NoError(t, err)
		assert.False(t, completed)
		assert.Equal(t, SlipStatusFailed, status)

		got, err := store.Load(ctx, "c")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.Status)
		assert.Empty(t, got.ClaimedFrom, "quiescent failure ends the claim so the commit is repaveable again")
		require.Len(t, store.ReleaseClaimCalls, 1)
		assert.Equal(t, libraryActor, store.ReleaseClaimCalls[0].ReleasedBy)
		last := got.StateHistory[len(got.StateHistory)-1]
		assert.Equal(t, ReleaseMarkerStep, last.Step)
		assert.Contains(t, last.Message, "kept failed")
	})

	t.Run("a sibling step still running: failed is written, the claim kept", func(t *testing.T) {
		store, client := newClaimed(t, map[string]Step{
			"builds":     {Status: StepStatusFailed},
			"secretscan": {Status: StepStatusRunning},
		}, nil)
		_, status, err := client.checkPipelineCompletion(ctx, "c")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, status)
		got, err := store.Load(ctx, "c")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the running step is still protected")
		assert.Empty(t, store.ReleaseClaimCalls)
	})

	t.Run("a component still running inside an aggregate step: the claim kept", func(t *testing.T) {
		store, client := newClaimed(t, map[string]Step{
			"builds": {Status: StepStatusFailed},
		}, map[string][]ComponentStepData{
			"builds": {{Component: "api", Status: StepStatusFailed}, {Component: "web", Status: StepStatusRunning}},
		})
		_, _, err := client.checkPipelineCompletion(ctx, "c")
		require.NoError(t, err)
		got, err := store.Load(ctx, "c")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "a building component is still protected")
		assert.Empty(t, store.ReleaseClaimCalls)
	})

	t.Run("unclaimed slip: no release is attempted", func(t *testing.T) {
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})
		store.AddSlip(&Slip{
			CorrelationID: "u", Repository: "o/r", Branch: "main", CommitSHA: "s", Status: SlipStatusInProgress,
			Steps: map[string]Step{"builds": {Status: StepStatusFailed}},
		})
		_, status, err := client.checkPipelineCompletion(ctx, "u")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, status)
		assert.Empty(t, store.ReleaseClaimCalls)
	})
}
