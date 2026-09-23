package slippy

import (
	"context"
	"strings"
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

// A claimed row carrying THIS push's own correlation ID is not another run's row to protect:
// it is this delivery's own retry. Both paths must reset it in place rather than dedup onto
// it, or the caller sees returned == sent on an ENDED row and dispatches against a terminal
// slip — the outcome persistSlipForPush's self-referential arm and the empty-run guard's
// self-correlation exclusion both exist to prevent. Create's SET list excludes claimed_from,
// so the reset keeps the claim (PR #87 re-review).
//
// This is the QUIESCENT half of the carve-out; its in-flight half is the test below.
func TestClient_CreateSlipForPush_SelfCorrelationClaimedSlipIsDedupedNotReset(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: testPipelineConfig()})

	store.AddSlip(&Slip{
		CorrelationID: "corr-self",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	})
	_, err := store.ClaimSlip(ctx, "corr-self", nil, "slippy-cli/prejob", "")
	require.NoError(t, err)

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-self", // the in-delivery retry reuses its id
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-self", result.Slip.CorrelationID)

	// THE BEHAVIOUR THAT CHANGED (PR #87 review, pkuzmenko). This used to reset in place, on
	// the premise that a row carrying this push's own id could only be this delivery's retry.
	// It cannot: the rerunner adopts the slip it looked up and claims under the ORIGINAL
	// push's correlation ID, and the Slippy CLI pre-job claims the slip its workflow was
	// dispatched for — so a self-correlated claimed row is routinely someone else's run. The
	// claim reports no step until its first post-job, so `quiescent` here means `queued`, not
	// `absent`, and the old reset wiped every step, aggregate and history entry of a live run.
	//
	// The store now refuses under the row lock and the push deduplicates onto the live row.
	assert.Empty(t, store.CreateCalls, "a claimed row is never overwritten by a push")
	assert.Empty(t, store.RepaveCalls, "a self-repave is still never attempted")
	assert.Empty(t, store.ResetInPlaceCalls,
		"and the reset is not even attempted: the gate deduped before resolveAndAbandonAncestors, "+
			"which would otherwise have abandoned this push's ancestors on behalf of a push that "+
			"then deduplicates onto someone else's run (PR #87 review, jhicks)")

	got, err := store.Load(ctx, "corr-self")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim is untouched")
	assert.Equal(t, StepStatusFailed, got.Steps["builds"].Status,
		"and so is every step the claimant's run owns")
	// handlePushRetry runs on this arm and resets push_parsed, which is the library's own
	// bookkeeping step — RunInFlight ignores it BY NAME, so it can never make the claimant's
	// run look in flight, and it touches nothing else the claim protects.
	assert.Equal(t, StepStatusRunning, got.Steps[PushParsedStep].Status,
		"push_parsed is the one step the claimed arm writes, and it is not the claimant's")
	assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep),
		"exactly the marker the claim wrote: nothing carried, because nothing was rewritten")
	assert.Equal(t, "slippy-cli/prejob", lastHistoryActor(got, ClaimMarkerStep),
		"and it still names the original claimant")
}

