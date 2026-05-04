package slippy

import (
	"context"
	"testing"
	"time"
)

const stateMachineRef = "\n\nInvariant violated. Validate your changes against .github/STATE_MACHINE_V3.md"

// ─────────────────────────────────────────────────────────────────────────────
// I1: Primary failure must propagate to slip.status=failed
// ─────────────────────────────────────────────────────────────────────────────

// TestStateMachine_I1_FailedStepSetsPipelineFailed verifies that calling FailStep
// on a running pipeline step drives slip.status to "failed".
// Precondition: slip=in_progress, unit_tests=running
// Action:       FailStep(unit_tests)
// Expected:     slip.status == failed
func TestStateMachine_I1_FailedStepSetsPipelineFailed(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i1-fail-step"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i1-fail",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"unit_tests": {Status: StepStatusRunning},
		},
	}
	store.AddSlip(slip)

	if err := client.FailStep(ctx, corrID, "unit_tests", "", "tests failed"); err != nil {
		t.Fatalf("FailStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusFailed {
		t.Errorf("expected slip.status=%q after FailStep, got %q%s",
			SlipStatusFailed, loaded.Status, stateMachineRef)
	}
}

// TestStateMachine_I1_ErrorStepSetsPipelineFailed verifies that setting a step to
// the "error" status (via UpdateStepWithStatus) also drives slip.status to "failed".
// Precondition: slip=in_progress, unit_tests=running
// Action:       UpdateStepWithStatus(unit_tests, error)
// Expected:     slip.status == failed
func TestStateMachine_I1_ErrorStepSetsPipelineFailed(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i1-error-step"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i1-error",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"unit_tests": {Status: StepStatusRunning},
		},
	}
	store.AddSlip(slip)

	if err := client.UpdateStepWithStatus(ctx, corrID, "unit_tests", "", StepStatusError, ""); err != nil {
		t.Fatalf("UpdateStepWithStatus(error) returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusFailed {
		t.Errorf("expected slip.status=%q after error step, got %q%s",
			SlipStatusFailed, loaded.Status, stateMachineRef)
	}
}

// TestStateMachine_I1_TimeoutStepSetsPipelineFailed verifies that calling TimeoutStep
// drives slip.status to "failed".
// Precondition: slip=in_progress, unit_tests=running (held)
// Action:       TimeoutStep(unit_tests)
// Expected:     slip.status == failed
func TestStateMachine_I1_TimeoutStepSetsPipelineFailed(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i1-timeout-step"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i1-timeout",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"unit_tests": {Status: StepStatusHeld},
		},
	}
	store.AddSlip(slip)

	if err := client.TimeoutStep(ctx, corrID, "unit_tests", "", "hold timeout exceeded"); err != nil {
		t.Fatalf("TimeoutStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusFailed {
		t.Errorf("expected slip.status=%q after TimeoutStep, got %q%s",
			SlipStatusFailed, loaded.Status, stateMachineRef)
	}
}

