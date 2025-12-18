package slippy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClient_WaitForPrerequisites(t *testing.T) {
	ctx := context.Background()

	t.Run("prerequisites already met", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  1 * time.Second,
			PollInterval: 100 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-wait-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "wait1",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-wait-1",
			Prerequisites: []string{"push_parsed", "builds_completed"},
		}

		err := client.WaitForPrerequisites(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty prerequisites", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		opts := HoldOptions{
			CorrelationID: "corr-wait-2",
			Prerequisites: []string{},
		}

		err := client.WaitForPrerequisites(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("prerequisite fails immediately", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  1 * time.Second,
			PollInterval: 100 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-wait-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "wait3",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-wait-3",
			Prerequisites: []string{"push_parsed", "builds_completed"},
		}

		err := client.WaitForPrerequisites(ctx, opts)
		if err == nil {
			t.Fatal("expected error for failed prerequisite")
		}
		if !errors.Is(err, ErrPrerequisiteFailed) {
			t.Errorf("expected ErrPrerequisiteFailed, got: %v", err)
		}
	})

	t.Run("timeout while waiting", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  200 * time.Millisecond,
			PollInterval: 50 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-wait-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "wait4",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusRunning}, // Never completes
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-wait-4",
			Prerequisites: []string{"push_parsed", "builds_completed"},
		}

		start := time.Now()
		err := client.WaitForPrerequisites(ctx, opts)
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("expected timeout error")
		}
		if !errors.Is(err, ErrHoldTimeout) {
			t.Errorf("expected ErrHoldTimeout, got: %v", err)
		}
		// Should have waited at least the timeout duration
		if elapsed < 200*time.Millisecond {
			t.Errorf("expected to wait at least 200ms, waited %v", elapsed)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  5 * time.Second,
			PollInterval: 100 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-wait-5",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "wait5",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"builds_completed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		cancelCtx, cancel := context.WithCancel(ctx)

		// Cancel after a short delay
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()

		opts := HoldOptions{
			CorrelationID: "corr-wait-5",
			Prerequisites: []string{"builds_completed"},
		}

		err := client.WaitForPrerequisites(cancelCtx, opts)
		if err == nil {
			t.Fatal("expected error from context cancellation")
		}
		if !errors.Is(err, ErrContextCancelled) {
			t.Errorf("expected ErrContextCancelled, got: %v", err)
		}
	})

	t.Run("sets held status when step name provided", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  1 * time.Second,
			PollInterval: 100 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-wait-6",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "wait6",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"builds_completed": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-wait-6",
			Prerequisites: []string{"builds_completed"},
			StepName:      "dev_deploy",
		}

		err := client.WaitForPrerequisites(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have set held status initially
		var foundHeldCall bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "dev_deploy" && call.Status == StepStatusHeld {
				foundHeldCall = true
				break
			}
		}
		if !foundHeldCall {
			t.Error("expected held status to be set for step")
		}
	})

	t.Run("timeout updates step to timeout status", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  150 * time.Millisecond,
			PollInterval: 50 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-wait-7",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "wait7",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"builds_completed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-wait-7",
			Prerequisites: []string{"builds_completed"},
			StepName:      "dev_deploy",
		}

		err := client.WaitForPrerequisites(ctx, opts)
		if err == nil {
			t.Fatal("expected timeout error")
		}

		// Should have set timeout status
		var foundTimeoutCall bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "dev_deploy" && call.Status == StepStatusTimeout {
				foundTimeoutCall = true
				break
			}
		}
		if !foundTimeoutCall {
			t.Error("expected timeout status to be set for step")
		}
	})

	t.Run("uses custom timeout and poll interval", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  5 * time.Second, // Default
			PollInterval: 1 * time.Second, // Default
		})

		slip := &Slip{
			CorrelationID: "corr-wait-8",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "wait8",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"builds_completed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-wait-8",
			Prerequisites: []string{"builds_completed"},
			Timeout:       100 * time.Millisecond, // Override
			PollInterval:  25 * time.Millisecond,  // Override
		}

		start := time.Now()
		err := client.WaitForPrerequisites(ctx, opts)
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("expected timeout error")
		}
		// Should timeout around 100ms, not 5s
		if elapsed > 500*time.Millisecond {
			t.Errorf("expected timeout around 100ms, took %v", elapsed)
		}
	})
}

