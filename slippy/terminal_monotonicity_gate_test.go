package slippy

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse/clickhousetest"
)

// mockGateRow constructs a MockRow whose Scan populates a single string status.
// Used to seed the prior status returned by the gate's argMax point-lookup
// against slip_component_states.
func mockGateRow(priorStatus string) *clickhousetest.MockRow {
	return &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			if len(dest) < 1 {
				return fmt.Errorf("mockGateRow: expected 1 scan dest, got %d", len(dest))
			}
			if ptr, ok := dest[0].(*string); ok {
				*ptr = priorStatus
			}
			return nil
		},
	}
}

// gateMockSession returns a MockSession whose QueryRow returns a row reporting
// priorStatus as the argMax(status) result for the gate's pre-flight query.
// Setting priorStatus to "" simulates "no prior row" (argMax returns empty).
func gateMockSession(priorStatus string) *clickhousetest.MockSession {
	return &clickhousetest.MockSession{
		QueryRowRow: mockGateRow(priorStatus),
	}
}

// -----------------------------------------------------------------------------
// gateEnabled — env-flag rollback contract (Plan v3 §G.1)
// -----------------------------------------------------------------------------

// TestGateEnabled_DefaultsOn verifies the fail-safe default: when the env var
// is unset, the gate is ON. This is the production-default rollback contract.
func TestGateEnabled_DefaultsOn(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "")
	// t.Setenv on empty string sets the var to ""; gateEnabled treats empty as default-ON.
	if !gateEnabled() {
		t.Fatal("gateEnabled() must default ON when env is empty")
	}
}

// TestGateEnabled_FalseEnvDisables verifies the explicit kill-switch path.
func TestGateEnabled_FalseEnvDisables(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "false")
	if gateEnabled() {
		t.Fatal("gateEnabled() must return false when SLIPPY_I5_GATE_ENABLED=false")
	}
}

// TestGateEnabled_TrueEnvEnables verifies explicit-on round-trips correctly.
func TestGateEnabled_TrueEnvEnables(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "true")
	if !gateEnabled() {
		t.Fatal("gateEnabled() must return true when SLIPPY_I5_GATE_ENABLED=true")
	}
}

// TestGateEnabled_UnparseableFailsSafe verifies an unparseable value is
// treated as ON (fail-safe). A typo or accidental garbage value must NOT
// silently disable the safety gate.
func TestGateEnabled_UnparseableFailsSafe(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "garbage")
	if !gateEnabled() {
		t.Fatal("gateEnabled() must fail-safe ON for unparseable env value")
	}
}

