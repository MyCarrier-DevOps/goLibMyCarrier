package slippy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClient_CompleteStep(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-complete-1")
		store.AddSlip(slip)

		err := client.CompleteStep(ctx, "corr-complete-1", "dev_deploy", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify UpdateStep was called with correct status
		if len(store.UpdateStepCalls) == 0 {
			t.Fatal("expected UpdateStep to be called")
		}
		call := store.UpdateStepCalls[0]
		if call.CorrelationID != "corr-complete-1" {
			t.Errorf("expected correlation_id 'corr-complete-1', got '%s'", call.CorrelationID)
		}
		if call.StepName != "dev_deploy" {
			t.Errorf("expected step name 'dev_deploy', got '%s'", call.StepName)
		}
		if call.Status != StepStatusCompleted {
			t.Errorf("expected status 'completed', got '%s'", call.Status)
		}

		// Verify history was appended
		if len(store.AppendHistoryCalls) == 0 {
			t.Error("expected history to be appended")
		}
	})

	t.Run("with component name", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-complete-2")
		store.AddSlip(slip)

		err := client.CompleteStep(ctx, "corr-complete-2", "build", "my-service")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.ComponentName != "my-service" {
			t.Errorf("expected component name 'my-service', got '%s'", call.ComponentName)
		}
	})

	t.Run("store error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-complete-3")
		store.AddSlip(slip)
		store.UpdateStepError = errors.New("update failed")

		err := client.CompleteStep(ctx, "corr-complete-3", "dev_deploy", "")
		if err == nil {
			t.Fatal("expected error")
		}

		var stepErr *StepError
		if !errors.As(err, &stepErr) {
			t.Fatalf("expected StepError, got %T", err)
		}
	})
}

func TestClient_FailStep(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-fail-1")
		store.AddSlip(slip)

		err := client.FailStep(ctx, "corr-fail-1", "dev_tests", "", "test assertion failed")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.Status != StepStatusFailed {
			t.Errorf("expected status 'failed', got '%s'", call.Status)
		}

		// Verify reason is recorded in history
		historyCall := store.AppendHistoryCalls[0]
		if historyCall.Entry.Message != "test assertion failed" {
			t.Errorf("expected message 'test assertion failed', got '%s'", historyCall.Entry.Message)
		}
	})
}

func TestClient_StartStep(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-start-1")
		store.AddSlip(slip)

		err := client.StartStep(ctx, "corr-start-1", "preprod_deploy", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.Status != StepStatusRunning {
			t.Errorf("expected status 'running', got '%s'", call.Status)
		}
	})
}

func TestClient_HoldStep(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-hold-1")
		store.AddSlip(slip)

		err := client.HoldStep(ctx, "corr-hold-1", "prod_deploy", "", "preprod_tests")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.Status != StepStatusHeld {
			t.Errorf("expected status 'held', got '%s'", call.Status)
		}

		historyCall := store.AppendHistoryCalls[0]
		if historyCall.Entry.Message != "waiting for: preprod_tests" {
			t.Errorf("expected message 'waiting for: preprod_tests', got '%s'", historyCall.Entry.Message)
		}
	})
}

func TestClient_AbortStep(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-abort-1")
		store.AddSlip(slip)

		err := client.AbortStep(ctx, "corr-abort-1", "prod_deploy", "", "upstream failure")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.Status != StepStatusAborted {
			t.Errorf("expected status 'aborted', got '%s'", call.Status)
		}
	})
}

func TestClient_TimeoutStep(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-timeout-1")
		store.AddSlip(slip)

		err := client.TimeoutStep(ctx, "corr-timeout-1", "dev_tests", "", "exceeded 30m limit")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.Status != StepStatusTimeout {
			t.Errorf("expected status 'timeout', got '%s'", call.Status)
		}
	})
}

func TestClient_SkipStep(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-skip-1")
		store.AddSlip(slip)

		err := client.SkipStep(ctx, "corr-skip-1", "alert_gate", "", "no alerts configured")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.Status != StepStatusSkipped {
			t.Errorf("expected status 'skipped', got '%s'", call.Status)
		}
	})
}

