package slippy

import (
	"context"
	"strings"
	"testing"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse/clickhousetest"
)

// --- D3 dirty-check write-suppression tests ---
//
// Spec: standup-notes/2026/07/slip-state-ch-fix-spec-and-plan.md §2 D3, BC-13.
//
// These tests exercise updateWithOverrides directly (rather than going through
// Load, which pulls in hydrateSlip/component-state scanning machinery already
// covered by the R2 tests in clickhouse_store_unit_test.go). Each test manually
// sets slip.loadedWriteFingerprint via store.writeFingerprint to simulate "this
// slip was Loaded and this is the fingerprint captured at that time" — the exact
// contract captureWriteFingerprint establishes for real Load/LoadByCommit/
// LoadLiveByCommit callers.

// newSuppressionTestStore builds a store whose derive query (QueryWithArgs against
// slip_component_states) returns no rows by default, so resolveEffectiveStepStatuses
// falls through to in-memory / override values only. derivedRows, when non-nil,
// stages (step, status) rows returned by the derive query instead.
func newSuppressionTestStore(
	t *testing.T,
	execFunc func(ctx context.Context, stmt string, args ...any) error,
	derivedRows []struct{ step, status string },
) (*ClickHouseStore, *clickhousetest.MockSession) {
	t.Helper()

	session := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(_ context.Context, query string, _ ...any) (ch.Rows, error) {
			if !strings.Contains(query, TableSlipComponentStates) || len(derivedRows) == 0 {
				return &clickhousetest.MockRows{}, nil
			}
			localIdx := 0
			return &clickhousetest.MockRows{
				NextFunc: func() bool {
					return localIdx < len(derivedRows)
				},
				ScanFunc: func(dest ...any) error {
					r := derivedRows[localIdx]
					localIdx++
					if p, ok := dest[0].(*string); ok {
						*p = r.step
					}
					if p, ok := dest[1].(*string); ok {
						*p = r.status
					}
					return nil
				},
			}, nil
		},
		ExecWithArgsFunc: execFunc,
	}
	store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
	return store, session
}

// baseSuppressionSlip returns a minimal, self-consistent loaded-style slip using
// testPipelineConfig's steps (push_parsed, builds, unit_tests, dev_deploy) and
// aggregates (builds -> "build", unit_tests -> "unit_test").
func baseSuppressionSlip() *Slip {
	return &Slip{
		CorrelationID: "corr-suppress-1",
		Repository:    "myorg/myrepo",
		Branch:        "main",
		CommitSHA:     "abc123",
		Status:        SlipStatusInProgress,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Version:       1,
		Steps: map[string]Step{
			"push_parsed": {Status: StepStatusCompleted},
			"builds":      {Status: StepStatusRunning},
			"unit_tests":  {Status: StepStatusPending},
			"dev_deploy":  {Status: StepStatusPending},
		},
		Aggregates: map[string][]ComponentStepData{
			"builds": {
				{Component: "api", Status: StepStatusRunning},
			},
		},
	}
}

// TestD3Suppression_ValueEqual_Suppressed verifies that when the resolved row is
// byte-identical to the fingerprint captured at load time, updateWithOverrides
// returns nil without issuing an INSERT and without bumping the version.
func TestD3Suppression_ValueEqual_Suppressed(t *testing.T) {
	execCalls := 0
	store, session := newSuppressionTestStore(t, func(_ context.Context, _ string, _ ...any) error {
		execCalls++
		return nil
	}, nil)

	slip := baseSuppressionSlip()
	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp
	originalVersion := slip.Version

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(session.ExecWithArgsCalls) != 0 {
		t.Errorf("expected 0 ExecWithArgs calls, got %d", len(session.ExecWithArgsCalls))
	}
	if execCalls != 0 {
		t.Errorf("expected 0 exec invocations, got %d", execCalls)
	}
	if slip.Version != originalVersion {
		t.Errorf("expected version to remain %d, got %d", originalVersion, slip.Version)
	}
}

// TestD3Suppression_AnyDelta_Writes verifies that a single differing step status
// defeats suppression and the write proceeds.
func TestD3Suppression_AnyDelta_Writes(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)

	slip := baseSuppressionSlip()
	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp

	// Real delta: dev_deploy flips from pending to running.
	step := slip.Steps["dev_deploy"]
	step.Status = StepStatusRunning
	slip.Steps["dev_deploy"] = step

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call, got %d", len(session.ExecWithArgsCalls))
	}
}