// The self-correlation carve-out stops at work in flight AS OF THE PUSH'S READ (finding p1).
// Create's ON CONFLICT arm rewrites every step and aggregate column AND the state history, so
// resetting a claimed row whose own dispatch is still executing destroys the state that run is
// writing — under an unchanged correlation ID, which leaves an operator no way to tell which
// attempt wrote what. The in-delivery retry that reaches here after its dispatch already
// started is exactly that shape, so the claimed branch keeps it and the push dedups instead.
//
// What this pins is the DECISION taken on the row as read — the FAST PATH, which is all a
// snapshot can decide: the read is unlocked, with seconds of GitHub calls before the write.
// A row that becomes claimed during that window is outside this test's reach, and is caught
// instead by the row lock SlipStore.ResetSlipInPlace takes — see
// TestClient_CreateSlipForPush_ResetSlipInPlaceArms, whose "a quiescent claim landing
// mid-window is refused under the lock" and "a refusal whose reload fails is fatal" subtests
// are those cases (DEVOPS-367; the citation here named a test that had been deleted, PR #87
// review, pkuzmenko). The full account is on
// CreateSlipForPush's claimed arm in push.go.
//
// Note what the dedup returns: handlePushRetry resets push_parsed and never writes the slip's
// top-level status, so the row comes back still reading `failed` — asserted below. A caller
// that must not report against an ended row gates on the claim and step evidence Slip already
// carries on the wire, not on that status.
func TestClient_CreateSlipForPush_SelfCorrelationClaimedSlipInFlightIsDedupedNotReset(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: shippedShapePipelineConfig()})

	store.AddSlip(&Slip{
		CorrelationID: "corr-self-live",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self-live",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}, "unit_tests": {Status: StepStatusRunning}},
		Aggregates: map[string][]ComponentStepData{
			"builds": {{Component: "api", Status: StepStatusFailed}, {Component: "web", Status: StepStatusRunning}},
		},
		StateHistory: []StateHistoryEntry{{Step: "builds", Status: StepStatusFailed, Actor: "post-job"}},
	})
	// The rerunner adopts a run that is executing by NAMING its status; a nil if_status no
	// longer admits one.
	_, err := store.ClaimSlip(ctx, "corr-self-live", []SlipStatus{SlipStatusFailed}, "pushhookparser/rerunner", "")
	require.NoError(t, err)

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-self-live", // the in-delivery retry reuses its id
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self-live",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-self-live", result.Slip.CorrelationID)
	assert.Equal(t, SlipStatusFailed, result.Slip.Status,
		"the dedup does NOT make the row live: handlePushRetry resets push_parsed and never writes status")
	assert.Empty(t, store.CreateCalls, "no upsert: the reset would have rewritten the running run's state")
	assert.Empty(t, store.RepaveCalls, "and a claimed row is never repaved either")

	got, err := store.Load(ctx, "corr-self-live")
	require.NoError(t, err)
	assert.Equal(t, StepStatusRunning, got.Steps["unit_tests"].Status, "the running step is untouched")
	assert.Equal(t, StepStatusFailed, got.Steps["builds"].Status, "and so is the failed one")
	require.Len(t, got.Aggregates["builds"], 2)
	assert.Equal(t, StepStatusRunning, got.Aggregates["builds"][1].Status, "the running component survives")
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives")
	assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep), "and so does its marker")
	assert.Equal(t, 1, countHistoryStep(got, "builds"), "the run's own history is not replaced")
}

// The backstop orders its mirror the same way — live, empty-run guard, self-referential,
// claimed, repave — so a lost insert race whose conflicting row is this push's own AND
// claimed converges on the main path's in-place reset instead of a plain dedup.
func TestClient_CreateSlipForPush_DuplicateBackstopDedupsASelfCorrelationClaimedSlip(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: testPipelineConfig()})

	conflicting := &Slip{
		CorrelationID: "corr-self-backstop",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self-backstop",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
		ClaimedFrom:   SlipStatusFailed,
	}
	store.SeedOnCreate["corr-self-backstop"] = conflicting
	store.CreateErrorOnce["corr-self-backstop"] = ErrDuplicateSlip

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-self-backstop",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self-backstop",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-self-backstop", result.Slip.CorrelationID)
	assert.Len(t, store.CreateCalls, 1, "one Create: the one that lost the race and raised ErrDuplicateSlip")
	assert.Empty(t, store.ResetInPlaceCalls,
		"the backstop's claimed arm now dedups before reaching the self-referential arm, so no "+
			"reset is attempted at all — it mirrors the main path's gate (PR #87 review, jhicks)")
	assert.Empty(t, store.RepaveCalls, "and Repave is never asked to supersede a row with itself")

	// The backstop converges on the same answer as the main path (PR #87 review, pkuzmenko):
	// the row it found is CLAIMED, so the locked decision refuses and the push deduplicates
	// onto it rather than overwriting a dispatched run. The two paths agreeing here is the
	// property that matters — a reset allowed on one and refused on the other would make the
	// outcome depend on which of the two raced first.
	got, err := store.Load(ctx, "corr-self-backstop")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim is untouched")
	assert.Equal(t, map[string]Step{"builds": {Status: StepStatusFailed}}, got.Steps,
		"and the claimant's step state is not rewritten")
	// This fixture sets ClaimedFrom directly rather than through ClaimSlip, so it never had a
	// marker; the point is that the refusal adds none either — no reset marker, no carried
	// claim marker, because no write happened.
	assert.Empty(t, got.StateHistory, "a refused reset writes nothing at all")
}