// TestStateMachine_I1_AbortedStepAloneDoesNotSetPipelineFailed verifies that an
// aborted step (cascade failure) does NOT independently set slip.status=failed.
// Precondition: slip=in_progress, dev_deploy=held (no primary failure)
// Action:       AbortStep(dev_deploy) — cascade abort, no primary failure exists
// Expected:     slip.status remains in_progress (aborted is cascade, not primary)
func TestStateMachine_I1_AbortedStepAloneDoesNotSetPipelineFailed(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i1-abort-only"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i1-abort",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"dev_deploy": {Status: StepStatusHeld},
		},
	}
	store.AddSlip(slip)

	if err := client.AbortStep(ctx, corrID, "dev_deploy", "", "upstream cancelled"); err != nil {
		t.Fatalf("AbortStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusInProgress {
		t.Errorf("expected slip.status=%q (aborted is cascade, not primary), got %q%s",
			SlipStatusInProgress, loaded.Status, stateMachineRef)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// I2: No false failure — recovered pipeline must restore to in_progress
// ─────────────────────────────────────────────────────────────────────────────

// TestStateMachine_I2_ResolvedFailureRestoresPipelineToInProgress verifies that
// completing the previously-failed step (resolving the last primary failure)
// restores slip.status back to in_progress.
// Precondition: slip=failed, unit_tests=failed, dev_deploy=aborted
// Action:       CompleteStep(unit_tests) → primaryFailures=0
// Expected:     slip.status == in_progress
func TestStateMachine_I2_ResolvedFailureRestoresPipelineToInProgress(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i2-recovery"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i2-recovery",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusFailed,
		Steps: map[string]Step{
			"unit_tests": {Status: StepStatusFailed},
			"dev_deploy": {Status: StepStatusAborted},
		},
	}
	store.AddSlip(slip)

	if err := client.CompleteStep(ctx, corrID, "unit_tests", ""); err != nil {
		t.Fatalf("CompleteStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusInProgress {
		t.Errorf("expected slip.status=%q after resolving last primary failure, got %q%s",
			SlipStatusInProgress, loaded.Status, stateMachineRef)
	}
}

// TestStateMachine_I2_RecoveryCascadeStepsResetToPending verifies that on recovery
// the cascade-aborted steps are reset to pending so they can be re-triggered.
// Precondition: slip=failed, unit_tests=failed, dev_deploy=aborted
// Action:       CompleteStep(unit_tests)
// Expected:     dev_deploy.status == pending
func TestStateMachine_I2_RecoveryCascadeStepsResetToPending(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i2-cascade-reset"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i2-cascade",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusFailed,
		Steps: map[string]Step{
			"unit_tests": {Status: StepStatusFailed},
			"dev_deploy": {Status: StepStatusAborted},
		},
	}
	store.AddSlip(slip)

	if err := client.CompleteStep(ctx, corrID, "unit_tests", ""); err != nil {
		t.Fatalf("CompleteStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}

	devDeploy, ok := loaded.Steps["dev_deploy"]
	if !ok {
		t.Fatalf("dev_deploy step not found after recovery%s", stateMachineRef)
	}
	if devDeploy.Status != StepStatusPending {
		t.Errorf("expected dev_deploy.status=%q after cascade reset, got %q%s",
			StepStatusPending, devDeploy.Status, stateMachineRef)
	}
}

// TestStateMachine_I2_PartialRecoveryDoesNotRestorePipeline verifies that when
// multiple primary failures exist, resolving only one does NOT restore slip.status.
// Precondition: slip=failed, unit_tests=failed, secret_scan=failed, dev_deploy=aborted
// Action:       CompleteStep(unit_tests) only
// Expected:     slip.status remains failed (secret_scan still failed)
func TestStateMachine_I2_PartialRecoveryDoesNotRestorePipeline(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i2-partial-recovery"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i2-partial",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusFailed,
		Steps: map[string]Step{
			"unit_tests":  {Status: StepStatusFailed},
			"secret_scan": {Status: StepStatusFailed},
			"dev_deploy":  {Status: StepStatusAborted},
		},
	}
	store.AddSlip(slip)

	// Resolve only unit_tests — secret_scan still failed
	if err := client.CompleteStep(ctx, corrID, "unit_tests", ""); err != nil {
		t.Fatalf("CompleteStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusFailed {
		t.Errorf("expected slip.status=%q (secret_scan still failed), got %q%s",
			SlipStatusFailed, loaded.Status, stateMachineRef)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// I3: Completion gate — slip=completed only when conditions are all met
// ─────────────────────────────────────────────────────────────────────────────

// TestStateMachine_I3_SteadyStateCompletionSetsPipelineCompleted verifies that
// completing prod_steady_state with no primary failures sets slip.status=completed.
// Precondition: slip=in_progress, prod_steady_state=running, no failures
// Action:       CompleteStep(prod_steady_state)
// Expected:     slip.status == completed
func TestStateMachine_I3_SteadyStateCompletionSetsPipelineCompleted(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i3-steady-state"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i3-steady",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"prod_steady_state": {Status: StepStatusRunning},
		},
	}
	store.AddSlip(slip)

	if err := client.CompleteStep(ctx, corrID, "prod_steady_state", ""); err != nil {
		t.Fatalf("CompleteStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusCompleted {
		t.Errorf("expected slip.status=%q after prod_steady_state completed, got %q%s",
			SlipStatusCompleted, loaded.Status, stateMachineRef)
	}
}

// TestStateMachine_I3_PrimaryFailureBlocksCompletion verifies that primary failures
// are checked BEFORE the prod_steady_state gate, so the pipeline is marked failed
// rather than completed when both conditions are simultaneously true.
// Precondition: slip=in_progress, prod_steady_state=completed (in mock), another step=failed
// Action:       checkPipelineCompletion directly
// Expected:     slip.status == failed (NOT completed) — I1 takes priority over I3
func TestStateMachine_I3_PrimaryFailureBlocksCompletion(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i3-failure-blocks"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i3-blocks",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"prod_steady_state": {Status: StepStatusCompleted},
			"prod_alert_gate":   {Status: StepStatusFailed},
		},
	}
	store.AddSlip(slip)

	completed, status, err := client.checkPipelineCompletion(ctx, corrID)
	if err != nil {
		t.Fatalf("checkPipelineCompletion returned unexpected error: %v%s", err, stateMachineRef)
	}
	if completed {
		t.Errorf("expected pipeline NOT to be marked complete when primary failure exists%s", stateMachineRef)
	}
	if status != SlipStatusFailed {
		t.Errorf("expected slip.status=%q (primaryFailures checked before prod_steady_state), got %q%s",
			SlipStatusFailed, status, stateMachineRef)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// I4: Completion is immutable
// ─────────────────────────────────────────────────────────────────────────────

// TestStateMachine_I4_CompletedSlipIsImmutable verifies that once slip=completed,
// subsequent FailStep calls do NOT change slip.status.
// Precondition: slip=completed
// Action:       FailStep(some_step)
// Expected:     slip.status remains completed
func TestStateMachine_I4_CompletedSlipIsImmutable(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i4-completed-immutable"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i4-immutable",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusCompleted,
		Steps: map[string]Step{
			"prod_steady_state": {Status: StepStatusCompleted},
			"some_step":         {Status: StepStatusRunning},
		},
	}
	store.AddSlip(slip)

	// FailStep triggers checkPipelineCompletion; the completed short-circuit must fire
	if err := client.FailStep(ctx, corrID, "some_step", "", "late failure"); err != nil {
		t.Fatalf("FailStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusCompleted {
		t.Errorf("expected slip.status=%q to remain immutable after FailStep, got %q%s",
			SlipStatusCompleted, loaded.Status, stateMachineRef)
	}
}

// TestStateMachine_I4_CompletedSlipIgnoresRecoveryAttempts verifies that calling
// checkPipelineCompletion on an already-completed slip with a failed step does not
// change its status — the completed short-circuit fires first.
// Precondition: slip=completed, some_step=failed in mock steps
// Action:       checkPipelineCompletion directly
// Expected:     slip.status remains completed
func TestStateMachine_I4_CompletedSlipIgnoresRecoveryAttempts(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i4-completed-no-recovery"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i4-norecovery",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusCompleted,
		Steps: map[string]Step{
			"prod_steady_state": {Status: StepStatusCompleted},
			"some_step":         {Status: StepStatusFailed},
		},
	}
	store.AddSlip(slip)

	completed, status, err := client.checkPipelineCompletion(ctx, corrID)
	if err != nil {
		t.Fatalf("checkPipelineCompletion returned unexpected error: %v%s", err, stateMachineRef)
	}
	// The function short-circuits immediately when slip.Status == completed;
	// it returns (true, SlipStatusCompleted, nil) from the early-return path.
	_ = completed
	_ = status

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}
	if loaded.Status != SlipStatusCompleted {
		t.Errorf("expected slip.status=%q to remain immutable, got %q%s",
			SlipStatusCompleted, loaded.Status, stateMachineRef)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// I5: Materialization consistency — step columns must match event-log-derived status
// ─────────────────────────────────────────────────────────────────────────────
//
// I5 states: routing_slips.<step>_status columns must always reflect the authoritative
// status derived from the event log (argMax(status, timestamp) FROM slip_component_states).
// A write-path bug can cause a stale clone of the prior row to overwrite the just-written
// step status when the new row is not yet visible (ClickHouse async insert visibility gap
// under VersionedCollapsingMergeTree without FINAL). The fix (bd issue goLibMyCarrier-nl3)
// is to pass stepStatusOverride literals into insertAtomicStatusUpdate via
// updateSlipStatusWithStepOverrides, so the INSERT SELECT uses the authoritative value
// rather than cloning from a potentially-stale row.
//
// MOCK CAVEAT: MockStore does not implement slipStatusOverrideWriter, so
// updateSlipStatusWithStepOverrides falls back to the standard UpdateSlipStatus path
// (which is correct for the in-memory case — there is no async insert visibility gap
// in a synchronous map). These tests therefore validate the WIRING CONTRACT: that
// checkPipelineCompletion computes the correct final state and that override parameters
// are constructed and threaded correctly. The actual stale-visibility race cannot be
// reproduced with mocks. For the end-to-end race test, see:
//   slippy/e2e_integration_test.go: TestE2E_ConcurrentTerminalStepEvents_RoutingSlipsMatchesEventLog
//   (build tag: integration)
//
// References: .github/STATE_MACHINE_V3.md §Invariants, bd issue goLibMyCarrier-nl3.

// TestStateMachine_I5_AtomicStatusUpdateRespectsStepOverride verifies that when
// checkPipelineCompletion is called after a primary step failure, the failing step's
// status is correctly reflected in the slip and the pipeline is marked failed.
//
// This is the single-step variant: one primary failure, one other running step.
// It validates that the override path in checkPipelineCompletion writes the correct
// slip.status and that the failing step's status is preserved in the resulting state.
//
// Precondition: slip=in_progress, prod_alert_gate=running, prod_rollback=running
// Action:       FailStep(prod_alert_gate) — triggers checkPipelineCompletion
// Expected:     slip.status == failed, Steps["prod_alert_gate"].Status == failed
func TestStateMachine_I5_AtomicStatusUpdateRespectsStepOverride(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i5-override-contract"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i5-override",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"prod_alert_gate": {Status: StepStatusRunning},
			"prod_rollback":   {Status: StepStatusRunning},
		},
	}
	store.AddSlip(slip)

	// FailStep writes prod_alert_gate=failed then calls checkPipelineCompletion.
	// checkPipelineCompletion detects the primary failure and calls
	// updateSlipStatusWithStepOverrides(ctx, corrID, SlipStatusFailed,
	//   stepStatusOverride{columnName: "prod_alert_gate_status", status: failed}).
	// MockStore does not implement slipStatusOverrideWriter, so the fallback
	// UpdateSlipStatus path fires — the contract under test is that slip.status
	// ends up as failed and the step column value is preserved correctly.
	if err := client.FailStep(ctx, corrID, "prod_alert_gate", "", "alert threshold breached"); err != nil {
		t.Fatalf("FailStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	loaded, err := store.Load(ctx, corrID)
	if err != nil {
		t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
	}

	// I5 contract (wiring): checkPipelineCompletion must pass the failing step's
	// override into updateSlipStatusWithStepOverrides. Behavioral proof: the slip
	// status must be failed, and the failing step column must retain its failed value.
	if loaded.Status != SlipStatusFailed {
		t.Errorf("expected slip.status=%q after primary step failure, got %q%s",
			SlipStatusFailed, loaded.Status, stateMachineRef)
	}

	alertGate, ok := loaded.Steps["prod_alert_gate"]
	if !ok {
		t.Fatalf("prod_alert_gate step not found after FailStep%s", stateMachineRef)
	}
	if alertGate.Status != StepStatusFailed {
		t.Errorf("expected prod_alert_gate.status=%q (override must not revert it), got %q%s",
			StepStatusFailed, alertGate.Status, stateMachineRef)
	}

	// The other running step must remain running — only the primary failure drives slip.status.
	rollback, ok := loaded.Steps["prod_rollback"]
	if !ok {
		t.Fatalf("prod_rollback step not found%s", stateMachineRef)
	}
	if rollback.Status != StepStatusRunning {
		t.Errorf("expected prod_rollback.status=%q (untouched), got %q%s",
			StepStatusRunning, rollback.Status, stateMachineRef)
	}
}

// TestStateMachine_I5_StaleStepColumnNotPropagated verifies that sequential terminal
// step events do not propagate a stale value for an earlier step's column. Each call
// to checkPipelineCompletion must pass the correct set of step-column overrides so that
// the authoritative status of every primary-failure step is preserved regardless of the
// order in which terminal events arrive.
//
// MOCK CAVEAT: MockStore is synchronous; the stale-visibility race (ClickHouse async
// insert gap) cannot manifest here. This test validates the contract: every
// checkPipelineCompletion invocation must compute overrides for ALL current primary
// failures, not just the step that triggered the call. Failure to include earlier steps
// in the override set would, in a real ClickHouse backend, allow those columns to be
// overwritten with a stale (pre-failure) value. See goLibMyCarrier-nl3 and
// TestE2E_ConcurrentTerminalStepEvents_RoutingSlipsMatchesEventLog (integration tag).
//
// Precondition: slip=in_progress, prod_alert_gate=running, prod_rollback=running,
//               prod_steady_state=running
// Actions (sequential):
//   1. FailStep(prod_alert_gate)     → slip=failed, one primary failure
//   2. CompleteStep(prod_rollback)   → checkPipelineCompletion re-runs; prod_alert_gate
//                                      still a primary failure, must remain in overrides
//   3. FailStep(prod_steady_state)   → second primary failure added
// Expected after all three actions:
//   - slip.status == failed
//   - prod_alert_gate.status == failed
//   - prod_rollback.status == completed
//   - prod_steady_state.status == failed
func TestStateMachine_I5_StaleStepColumnNotPropagated(t *testing.T) {
	ctx := context.Background()

	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{})

	corrID := "i5-stale-column"
	slip := &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-i5-stale",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"prod_alert_gate":   {Status: StepStatusRunning},
			"prod_rollback":     {Status: StepStatusRunning},
			"prod_steady_state": {Status: StepStatusRunning},
		},
	}
	store.AddSlip(slip)

	t.Run("step1_FailProdAlertGate", func(t *testing.T) {
		if err := client.FailStep(ctx, corrID, "prod_alert_gate", "", "alert triggered"); err != nil {
			t.Fatalf("FailStep(prod_alert_gate) unexpected error: %v%s", err, stateMachineRef)
		}
		loaded, err := store.Load(ctx, corrID)
		if err != nil {
			t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
		}
		if loaded.Status != SlipStatusFailed {
			t.Errorf("after FailStep(prod_alert_gate): expected slip.status=%q, got %q%s",
				SlipStatusFailed, loaded.Status, stateMachineRef)
		}
		if loaded.Steps["prod_alert_gate"].Status != StepStatusFailed {
			t.Errorf("after FailStep(prod_alert_gate): expected prod_alert_gate.status=%q, got %q%s",
				StepStatusFailed, loaded.Steps["prod_alert_gate"].Status, stateMachineRef)
		}
	})

	t.Run("step2_CompleteProdRollback", func(t *testing.T) {
		// CompleteStep triggers checkPipelineCompletion again. prod_alert_gate is still a
		// primary failure. The override set passed into updateSlipStatusWithStepOverrides
		// must include prod_alert_gate_status=failed. In the real ClickHouse backend,
		// omitting it would allow the INSERT SELECT to clone a stale (running) value.
		if err := client.CompleteStep(ctx, corrID, "prod_rollback", ""); err != nil {
			t.Fatalf("CompleteStep(prod_rollback) unexpected error: %v%s", err, stateMachineRef)
		}
		loaded, err := store.Load(ctx, corrID)
		if err != nil {
			t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
		}
		// Pipeline must stay failed — prod_alert_gate is still a primary failure.
		if loaded.Status != SlipStatusFailed {
			t.Errorf("after CompleteStep(prod_rollback): expected slip.status=%q, got %q%s",
				SlipStatusFailed, loaded.Status, stateMachineRef)
		}
		// prod_alert_gate must not have reverted to a stale value.
		if loaded.Steps["prod_alert_gate"].Status != StepStatusFailed {
			t.Errorf("after CompleteStep(prod_rollback): prod_alert_gate reverted — expected %q, got %q%s",
				StepStatusFailed, loaded.Steps["prod_alert_gate"].Status, stateMachineRef)
		}
		if loaded.Steps["prod_rollback"].Status != StepStatusCompleted {
			t.Errorf("after CompleteStep(prod_rollback): expected prod_rollback.status=%q, got %q%s",
				StepStatusCompleted, loaded.Steps["prod_rollback"].Status, stateMachineRef)
		}
	})

	t.Run("step3_FailProdSteadyState", func(t *testing.T) {
		if err := client.FailStep(ctx, corrID, "prod_steady_state", "", "steady state check failed"); err != nil {
			t.Fatalf("FailStep(prod_steady_state) unexpected error: %v%s", err, stateMachineRef)
		}
		loaded, err := store.Load(ctx, corrID)
		if err != nil {
			t.Fatalf("failed to load slip: %v%s", err, stateMachineRef)
		}

		// Final state assertions — all three step columns must reflect authoritative values.
		if loaded.Status != SlipStatusFailed {
			t.Errorf("final: expected slip.status=%q, got %q%s",
				SlipStatusFailed, loaded.Status, stateMachineRef)
		}
		if loaded.Steps["prod_alert_gate"].Status != StepStatusFailed {
			t.Errorf("final: prod_alert_gate expected %q, got %q%s",
				StepStatusFailed, loaded.Steps["prod_alert_gate"].Status, stateMachineRef)
		}
		if loaded.Steps["prod_rollback"].Status != StepStatusCompleted {
			t.Errorf("final: prod_rollback expected %q, got %q%s",
				StepStatusCompleted, loaded.Steps["prod_rollback"].Status, stateMachineRef)
		}
		if loaded.Steps["prod_steady_state"].Status != StepStatusFailed {
			t.Errorf("final: prod_steady_state expected %q, got %q%s",
				StepStatusFailed, loaded.Steps["prod_steady_state"].Status, stateMachineRef)
		}
	})
}