func TestClient_WaitForPrerequisitesWithResult(t *testing.T) {
	ctx := context.Background()

	t.Run("proceeded", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  1 * time.Second,
			PollInterval: 100 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-result-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "result1",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-result-1",
			Prerequisites: []string{"push_parsed"},
		}

		result, err := client.WaitForPrerequisitesWithResult(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Outcome != HoldOutcomeProceeded {
			t.Errorf("expected outcome 'proceeded', got '%s'", result.Outcome)
		}
		if result.WaitedFor <= 0 {
			t.Error("expected WaitedFor to be positive")
		}
		if result.Message != "all prerequisites completed" {
			t.Errorf("unexpected message: %s", result.Message)
		}
	})

	t.Run("failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  1 * time.Second,
			PollInterval: 100 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-result-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "result2",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-result-2",
			Prerequisites: []string{"push_parsed"},
		}

		result, err := client.WaitForPrerequisitesWithResult(ctx, opts)
		if err == nil {
			t.Fatal("expected error")
		}

		if result.Outcome != HoldOutcomeFailed {
			t.Errorf("expected outcome 'failed', got '%s'", result.Outcome)
		}
		if len(result.FailedPrereqs) != 1 {
			t.Errorf("expected 1 failed prereq, got %d", len(result.FailedPrereqs))
		}
	})

	t.Run("timeout", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  100 * time.Millisecond,
			PollInterval: 25 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-result-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "result3",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		opts := HoldOptions{
			CorrelationID: "corr-result-3",
			Prerequisites: []string{"push_parsed"},
		}

		result, err := client.WaitForPrerequisitesWithResult(ctx, opts)
		if err == nil {
			t.Fatal("expected error")
		}

		if result.Outcome != HoldOutcomeTimeout {
			t.Errorf("expected outcome 'timeout', got '%s'", result.Outcome)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:  5 * time.Second,
			PollInterval: 100 * time.Millisecond,
		})

		slip := &Slip{
			CorrelationID: "corr-result-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "result4",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		cancelCtx, cancel := context.WithCancel(ctx)
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		opts := HoldOptions{
			CorrelationID: "corr-result-4",
			Prerequisites: []string{"push_parsed"},
		}

		result, err := client.WaitForPrerequisitesWithResult(cancelCtx, opts)
		if err == nil {
			t.Fatal("expected error")
		}

		if result.Outcome != HoldOutcomeCancelled {
			t.Errorf("expected outcome 'cancelled', got '%s'", result.Outcome)
		}
	})
}

func TestHoldOutcome_Values(t *testing.T) {
	tests := []struct {
		outcome HoldOutcome
		want    string
	}{
		{HoldOutcomeProceeded, "proceeded"},
		{HoldOutcomeFailed, "failed"},
		{HoldOutcomeTimeout, "timeout"},
		{HoldOutcomeCancelled, "cancelled"},
	}

	for _, tt := range tests {
		if string(tt.outcome) != tt.want {
			t.Errorf("HoldOutcome %s: expected '%s', got '%s'", tt.want, tt.want, string(tt.outcome))
		}
	}
}

func TestErrorHelpers(t *testing.T) {
	t.Run("isTimeoutError", func(t *testing.T) {
		if !isTimeoutError(errors.New("hold timeout exceeded")) {
			t.Error("expected timeout error to be detected")
		}
		if isTimeoutError(errors.New("some other error")) {
			t.Error("expected non-timeout error to not be detected")
		}
		if isTimeoutError(nil) {
			t.Error("expected nil to return false")
		}
	})

	t.Run("isPrereqFailedError", func(t *testing.T) {
		if !isPrereqFailedError(errors.New("prerequisite failed")) {
			t.Error("expected prereq error to be detected")
		}
		if isPrereqFailedError(errors.New("some other error")) {
			t.Error("expected non-prereq error to not be detected")
		}
		if isPrereqFailedError(nil) {
			t.Error("expected nil to return false")
		}
	})

	t.Run("isContextError", func(t *testing.T) {
		if !isContextError(errors.New("operation cancelled")) {
			t.Error("expected context error to be detected")
		}
		if isContextError(errors.New("some other error")) {
			t.Error("expected non-context error to not be detected")
		}
		if isContextError(nil) {
			t.Error("expected nil to return false")
		}
	})
}