// The backstop's twin of the in-flight carve-out: its claimed arm now sits ABOVE its
// self-referential arm with the same `claimed && (different id || RunInFlight)` condition, so
// a lost insert race whose conflicting row is this push's own AND has work executing dedups
// instead of resetting (finding p1).
func TestClient_CreateSlipForPush_DuplicateBackstopDedupsASelfCorrelationClaimedSlipInFlight(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: shippedShapePipelineConfig()})

	conflicting := &Slip{
		CorrelationID: "corr-self-live-backstop",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self-live-backstop",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusRunning}},
		StateHistory:  []StateHistoryEntry{ClaimMarker(SlipStatusFailed, "pushhookparser/rerunner", "")},
		ClaimedFrom:   SlipStatusFailed,
	}
	store.SeedOnCreate["corr-self-live-backstop"] = conflicting
	store.CreateErrorOnce["corr-self-live-backstop"] = ErrDuplicateSlip

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-self-live-backstop",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-self-live-backstop",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-self-live-backstop", result.Slip.CorrelationID)
	assert.Len(t, store.CreateCalls, 1, "the claimed arm answered: no insert retry, so no in-place reset")
	assert.Empty(t, store.RepaveCalls)

	got, err := store.Load(ctx, "corr-self-live-backstop")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.Status, "the row is left exactly as the running run has it")
	assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
	assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep))
}

// Every other push-claim test in this file runs on testPipelineConfig(), whose step 0 is
// `push_parsed` — the ONE step RunInFlight skips by name — while both shipped configs
// (default.json, production.json) start at `builds`, an aggregate that build post-jobs report.
// So those tests could not observe the in-flight evidence production actually has: the step a
// fresh push marks running there is invisible to the claim (finding p6). This one runs on a
// config shaped like the shipped ones and asserts the step, the aggregate and the marker rather
// than only ClaimedFrom.
func TestClient_CreateSlipForPush_ClaimedSlipOnAShippedShapeConfigKeepsItsRunningWork(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: shippedShapePipelineConfig()})

	store.AddSlip(&Slip{
		CorrelationID: "corr-shipped",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-shipped",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusRunning}, "unit_tests": {Status: StepStatusPending}},
		Aggregates:    map[string][]ComponentStepData{"builds": {{Component: "api", Status: StepStatusRunning}}},
		StateHistory:  []StateHistoryEntry{},
	})
	claim, err := store.ClaimSlip(ctx, "corr-shipped", []SlipStatus{SlipStatusFailed}, "pushhookparser/rerunner", "rerun")
	require.NoError(t, err)
	require.True(t, claim.InFlight, "step 0 is an aggregate here, so the claim can see the run at all")

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-shipped-push",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-shipped",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Slip)
	assert.Equal(t, "corr-shipped", result.Slip.CorrelationID, "the push dedups onto the claimed run")
	assert.Empty(t, store.CreateCalls)
	assert.Empty(t, store.RepaveCalls)

	got, err := store.Load(ctx, "corr-shipped")
	require.NoError(t, err)
	assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status, "the running step is left alone")
	require.Len(t, got.Aggregates["builds"], 1)
	assert.Equal(t, StepStatusRunning, got.Aggregates["builds"][0].Status, "and so is the running component")
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
	assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep), "the claim marker is intact, so the column has its pair")

	// A release from a post-job of THAT run still finds work in flight, which is the property
	// the dedup exists to preserve.
	out, err := store.ReleaseClaim(ctx, "corr-shipped", "pushhookparser/rerunner", "")
	require.NoError(t, err)
	assert.False(t, out.Released, "the deduped push did not make the run look quiescent")
}

// shippedShapePipelineConfig mirrors the SHIPPED configs' shape rather than the test config's:
// step 0 is `builds`, an aggregate that every build post-job reports, not `push_parsed`. That
// difference is load-bearing for anything that reads in-flight evidence, because RunInFlight
// skips push_parsed BY NAME (see PushParsedStep) — on testPipelineConfig() the step
// initializeSlipForPush marks running for a fresh push is precisely the step the claim cannot
// see.
func shippedShapePipelineConfig() *PipelineConfig {
	config := &PipelineConfig{
		Version:     "1",
		Name:        "shipped-shape",
		Description: "step 0 is an aggregate, as in default.json and production.json",
		Steps: []StepConfig{
			{Name: "builds", Description: "All component container builds finished", Aggregates: "build"},
			{Name: "unit_tests", Description: "Unit tests", Prerequisites: []string{"builds"}},
			{Name: "dev_deploy", Description: "Dev deploy", Prerequisites: []string{"unit_tests"}},
		},
	}
	config.initialize()
	return config
}