func TestClient_UpdateStepWithStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("combined step and history update error returns error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-update-1")
		store.AddSlip(slip)
		// Since UpdateStepWithHistory is now a combined operation, we inject error via UpdateStepError
		store.UpdateStepError = errors.New("combined update failed")

		// Combined operation returns error
		err := client.UpdateStepWithStatus(ctx, "corr-update-1", "dev_deploy", "", StepStatusCompleted, "done")
		if err == nil {
			t.Fatal("expected error for combined update failure")
		}
		// Error is wrapped by NewStepError
		if !strings.Contains(err.Error(), "combined update failed") {
			t.Errorf("expected error to contain 'combined update failed', got: %v", err)
		}
	})
}

// TestClient_UpdateStepWithStatus_SingleStoreCallPerUpdate is a regression test verifying that
// the client no longer performs a second store load+update via checkAndUpdateAggregate after
// each component step update. The aggregate status is now computed entirely within the store
// (via updateAggregateStatusFromComponentStatesWithHistory), so the client should issue exactly
// one UpdateStepWithHistory call per UpdateStepWithStatus invocation.
func TestClient_UpdateStepWithStatus_SingleStoreCallPerUpdate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		stepName      string
		componentName string
		status        StepStatus
	}{
		{
			name:          "component build step - only one store call",
			stepName:      "build",
			componentName: "svc-a",
			status:        StepStatusCompleted,
		},
		{
			name:          "pure pipeline step - only one store call",
			stepName:      "push_parsed",
			componentName: "",
			status:        StepStatusCompleted,
		},
		{
			name:          "aggregate step - only one store call",
			stepName:      "builds_completed",
			componentName: "",
			status:        StepStatusCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMockStore()
			github := NewMockGitHubAPI()
			config := testPipelineConfig()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

			slip := createTestSlip("corr-single-call")
			store.AddSlip(slip)

			err := client.UpdateStepWithStatus(
				ctx,
				"corr-single-call",
				tt.stepName,
				tt.componentName,
				tt.status,
				"test msg",
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Exactly one UpdateStepWithHistory call must be made: no client-side
			// aggregate re-check or second load+update cycle.
			if len(store.UpdateStepCalls) != 1 {
				t.Fatalf("expected exactly 1 UpdateStepWithHistory call, got %d\n"+
					"calls: %+v", len(store.UpdateStepCalls), store.UpdateStepCalls)
			}
			if store.UpdateStepCalls[0].StepName != tt.stepName {
				t.Errorf("expected step %q, got %q", tt.stepName, store.UpdateStepCalls[0].StepName)
			}
			if store.UpdateStepCalls[0].Status != tt.status {
				t.Errorf("expected status %q, got %q", tt.status, store.UpdateStepCalls[0].Status)
			}
		})
	}
}

func TestClient_UpdateComponentBuildStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-comp-build-1")
		store.AddSlip(slip)

		err := client.UpdateComponentBuildStatus(
			ctx,
			"corr-comp-build-1",
			"my-service",
			StepStatusCompleted,
			"build succeeded",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.StepName != "build" {
			t.Errorf("expected step name 'build', got '%s'", call.StepName)
		}
		if call.ComponentName != "my-service" {
			t.Errorf("expected component name 'my-service', got '%s'", call.ComponentName)
		}
	})
}

func TestClient_UpdateComponentTestStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-comp-test-1")
		store.AddSlip(slip)

		err := client.UpdateComponentTestStatus(ctx, "corr-comp-test-1", "my-service", StepStatusFailed, "test failed")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		call := store.UpdateStepCalls[0]
		if call.StepName != "unit_test" {
			t.Errorf("expected step name 'unit_test', got '%s'", call.StepName)
		}
		if call.ComponentName != "my-service" {
			t.Errorf("expected component name 'my-service', got '%s'", call.ComponentName)
		}
	})
}