// TestEnforceTerminalMonotonicity_GateDisabled_ReturnsNil proves the
// short-circuit path: when the env-flag disables the gate, enforce returns
// nil immediately and NEVER issues a CH pre-flight query — even if the prior
// state would otherwise trip the gate.
func TestEnforceTerminalMonotonicity_GateDisabled_ReturnsNil(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "false")

	session := &clickhousetest.MockSession{
		// If the short-circuit fails, the gate would issue a QueryRow and we'd
		// fail the test loudly. Configure to t.Fatal on any query.
		QueryRowFunc: func(_ context.Context, _ string, _ ...any) driver.Row {
			t.Fatal("gate must NOT issue QueryRow when SLIPPY_I5_GATE_ENABLED=false")
			return mockGateRow("completed")
		},
	}
	store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")

	// Even with a hypothetical prior=completed (a refuse cell in normal mode),
	// the disabled gate must allow the write.
	err := store.enforceTerminalMonotonicity(
		context.Background(), "corr-gate-disabled", "dev_deploy", "", StepStatusRunning,
	)
	if err != nil {
		t.Fatalf("disabled gate must return nil; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// isRecoveryAllowed — predicate unit tests
// -----------------------------------------------------------------------------

func TestIsRecoveryAllowed_AbortedToPending_True(t *testing.T) {
	if !isRecoveryAllowed(StepStatusAborted, StepStatusPending) {
		t.Fatal("aborted → pending must be allowed (cascade-reset rule 1)")
	}
}

func TestIsRecoveryAllowed_RecoverableToCompleted_True(t *testing.T) {
	cases := []StepStatus{
		StepStatusFailed, StepStatusAborted, StepStatusError,
		StepStatusTimeout, StepStatusSkipped,
	}
	for _, prior := range cases {
		t.Run(string(prior), func(t *testing.T) {
			if !isRecoveryAllowed(prior, StepStatusCompleted) {
				t.Errorf("%s → completed must be allowed (recovery rule 2)", prior)
			}
		})
	}
}

func TestIsRecoveryAllowed_CompletedAsPrior_AnythingRefused(t *testing.T) {
	for _, incoming := range []StepStatus{
		StepStatusPending, StepStatusHeld, StepStatusRunning,
		StepStatusCompleted, StepStatusFailed, StepStatusAborted,
		StepStatusError, StepStatusTimeout, StepStatusSkipped,
	} {
		t.Run(string(incoming), func(t *testing.T) {
			if isRecoveryAllowed(StepStatusCompleted, incoming) {
				t.Errorf("completed → %s must NOT be allowed (completed is final)", incoming)
			}
		})
	}
}

func TestIsRecoveryAllowed_AbortedToNonPendingNonCompleted_Refused(t *testing.T) {
	// held, running, failed, error, aborted, timeout, skipped all refused.
	cases := []StepStatus{
		StepStatusHeld, StepStatusRunning, StepStatusFailed,
		StepStatusError, StepStatusAborted, StepStatusTimeout, StepStatusSkipped,
	}
	for _, incoming := range cases {
		t.Run(string(incoming), func(t *testing.T) {
			if isRecoveryAllowed(StepStatusAborted, incoming) {
				t.Errorf("aborted → %s must NOT be allowed (only pending or completed)", incoming)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// enforceTerminalMonotonicity — gate matrix tests
// -----------------------------------------------------------------------------

// TestEnforceTerminalMonotonicity_NoPriorAllow covers the "empty event log"
// row of the §D matrix — no prior row exists for the tuple.
func TestEnforceTerminalMonotonicity_NoPriorAllow(t *testing.T) {
	session := gateMockSession("") // argMax returns "" → no prior signal
	store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")

	for _, incoming := range []StepStatus{
		StepStatusRunning, StepStatusCompleted, StepStatusFailed, StepStatusPending,
	} {
		t.Run(string(incoming), func(t *testing.T) {
			err := store.enforceTerminalMonotonicity(
				context.Background(), "corr-no-prior", "dev_deploy", "", incoming,
			)
			if err != nil {
				t.Errorf("expected nil for no-prior gate; got %v", err)
			}
		})
	}
}

// TestEnforceTerminalMonotonicity_PriorNonTerminal_AllowAnything covers the
// non-terminal-prior rows of §D.1 (27 cells).
func TestEnforceTerminalMonotonicity_PriorNonTerminal_AllowAnything(t *testing.T) {
	priors := []StepStatus{StepStatusPending, StepStatusHeld, StepStatusRunning}
	incomings := []StepStatus{
		StepStatusPending, StepStatusHeld, StepStatusRunning,
		StepStatusCompleted, StepStatusFailed, StepStatusError,
		StepStatusAborted, StepStatusTimeout, StepStatusSkipped,
	}
	for _, prior := range priors {
		for _, incoming := range incomings {
			t.Run(fmt.Sprintf("%s_to_%s", prior, incoming), func(t *testing.T) {
				session := gateMockSession(string(prior))
				store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
				err := store.enforceTerminalMonotonicity(
					context.Background(), "corr-nonterm-prior", "dev_deploy", "", incoming,
				)
				if err != nil {
					t.Errorf("prior=%s incoming=%s: expected nil, got %v", prior, incoming, err)
				}
			})
		}
	}
}

// TestEnforceTerminalMonotonicity_RecoveryAllow covers the §D.4 recovery
// allow-list arm of the gate. recoverable → completed AND aborted → pending.
func TestEnforceTerminalMonotonicity_RecoveryAllow(t *testing.T) {
	cases := []struct {
		prior, incoming StepStatus
	}{
		{StepStatusFailed, StepStatusCompleted},
		{StepStatusAborted, StepStatusCompleted},
		{StepStatusError, StepStatusCompleted},
		{StepStatusTimeout, StepStatusCompleted},
		{StepStatusSkipped, StepStatusCompleted},
		{StepStatusAborted, StepStatusPending}, // cascade-reset
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s_to_%s", c.prior, c.incoming), func(t *testing.T) {
			session := gateMockSession(string(c.prior))
			store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
			err := store.enforceTerminalMonotonicity(
				context.Background(), "corr-recovery", "dev_deploy", "", c.incoming,
			)
			if err != nil {
				t.Errorf("prior=%s incoming=%s: expected nil (recovery allowed), got %v",
					c.prior, c.incoming, err)
			}
		})
	}
}

// TestEnforceTerminalMonotonicity_PriorTerminal_IncomingNonTerminal_Refused covers
// the §D.2 terminal × non-terminal sub-matrix EXCEPT the aborted → pending cell.
// This is the I5 bug-class hotspot.
func TestEnforceTerminalMonotonicity_PriorTerminal_IncomingNonTerminal_Refused(t *testing.T) {
	priors := []StepStatus{
		StepStatusCompleted, StepStatusFailed, StepStatusError,
		StepStatusAborted, StepStatusTimeout, StepStatusSkipped,
	}
	incomings := []StepStatus{StepStatusPending, StepStatusHeld, StepStatusRunning}
	for _, prior := range priors {
		for _, incoming := range incomings {
			// Skip the one explicit allow-list cell.
			if prior == StepStatusAborted && incoming == StepStatusPending {
				continue
			}
			t.Run(fmt.Sprintf("%s_to_%s", prior, incoming), func(t *testing.T) {
				session := gateMockSession(string(prior))
				store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
				err := store.enforceTerminalMonotonicity(
					context.Background(), "corr-block", "dev_deploy", "", incoming,
				)
				if !errors.Is(err, ErrTerminalAlreadyExists) {
					t.Errorf("prior=%s incoming=%s: expected ErrTerminalAlreadyExists, got %v",
						prior, incoming, err)
				}
			})
		}
	}
}

// TestEnforceTerminalMonotonicity_PriorTerminal_IncomingTerminal_Same_Refused
// covers the §D.3 main-diagonal cells (e.g. failed → failed). At lib level we
// refuse same-terminal — HTTP layer may convert to 204 idempotent.
func TestEnforceTerminalMonotonicity_PriorTerminal_IncomingTerminal_Same_Refused(t *testing.T) {
	terminals := []StepStatus{
		StepStatusCompleted, StepStatusFailed, StepStatusError,
		StepStatusAborted, StepStatusTimeout, StepStatusSkipped,
	}
	for _, s := range terminals {
		t.Run(string(s), func(t *testing.T) {
			session := gateMockSession(string(s))
			store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
			err := store.enforceTerminalMonotonicity(
				context.Background(), "corr-same-terminal", "dev_deploy", "", s,
			)
			// Same-terminal of {failed, aborted, error, timeout, skipped} → completed
			// is the recovery case — not same. Only {completed → completed}, {failed → failed},
			// etc. are same-terminal. recovery rule never fires for same-terminal where
			// incoming != completed; for {completed→completed} the allow-list refuses (no rule
			// matches because completed is not in the recoverable set).
			//
			// Exception per §D.3: {failed → completed}, {aborted → completed} etc. are recovery
			// cells, not same-terminal — we filtered to incoming==prior above.
			if !errors.Is(err, ErrTerminalAlreadyExists) {
				t.Errorf("prior=%s incoming=%s (same-terminal): expected ErrTerminalAlreadyExists, got %v",
					s, s, err)
			}
		})
	}
}

// TestEnforceTerminalMonotonicity_PriorTerminal_IncomingTerminal_DifferentNonRecovery_Refused
// covers §D.3 off-diagonal cells that are NOT in the recovery allow-list — e.g.
// completed → failed (must refuse), failed → aborted (cross-terminal label swap).
func TestEnforceTerminalMonotonicity_PriorTerminal_IncomingTerminal_DifferentNonRecovery_Refused(t *testing.T) {
	cases := []struct {
		prior, incoming StepStatus
	}{
		// completed → anything-terminal-other-than-completed is refused.
		{StepStatusCompleted, StepStatusFailed},
		{StepStatusCompleted, StepStatusAborted},
		{StepStatusCompleted, StepStatusError},
		{StepStatusCompleted, StepStatusTimeout},
		{StepStatusCompleted, StepStatusSkipped},
		// recoverable → recoverable cross-terminal swaps are refused.
		{StepStatusFailed, StepStatusAborted},
		{StepStatusFailed, StepStatusError},
		{StepStatusFailed, StepStatusTimeout},
		{StepStatusFailed, StepStatusSkipped},
		{StepStatusAborted, StepStatusFailed},
		{StepStatusAborted, StepStatusError},
		{StepStatusError, StepStatusFailed},
		{StepStatusError, StepStatusAborted},
		{StepStatusTimeout, StepStatusFailed},
		{StepStatusSkipped, StepStatusFailed},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s_to_%s", c.prior, c.incoming), func(t *testing.T) {
			session := gateMockSession(string(c.prior))
			store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
			err := store.enforceTerminalMonotonicity(
				context.Background(), "corr-cross-terminal", "dev_deploy", "", c.incoming,
			)
			if !errors.Is(err, ErrTerminalAlreadyExists) {
				t.Errorf("prior=%s incoming=%s: expected ErrTerminalAlreadyExists, got %v",
					c.prior, c.incoming, err)
			}
		})
	}
}

// TestEnforceTerminalMonotonicity_PushParsedBypass covers §B.15.
// Seed terminal completed for push_parsed; expect the gate to ALLOW any
// incoming status (including running) because push_parsed is on
// gateBypassSteps. The CH pre-flight query MUST NOT be issued at all.
func TestEnforceTerminalMonotonicity_PushParsedBypass(t *testing.T) {
	session := &clickhousetest.MockSession{
		// Configure the row to return "completed" so that IF the bypass were
		// missing, the gate would refuse the running incoming. With the bypass
		// in place, QueryRow must not be called at all.
		QueryRowFunc: func(_ context.Context, _ string, _ ...any) driver.Row {
			t.Fatal("gate must NOT issue QueryRow for push_parsed (gateBypassSteps short-circuit)")
			return mockGateRow("completed")
		},
	}
	store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")

	for _, incoming := range []StepStatus{
		StepStatusRunning, StepStatusPending, StepStatusFailed, StepStatusCompleted,
	} {
		t.Run(string(incoming), func(t *testing.T) {
			err := store.enforceTerminalMonotonicity(
				context.Background(), "corr-pushparsed", "push_parsed", "", incoming,
			)
			if err != nil {
				t.Errorf("push_parsed bypass: expected nil for any incoming, got %v", err)
			}
		})
	}
}

// TestEnforceTerminalMonotonicity_CHError_FailsOpen covers the §J error policy.
// A CH transport error during pre-flight MUST fail-OPEN (return nil) so the
// gate's CH availability degradation does not break legitimate writes. The
// per-corr-id lock (slippy-api scope) remains the primary safety net.
func TestEnforceTerminalMonotonicity_CHError_FailsOpen(t *testing.T) {
	session := &clickhousetest.MockSession{
		QueryRowFunc: func(_ context.Context, _ string, _ ...any) driver.Row {
			// Returning nil triggers the "query returned nil row" branch in
			// latestComponentStateStatus → fmt.Errorf — gate must catch and
			// fail-open.
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")

	err := store.enforceTerminalMonotonicity(
		context.Background(), "corr-ch-broken", "dev_deploy", "", StepStatusCompleted,
	)
	if err != nil {
		t.Errorf("CH error must fail-OPEN; got %v", err)
	}
}

// TestEnforceTerminalMonotonicity_CHScanError_FailsOpen covers a Scan-level
// CH transport failure (different code path from nil row).
func TestEnforceTerminalMonotonicity_CHScanError_FailsOpen(t *testing.T) {
	session := &clickhousetest.MockSession{
		QueryRowRow: &clickhousetest.MockRow{
			ScanFunc: func(_ ...any) error {
				return fmt.Errorf("simulated CH connection reset")
			},
		},
	}
	store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")

	err := store.enforceTerminalMonotonicity(
		context.Background(), "corr-ch-scanerr", "dev_deploy", "", StepStatusCompleted,
	)
	if err != nil {
		t.Errorf("Scan error must fail-OPEN; got %v", err)
	}
}

// TestEnforceTerminalMonotonicity_CascadeReset_AbortedToPending_Allowed
// — closes DA-v2 CRIT-V2-1. End-to-end proof that the cascade-reset path
// (executor.go:377) is NOT blocked by the gate. Without the rule 1 cell in
// isRecoveryAllowed, this transition would fail and leave aborted steps
// orphaned on resolved failures.
func TestEnforceTerminalMonotonicity_CascadeReset_AbortedToPending_Allowed(t *testing.T) {
	session := gateMockSession(string(StepStatusAborted))
	store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")

	err := store.enforceTerminalMonotonicity(
		context.Background(), "corr-cascade-reset", "dev_deploy", "", StepStatusPending,
	)
	if err != nil {
		t.Fatalf("cascade-reset aborted → pending must be allowed (CRIT-V2-1 closure); got %v", err)
	}

	// Cross-check: rule must remain narrow. aborted → running and aborted → held
	// MUST still be refused (regression guard against accidentally widening the
	// allow-list — §J risk #13).
	for _, incoming := range []StepStatus{StepStatusRunning, StepStatusHeld} {
		t.Run(fmt.Sprintf("regression_guard_aborted_to_%s", incoming), func(t *testing.T) {
			session := gateMockSession(string(StepStatusAborted))
			store := NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
			err := store.enforceTerminalMonotonicity(
				context.Background(), "corr-cascade-reset-narrow", "dev_deploy", "", incoming,
			)
			if !errors.Is(err, ErrTerminalAlreadyExists) {
				t.Errorf("aborted → %s must STILL be refused (rule must remain narrow); got %v",
					incoming, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Client wrapper sentinel preservation (§B.14)
// -----------------------------------------------------------------------------

// stubStoreReturningErr is a SlipStore stub that returns the given error from
// UpdateStepWithHistory. Used to verify that NewStepError wrapping does not
// break errors.Is(err, ErrTerminalAlreadyExists) at the outermost caller.
type stubStoreReturningErr struct {
	SlipStore
	err error
}

func (s *stubStoreReturningErr) UpdateStepWithHistory(
	_ context.Context, _, _, _ string, _ StepStatus, _ StateHistoryEntry,
) error {
	return s.err
}

// TestClient_StartStep_PreservesErrTerminalAlreadyExistsUnwrap proves that the
// sentinel survives wrapping through Client.StartStep → UpdateStepWithStatus →
// NewStepError. Without an Unwrap method on StepError, errors.Is would return
// false at the caller and the slippy-api 409 mapping would silently break.
func TestClient_StartStep_PreservesErrTerminalAlreadyExistsUnwrap(t *testing.T) {
	store := &stubStoreReturningErr{err: ErrTerminalAlreadyExists}
	client := NewClientWithDependencies(store, nil, Config{Database: "ci"})

	err := client.StartStep(context.Background(), "corr-sentinel-unwrap", "dev_deploy", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrTerminalAlreadyExists) {
		t.Fatalf("errors.Is(err, ErrTerminalAlreadyExists) must hold through StepError wrap; got err=%v", err)
	}

	// Also assert that a non-sentinel error does NOT match (negative control).
	store.err = errors.New("some other failure")
	err = client.StartStep(context.Background(), "corr-sentinel-unwrap", "dev_deploy", "")
	if errors.Is(err, ErrTerminalAlreadyExists) {
		t.Fatal("errors.Is must NOT match an unrelated error")
	}
}