// hasHistoryMessage reports whether any state-history entry's message contains substr.
func hasHistoryMessage(slip *Slip, substr string) bool {
	for _, entry := range slip.StateHistory {
		if strings.Contains(entry.Message, substr) {
			return true
		}
	}
	return false
}

// countHistoryStep counts state-history entries written against one step name.
func countHistoryStep(slip *Slip, step string) int {
	n := 0
	for _, entry := range slip.StateHistory {
		if entry.Step == step {
			n++
		}
	}
	return n
}

// lastHistoryActor returns the actor of the last state-history entry written against step,
// or "" when there is none. It is the reader's half of the claim marker's actor: the tests assert
// on the actor a later reader would derive, not on the one this library happened to pass.
func lastHistoryActor(slip *Slip, step string) string {
	actor := ""
	for _, entry := range slip.StateHistory {
		if entry.Step == step {
			actor = entry.Actor
		}
	}
	return actor
}

// appendResetMarker records the prior attempt's STATUS and nothing else. It used to carry the
// prior row's claim marker forward as well; that half now lives in SlipStore.ResetSlipInPlace,
// which re-states it from the row it has LOCKED (DEVOPS-367). The difference is not stylistic:
// this function only ever saw the push's snapshot, so it could restore a claim the snapshot had
// already recorded and nothing else — while the reachable half of the race is a claim taken
// after that read, which no snapshot can show. Pinned here so a well-meaning restoration of the

// SlipStore.ResetSlipInPlace closes (DEVOPS-367).
// claimQuiescentDuringTheWindow is the same interleaving with NO step started — the shape that
// used to be allowed through and reset a dispatched run, and the reason DecideReset refuses on
// any claim (PR #87 review, pkuzmenko).
func claimQuiescentDuringTheWindow(correlationID string) func(*MockStore) {
	fired := false
	return func(m *MockStore) {
		if fired {
			return
		}
		fired = true
		slip, ok := m.Slips[correlationID]
		if !ok {
			return
		}
		slip.ClaimedFrom = SlipStatusFailed
		slip.StateHistory = append(slip.StateHistory,
			ClaimMarker(SlipStatusFailed, "pushhookparser/rerunner", "rerun scope=all"))
	}
}

func claimDuringTheWindow(correlationID string) func(*MockStore) {
	fired := false
	return func(m *MockStore) {
		if fired {
			return
		}
		fired = true
		slip, ok := m.Slips[correlationID]
		if !ok {
			return
		}
		slip.ClaimedFrom = SlipStatusFailed
		slip.StateHistory = append(slip.StateHistory,
			ClaimMarker(SlipStatusFailed, "pushhookparser/rerunner", "rerun scope=all"))
		slip.Steps["builds"] = Step{Status: StepStatusRunning}
	}
}