func TestClient_SetComponentImageTag(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Get the aggregate step name from config for "build" component type
		buildAggregateStep := config.GetAggregateStep("build")
		if buildAggregateStep == "" {
			t.Fatal("expected config to have an aggregate step for 'build'")
		}

		slip := &Slip{
			CorrelationID: "corr-img-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "img123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				buildAggregateStep: {
					{Component: "my-service"},
				},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.SetComponentImageTag(ctx, "corr-img-1", "my-service", "mycarrier/my-service:abc123-1234567890")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the event was recorded via SetComponentImageTag on the store.
		if len(store.SetImageTagCalls) != 1 {
			t.Fatalf("expected SetComponentImageTag to be called once, got %d", len(store.SetImageTagCalls))
		}
		call := store.SetImageTagCalls[0]
		if call.CorrelationID != "corr-img-1" {
			t.Errorf("expected correlationID 'corr-img-1', got %q", call.CorrelationID)
		}
		if call.ComponentName != "my-service" {
			t.Errorf("expected componentName 'my-service', got %q", call.ComponentName)
		}
		if call.ImageTag != "mycarrier/my-service:abc123-1234567890" {
			t.Errorf("expected imageTag 'mycarrier/my-service:abc123-1234567890', got %q", call.ImageTag)
		}
		// Verify the in-memory slip was updated by the mock.
		updatedSlip, err := store.Load(ctx, "corr-img-1")
		if err != nil {
			t.Fatalf("failed to load updated slip: %v", err)
		}
		if updatedSlip.Aggregates[buildAggregateStep][0].ImageTag != "mycarrier/my-service:abc123-1234567890" {
			t.Errorf(
				"expected image tag in slip aggregates, got %q",
				updatedSlip.Aggregates[buildAggregateStep][0].ImageTag,
			)
		}
	})

	t.Run("component not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Get the aggregate step name from config for "build" component type
		buildAggregateStep := config.GetAggregateStep("build")
		if buildAggregateStep == "" {
			t.Fatal("expected config to have an aggregate step for 'build'")
		}

		slip := &Slip{
			CorrelationID: "corr-img-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "img124",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				buildAggregateStep: {
					{Component: "other-service"},
				},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.SetComponentImageTag(ctx, "corr-img-2", "nonexistent", "some:tag")
		if err == nil {
			t.Fatal("expected error for nonexistent component")
		}

		var stepErr *StepError
		if !errors.As(err, &stepErr) {
			t.Fatalf("expected StepError, got %T", err)
		}
	})

	t.Run("slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		err := client.SetComponentImageTag(ctx, "nonexistent", "my-service", "some:tag")
		if err == nil {
			t.Fatal("expected error for nonexistent slip")
		}

		var stepErr *StepError
		if !errors.As(err, &stepErr) {
			t.Fatalf("expected StepError, got %T", err)
		}
	})
}

// createTestSlip creates a basic test slip with initialized fields.
func createTestSlip(correlationID string) *Slip {
	now := time.Now()
	return &Slip{
		CorrelationID: correlationID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     correlationID + "-sha",
		CreatedAt:     now,
		UpdatedAt:     now,
		Status:        SlipStatusInProgress,
		Aggregates:    map[string][]ComponentStepData{},
		Steps:         make(map[string]Step),
		StateHistory:  []StateHistoryEntry{},
	}
}

