package slippy

import (
	"context"
	"errors"
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

	t.Run("history append failure is non-fatal", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := createTestSlip("corr-update-1")
		store.AddSlip(slip)
		store.AppendHistoryError = errors.New("history append failed")

		// Should not return error even though history append fails
		err := client.UpdateStepWithStatus(ctx, "corr-update-1", "dev_deploy", "", StepStatusCompleted, "done")
		if err != nil {
			t.Fatalf("expected success (history error is non-fatal), got: %v", err)
		}

		// Verify the step was still updated
		if len(store.UpdateStepCalls) != 1 {
			t.Error("expected step to be updated despite history error")
		}
	})
}

func TestClient_CheckAndUpdateAggregates(t *testing.T) {
	ctx := context.Background()

	t.Run("builds_completed - all success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		slip := &Slip{
			CorrelationID: "corr-agg-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				"builds": {
					{Component: "svc-a", Status: StepStatusCompleted},
					{Component: "svc-b", Status: StepStatusCompleted},
				},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		// Trigger aggregate check via UpdateStepWithStatus with "build" step
		err := client.UpdateStepWithStatus(ctx, "corr-agg-1", "build", "svc-a", StepStatusCompleted, "build done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have two UpdateStep calls: one for "build" and one for "builds_completed"
		var foundBuildsCompleted bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "builds_completed" && call.Status == StepStatusCompleted {
				foundBuildsCompleted = true
				break
			}
		}
		if !foundBuildsCompleted {
			t.Error("expected builds_completed to be updated to completed")
		}
	})

	t.Run("builds_completed - one failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		slip := &Slip{
			CorrelationID: "corr-agg-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc124",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				"builds": {
					{Component: "svc-a", Status: StepStatusCompleted},
					{Component: "svc-b", Status: StepStatusFailed},
				},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.UpdateStepWithStatus(ctx, "corr-agg-2", "build", "svc-a", StepStatusCompleted, "build done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// builds_completed should be failed
		var foundBuildsCompleted bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "builds_completed" && call.Status == StepStatusFailed {
				foundBuildsCompleted = true
				break
			}
		}
		if !foundBuildsCompleted {
			t.Error("expected builds_completed to be updated to failed")
		}
	})

	t.Run("builds_completed - still running", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		slip := &Slip{
			CorrelationID: "corr-agg-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc125",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				"builds": {
					{Component: "svc-a", Status: StepStatusCompleted},
					{Component: "svc-b", Status: StepStatusRunning}, // Still running
				},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.UpdateStepWithStatus(ctx, "corr-agg-3", "build", "svc-a", StepStatusCompleted, "build done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// builds_completed should NOT be updated (only 1 call for "build" step)
		var buildsCompletedUpdated bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "builds_completed" {
				buildsCompletedUpdated = true
				break
			}
		}
		if buildsCompletedUpdated {
			t.Error("expected builds_completed to NOT be updated while builds still running")
		}
	})

	t.Run("unit_tests_completed aggregate", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		slip := &Slip{
			CorrelationID: "corr-agg-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc126",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				"unit_tests": {
					{Component: "svc-a", Status: StepStatusCompleted},
					{Component: "svc-b", Status: StepStatusCompleted},
				},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.UpdateStepWithStatus(ctx, "corr-agg-4", "unit_test", "svc-a", StepStatusCompleted, "tests done")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should update unit_tests_completed
		var foundUnitTestsCompleted bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "unit_tests_completed" && call.Status == StepStatusCompleted {
				foundUnitTestsCompleted = true
				break
			}
		}
		if !foundUnitTestsCompleted {
			t.Error("expected unit_tests_completed to be updated to completed")
		}
	})
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

		slip := &Slip{
			CorrelationID: "corr-img-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "img123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				"builds": {
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

		// Verify the update
		if len(store.UpdateCalls) != 1 {
			t.Fatal("expected Update to be called")
		}
		updatedSlip := store.UpdateCalls[0].Slip
		if updatedSlip.Aggregates["builds"][0].ImageTag != "mycarrier/my-service:abc123-1234567890" {
			t.Errorf("expected image tag to be set, got '%s'", updatedSlip.Aggregates["builds"][0].ImageTag)
		}
	})

	t.Run("component not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		slip := &Slip{
			CorrelationID: "corr-img-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "img124",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				"builds": {
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

		var slipErr *SlipError
		if !errors.As(err, &slipErr) {
			t.Fatalf("expected SlipError, got %T", err)
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
