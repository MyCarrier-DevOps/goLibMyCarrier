package slippy

// terminal_monotonicity_gate_test.go — unit tests for the I5 v2 freshness gate.
//
// Tests cover enforceTerminalFreshnessGate, latestComponentStateRow, gateEnabled,
// and freshnessWindow as specified in the I5 v2 plan §Test Plan (ADO #83405).
//
// All tests are pure-unit (no Docker / ClickHouse required); CH interactions are
// intercepted via the MockSession / MockRow fixtures from the clickhousetest package.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse/clickhousetest"
)

// gateTestStore builds a ClickHouseStore connected to a MockSession. The pipeline
// config is testPipelineConfig() so that step/aggregate lookups work correctly.
func gateTestStore(session *clickhousetest.MockSession) *ClickHouseStore {
	return NewClickHouseStoreFromSession(session, testPipelineConfig(), "ci")
}

// gateQueryRowForStatus returns a MockRow that scans (rawStatus, eventTimestamp, age_ms).
// Use rawStatus="" to simulate no-match (empty aggregate) from ClickHouse.
// age is the server-computed dateDiff value; pass 0 for tests that do not reach rule 7.
func gateQueryRowForStatus(rawStatus string, eventTimestamp time.Time, age time.Duration) *clickhousetest.MockRow {
	return &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			if len(dest) < 3 {
				return fmt.Errorf("expected 3 scan destinations, got %d", len(dest))
			}
			if ptr, ok := dest[0].(*string); ok {
				*ptr = rawStatus
			}
			if ptr, ok := dest[1].(*time.Time); ok {
				*ptr = eventTimestamp
			}
			if ptr, ok := dest[2].(*int64); ok {
				*ptr = age.Milliseconds()
			}
			return nil
		},
	}
}

// --- gateEnabled / freshnessWindow -------------------------------------------------------

func TestGateEnabled_Default(t *testing.T) {
	// sentinel+unset ensures auto-restore even if the env var was set before this test.
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_GATE_ENABLED")
	if !gateEnabled() {
		t.Error("expected gate to be ON by default (fail-safe)")
	}
}

func TestGateEnabled_ExplicitTrue(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "true")
	if !gateEnabled() {
		t.Error("expected gate enabled when SLIPPY_I5_GATE_ENABLED=true")
	}
}

func TestGateEnabled_ExplicitFalse(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "false")
	if gateEnabled() {
		t.Error("expected gate disabled when SLIPPY_I5_GATE_ENABLED=false")
	}
}

func TestGateEnabled_InvalidValue_FailSafeOn(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "notabool")
	if !gateEnabled() {
		t.Error("expected gate to be ON (fail-safe) when env value is unparseable")
	}
}

func TestFreshnessWindow_Default(t *testing.T) {
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS")
	if got := freshnessWindow(); got != defaultFreshnessWindowSeconds*time.Second {
		t.Errorf("expected default %v, got %v", defaultFreshnessWindowSeconds*time.Second, got)
	}
}

func TestFreshnessWindow_Custom(t *testing.T) {
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "10")
	if got := freshnessWindow(); got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

func TestFreshnessWindow_Invalid_FallsBackToDefault(t *testing.T) {
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "abc")
	if got := freshnessWindow(); got != defaultFreshnessWindowSeconds*time.Second {
		t.Errorf("expected default on invalid value, got %v", got)
	}
}

func TestFreshnessWindow_Zero_FallsBackToDefault(t *testing.T) {
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "0")
	if got := freshnessWindow(); got != defaultFreshnessWindowSeconds*time.Second {
		t.Errorf("expected default on zero value, got %v", got)
	}
}

// --- latestComponentStateRow -------------------------------------------------------------

func TestLatestComponentStateRow_NotFound(t *testing.T) {
	// CH returns empty aggregate (rawStatus="") → not found.
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus("", time.Time{}, 0),
	}
	store := gateTestStore(session)

	_, _, _, found, err := store.latestComponentStateRow(context.Background(), "corr-1", "push_parsed", "")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if found {
		t.Error("expected found=false for empty aggregate result")
	}
}

