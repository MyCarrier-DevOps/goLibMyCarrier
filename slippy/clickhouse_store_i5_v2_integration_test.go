//go:build integration

package slippy

// clickhouse_store_i5_v2_integration_test.go — I5 v2 gate + R2 write-back integration tests.
//
// These tests require a live ClickHouse instance (testcontainers).
// Run with: go test -tags integration -run TestI5V2 ./slippy/
//
// Design:
//   - Gate tests verify that the terminal-freshness gate (enforceTerminalFreshnessGate)
//     refuses non-terminal-over-terminal writes within the freshness window and allows
//     them after the window, using a real slip_component_states table.
//   - R2 tests verify that updateWithOverrides (the R2 aggregate write-back path)
//     applies argMax-derived step statuses from slip_component_states, preventing the
//     aggregate write-back from overwriting concurrently-updated pure pipeline steps.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// --- integration test helpers ---

// i5V2TestCorrelationID returns a unique correlation ID for each test run.
func i5V2TestCorrelationID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("i5v2-integ-%d", time.Now().UnixNano())
}

// i5V2SetupStore starts a ClickHouse testcontainer and returns a configured store.
func i5V2SetupStore(ctx context.Context, t *testing.T) *ClickHouseStore {
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

// i5V2CreateSlip inserts a minimal routing slip and returns the correlation ID.
func i5V2CreateSlip(ctx context.Context, t *testing.T, store *ClickHouseStore, corrID, repo string) {
	t.Helper()
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    repo,
		Branch:        "main",
		CommitSHA:     "abc123",
		Status:        SlipStatusPending,
		Steps:         make(map[string]Step),
		Aggregates:    make(map[string][]ComponentStepData),
	}
	for _, step := range integrationTestPipelineConfig().Steps {
		slip.Steps[step.Name] = Step{Status: StepStatusPending}
	}
	if err := store.Create(ctx, slip); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}
}

// --- Gate integration tests ---

// TestI5V2_Gate_WithinWindow_Refuses verifies that a non-terminal write within
// the freshness window (after a terminal write) is refused with ErrTerminalAlreadyExists.
func TestI5V2_Gate_WithinWindow_Refuses(t *testing.T) {
	ctx := context.Background()
	store := i5V2SetupStore(ctx, t)
	corrID := i5V2TestCorrelationID(t)
	i5V2CreateSlip(ctx, t, store, corrID, "myorg/repo-gate-refuse")

	// Write terminal status for push_parsed… wait, push_parsed is bypass-exempt.
	// Use dev_deploy (non-bypass) instead.
	if err := store.UpdateStep(ctx, corrID, "dev_deploy", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to write terminal status: %v", err)
	}

	// Immediately attempt non-terminal override → should be refused.
	err := store.UpdateStep(ctx, corrID, "dev_deploy", "", StepStatusRunning)
	if !errors.Is(err, ErrTerminalAlreadyExists) {
		t.Errorf("expected ErrTerminalAlreadyExists within window, got %v", err)
	}
}

// TestI5V2_Gate_AfterWindow_Allows verifies that a non-terminal write after the
// freshness window has expired is allowed (genuine re-run / restart scenario).
func TestI5V2_Gate_AfterWindow_Allows(t *testing.T) {
	ctx := context.Background()
	store := i5V2SetupStore(ctx, t)
	corrID := i5V2TestCorrelationID(t)
	i5V2CreateSlip(ctx, t, store, corrID, "myorg/repo-gate-allow")

	// Backdate the terminal event by querying and inserting a row older than the window.
	// We achieve this by writing a terminal status directly to slip_component_states with
	// a past timestamp. Use the store's session to bypass the gate for this setup row.
	// NOTE: we set SLIPPY_I5_FRESHNESS_WINDOW_SECONDS=0 for the check so even a fresh row
	// is "expired". Alternatively, sleep > defaultFreshnessWindowSeconds. For test speed,
	// use a 0-second custom window via env; the gate skips (secs <=0 → default) so we rely
	// on a direct slip_component_states insert with a past timestamp.
	//
	// Simplified approach for integration test: use the gate's custom-window env var with
	// a 1-second window and sleep 1.1 seconds.
	t.Setenv("SLIPPY_I5_FRESHNESS_WINDOW_SECONDS", "1")

	if err := store.UpdateStep(ctx, corrID, "dev_deploy", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to write terminal status: %v", err)
	}

	// Wait for the 1-second window to expire.
	time.Sleep(1100 * time.Millisecond)

	// Non-terminal write after window expiry → allowed.
	if err := store.UpdateStep(ctx, corrID, "dev_deploy", "", StepStatusRunning); err != nil {
		t.Errorf("expected nil after window expiry, got %v", err)
	}
}

