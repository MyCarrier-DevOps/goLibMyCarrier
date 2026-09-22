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
	assert.Len(t, store.ResetInPlaceCalls, 1, "the claimed branch must not pre-empt the in-place reset")
	assert.Empty(t, store.CreateCalls, "and the reset is the locked store operation, not a bare Create")
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
	// AND IT CARRIES THE CLAIMANT, not just the claim (PR #87, jhicks review). The invariant
	// this marker restores is what pushhookparser's ClaimedBy reads, and ClaimedBy IS the
	// actor of the most recent slip_claimed marker — so writing the library's own actor here
	// keeps the row exempt from the stranded cleanup while renaming its adopter to
	// "slippy-library" for that reader and for every audit query on who adopted the slip.
	assert.Equal(t, "slippy-cli/prejob", lastHistoryActor(got, ClaimMarkerStep),
		"the carried-forward marker names the original claimant, not the library")
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
// A row that becomes claimed or goes in flight during that window is outside this test's
// reach, and is caught instead by the row lock SlipStore.ResetSlipInPlace takes —
// TestClient_CreateSlipForPush_ResetRefusedWhenAClaimLandsAfterTheRead is that case
// (DEVOPS-367, closing PR #87 pkuzmenko finding 2). The full account is on
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
	assert.Len(t, store.CreateCalls, 1, "one Create: the one that lost the race and raised ErrDuplicateSlip")
	assert.Len(t, store.ResetInPlaceCalls, 1,
		"and the backstop's self-referential arm performs the reset itself, through the shared helper")
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

// lastHistoryActor returns the actor of the last state-history entry written against step,
// or "" when there is none. It is the reader's half of claimantFromHistory: the tests assert
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

// ResetClaimMarker is what the store and both doubles write in its place, so the three cannot
// drift. The actor is the half that matters to a consumer: pushhookparser's ClaimedBy IS the
// most recent slip_claimed marker's actor, so a marker naming the library would restore the
// row's stranded-cleanup exemption while renaming its adopter.
func TestResetClaimMarker(t *testing.T) {
	t.Run("names the original claimant, read off the prior history", func(t *testing.T) {
		entry := ResetClaimMarker(SlipStatusFailed, []StateHistoryEntry{
			{Step: "builds", Status: StepStatusFailed, Actor: "post-job"},
			ClaimMarker(SlipStatusFailed, "pushhookparser/rerunner", "rerun scope=all"),
		})
		assert.Equal(t, ClaimMarkerStep, entry.Step)
		assert.Equal(t, "pushhookparser/rerunner", entry.Actor)
		assert.Contains(t, entry.Message, "adopted failed slip", "the prior is the RECORDED claimed_from")
		assert.Contains(t, entry.Message, "carried forward across an in-delivery retry reset")
	})

	t.Run("falls back to the library when no claimant is recorded", func(t *testing.T) {
		entry := ResetClaimMarker(SlipStatusPending, nil)
		assert.Equal(t, ClaimMarkerStep, entry.Step)
		assert.Equal(t, LibraryActor, entry.Actor,
			"presence is the invariant; an unnameable claimant does not excuse dropping the marker")
	})

	t.Run("a released claim leaves no claimant, so the fallback applies", func(t *testing.T) {
		entry := ResetClaimMarker(SlipStatusFailed, []StateHistoryEntry{
			ClaimMarker(SlipStatusFailed, "slippy-cli/prejob", ""),
			ReleaseMarker(SlipStatusFailed, "slippy-cli/postjob", ""),
		})
		assert.Equal(t, LibraryActor, entry.Actor)
	})
}

// claimDuringTheWindow returns a one-shot LoadByCommit hook that does what a claimant does in
// the seconds between the push's read and the push's write: claims the row and starts a step
// on it. The push's own snapshot is taken before the hook runs, so every decision the push
// makes on that snapshot is already stale — which is the shape of the race
// SlipStore.ResetSlipInPlace closes (DEVOPS-367).
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

