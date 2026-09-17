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
func TestClient_CreateSlipForPush_SelfCorrelationClaimedSlipIsResetNotDeduped(t *testing.T) {
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
	assert.Len(t, store.CreateCalls, 1, "the claimed branch must not pre-empt the in-place reset")
	assert.Empty(t, store.RepaveCalls, "a self-repave is still never attempted")

	got, err := store.Load(ctx, "corr-self")
	require.NoError(t, err)
	assert.True(t, got.Status.IsLive(), "the reset makes the row live again, so the caller's dispatch is correct")
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "and the claim survives: Create never writes claimed_from")
	// THE INVARIANT, pinned directly (finding p2): claimed_from and the slip_claimed marker are
	// both present or both absent. Create rewrites state_history — it is in slipColumns() —
	// while claimed_from survives, so without appendResetMarkers carrying the claim forward the
	// column would be set and the marker gone. pushhookparser derives ClaimedBy from the
	// markers and gates its stranded-cleanup exemption on it, so the two readers would disagree
	// about one row and a still-claimed slip would become reapable.
	assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep),
		"the reset carries the claim marker forward, so column and marker agree")
	assert.True(t, hasHistoryMessage(got, "reset in place after failed attempt"),
		"alongside the reset marker, not instead of it")
}

// The self-correlation carve-out stops at work in flight (finding p1). Create's ON CONFLICT
// arm rewrites every step and aggregate column AND the state history, so resetting a claimed
// row whose own dispatch is still executing destroys the state that run is writing — under an
// unchanged correlation ID, which leaves an operator no way to tell which attempt wrote what.
// The in-delivery retry that reaches here after its dispatch already started is exactly that
// shape, so the claimed branch keeps it and the push dedups instead.
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
func TestClient_CreateSlipForPush_DuplicateBackstopResetsASelfCorrelationClaimedSlip(t *testing.T) {
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
	assert.Len(t, store.CreateCalls, 2, "the self-referential arm hands back to the insert retry, not a dedup")
	assert.Empty(t, store.RepaveCalls, "and Repave is never asked to supersede a row with itself")

	got, err := store.Load(ctx, "corr-self-backstop")
	require.NoError(t, err)
	assert.True(t, got.Status.IsLive(), "the retry's upsert reset the row")
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives the reset")
	assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep),
		"and the marker is carried forward here too, or the two convergent paths would differ on it")
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