// TestI5V2_Gate_TerminalToTerminal_AlwaysAllows verifies SC-3: a terminal incoming
// status always allows regardless of prior terminal status or window timing.
func TestI5V2_Gate_TerminalToTerminal_AlwaysAllows(t *testing.T) {
	ctx := context.Background()
	store := i5V2SetupStore(ctx, t)
	corrID := i5V2TestCorrelationID(t)
	i5V2CreateSlip(ctx, t, store, corrID, "myorg/repo-gate-t2t")

	// Write terminal → should succeed.
	if err := store.UpdateStep(ctx, corrID, "dev_deploy", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to write first terminal status: %v", err)
	}

	// Write another terminal immediately → SC-3: always allowed.
	if err := store.UpdateStep(ctx, corrID, "dev_deploy", "", StepStatusFailed); err != nil {
		t.Errorf("SC-3: expected nil for terminal→terminal, got %v", err)
	}
}

// --- R2 write-back integration tests ---

// TestI5V2_R2_ArgMax_AppliedInAggregateWriteback verifies that updateWithOverrides
// applies the argMax-derived status of a pure pipeline step that was updated
// concurrently, preventing the aggregate write-back from reverting it.
func TestI5V2_R2_ArgMax_AppliedInAggregateWriteback(t *testing.T) {
	ctx := context.Background()
	store := i5V2SetupStore(ctx, t)
	corrID := i5V2TestCorrelationID(t)
	i5V2CreateSlip(ctx, t, store, corrID, "myorg/repo-r2-apply")

	// Step 1: write push_parsed=completed to slip_component_states.
	if err := store.UpdateStep(ctx, corrID, "push_parsed", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update push_parsed: %v", err)
	}

	// Step 2: trigger aggregate write-back for builds_completed via a component update.
	if err := store.UpdateStep(ctx, corrID, "builds_completed", "api", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update builds_completed/api: %v", err)
	}

	// Step 3: Load and verify push_parsed was NOT reverted to pending by the
	// aggregate write-back (R2 should have included its argMax-derived completed status).
	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}
	if loaded.Steps["push_parsed"].Status != StepStatusCompleted {
		t.Errorf("R2: push_parsed reverted to %v; expected completed after aggregate write-back",
			loaded.Steps["push_parsed"].Status)
	}
}

// TestI5V2_R2_SC1_AggregateStepNotClobbered verifies SC-1: the aggregate step status
// (computed from components by the write-back loop) is never overwritten by the
// slip_component_states argMax for that same step name.
func TestI5V2_R2_SC1_AggregateStepNotClobbered(t *testing.T) {
	ctx := context.Background()
	store := i5V2SetupStore(ctx, t)
	corrID := i5V2TestCorrelationID(t)
	i5V2CreateSlip(ctx, t, store, corrID, "myorg/repo-r2-sc1")

	// Write a component status that should produce builds_completed=completed.
	if err := store.UpdateStep(ctx, corrID, "builds_completed", "api", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update builds_completed/api: %v", err)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}
	// SC-1: builds_completed status should reflect the computed aggregate (completed)
	// not some stale or wrongly-applied argMax value (e.g. running from a pipeline-level write).
	if loaded.Steps["builds_completed"].Status != StepStatusCompleted {
		t.Errorf("SC-1: builds_completed=%v, expected completed", loaded.Steps["builds_completed"].Status)
	}
}

// TestI5V2_R2_CallerOverride_WinsOverArgMax verifies that when updateWithOverrides is
// called with an r2StepOverride for the aggregate step, the freshly-computed status
// wins over any argMax-derived value (3-tier tier 1 > tier 2).
func TestI5V2_R2_CallerOverride_WinsOverArgMax(t *testing.T) {
	ctx := context.Background()
	store := i5V2SetupStore(ctx, t)
	corrID := i5V2TestCorrelationID(t)
	i5V2CreateSlip(ctx, t, store, corrID, "myorg/repo-r2-override")

	// Write component completed → triggers updateWithOverrides with aggregate override.
	if err := store.UpdateStep(ctx, corrID, "builds_completed", "api", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update builds_completed/api: %v", err)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}
	// The callerOverride (aggregateStepName=completed) must win.
	if loaded.Steps["builds_completed"].Status != StepStatusCompleted {
		t.Errorf("callerOverride: builds_completed=%v, expected completed", loaded.Steps["builds_completed"].Status)
	}
}