// assertRefusedResetDeduped is the SHARED assertion both reset arms are held to. push.go
// asserts in two places that the two paths converge on the same outcome for the same inputs,
// and the refusal is the newest of those outcomes: a claim that landed after the push's read
// is found under the row lock, the reset is refused, and the push deduplicates onto the live
// row instead of failing. Identical inputs, identical outcome, whichever arm got there.
func assertRefusedResetDeduped(t *testing.T, store *MockStore, result *CreateSlipResult, correlationID string) {
	t.Helper()
	require.NotNil(t, result.Slip)
	assert.Equal(t, correlationID, result.Slip.CorrelationID, "the dedup is onto the row the reset was refused on")
	assert.Equal(t, SlipStatusFailed, result.Slip.ClaimedFrom,
		"and the returned copy is RELOADED, so it shows the claim the push's own snapshot never saw")
	assert.Equal(t, StepStatusRunning, result.Slip.Steps["builds"].Status)
	assert.Len(t, store.ResetInPlaceCalls, 1, "the reset was attempted exactly once")
	assert.Empty(t, store.RepaveCalls, "and a claimed row is never repaved either")

	got, err := store.Load(context.Background(), correlationID)
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.Status, "nothing was written: the claimant's run is untouched")
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
	assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status)
	assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep),
		"the claim marker survives, so claimed_from and slip_claimed still agree")
	assert.False(t, hasHistoryMessage(got, "reset in place after"),
		"and the successor's reset marker was never written")
}

// The main path's half of the race, end to end through the client.
//
// The push reads an ended, unclaimed, quiescent row carrying its OWN correlation ID — the
// in-delivery retry — so it takes the self-referential reset arm. A claimant then claims the
// row and starts a step while resolveAndAbandonAncestors is making its GitHub calls. Before
// DEVOPS-367 the write was an unlocked upsert that re-read nothing: it kept claimed_from
// (excluded from slipColumns()) and replaced state_history (included), leaving the column set
// with the slip_claimed marker gone, and it rewrote the step columns of a run that was
// executing. Now the store re-reads FOR UPDATE, refuses, and the push dedups.
func TestClient_CreateSlipForPush_ResetRefusedWhenAClaimLandsAfterTheRead(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: shippedShapePipelineConfig()})

	store.AddSlip(&Slip{
		CorrelationID: "corr-window",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-window",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	})
	store.AfterLoadByCommit = claimDuringTheWindow("corr-window")

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-window", // the in-delivery retry reuses its id
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-window",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err, "a refused reset is a dedup, not a failed push: the desired end "+
		"state — one run for this commit — already holds")
	assertRefusedResetDeduped(t, store, result, "corr-window")
	assert.Empty(t, store.CreateCalls, "the main path's reset never falls back to an unlocked Create")
}

// The backstop's half of the same race, held to the SAME assertion. Its window is shorter —
// its own LoadByCommit runs after ancestor resolution, immediately before the insert retry —
// but it is not zero, and the two arms must not differ on what happens when it loses.
//
// Dormant until migration v5's unique index exists (ErrDuplicateSlip is what routes here),
// which is exactly why it needs a test: nothing exercises this arm in production yet.
func TestClient_CreateSlipForPush_BackstopResetRefusedWhenAClaimLandsAfterTheRead(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{PipelineConfig: shippedShapePipelineConfig()})

	// The conflicting row appears only when this push's own Create loses the race, and carries
	// THIS push's correlation ID — the self-referential shape that reaches the reset arm.
	store.CreateErrorOnce["corr-window-backstop"] = ErrDuplicateSlip
	store.SeedOnCreate["corr-window-backstop"] = &Slip{
		CorrelationID: "corr-window-backstop",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-window-backstop",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	}
	store.AfterLoadByCommit = claimDuringTheWindow("corr-window-backstop")

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-window-backstop",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-window-backstop",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	})
	require.NoError(t, err)
	assertRefusedResetDeduped(t, store, result, "corr-window-backstop")
	assert.Len(t, store.CreateCalls, 1,
		"one Create: the one that lost the repo:sha race and routed this push to the backstop")
}

// The arms of resetSlipInPlace that are not the ordinary reset. Each one answers "what does
// the push do when the store cannot, or will not, perform the locked reset?", and the answers
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
}