// TestClient_AggregateBuildFailurePropagatesSlipFailed verifies the end-to-end rule:
// a failing build component causes the aggregate "builds" step to become failed,
// which in turn drives slip.status → failed.
//
// State machine reference: .github/STATE_MACHINE_V3.md §Lifecycle:
//
//	"Aggregate builds: any single component primary failure → aggregate failed →
//	 slip=failed. Aggregate completed only when all components terminal-success."
//
// The mock store does not perform ClickHouse-side aggregate rollup
// (updateAggregateStatusFromComponentStatesWithHistory), so this test exercises the
// pipeline-step path directly: once the store rolls up a component failure into the
// aggregate step status, executor calls FailStep("builds", "", reason) at the
// pipeline-step level (componentName == ""), which triggers checkPipelineCompletion
// and sets slip.status = failed.
func TestClient_AggregateBuildFailurePropagatesSlipFailed(t *testing.T) {
	ctx := context.Background()

	t.Run("single component failure sets builds=failed and slip=failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Seed a slip with three build components: two running, one will fail.
		// "builds" is the aggregate pipeline step; "build" is the component type.
		slip := &Slip{
			CorrelationID: "corr-agg-build-fail",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-agg-build-fail",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"builds": {Status: StepStatusRunning},
			},
			Aggregates: map[string][]ComponentStepData{
				"builds": {
					{Component: "api", Status: StepStatusRunning},
					{Component: "worker", Status: StepStatusRunning},
					{Component: "gateway", Status: StepStatusRunning},
				},
			},
		}
		store.AddSlip(slip)

		// Simulate two components completing successfully and one failing.
		// At the component level the mock store records the call but does NOT
		// recompute the aggregate status (that is ClickHouse-store behaviour).
		// We therefore update the in-memory aggregate state manually to match
		// what the production path would produce, then call FailStep on the
		// pipeline-level aggregate step ("builds", componentName="") to trigger
		// checkPipelineCompletion — exactly what the executor does after rollup.
		store.Slips["corr-agg-build-fail"].Aggregates["builds"][0].Status = StepStatusCompleted // api
		store.Slips["corr-agg-build-fail"].Aggregates["builds"][1].Status = StepStatusFailed    // worker
		// gateway remains running (preserved, unchanged by the failure)

		// Production path: aggregate rollup determines builds=failed because
		// at least one component is a primary failure.  Executor then calls
		// FailStep on the pipeline-level "builds" step (componentName == "").
		if err := client.FailStep(ctx, "corr-agg-build-fail", "builds", "", "worker build failed"); err != nil {
			t.Fatalf("FailStep(builds) returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-agg-build-fail")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}

		// Primary assertion: aggregate step "builds" must be failed.
		if loaded.Steps["builds"].Status != StepStatusFailed {
			t.Errorf("expected builds step status %q, got %q",
				StepStatusFailed, loaded.Steps["builds"].Status)
		}

		// Primary assertion: slip.status must be failed (I1 invariant).
		if loaded.Status != SlipStatusFailed {
			t.Errorf("expected slip.status %q after builds failure, got %q",
				SlipStatusFailed, loaded.Status)
		}

		// Secondary assertion: other component states are preserved as-is.
		apiStatus := loaded.Aggregates["builds"][0].Status
		if apiStatus != StepStatusCompleted {
			t.Errorf("expected api component status %q to be preserved, got %q",
				StepStatusCompleted, apiStatus)
		}
		workerStatus := loaded.Aggregates["builds"][1].Status
		if workerStatus != StepStatusFailed {
			t.Errorf("expected worker component status %q to be preserved, got %q",
				StepStatusFailed, workerStatus)
		}
		gatewayStatus := loaded.Aggregates["builds"][2].Status
		if gatewayStatus != StepStatusRunning {
			t.Errorf("expected gateway component status %q to remain unchanged, got %q",
				StepStatusRunning, gatewayStatus)
		}
	})

	t.Run("all components succeed keeps slip in_progress", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		slip := &Slip{
			CorrelationID: "corr-agg-build-ok",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-agg-build-ok",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"builds": {Status: StepStatusRunning},
			},
			Aggregates: map[string][]ComponentStepData{
				"builds": {
					{Component: "api", Status: StepStatusRunning},
					{Component: "worker", Status: StepStatusRunning},
				},
			},
		}
		store.AddSlip(slip)

		// All components succeed → executor calls CompleteStep on the aggregate step.
		if err := client.CompleteStep(ctx, "corr-agg-build-ok", "builds", ""); err != nil {
			t.Fatalf("CompleteStep(builds) returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-agg-build-ok")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}

		if loaded.Steps["builds"].Status != StepStatusCompleted {
			t.Errorf("expected builds step status %q, got %q",
				StepStatusCompleted, loaded.Steps["builds"].Status)
		}
		// slip should remain in_progress (not yet at prod_steady_state)
		if loaded.Status != SlipStatusInProgress {
			t.Errorf("expected slip.status %q after builds success, got %q",
				SlipStatusInProgress, loaded.Status)
		}
	})
}