// TestD3Suppression_AggregateJSONDeltaOnly_Writes verifies that a change confined
// entirely to the aggregate JSON column (component data, e.g. the `builds` JSON
// array) — with all step-status columns and slip-level status unchanged — is NOT
// suppressed. Comparing all tracked columns (not just status columns) matters for
// BC-07-style metadata-only writes.
func TestD3Suppression_AggregateJSONDeltaOnly_Writes(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)

	slip := baseSuppressionSlip()
	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp

	// Aggregate-only delta: add a second component to the "builds" aggregate.
	// slip.Steps and slip.Status are untouched.
	slip.Aggregates["builds"] = append(slip.Aggregates["builds"], ComponentStepData{
		Component: "worker",
		Status:    StepStatusRunning,
	})

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call (aggregate JSON delta must not be suppressed), got %d",
			len(session.ExecWithArgsCalls))
	}
}

// TestD3Suppression_SlipLevelStatusDeltaOnly_Writes verifies that a change confined
// to slip.Status (with all step/aggregate/step_details values unchanged) is not
// suppressed.
func TestD3Suppression_SlipLevelStatusDeltaOnly_Writes(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)

	slip := baseSuppressionSlip()
	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp

	slip.Status = SlipStatusFailed

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call (slip-level status delta must not be suppressed), got %d",
			len(session.ExecWithArgsCalls))
	}
}

// TestD3Suppression_StepDetailsDeltaOnly_Writes verifies that a change confined to
// step_details (e.g. a newly-set Error field on an otherwise-unchanged step) is not
// suppressed, even though the step's Status value itself is unchanged.
func TestD3Suppression_StepDetailsDeltaOnly_Writes(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)

	slip := baseSuppressionSlip()
	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp

	// step_details-only delta: same status, but an error message is now set.
	step := slip.Steps["builds"]
	step.Error = "component api: transient network error"
	slip.Steps["builds"] = step

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call (step_details delta must not be suppressed), got %d",
			len(session.ExecWithArgsCalls))
	}
}

// TestD3Suppression_NoFingerprint_AlwaysWrites verifies that a slip which was never
// Loaded (loadedWriteFingerprint == "") always writes, even when nothing would have
// changed — suppression is disabled without a captured baseline (fail-open).
func TestD3Suppression_NoFingerprint_AlwaysWrites(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)

	slip := baseSuppressionSlip()
	// loadedWriteFingerprint intentionally left at its zero value ("").

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call (no fingerprint => always write), got %d",
			len(session.ExecWithArgsCalls))
	}
}