func TestLatestComponentStateRow_RowsError(t *testing.T) {
	// CH scan returns sql.ErrNoRows → not found (not an error to the caller).
	session := &clickhousetest.MockSession{
		QueryRowRow: &clickhousetest.MockRow{ScanErr: sql.ErrNoRows},
	}
	store := gateTestStore(session)

	_, _, _, found, err := store.latestComponentStateRow(context.Background(), "corr-1", "push_parsed", "")
	if err != nil {
		t.Errorf("expected no error on sql.ErrNoRows, got %v", err)
	}
	if found {
		t.Error("expected found=false on sql.ErrNoRows")
	}
}

func TestLatestComponentStateRow_CHError(t *testing.T) {
	chErr := errors.New("clickhouse error")
	session := &clickhousetest.MockSession{
		QueryRowRow: &clickhousetest.MockRow{ScanErr: chErr},
	}
	store := gateTestStore(session)

	_, _, _, _, err := store.latestComponentStateRow(context.Background(), "corr-1", "build", "api")
	if !errors.Is(err, chErr) {
		t.Errorf("expected wrapped chErr, got %v", err)
	}
}

func TestLatestComponentStateRow_Found(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	const mockAge = 1500 * time.Millisecond // arbitrary; just verifies age is scanned and returned
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusCompleted), now, mockAge),
	}
	store := gateTestStore(session)

	status, ts, age, found, err := store.latestComponentStateRow(context.Background(), "corr-1", "unit_tests", "")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !found {
		t.Error("expected found=true")
	}
	if status != StepStatusCompleted {
		t.Errorf("expected completed, got %v", status)
	}
	// Timestamps compared to millisecond granularity.
	if !ts.Equal(now) {
		t.Errorf("expected ts %v, got %v", now, ts)
	}
	// Server-computed age must be propagated correctly.
	if age != mockAge {
		t.Errorf("expected age %v, got %v", mockAge, age)
	}
	// Verify the query targeted slip_component_states with the correct args.
	if len(session.QueryRowCalls) != 1 {
		t.Fatalf("expected 1 QueryRow call, got %d", len(session.QueryRowCalls))
	}
	call := session.QueryRowCalls[0]
	if !strings.Contains(call.Query, TableSlipComponentStates) {
		t.Errorf("query should target %s, got: %s", TableSlipComponentStates, call.Query)
	}
	if !strings.Contains(call.Query, componentEventSortKeyNoImageTag) {
		t.Errorf("query should use componentEventSortKeyNoImageTag")
	}
	if !strings.Contains(call.Query, "dateDiff") {
		t.Errorf("query should include server-side dateDiff for age_ms")
	}
}

// --- enforceTerminalFreshnessGate --------------------------------------------------------

func TestGate_NoPrior_Allows(t *testing.T) {
	// No prior event (rawStatus="") → allow.
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus("", time.Time{}, 0),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("NoPrior_Allows: expected nil, got %v", err)
	}
}

func TestGate_PriorNonTerminal_Allows(t *testing.T) {
	// Prior status is running (non-terminal) → allow incoming running.
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusRunning), time.Now(), 0),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("PriorNonTerminal_Allows: expected nil, got %v", err)
	}
}

func TestGate_Within4900ms_Refuses(t *testing.T) {
	// Server-computed age = 4.9 s < default 5 s window → refuse incoming running.
	// Age is now returned directly from the mock (server-side dateDiff); no timestamp math needed.
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS")
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusCompleted), time.Now(), 4900*time.Millisecond),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if !errors.Is(err, ErrTerminalAlreadyExists) {
		t.Errorf("Within4900ms_Refuses: expected ErrTerminalAlreadyExists, got %v", err)
	}
}

func TestGate_After5100ms_Allows(t *testing.T) {
	// Server-computed age = 5.1 s > default 5 s window → allow (window expired).
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS")
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusCompleted), time.Now(), 5100*time.Millisecond),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("After5100ms_Allows: expected nil, got %v", err)
	}
}