// differ on purpose: one falls back, one dedups through a different route, two are fatal.
func TestClient_CreateSlipForPush_ResetSlipInPlaceArms(t *testing.T) {
	ctx := context.Background()

	endedSelfRow := func(store *MockStore, id, sha string) {
		store.AddSlip(&Slip{
			CorrelationID: id, Repository: "owner/repo", Branch: "main", CommitSHA: sha,
			Status: SlipStatusFailed, Steps: map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory: []StateHistoryEntry{},
		})
	}
	push := func(client *Client, id, sha string) (*CreateSlipResult, error) {
		return client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: id, Repository: "owner/repo", Branch: "main", CommitSHA: sha,
			Components: []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
	}

	// A store that cannot decide under a lock (ClickHouseStore) must not fail the push: it has
	// no claimed_from column either, so there is no claim for the refused decision to protect
	// and the plain upsert is exactly what this arm did before the decision moved into the
	// store. Same shape as repaveExistingSlip's ErrRepaveUnsupported fallback.
	t.Run("ErrResetUnsupported falls back to a plain Create", func(t *testing.T) {
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(),
			Config{PipelineConfig: shippedShapePipelineConfig()})
		endedSelfRow(store, "corr-unsupported", "sha-unsupported")
		store.ResetInPlaceError = ErrResetUnsupported

		result, err := push(client, "corr-unsupported", "sha-unsupported")
		require.NoError(t, err)
		require.NotNil(t, result.Slip)
		assert.Len(t, store.CreateCalls, 1, "the fallback is the plain upsert")
		got, loadErr := store.Load(ctx, "corr-unsupported")
		require.NoError(t, loadErr)
		assert.True(t, got.Status.IsLive(), "and it still resets the row, as it always did")
	})

	// A duplicate means the target row was gone when the reset locked it and another
	// correlation ID now holds this (repository, commit_sha) — a concurrent repave. The main
	// path routes that to the same backstop createFreshSlip uses for the same sentinel, and
	// here the backstop finds the winner's live row and dedups onto it.
	t.Run("ErrDuplicateSlip routes to the duplicate backstop", func(t *testing.T) {
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(),
			Config{PipelineConfig: shippedShapePipelineConfig()})
		endedSelfRow(store, "corr-dup", "sha-dup")
		store.ResetInPlaceError = ErrDuplicateSlip
		// The concurrent repave, landing in the same window: our row is gone and the winner's
		// successor holds the commit.
		swapped := false
		store.AfterLoadByCommit = func(m *MockStore) {
			if swapped {
				return
			}
			swapped = true
			delete(m.Slips, "corr-dup")
			m.Slips["corr-winner"] = &Slip{
				CorrelationID: "corr-winner", Repository: "owner/repo", Branch: "main",
				CommitSHA: "sha-dup", Status: SlipStatusInProgress,
			}
		}

		result, err := push(client, "corr-dup", "sha-dup")
		require.NoError(t, err, "the backstop resolves it; the push does not fail")
		require.NotNil(t, result.Slip)
		assert.Equal(t, "corr-winner", result.Slip.CorrelationID,
			"deduped onto the row that won the commit, so the caller sees returned != sent")
		assert.Len(t, store.ResetInPlaceCalls, 1, "and the reset was not retried after the backstop answered")
	})

	// Any other store failure is fatal, as a failed Create always was: nothing was written, so
	// there is no successor to fall through to, and Kafka redelivery converges.
	t.Run("any other error fails the push", func(t *testing.T) {
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(),
			Config{PipelineConfig: shippedShapePipelineConfig()})
		endedSelfRow(store, "corr-boom", "sha-boom")
		store.ResetInPlaceError = ErrStoreConnection

		_, err := push(client, "corr-boom", "sha-boom")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStoreConnection)
	})

	// THE SUCCESS ARM DOES NOT RELOAD, and nothing tested that until this case (PR #87
	// review, jhicks). The decision has flipped twice in three commits — a reload was added to
	// close a review finding, then reverted once that finding's premise became unreachable —
	// so the next reader of the earlier thread has a well-argued case for re-adding it and
	// needs a failing test rather than a comment to stop them.
	//
	// The damage a reload does is silent: Slip.Ancestry has no `omitempty` and populate never
	// hydrates it, because ancestry lives in a child table and is absent from
	// slipSelectColumns(). So overwriting the caller's value with a loaded row turns a resolved
	// chain into "ancestry": null on the 201 — a valid field value, not an error.
	t.Run("a successful reset returns the caller's resolved ancestry, not a reloaded nil", func(t *testing.T) {
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(),
			Config{PipelineConfig: shippedShapePipelineConfig()})
		endedSelfRow(store, "corr-ancestry", "sha-ancestry")

		result, err := push(client, "corr-ancestry", "sha-ancestry")
		require.NoError(t, err)
		require.NotNil(t, result.Slip)
		require.Len(t, store.ResetInPlaceCalls, 1, "the reset was performed: the row is unclaimed")

		// Asserted on the MECHANISM rather than on a field value, because a fixture whose
		// ancestry happens to be empty would make a value assertion vacuously true. A reload
		// is a Load against this correlation ID; the success arm must make none.
		assert.NotContains(t, store.LoadCalls, "corr-ancestry",
			"the success arm must not reload: Load drops Ancestry, which has no omitempty, "+
				"so reloading turns a resolved chain into \"ancestry\": null on the 201")

		// And the precondition that makes a reload lossy, pinned here so the reason survives
		// even if someone changes the arm: no store hydrates Ancestry on load.
		reloaded, loadErr := store.Load(ctx, "corr-ancestry")
		require.NoError(t, loadErr)
		assert.Nil(t, reloaded.Ancestry,
			"a loaded row carries no Ancestry — it lives in a child table, absent from slipSelectColumns()")
	})

	// The refusal's own failure mode: the dedup has to RELOAD the row, because the snapshot
	// the push still holds says unclaimed and quiescent and returning it would report a state
	// known to be false. If that reload fails there is nothing truthful left to return.
	t.Run("a refusal whose reload fails is fatal", func(t *testing.T) {
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(),
			Config{PipelineConfig: shippedShapePipelineConfig()})
		endedSelfRow(store, "corr-reload", "sha-reload")
		claim := claimDuringTheWindow("corr-reload")
		store.AfterLoadByCommit = func(m *MockStore) {
			claim(m)
			m.LoadError = ErrStoreConnection
		}

		_, err := push(client, "corr-reload", "sha-reload")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrStoreConnection)
		assert.Contains(t, err.Error(), "after a refused in-place reset")
	})

	// The claim that lands mid-window with NOTHING started. This is the case the reset used to
	// allow through, and the one the client-level suite lost when its apparatus was deleted
	// (PR #87 review, pkuzmenko): the push's snapshot is taken before the hook fires, so the
	// gate sees an unclaimed row and falls through, and only the store's locked read sees the
	// claim. Nothing but this exercises that path outside the integration build tag.
	t.Run("a quiescent claim landing mid-window is refused under the lock", func(t *testing.T) {
		store := NewMockStore()
		client := NewClientWithDependencies(store, NewMockGitHubAPI(),
			Config{PipelineConfig: shippedShapePipelineConfig()})
		endedSelfRow(store, "corr-midwindow", "sha-midwindow")
		store.AfterLoadByCommit = claimQuiescentDuringTheWindow("corr-midwindow")

		result, err := push(client, "corr-midwindow", "sha-midwindow")
		require.NoError(t, err, "a refusal deduplicates; it does not fail the push")
		require.NotNil(t, result.Slip)
		assert.Len(t, store.ResetInPlaceCalls, 1, "the reset was attempted and refused under the lock")
		assert.Empty(t, store.CreateCalls, "and nothing overwrote the claimant's row")

		got, loadErr := store.Load(ctx, "corr-midwindow")
		require.NoError(t, loadErr)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim the push never saw survives")
		assert.Equal(t, StepStatusFailed, got.Steps["builds"].Status, "and so does its step state")
	})
}