// TestD3Suppression_BC13_MissedFinalTransition_ThenConverges stages the exact
// mutual-invisibility scenario from BC-13: the final completer's rollup observes a
// sibling step as still "running" (async-insert visibility lag). The pipeline-level
// argMax derive for the sibling step also returns "running" (lagged), so the
// resolved values match the loaded row and the write is suppressed (0 inserts).
// The NEXT write, from any component, re-runs the derive; when it now observes
// "completed" for the sibling, resolveEffectiveStepStatuses produces a differing
// value, the write is no longer suppressed, and the INSERT's effective status map
// contains "completed" for that step — demonstrating convergence.
//
// Spec: standup-notes/2026/07/slip-state-ch-fix-spec-and-plan.md §2 D3, BC-13.
func TestD3Suppression_BC13_MissedFinalTransition_ThenConverges(t *testing.T) {
	// "dev_deploy" is a pure pipeline step (component="") in testPipelineConfig, so
	// argMax-derived values apply directly (tier 2) per resolveEffectiveStepStatuses.
	derivedRunning := []struct{ step, status string }{
		{"dev_deploy", string(StepStatusRunning)},
	}
	store, session := newSuppressionTestStore(t, nil, derivedRunning)

	slip := baseSuppressionSlip()
	// Loaded snapshot already shows dev_deploy as "running" (the sibling's lagged view).
	step := slip.Steps["dev_deploy"]
	step.Status = StepStatusRunning
	slip.Steps["dev_deploy"] = step

	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp

	// First write: derive still returns "running" (lagged) — resolved == loaded => suppressed.
	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error on first (suppressed) write, got %v", err)
	}
	if len(session.ExecWithArgsCalls) != 0 {
		t.Fatalf("expected 0 ExecWithArgs calls on the lagged write (BC-13 suppression), got %d",
			len(session.ExecWithArgsCalls))
	}
	if slip.Steps["dev_deploy"].Status != StepStatusRunning {
		t.Fatalf("expected dev_deploy to remain running after suppression, got %v",
			slip.Steps["dev_deploy"].Status)
	}

	// Second write (any component/writer): the fresh argMax derive now sees "completed".
	// Simulate by mutating the mock session's derive rows and re-invoking updateWithOverrides
	// on the SAME in-memory slip (fingerprint was NOT refreshed since the first write was
	// suppressed, so it's still the "running" baseline).
	session.QueryWithArgsFunc = func(_ context.Context, query string, _ ...any) (ch.Rows, error) {
		if !strings.Contains(query, TableSlipComponentStates) {
			return &clickhousetest.MockRows{}, nil
		}
		rows := []struct{ step, status string }{{"dev_deploy", string(StepStatusCompleted)}}
		idx := 0
		return &clickhousetest.MockRows{
			NextFunc: func() bool { return idx < len(rows) },
			ScanFunc: func(dest ...any) error {
				r := rows[idx]
				idx++
				if p, ok := dest[0].(*string); ok {
					*p = r.step
				}
				if p, ok := dest[1].(*string); ok {
					*p = r.status
				}
				return nil
			},
		}, nil
	}
	var capturedArgs []any
	session.ExecWithArgsFunc = func(_ context.Context, _ string, args ...any) error {
		capturedArgs = args
		return nil
	}

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error on convergence write, got %v", err)
	}
	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call on the convergence write, got %d",
			len(session.ExecWithArgsCalls))
	}
	if slip.Steps["dev_deploy"].Status != StepStatusCompleted {
		t.Fatalf("expected dev_deploy to converge to completed, got %v", slip.Steps["dev_deploy"].Status)
	}
	if capturedArgs == nil {
		t.Fatal("expected ExecWithArgs to have been invoked with args")
	}
}

// TestD3Suppression_RepeatedIdenticalUpdate_SecondSuppressed verifies that after a
// successful write, slip.loadedWriteFingerprint is refreshed to the just-written
// row, so an immediately-repeated identical Update() on the same in-memory slip is
// also suppressed (second call: 0 additional inserts).
func TestD3Suppression_RepeatedIdenticalUpdate_SecondSuppressed(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)

	slip := baseSuppressionSlip()
	// No fingerprint captured (simulates a freshly-created slip) so the first write
	// always proceeds regardless of suppression.
	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error on first write, got %v", err)
	}
	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call after first write, got %d", len(session.ExecWithArgsCalls))
	}
	if slip.loadedWriteFingerprint == "" {
		t.Fatal("expected loadedWriteFingerprint to be refreshed after a successful write")
	}

	// Second call: nothing changed since the first write. Fingerprint refresh means
	// this must now be suppressed.
	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error on second (suppressed) write, got %v", err)
	}
	if len(session.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected still 1 ExecWithArgs call after the repeated identical update, got %d",
			len(session.ExecWithArgsCalls))
	}
}

// TestD3Suppression_FingerprintError_FailOpenWrite documents the fail-open
// contract for a fingerprint-computation error: writeFingerprint can only fail via
// pipelineConfig being nil (JSON marshaling of the composed structures here cannot
// fail with the fields in Slip/Step/ComponentStepData). We exercise that exact path
// directly against writeFingerprint/captureWriteFingerprint since staging a JSON
// marshal failure through updateWithOverrides is not reachable with the current
// field types — this test documents and locks in the fail-open contract at the
// unit closest to the failure condition.
func TestD3Suppression_FingerprintError_FailOpenWrite(t *testing.T) {
	store := NewClickHouseStoreFromSession(&clickhousetest.MockSession{}, nil, "ci")

	slip := baseSuppressionSlip()

	if _, err := store.writeFingerprint(slip); err == nil {
		t.Fatal("expected writeFingerprint to error when pipelineConfig is nil")
	}

	// captureWriteFingerprint must not panic and must leave the fingerprint empty
	// (suppression disabled, i.e. fail-open to writing) on error.
	store.captureWriteFingerprint(context.Background(), slip)
	if slip.loadedWriteFingerprint != "" {
		t.Fatalf("expected loadedWriteFingerprint to remain empty on error, got %q",
			slip.loadedWriteFingerprint)
	}
}