func TestGate_NearBoundary_Refuses(t *testing.T) {
	// Server-computed age = 4.5 s < default 5 s window → inside window → refuse.
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS")
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusCompleted), time.Now(), 4500*time.Millisecond),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if !errors.Is(err, ErrTerminalAlreadyExists) {
		t.Errorf("NearBoundary_Refuses: expected ErrTerminalAlreadyExists, got %v", err)
	}
}

func TestGate_AbortedToPending_AlwaysAllow_Within(t *testing.T) {
	// aborted → pending: cascade-reset exception → allow (rule 6 fires before rule 7; age irrelevant).
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusAborted), time.Now(), 100*time.Millisecond),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "dev_deploy", "", StepStatusPending,
	)
	if err != nil {
		t.Errorf("AbortedToPending_AlwaysAllow_Within: expected nil, got %v", err)
	}
}

func TestGate_AbortedToPending_AlwaysAllow_After(t *testing.T) {
	// aborted → pending: exception is unconditional; rule 6 fires before rule 7 (age irrelevant).
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusAborted), time.Now(), 10*time.Second),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "dev_deploy", "", StepStatusPending,
	)
	if err != nil {
		t.Errorf("AbortedToPending_AlwaysAllow_After: expected nil, got %v", err)
	}
}

func TestGate_GateDisabled_Allows(t *testing.T) {
	// Gate disabled via env var → always allow, no CH call.
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "false")

	// QueryRowRow intentionally left nil; any CH call would panic.
	session := &clickhousetest.MockSession{}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("GateDisabled_Allows: expected nil, got %v", err)
	}
	if len(session.QueryRowCalls) != 0 {
		t.Errorf("GateDisabled_Allows: expected 0 QueryRow calls (gate disabled), got %d",
			len(session.QueryRowCalls))
	}
}

func TestGate_CHError_FailOpen(t *testing.T) {
	// CH scan returns an unexpected error → gate fails open (WARN + nil).
	chErr := errors.New("simulated CH error")
	session := &clickhousetest.MockSession{
		QueryRowRow: &clickhousetest.MockRow{ScanErr: chErr},
	}
	store := gateTestStore(session)

	t.Setenv("SLIPPY_I5_GATE_ENABLED", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_GATE_ENABLED")
	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("CHError_FailOpen: expected nil (fail-open), got %v", err)
	}
}

func TestGate_CustomWindow_2s_Refuse(t *testing.T) {
	// Custom 2 s window; server-computed age = 1.9 s → refuse.
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "2")

	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusFailed), time.Now(), 1900*time.Millisecond),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if !errors.Is(err, ErrTerminalAlreadyExists) {
		t.Errorf("CustomWindow_2s_Refuse: expected ErrTerminalAlreadyExists, got %v", err)
	}
}

func TestGate_CustomWindow_2s_Allow(t *testing.T) {
	// Custom 2 s window; server-computed age = 2.1 s → allow (window expired).
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "2")

	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusFailed), time.Now(), 2100*time.Millisecond),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("CustomWindow_2s_Allow: expected nil, got %v", err)
	}
}

func TestGate_PushParsedBypassed_WithinWindow(t *testing.T) {
	// push_parsed with component="" is a bypass step → no CH call, always allowed.
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_GATE_ENABLED")
	session := &clickhousetest.MockSession{}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "push_parsed", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("PushParsedBypassed_WithinWindow: expected nil, got %v", err)
	}
	// No CH query should have been issued.
	if len(session.QueryRowCalls) != 0 {
		t.Errorf("PushParsedBypassed_WithinWindow: expected 0 QueryRow calls, got %d",
			len(session.QueryRowCalls))
	}
}

