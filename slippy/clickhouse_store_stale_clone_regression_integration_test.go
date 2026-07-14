//go:build integration

package slippy

// clickhouse_store_stale_clone_regression_integration_test.go — deterministic ClickHouse
// (testcontainers) regression tests for the stale-clone step-column derive fix (bd
// mycarrier-5dv5, ADO #83405 follow-up / commit 74cd6676's production wedge).
//
// These tests require a live ClickHouse instance (testcontainers).
// Run with: go test -tags integration -run TestStaleCloneDerive ./slippy/
//
// Unlike the storm/race-style tests in clickhouse_store_i5_v2_integration_test.go, every
// scenario here is constructed DETERMINISTICALLY by directly manipulating DB state (via
// store.Create with a hand-built Slip, and via the unexported insertComponentState /
// insertAtomicStatusUpdate / appendHistoryWithOverrides / updateSlipStatusWithOverrides /
// derivePipelineStepStatuses helpers, all package-internal and callable directly from this
// test file since it lives in package slippy) rather than by racing goroutines. This makes
// the "stale clone source" reproducible on every run: a routing_slips top row is seeded with
// an explicit (now provably stale) step-status value, then a slip_component_states event with
// a different (fresher) value is inserted directly, emulating the ClickHouse async-insert
// cross-connection visibility lag that produced the original wedge.
//
// integrationTestPipelineConfig() (clickhouse_store_integration_test.go) has 4 steps:
//   push_parsed (pure), builds_completed (aggregate: "build"),
//   unit_tests_completed (aggregate: "unit_test"), dev_deploy (pure).
// This file uses "dev_deploy" and "push_parsed" as the two pure (non-aggregate) steps, and
// "builds_completed" as the aggregate step under test.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// --- shared helpers for this file ---

// staleCloneSetupStore starts a ClickHouse testcontainer and returns a configured store
// using the shared integration-test pipeline config and container bootstrap helpers
// (setupClickHouseContainer / createTestStore, clickhouse_store_integration_test.go).
func staleCloneSetupStore(ctx context.Context, t *testing.T) *ClickHouseStore {
	t.Helper()
	container, err := setupClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to start ClickHouse container: %v", err)
	}
	t.Cleanup(func() { _ = container.terminate(context.Background()) })

	store, err := createTestStore(ctx, t, container, integrationTestPipelineConfig())
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return store
}

// staleCloneCorrelationID returns a unique correlation ID for each test run.
func staleCloneCorrelationID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("stale-clone-%d", time.Now().UnixNano())
}

// staleCloneCreateSlip inserts a routing_slips top row with all steps defaulted to
// StepStatusPending except for the overrides supplied in stepStatuses (keyed by step name).
// This lets each test seed an explicit, provably-stale verbatim column value for the CLONE
// SELECT to copy, rather than relying on the Create default (pending) to stand in for
// staleness implicitly.
func staleCloneCreateSlip(
	ctx context.Context, t *testing.T, store *ClickHouseStore, corrID, repo string,
	stepStatuses map[string]StepStatus,
) {
	t.Helper()
	now := time.Now()
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    repo,
		Branch:        "main",
		CommitSHA:     "abc123",
		Status:        SlipStatusInProgress,
		CreatedAt:     now,
		UpdatedAt:     now,
		Steps:         make(map[string]Step),
		Aggregates:    make(map[string][]ComponentStepData),
	}
	for _, step := range integrationTestPipelineConfig().Steps {
		status := StepStatusPending
		if override, ok := stepStatuses[step.Name]; ok {
			status = override
		}
		slip.Steps[step.Name] = Step{Status: status}
	}
	if err := store.Create(ctx, slip); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}
}

