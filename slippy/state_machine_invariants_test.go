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