// old behaviour shows up as a doubled marker rather than as a silent duplicate.
func TestAppendResetMarker_RecordsThePriorAttemptOnly(t *testing.T) {
	t.Run("the prior attempt's status, on the successor's history", func(t *testing.T) {
		slip := &Slip{CorrelationID: "c"}
		prior := &Slip{Status: SlipStatusFailed}
		appendResetMarker(slip, prior, "0123456789abcdef")
		assert.Equal(t, 1, countHistoryStep(slip, PushParsedStep))
		assert.True(t, hasHistoryMessage(slip, "reset in place after failed attempt"))
		assert.Equal(t, LibraryActor, lastHistoryActor(slip, PushParsedStep))
	})

	t.Run("a claimed prior: no claim marker here, because the store writes it under the lock", func(t *testing.T) {
		slip := &Slip{CorrelationID: "c"}
		prior := &Slip{
			Status:      SlipStatusFailed,
			ClaimedFrom: SlipStatusFailed,
			StateHistory: []StateHistoryEntry{
				ClaimMarker(SlipStatusFailed, "pushhookparser/rerunner", "rerun scope=all"),
			},
		}
		appendResetMarker(slip, prior, "0123456789abcdef")
		assert.Equal(t, 0, countHistoryStep(slip, ClaimMarkerStep),
			"the claim is re-stated from the locked row, not from this snapshot: appending here too would double it")
		assert.Equal(t, 1, countHistoryStep(slip, PushParsedStep))
	})
}

// The idempotency guard on appendResetMarker had no test of its own — it was added for a
// reviewer and its dedicated test was deleted in the same commit, so removing the guard would
// have failed nothing (PR #87 review, pkuzmenko).
func TestAppendResetMarker_IsIdempotentPerSuccessor(t *testing.T) {
	prior := &Slip{CorrelationID: "p", Status: SlipStatusFailed}
	slip := &Slip{CorrelationID: "p"}

	appendResetMarker(slip, prior, "abcdef1234")
	require.Len(t, slip.StateHistory, 1, "the first append records the prior attempt")

	// The second call is the real interleaving: the main arm appends, hands the SAME *Slip to
	// handleDuplicateSlipBackstop on ErrDuplicateSlip, and its self-referential arm appends
	// again. One push must not record two resets for one row.
	appendResetMarker(slip, prior, "abcdef1234")
	assert.Len(t, slip.StateHistory, 1, "a second reset attempt on the same successor adds nothing")

	// A different successor is a different row and keeps its own marker.
	other := &Slip{CorrelationID: "q"}
	appendResetMarker(other, prior, "abcdef1234")
	assert.Len(t, other.StateHistory, 1)
}