// staleCloneReadColumn reads a single column from the current active (sign=1, latest
// version) routing_slips row for corrID. Retries a few times on a zero-row result to
// tolerate the transient container-warmup visibility gap documented on i5V2CountSign1Rows
// (clickhouse_store_i5_v2_integration_test.go).
func staleCloneReadColumn(ctx context.Context, t *testing.T, store *ClickHouseStore, corrID, column string) string {
	t.Helper()
	query := fmt.Sprintf(`
		SELECT %s
		FROM ci_test.routing_slips
		WHERE correlation_id = ? AND sign = 1
		ORDER BY version DESC LIMIT 1
	`, column)
	var val string
	for attempt := 0; attempt < 5; attempt++ {
		rows, err := store.Session().QueryWithArgs(ctx, query, corrID)
		if err != nil {
			t.Fatalf("failed to query column %s: %v", column, err)
		}
		found := false
		if rows.Next() {
			if scanErr := rows.Scan(&val); scanErr != nil {
				rows.Close()
				t.Fatalf("failed to scan column %s: %v", column, scanErr)
			}
			found = true
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			t.Fatalf("row iteration error reading column %s: %v", column, rowsErr)
		}
		rows.Close()
		if found {
			return val
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no active routing_slips row found for %s reading column %s after retries", corrID, column)
	return ""
}

// staleCloneReadCancelledColumn reads a column from the cancelled (sign=-1) routing_slips
// row at the given version for corrID. Used to verify the cancel-branch UNION ALL SELECT
// mirrors the pre-update row verbatim (Test 6, cancel-branch integrity).
func staleCloneReadCancelledColumn(
	ctx context.Context, t *testing.T, store *ClickHouseStore, corrID string, version uint64, column string,
) string {
	t.Helper()
	query := fmt.Sprintf(`
		SELECT %s
		FROM ci_test.routing_slips
		WHERE correlation_id = ? AND sign = -1 AND version = ?
		LIMIT 1
	`, column)
	var val string
	for attempt := 0; attempt < 5; attempt++ {
		rows, err := store.Session().QueryWithArgs(ctx, query, corrID, version)
		if err != nil {
			t.Fatalf("failed to query cancelled column %s: %v", column, err)
		}
		found := false
		if rows.Next() {
			if scanErr := rows.Scan(&val); scanErr != nil {
				rows.Close()
				t.Fatalf("failed to scan cancelled column %s: %v", column, scanErr)
			}
			found = true
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			t.Fatalf("row iteration error reading cancelled column %s: %v", column, rowsErr)
		}
		rows.Close()
		if found {
			return val
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no cancelled (sign=-1) routing_slips row found for %s at version %d reading column %s after retries",
		corrID, version, column)
	return ""
}

// staleCloneInsertComponentEvent inserts a raw slip_component_states event row directly
// (bypassing UpdateStep / the I5 freshness gate entirely), with an explicit timestamp so
// tests can control event ordering/ties deterministically. Mirrors insertComponentState's
// query shape (clickhouse_store.go) but is not itself the function under test — this is
// test-only setup plumbing for constructing DB state.
func staleCloneInsertComponentEvent(
	ctx context.Context, t *testing.T, store *ClickHouseStore,
	corrID, step, component string, status StepStatus, timestamp time.Time,
) {
	t.Helper()
	query := `
		INSERT INTO ci_test.slip_component_states (correlation_id, step, component, status, message, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	if err := store.Session().
		ExecWithArgs(ctx, query, corrID, step, component, string(status), "", timestamp); err != nil {
		t.Fatalf("failed to insert component event (step=%s component=%q status=%s): %v", step, component, status, err)
	}
}

// --- Test 1: REGRESSION 74cd6676 via status path ---

// TestStaleCloneDerive_StatusPath_HealsStaleSiblingStep is the direct regression test for
// commit 74cd6676's production wedge: checkPipelineCompletion calls
// updateSlipStatusWithStepOverrides (-> updateSlipStatusWithOverrides -> insertAtomicStatusUpdate)
// with an override for the step that just failed. Before the CLONE_DERIVED fix, every OTHER
// pure pipeline step's column was cloned verbatim from the current routing_slips row, which
// could be stale relative to slip_component_states under ClickHouse async-insert visibility
// lag — silently reverting an already-completed sibling step back to an older status.
//
// Setup (deterministic, no goroutines):
//  1. Seed a routing_slips top row with dev_deploy_status = "running" (the stale clone source).
//  2. Insert a slip_component_states event for dev_deploy (component=”) = "completed" directly
//     (simulating the event that already landed but whose visibility the earlier clone missed).
//  3. Call updateSlipStatusWithOverrides for SlipStatusFailed with an override ONLY for a
//     DIFFERENT step (push_parsed) — mirroring checkPipelineCompletion's failed-path override
//     construction (buildStepOverridesFromSlip only overrides the primary-failure step(s)).
//
// Assert: new top row status="failed" AND dev_deploy_status="completed" (derived), not the
// stale "running" that a verbatim clone would have produced.
func TestStaleCloneDerive_StatusPath_HealsStaleSiblingStep(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-74cd6676-status",
		map[string]StepStatus{"dev_deploy": StepStatusRunning})

	staleCloneInsertComponentEvent(ctx, t, store, corrID, "dev_deploy", "", StepStatusCompleted, time.Now())

	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("push_parsed"),
		status:     StepStatusFailed,
	}
	if err := store.updateSlipStatusWithOverrides(ctx, corrID, SlipStatusFailed, override); err != nil {
		t.Fatalf("updateSlipStatusWithOverrides failed: %v", err)
	}

	if got := staleCloneReadColumn(ctx, t, store, corrID, "status"); got != string(SlipStatusFailed) {
		t.Errorf("status = %q, want %q", got, SlipStatusFailed)
	}
	if got := staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status"); got != string(StepStatusCompleted) {
		t.Errorf("74cd6676 regression: dev_deploy_status = %q, want %q (derived from slip_component_states, "+
			"not the stale cloned %q)", got, StepStatusCompleted, StepStatusRunning)
	}
	// Sanity: the override itself landed.
	if got := staleCloneReadColumn(ctx, t, store, corrID, "push_parsed_status"); got != string(StepStatusFailed) {
		t.Errorf("push_parsed_status (override) = %q, want %q", got, StepStatusFailed)
	}
}

// --- Test 2: same interleaving via history path ---

// TestStaleCloneDerive_HistoryPath_HealsStaleSiblingStep is the same staleness interleaving
// as Test 1, but exercised via appendHistoryWithOverrides (the insertAtomicHistoryUpdate
// CLONE_DERIVED writer used by UpdateStepWithHistory's pure-pipeline-step branch and by plain
// AppendHistory) instead of the status path.
func TestStaleCloneDerive_HistoryPath_HealsStaleSiblingStep(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-74cd6676-history",
		map[string]StepStatus{"dev_deploy": StepStatusRunning})

	staleCloneInsertComponentEvent(ctx, t, store, corrID, "dev_deploy", "", StepStatusCompleted, time.Now())

	entry := StateHistoryEntry{
		Step:      "push_parsed",
		Status:    StepStatusFailed,
		Timestamp: time.Now(),
		Actor:     "test",
	}
	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("push_parsed"),
		status:     StepStatusFailed,
	}
	if err := store.appendHistoryWithOverrides(ctx, corrID, entry, override); err != nil {
		t.Fatalf("appendHistoryWithOverrides failed: %v", err)
	}

	if got := staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status"); got != string(StepStatusCompleted) {
		t.Errorf("history path: dev_deploy_status = %q, want %q (derived, not stale %q)",
			got, StepStatusCompleted, StepStatusRunning)
	}
	if got := staleCloneReadColumn(ctx, t, store, corrID, "push_parsed_status"); got != string(StepStatusFailed) {
		t.Errorf("push_parsed_status (override) = %q, want %q", got, StepStatusFailed)
	}
}

// --- Test 3: override precedence over derive ---

// TestStaleCloneDerive_OverridePrecedenceOverDerive verifies tier-1 precedence end-to-end
// against a live database: a caller-supplied stepStatusOverride for a step wins even when
// slip_component_states has a DIFFERENT (and more "recent" by insertion order) status for
// that same step.
func TestStaleCloneDerive_OverridePrecedenceOverDerive(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-override-precedence",
		map[string]StepStatus{"dev_deploy": StepStatusRunning})

	// Events say dev_deploy is completed...
	staleCloneInsertComponentEvent(ctx, t, store, corrID, "dev_deploy", "", StepStatusCompleted, time.Now())

	// ...but the override for the SAME step says failed. Override (tier 1) must win.
	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("dev_deploy"),
		status:     StepStatusFailed,
	}
	if err := store.updateSlipStatusWithOverrides(ctx, corrID, SlipStatusFailed, override); err != nil {
		t.Fatalf("updateSlipStatusWithOverrides failed: %v", err)
	}

	if got := staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status"); got != string(StepStatusFailed) {
		t.Errorf("override precedence: dev_deploy_status = %q, want %q (tier-1 override must win over "+
			"tier-2 derived %q)", got, StepStatusFailed, StepStatusCompleted)
	}
}

// --- Test 4: no-events fallback ---

// TestStaleCloneDerive_NoEvents_FallsBackToVerbatimClone verifies tier-3 fallback: a pure
// pipeline step column with ZERO slip_component_states rows keeps its verbatim cloned value.
// Production does NOT have a "HAVING st != ”" clause on the derive CTE (a prior version did,
// but it was deliberately removed — comparing the Enum8 status column against the string
// literal ” is invalid for that enum type and ClickHouse rejects it at query-analysis time
// with error 691, "Unknown element ” for enum"). Instead, with zero matching rows the CTE's
// GROUP BY simply produces no group for that step, so the tier-2 scalar subquery
// (SELECT st FROM derived WHERE step = '<name>') returns an empty result set, ClickHouse
// evaluates that as NULL, and coalesce(...) falls through to the tier-3 verbatim column
// reference — rather than being reset to pending or emptied out.
func TestStaleCloneDerive_NoEvents_FallsBackToVerbatimClone(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	// push_parsed seeded to "running" verbatim; NO slip_component_states event is ever
	// written for push_parsed in this test.
	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-no-events-fallback",
		map[string]StepStatus{"push_parsed": StepStatusRunning})

	// Trigger CLONE_DERIVED via a DIFFERENT step's override (dev_deploy).
	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("dev_deploy"),
		status:     StepStatusCompleted,
	}
	if err := store.updateSlipStatusWithOverrides(ctx, corrID, SlipStatusInProgress, override); err != nil {
		t.Fatalf("updateSlipStatusWithOverrides failed: %v", err)
	}

	if got := staleCloneReadColumn(ctx, t, store, corrID, "push_parsed_status"); got != string(StepStatusRunning) {
		t.Errorf("no-events fallback: push_parsed_status = %q, want verbatim clone %q (not pending, not empty)",
			got, StepStatusRunning)
	}
}

// --- Test 5: aggregate column untouched ---

// TestStaleCloneDerive_AggregateColumn_AlwaysVerbatim verifies that an aggregate step's
// status column is ALWAYS copied verbatim by CLONE_DERIVED, even when slip_component_states
// has a pipeline-level (component=”) event for that same step name that would otherwise
// satisfy the derive CTE's WHERE/GROUP BY. buildCloneStepColumnDerive must classify aggregate
// columns via cfg.IsAggregateStep and skip the coalesce(...) rewrite entirely for them — the
// R2 path (resolveEffectiveStepStatuses/updateWithOverrides), not CLONE_DERIVED, owns
// aggregate rollups.
func TestStaleCloneDerive_AggregateColumn_AlwaysVerbatim(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	// builds_completed (aggregate) seeded to "running" verbatim.
	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-aggregate-verbatim",
		map[string]StepStatus{"builds_completed": StepStatusRunning})

	// A stray pipeline-level (component='') event for the aggregate step itself, with a
	// DIFFERENT status — if CLONE_DERIVED mistakenly treated this like a pure step, the
	// coalesce(...) CTE lookup would resolve to "failed" instead of leaving the column alone.
	staleCloneInsertComponentEvent(ctx, t, store, corrID, "builds_completed", "", StepStatusFailed, time.Now())

	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("dev_deploy"),
		status:     StepStatusCompleted,
	}
	if err := store.updateSlipStatusWithOverrides(ctx, corrID, SlipStatusInProgress, override); err != nil {
		t.Fatalf("updateSlipStatusWithOverrides failed: %v", err)
	}

	if got := staleCloneReadColumn(ctx, t, store, corrID, "builds_completed_status"); got != string(StepStatusRunning) {
		t.Errorf("aggregate column verbatim: builds_completed_status = %q, want verbatim clone %q "+
			"(must NOT derive %q from the stray component='' event)",
			got, StepStatusRunning, StepStatusFailed)
	}
}

// --- Test 6: cancel-branch integrity ---

// TestStaleCloneDerive_CancelBranch_MirrorsPreUpdateRowVerbatim verifies that the cancelled
// (sign=-1) row produced by the same UNION ALL statement mirrors the pre-update row exactly,
// including the step column that is stale relative to slip_component_states. The cancel
// branch must NEVER apply a derive expression — cancel rows exist purely to flip the sign of
// the row being superseded, not to "improve" its data.
func TestStaleCloneDerive_CancelBranch_MirrorsPreUpdateRowVerbatim(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-cancel-branch",
		map[string]StepStatus{"dev_deploy": StepStatusRunning})

	// Capture the pre-update row's version and dev_deploy_status BEFORE triggering the update.
	preVersionU64, err := store.loadVersionFromDB(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load pre-update version: %v", err)
	}
	preDevDeployStatus := staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status")
	preStatus := staleCloneReadColumn(ctx, t, store, corrID, "status")

	// Make dev_deploy stale relative to the event log (event says completed).
	staleCloneInsertComponentEvent(ctx, t, store, corrID, "dev_deploy", "", StepStatusCompleted, time.Now())

	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("push_parsed"),
		status:     StepStatusFailed,
	}
	if err := store.updateSlipStatusWithOverrides(ctx, corrID, SlipStatusFailed, override); err != nil {
		t.Fatalf("updateSlipStatusWithOverrides failed: %v", err)
	}

	// New active row must have healed dev_deploy_status (sanity, same as Test 1).
	if got := staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status"); got != string(StepStatusCompleted) {
		t.Fatalf("sanity check failed: new row dev_deploy_status = %q, want %q", got, StepStatusCompleted)
	}

	// The CANCELLED row (sign=-1, version=preVersion) must mirror the pre-update row exactly:
	// dev_deploy_status must STILL be the stale "running" value, and status must still be the
	// pre-update status — verbatim, unhealed, uninfluenced by the derive logic.
	if got := staleCloneReadCancelledColumn(
		ctx,
		t,
		store,
		corrID,
		preVersionU64,
		"dev_deploy_status",
	); got != preDevDeployStatus {
		t.Errorf("cancel branch: cancelled row dev_deploy_status = %q, want verbatim pre-update value %q "+
			"(cancel branch must never apply the derive expression)", got, preDevDeployStatus)
	}
	if got := staleCloneReadCancelledColumn(ctx, t, store, corrID, preVersionU64, "status"); got != preStatus {
		t.Errorf("cancel branch: cancelled row status = %q, want verbatim pre-update value %q", got, preStatus)
	}
}

// --- Test 7: PARITY CHECK — SQL-side derive vs. client-side derivePipelineStepStatuses ---

// TestStaleCloneDerive_ParityWithDerivePipelineStepStatuses is a same-timestamp tie-break
// parity check between the two independent argMax consumers of slip_component_states:
//
//  1. buildCloneStepColumnDerive's server-side CTE (clickhouse_store.go ~1560-1567):
//     "WITH derived AS (SELECT step, argMax(status, <key>) AS st FROM slip_component_states
//     WHERE correlation_id = ? AND component = ” GROUP BY step)" — no HAVING clause; a
//     prior version had "HAVING st != ”" but it was deliberately removed (invalid against
//     the Enum8 status column, ClickHouse error 691, "Unknown element ” for enum"). The
//     no-matching-row case is instead handled by the caller's coalesce(...): an empty CTE
//     scalar subquery result evaluates to NULL, which coalesce(...) falls back from.
//  2. derivePipelineStepStatuses (clickhouse_store.go ~1054-1088), the client-side Go
//     method used by the R2 write-back path (resolveEffectiveStepStatuses):
//     "SELECT step, argMax(status, <key>) AS latest_status FROM slip_component_states
//     WHERE correlation_id = ? AND component = ” GROUP BY step"
//
// PARITY VERDICT (read directly from clickhouse_store.go, not inferred): both sides use the
// EXACT SAME Go constant for the argMax sort-key expression —
//
//	componentEventSortKeyNoImageTag = "toUInt64(toUnixTimestamp64Micro(timestamp)) * 100 +
//	                                    toUInt64(toUInt8(status))"
//
// (clickhouse_store.go:64, referenced by both buildCloneStepColumnDerive at line 1566 and
// derivePipelineStepStatuses at line 1061). There is only one sort-key expression in this
// codebase, not two independently-authored ones — so no divergence is possible by
// construction. This test proves that empirically: it seeds two slip_component_states events
// for the SAME step at the SAME timestamp (status ordinals running=3 vs completed=4, so the
// tiebreak/higher-ordinal event, "completed", must win on both sides) and asserts the
// SQL-derived value (read back from the routing_slips column after a CLONE_DERIVED write)
// equals the client-side derivePipelineStepStatuses result.
//
// FINDING: no divergence. Both consumers share the identical sort-key constant; this is NOT
// the "two independently-tuned tiebreak formulas that happen to disagree" scenario the task
// was probing for — it is a single shared constant used in two query sites. No production
// code change is warranted for this check.
func TestStaleCloneDerive_ParityWithDerivePipelineStepStatuses(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-parity-check", nil)

	// Two events for the SAME step (dev_deploy, pure) at the SAME timestamp, differing only
	// in status ordinal: running(3) vs completed(4). The sort key componentEventSortKeyNoImageTag
	// = ts*100 + statusOrdinal, so completed (ordinal 4) must win the tiebreak on both sides.
	tie := time.Now()
	staleCloneInsertComponentEvent(ctx, t, store, corrID, "dev_deploy", "", StepStatusRunning, tie)
	staleCloneInsertComponentEvent(ctx, t, store, corrID, "dev_deploy", "", StepStatusCompleted, tie)

	// Client-side derive.
	clientDerived, err := store.derivePipelineStepStatuses(ctx, corrID)
	if err != nil {
		t.Fatalf("derivePipelineStepStatuses failed: %v", err)
	}
	clientResult, ok := clientDerived["dev_deploy"]
	if !ok {
		t.Fatalf("derivePipelineStepStatuses: no entry for dev_deploy, got map: %v", clientDerived)
	}

	// SQL-side derive: trigger CLONE_DERIVED for dev_deploy (unoverridden pure step) via a
	// different step's override, then read the resulting column back.
	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("push_parsed"),
		status:     StepStatusFailed,
	}
	if err := store.updateSlipStatusWithOverrides(ctx, corrID, SlipStatusFailed, override); err != nil {
		t.Fatalf("updateSlipStatusWithOverrides failed: %v", err)
	}
	sqlResult := StepStatus(staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status"))

	t.Logf("parity check: client-side derivePipelineStepStatuses[dev_deploy] = %q; "+
		"SQL-side CLONE_DERIVED dev_deploy_status = %q", clientResult, sqlResult)

	if clientResult != StepStatusCompleted {
		t.Errorf("client-side derive: dev_deploy = %q, want %q (same-timestamp tiebreak, higher status ordinal)",
			clientResult, StepStatusCompleted)
	}
	if sqlResult != StepStatusCompleted {
		t.Errorf("SQL-side derive: dev_deploy_status = %q, want %q (same-timestamp tiebreak, higher status ordinal)",
			sqlResult, StepStatusCompleted)
	}
	if clientResult != sqlResult {
		t.Errorf("PARITY DIVERGENCE: client-side derivePipelineStepStatuses[dev_deploy]=%q != "+
			"SQL-side CLONE_DERIVED dev_deploy_status=%q — see this test's doc comment for the shared "+
			"componentEventSortKeyNoImageTag constant both sides are expected to use", clientResult, sqlResult)
	}
}

// --- Test 8: terminal-seal end-to-end ---

// TestStaleCloneDerive_TerminalSeal_EndToEndThenStable documents the full
// checkPipelineCompletion-shaped flow: a component/pipeline event for step B lands, then the
// slip is sealed to a terminal status with an override ONLY for step A (the step that
// triggered the failure) — mirroring checkPipelineCompletion's buildStepOverridesFromSlip,
// which only overrides the primary-failure step(s), never every step. After the seal, no
// further writes occur; the row must remain stable (self-heal already happened at seal time,
// nothing left to heal).
func TestStaleCloneDerive_TerminalSeal_EndToEndThenStable(t *testing.T) {
	ctx := context.Background()
	store := staleCloneSetupStore(ctx, t)
	corrID := staleCloneCorrelationID(t)

	// Step B (dev_deploy) is stale in the row (seeded running) relative to the event log.
	staleCloneCreateSlip(ctx, t, store, corrID, "myorg/repo-terminal-seal",
		map[string]StepStatus{"dev_deploy": StepStatusRunning})

	// Event lands for step B: completed.
	staleCloneInsertComponentEvent(ctx, t, store, corrID, "dev_deploy", "", StepStatusCompleted, time.Now())

	// Seal to terminal "failed" with an override ONLY for step A (push_parsed) — the
	// checkPipelineCompletion shape: buildStepOverridesFromSlip only overrides the
	// primary-failure step, not every step in the pipeline.
	override := stepStatusOverride{
		columnName: store.queryBuilder.StepStatusColumn("push_parsed"),
		status:     StepStatusFailed,
	}
	if err := store.updateSlipStatusWithOverrides(ctx, corrID, SlipStatusFailed, override); err != nil {
		t.Fatalf("updateSlipStatusWithOverrides (terminal seal) failed: %v", err)
	}

	// Top row must be terminal (failed) and step B must be correctly healed.
	if got := staleCloneReadColumn(ctx, t, store, corrID, "status"); got != string(SlipStatusFailed) {
		t.Fatalf("terminal seal: status = %q, want %q", got, SlipStatusFailed)
	}
	if got := staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status"); got != string(StepStatusCompleted) {
		t.Fatalf("terminal seal: dev_deploy_status = %q, want %q (healed at seal time)", got, StepStatusCompleted)
	}
	sealedVersion, err := store.loadVersionFromDB(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load sealed version: %v", err)
	}

	// No further writes. Re-read: everything must be stable/unchanged (documents "no heal
	// needed" — there is nothing left in the event log newer than what was already applied
	// at seal time, and no additional write occurs to disturb the row).
	if got := staleCloneReadColumn(ctx, t, store, corrID, "status"); got != string(SlipStatusFailed) {
		t.Errorf("stability check: status = %q, want unchanged %q", got, SlipStatusFailed)
	}
	if got := staleCloneReadColumn(ctx, t, store, corrID, "dev_deploy_status"); got != string(StepStatusCompleted) {
		t.Errorf("stability check: dev_deploy_status = %q, want unchanged %q", got, StepStatusCompleted)
	}
	stableVersion, err := store.loadVersionFromDB(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load stable-check version: %v", err)
	}
	if stableVersion != sealedVersion {
		t.Errorf("stability check: version changed from %d to %d with no further writes issued",
			sealedVersion, stableVersion)
	}
}
