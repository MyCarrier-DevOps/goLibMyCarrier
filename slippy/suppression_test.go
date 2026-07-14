package slippy

import (
	"context"
	"strings"
	"testing"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse/clickhousetest"
	golclogger "github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// capturedLogCall records a single logger invocation for assertions in the
// D3-suppression state_history_lost tests below.
type capturedLogCall struct {
	level   string
	message string
	fields  map[string]interface{}
}

// capturingLogger is a minimal Logger implementation that records every call
// instead of discarding it, so tests can assert on the exact message/fields
// pair emitted by the suppression branch in updateWithOverrides.
type capturingLogger struct {
	calls []capturedLogCall
}

func (l *capturingLogger) Info(_ context.Context, message string, fields map[string]interface{}) {
	l.calls = append(l.calls, capturedLogCall{level: "info", message: message, fields: fields})
}

func (l *capturingLogger) Debug(_ context.Context, message string, fields map[string]interface{}) {
	l.calls = append(l.calls, capturedLogCall{level: "debug", message: message, fields: fields})
}

func (l *capturingLogger) Warn(_ context.Context, message string, fields map[string]interface{}) {
	l.calls = append(l.calls, capturedLogCall{level: "warn", message: message, fields: fields})
}

func (l *capturingLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {
	l.Warn(ctx, message, fields)
}

func (l *capturingLogger) Error(
	_ context.Context, message string, err error, fields map[string]interface{},
) {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	l.calls = append(l.calls, capturedLogCall{level: "error", message: message, fields: fields})
}

func (l *capturingLogger) WithFields(map[string]interface{}) golclogger.Logger {
	return l
}

var _ golclogger.Logger = (*capturingLogger)(nil)

// callsWithField returns the subset of captured calls whose fields map has
// the given key set to true.
func (l *capturingLogger) callsWithField(key string) []capturedLogCall {
	var out []capturedLogCall
	for _, c := range l.calls {
		if v, ok := c.fields[key]; ok {
			if b, ok := v.(bool); ok && b {
				out = append(out, c)
			}
		}
	}
	return out
}

// callsWithLevel returns the subset of captured calls at the given log level
// (e.g. "warn", "info", "error").
func (l *capturingLogger) callsWithLevel(level string) []capturedLogCall {
	var out []capturedLogCall
	for _, c := range l.calls {
		if c.level == level {
			out = append(out, c)
		}
	}
	return out
}

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

// TestD3Suppression_BaselineIsPersistedRow_NotHydratedView is the regression test
// for the collapse-suppression bug documented in
// TestI5V2_Suppression_CollapseIdenticalWritebacks
// (clickhouse_store_i5_v2_integration_test.go): the D3 baseline must be the
// fingerprint of the slip as scanned from the persisted routing_slips row, captured
// BEFORE hydrateSlip/applyComponentStatesToAggregate mutates slip.Steps/Aggregates —
// never a fingerprint of the already-hydrated/derived view.
//
// This stages the exact scenario: the persisted row's "builds" aggregate step is
// still "running" (scan result, pre-hydration), but slip_component_states has since
// advanced to "completed" for the sole active component — the value hydrateSlip
// would derive. If captureWriteFingerprint were (incorrectly) called on the
// post-hydration slip, the baseline would already read "completed", R2's derive at
// write time would reproduce "completed", and the INSERT would be wrongly suppressed
// forever (the exact bug). With the fix, the baseline is fingerprinted from the
// pre-hydration (persisted-row) state, so it still reads "running"; R2's derive at
// write time resolves "completed", the fingerprints differ, and the write proceeds.
//
// Scope note: this test locks in the SEMANTIC contract (baseline must reflect the
// pre-hydration/persisted-row state, not the post-hydration/derived view) at the
// writeFingerprint/updateWithOverrides unit level, using applyComponentStatesToAggregate
// directly to model hydrateSlip's mutation without the JSON/chcol scan-mock scaffolding
// a full Load()-level test would require. It does NOT exercise the actual Load/
// LoadByCommit/LoadLiveByCommit call-site ordering (scanSlip -> captureWriteFingerprint
// -> hydrateSlip) — that wiring is guarded end-to-end, deterministically, by
// TestI5V2_Suppression_CollapseIdenticalWritebacks (integration test, real ClickHouse).
func TestD3Suppression_BaselineIsPersistedRow_NotHydratedView(t *testing.T) {
	derivedCompleted := []struct{ step, status string }{
		{"api", string(StepStatusCompleted)},
	}

	t.Run("baseline captured pre-hydration => fingerprint mismatch => write proceeds", func(t *testing.T) {
		// R2's derive-at-write-time resolves "completed" for the sole active component,
		// modeling slip_component_states having already advanced.
		store, session := newSuppressionTestStore(t, nil, derivedCompleted)

		// Slip exactly as scanned from routing_slips: "builds" aggregate step still
		// "running", matching what is actually persisted (pre-hydration state). This
		// mirrors the corrected Load ordering: captureWriteFingerprint runs immediately
		// after scanSlip, before hydrateSlip has a chance to mutate anything.
		slip := baseSuppressionSlip()
		fp, err := store.writeFingerprint(slip)
		if err != nil {
			t.Fatalf("writeFingerprint: %v", err)
		}
		slip.loadedWriteFingerprint = fp

		// Now simulate hydrateSlip's mutation (applyComponentStatesToAggregate), which
		// happens AFTER the baseline above was captured — exactly matching the corrected
		// Load call ordering being tested here.
		store.applyComponentStatesToAggregate(slip, "builds", "builds", map[string]componentStateRow{
			"api": {Step: "builds", Component: "api", Status: string(StepStatusCompleted), Timestamp: time.Now()},
		})
		if slip.Steps["builds"].Status != StepStatusCompleted {
			t.Fatalf("expected hydration to advance builds to completed, got %v", slip.Steps["builds"].Status)
		}

		// updateWithOverrides' R2 derive re-resolves "completed" from the same source;
		// since the baseline was captured pre-hydration ("running"), the fingerprints
		// must differ and the write must proceed.
		if err := store.updateWithOverrides(context.Background(), slip); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if len(session.ExecWithArgsCalls) != 1 {
			t.Fatalf("expected 1 ExecWithArgs call (baseline=persisted-row must NOT match the "+
				"resolved/derived value), got %d", len(session.ExecWithArgsCalls))
		}
	})

	t.Run("persisted == derived == in-memory => suppressed", func(t *testing.T) {
		// Inverse case: the persisted row already reflects "completed" (no lag), and R2's
		// derive also resolves "completed" — baseline and resolved values match, so the
		// write is correctly suppressed (BC-13 convergence semantics preserved).
		store, session := newSuppressionTestStore(t, nil, derivedCompleted)

		slip := baseSuppressionSlip()
		step := slip.Steps["builds"]
		step.Status = StepStatusCompleted
		slip.Steps["builds"] = step
		slip.Aggregates["builds"] = []ComponentStepData{
			{Component: "api", Status: StepStatusCompleted},
		}

		fp, err := store.writeFingerprint(slip)
		if err != nil {
			t.Fatalf("writeFingerprint: %v", err)
		}
		slip.loadedWriteFingerprint = fp

		if err := store.updateWithOverrides(context.Background(), slip); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if len(session.ExecWithArgsCalls) != 0 {
			t.Fatalf("expected 0 ExecWithArgs calls (persisted == derived == in-memory must "+
				"suppress), got %d", len(session.ExecWithArgsCalls))
		}
	})
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

// --- D3 suppression / state_history_lost Warn escalation tests ---
//
// When a D3 suppression fires (fingerprints match) AND slip.StateHistory grew
// since the pre-hydration baseline was captured (loadedStateHistoryLen), the
// appended journal entry is silently dropped by the suppressed write. These
// tests assert the Warn-vs-Debug branch added to updateWithOverrides' D3
// suppression path for that case, using a capturingLogger swapped onto the
// store (store.logger is unexported/same-package, following the same
// call-updateWithOverrides-directly pattern as the rest of this file).

// TestD3Suppression_HistoryGrownSinceLoad_WarnsStateHistoryLost verifies that
// when suppression fires and slip.StateHistory has grown past the baseline
// captured at (simulated) Load time, updateWithOverrides logs at Warn with the
// state_history_lost structured key instead of the routine Debug line.
func TestD3Suppression_HistoryGrownSinceLoad_WarnsStateHistoryLost(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)
	capLog := &capturingLogger{}
	store.logger = capLog

	slip := baseSuppressionSlip()
	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp
	// Baseline captured pre-hydration at Load time, matching captureWriteFingerprint's
	// contract: loadedStateHistoryLen == len(StateHistory) at that same point.
	slip.loadedStateHistoryLen = len(slip.StateHistory)

	// Simulate updateAggregateStatusFromComponentStatesWithHistory appending a
	// journal entry to the same in-memory slip before calling updateWithOverrides
	// (clickhouse_store.go ~:2860), with no other tracked field changing — so the
	// fingerprint still matches the baseline and D3 suppression fires.
	slip.StateHistory = append(slip.StateHistory, StateHistoryEntry{
		Step:      "builds",
		Component: "api",
		Status:    StepStatusRunning,
		Timestamp: time.Now(),
		Actor:     "ci-bot",
	})

	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Suppression must still hold: no INSERT issued.
	if len(session.ExecWithArgsCalls) != 0 {
		t.Fatalf("expected 0 ExecWithArgs calls (suppression must still fire), got %d",
			len(session.ExecWithArgsCalls))
	}

	warnCalls := capLog.callsWithField("state_history_lost")
	if len(warnCalls) != 1 {
		t.Fatalf("expected exactly 1 log call with state_history_lost=true, got %d (all calls: %+v)",
			len(warnCalls), capLog.calls)
	}
	if warnCalls[0].level != "warn" {
		t.Errorf("expected state_history_lost log at Warn level, got %q", warnCalls[0].level)
	}
	if cid, _ := warnCalls[0].fields["correlation_id"].(string); cid != slip.CorrelationID {
		t.Errorf("expected correlation_id %q in Warn fields, got %q", slip.CorrelationID, cid)
	}
	if reason, _ := warnCalls[0].fields["reason"].(string); reason != "d3_suppression" {
		t.Errorf("expected reason %q in Warn fields, got %q", "d3_suppression", reason)
	}

	// The routine Debug suppression line must NOT have also fired for this case.
	for _, c := range capLog.calls {
		if c.level == "debug" && c.message == "write suppressed: no change since load" {
			t.Errorf("did not expect the routine Debug suppression line when history grew, got: %+v", c)
		}
	}
}

// TestD3Suppression_NoHistoryGrowth_NoStateHistoryLostWarn verifies that when
// suppression fires and StateHistory did NOT grow since the baseline, the
// routine Debug line is used and no state_history_lost Warn is emitted.
func TestD3Suppression_NoHistoryGrowth_NoStateHistoryLostWarn(t *testing.T) {
	store, session := newSuppressionTestStore(t, nil, nil)
	capLog := &capturingLogger{}
	store.logger = capLog

	slip := baseSuppressionSlip()
	fp, err := store.writeFingerprint(slip)
	if err != nil {
		t.Fatalf("writeFingerprint: %v", err)
	}
	slip.loadedWriteFingerprint = fp
	slip.loadedStateHistoryLen = len(slip.StateHistory)

	// No StateHistory mutation and no other tracked-field delta: suppression fires
	// with loadedStateHistoryLen == len(StateHistory).
	if err := store.updateWithOverrides(context.Background(), slip); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(session.ExecWithArgsCalls) != 0 {
		t.Fatalf("expected 0 ExecWithArgs calls (suppression must fire), got %d",
			len(session.ExecWithArgsCalls))
	}

	if warnCalls := capLog.callsWithField("state_history_lost"); len(warnCalls) != 0 {
		t.Fatalf("expected 0 state_history_lost warn calls when history did not grow, got %d (%+v)",
			len(warnCalls), warnCalls)
	}

	var sawDebug bool
	for _, c := range capLog.calls {
		if c.level == "debug" && c.message == "write suppressed: no change since load" {
			sawDebug = true
		}
	}
	if !sawDebug {
		t.Errorf("expected the routine Debug suppression line to fire, calls: %+v", capLog.calls)
	}
}