func TestGate_TerminalToTerminal_WithinWindow_Allows(t *testing.T) {
	// SC-3: incoming terminal → always allow, regardless of prior terminal + window.
	// This test documents that the plan's earlier row "TerminalToTerminal_WithinWindow_Refuses"
	// is STALE (superseded by SC-3 resolution). The gate must ALLOW.
	// Rule 3 short-circuits before CH query, so the mock row is never scanned.
	session := &clickhousetest.MockSession{
		// Return prior completed (terminal) — will not be reached due to SC-3 short-circuit.
		QueryRowRow: gateQueryRowForStatus(string(StepStatusCompleted), time.Now(), 100*time.Millisecond),
	}
	store := gateTestStore(session)

	// Incoming = failed (terminal) — rule 3 fires immediately, no CH call.
	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusFailed,
	)
	if err != nil {
		t.Errorf("TerminalToTerminal_WithinWindow_Allows: expected nil (SC-3), got %v", err)
	}
	// No QueryRow call because rule 3 short-circuits before CH query.
	if len(session.QueryRowCalls) != 0 {
		t.Errorf("TerminalToTerminal_WithinWindow_Allows: expected 0 QueryRow calls (SC-3), got %d",
			len(session.QueryRowCalls))
	}
}

func TestGate_TerminalToTerminal_AfterWindow_Allows(t *testing.T) {
	// SC-3: incoming terminal → always allow even after the window has expired.
	// Rule 3 short-circuits; age value not used.
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus(string(StepStatusCompleted), time.Now(), 10*time.Second),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusCompleted,
	)
	if err != nil {
		t.Errorf("TerminalToTerminal_AfterWindow_Allows: expected nil (SC-3), got %v", err)
	}
}

// --- Gate call-count assertions (white-box via enforceTerminalFreshnessGate) ------------

// TestGate_BypassStep_NoQueryRowCall verifies that push_parsed with component=""
// short-circuits before any CH query (rule 2).
func TestGate_BypassStep_NoQueryRowCall(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_GATE_ENABLED")
	session := &clickhousetest.MockSession{}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "push_parsed", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("expected nil for bypass step, got %v", err)
	}
	if len(session.QueryRowCalls) != 0 {
		t.Errorf("expected 0 QueryRow calls for bypass step, got %d", len(session.QueryRowCalls))
	}
}

// TestGate_TerminalIncoming_NoQueryRowCall verifies rule 3 (SC-3): terminal incoming
// fires before the CH query, so QueryRow is never called.
func TestGate_TerminalIncoming_NoQueryRowCall(t *testing.T) {
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_GATE_ENABLED")
	session := &clickhousetest.MockSession{}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "unit_tests", "", StepStatusCompleted,
	)
	if err != nil {
		t.Errorf("expected nil for terminal incoming (rule 3), got %v", err)
	}
	if len(session.QueryRowCalls) != 0 {
		t.Errorf("expected 0 QueryRow calls for terminal incoming (SC-3), got %d", len(session.QueryRowCalls))
	}
}

// TestGate_NonTerminalIncoming_IssuedQueryRow verifies that a non-terminal incoming
// status on a non-bypass step issues exactly one gate CH query.
func TestGate_NonTerminalIncoming_IssuedQueryRow(t *testing.T) {
	// Return "no prior" from the gate query → allow.
	t.Setenv("SLIPPY_I5_GATE_ENABLED", "__sentinel__")
	os.Unsetenv("SLIPPY_I5_GATE_ENABLED")
	session := &clickhousetest.MockSession{
		QueryRowRow: gateQueryRowForStatus("", time.Time{}, 0),
	}
	store := gateTestStore(session)

	err := store.enforceTerminalFreshnessGate(
		context.Background(), "corr-1", "dev_deploy", "", StepStatusRunning,
	)
	if err != nil {
		t.Errorf("expected nil (no prior → allow), got %v", err)
	}
	if len(session.QueryRowCalls) != 1 {
		t.Errorf("expected exactly 1 gate QueryRow call, got %d", len(session.QueryRowCalls))
	}
	call := session.QueryRowCalls[0]
	if !strings.Contains(call.Query, TableSlipComponentStates) {
		t.Errorf("gate query should target %s, got: %s", TableSlipComponentStates, call.Query)
	}
}
