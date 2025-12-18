package slippy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClient_RunPreExecution(t *testing.T) {
	ctx := context.Background()

	t.Run("proceed without prerequisites", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth: 20,
		})

		slip := &Slip{
			CorrelationID: "corr-preexec-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)
		github.SetAncestry("owner", "repo", "HEAD", []string{"abc123"})

		result, err := client.RunPreExecution(ctx, PreExecutionOptions{
			Repository:    "owner/repo",
			Branch:        "main",
			Ref:           "HEAD",
			StepName:      "dev_deploy",
			Prerequisites: []string{}, // No prerequisites
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Outcome != PreExecutionOutcomeProceed {
			t.Errorf("expected outcome 'proceed', got '%s'", result.Outcome)
		}
		if result.CorrelationID != "corr-preexec-1" {
			t.Errorf("expected correlation_id 'corr-preexec-1', got '%s'", result.CorrelationID)
		}
		if result.ResolvedBy != "ancestry" {
			t.Errorf("expected resolved_by 'ancestry', got '%s'", result.ResolvedBy)
		}
	})

	t.Run("proceed after prerequisites satisfied", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:   1 * time.Second,
			PollInterval:  100 * time.Millisecond,
			AncestryDepth: 20,
		})

		slip := &Slip{
			CorrelationID: "corr-preexec-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)
		github.SetAncestry("owner", "repo", "HEAD", []string{"abc123"})

		result, err := client.RunPreExecution(ctx, PreExecutionOptions{
			Repository:    "owner/repo",
			Branch:        "main",
			Ref:           "HEAD",
			StepName:      "dev_deploy",
			Prerequisites: []string{"push_parsed", "builds_completed"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Outcome != PreExecutionOutcomeProceed {
			t.Errorf("expected outcome 'proceed', got '%s'", result.Outcome)
		}
	})

	t.Run("abort when prerequisite fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:   1 * time.Second,
			PollInterval:  100 * time.Millisecond,
			AncestryDepth: 20,
		})

		slip := &Slip{
			CorrelationID: "corr-preexec-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusFailed}, // Failed
			},
		}
		store.AddSlip(slip)
		github.SetAncestry("owner", "repo", "HEAD", []string{"abc123"})

		result, err := client.RunPreExecution(ctx, PreExecutionOptions{
			Repository:    "owner/repo",
			Branch:        "main",
			Ref:           "HEAD",
			StepName:      "dev_deploy",
			Prerequisites: []string{"push_parsed", "builds_completed"},
		})
		if err == nil {
			t.Fatal("expected error for failed prerequisite")
		}

		if result.Outcome != PreExecutionOutcomeAbort {
			t.Errorf("expected outcome 'abort', got '%s'", result.Outcome)
		}
	})

	t.Run("timeout waiting for prerequisites", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:   150 * time.Millisecond,
			PollInterval:  50 * time.Millisecond,
			AncestryDepth: 20,
		})

		slip := &Slip{
			CorrelationID: "corr-preexec-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusRunning}, // Never completes
			},
		}
		store.AddSlip(slip)
		github.SetAncestry("owner", "repo", "HEAD", []string{"abc123"})

		result, err := client.RunPreExecution(ctx, PreExecutionOptions{
			Repository:    "owner/repo",
			Branch:        "main",
			Ref:           "HEAD",
			StepName:      "dev_deploy",
			Prerequisites: []string{"push_parsed", "builds_completed"},
		})
		if err == nil {
			t.Fatal("expected error for timeout")
		}

		if result.Outcome != PreExecutionOutcomeTimeout {
			t.Errorf("expected outcome 'timeout', got '%s'", result.Outcome)
		}
	})

	t.Run("no slip found in shadow mode", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			ShadowMode:    true,
			AncestryDepth: 20,
		})

		// No slip exists
		github.SetAncestry("owner", "repo", "HEAD", []string{"nomatch"})

		result, err := client.RunPreExecution(ctx, PreExecutionOptions{
			Repository: "owner/repo",
			Branch:     "main",
			Ref:        "HEAD",
			StepName:   "dev_deploy",
		})
		if err != nil {
			t.Fatalf("unexpected error in shadow mode: %v", err)
		}

		if result.Outcome != PreExecutionOutcomeNoSlip {
			t.Errorf("expected outcome 'no_slip', got '%s'", result.Outcome)
		}
	})

	t.Run("no slip found without shadow mode", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			ShadowMode:    false,
			AncestryDepth: 20,
		})

		// No slip exists
		github.SetAncestry("owner", "repo", "HEAD", []string{"nomatch"})

		_, err := client.RunPreExecution(ctx, PreExecutionOptions{
			Repository: "owner/repo",
			Branch:     "main",
			Ref:        "HEAD",
			StepName:   "dev_deploy",
		})
		if err == nil {
			t.Fatal("expected error when no slip found")
		}
	})

	t.Run("uses custom timeout from options", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			HoldTimeout:   5 * time.Second, // Default long timeout
			PollInterval:  1 * time.Second,
			AncestryDepth: 20,
		})

		slip := &Slip{
			CorrelationID: "corr-preexec-5",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"builds_completed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)
		github.SetAncestry("owner", "repo", "HEAD", []string{"abc123"})

		start := time.Now()
		_, err := client.RunPreExecution(ctx, PreExecutionOptions{
			Repository:    "owner/repo",
			Branch:        "main",
			Ref:           "HEAD",
			StepName:      "dev_deploy",
			Prerequisites: []string{"builds_completed"},
			HoldTimeout:   100 * time.Millisecond, // Custom short timeout
			PollInterval:  25 * time.Millisecond,
		})
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("expected timeout error")
		}
		// Should timeout around 100ms, not 5s
		if elapsed > 500*time.Millisecond {
			t.Errorf("expected quick timeout, took %v", elapsed)
		}
	})
}

func TestClient_RunPostExecution(t *testing.T) {
	ctx := context.Background()

	t.Run("workflow succeeded", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-postexec-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		result, err := client.RunPostExecution(ctx, PostExecutionOptions{
			CorrelationID:     "corr-postexec-1",
			StepName:          "dev_deploy",
			WorkflowSucceeded: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.StepStatus != StepStatusCompleted {
			t.Errorf("expected step status 'completed', got '%s'", result.StepStatus)
		}

		// Verify CompleteStep was called
		var foundComplete bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "dev_deploy" && call.Status == StepStatusCompleted {
				foundComplete = true
				break
			}
		}
		if !foundComplete {
			t.Error("expected dev_deploy to be marked completed")
		}
	})

	t.Run("workflow failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-postexec-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		result, err := client.RunPostExecution(ctx, PostExecutionOptions{
			CorrelationID:     "corr-postexec-2",
			StepName:          "dev_tests",
			WorkflowSucceeded: false,
			FailureMessage:    "tests failed with 5 errors",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.StepStatus != StepStatusFailed {
			t.Errorf("expected step status 'failed', got '%s'", result.StepStatus)
		}

		// Verify FailStep was called
		var foundFailed bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "dev_tests" && call.Status == StepStatusFailed {
				foundFailed = true
				break
			}
		}
		if !foundFailed {
			t.Error("expected dev_tests to be marked failed")
		}
	})

	t.Run("workflow failed with default message", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-postexec-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		result, err := client.RunPostExecution(ctx, PostExecutionOptions{
			CorrelationID:     "corr-postexec-3",
			StepName:          "dev_tests",
			WorkflowSucceeded: false,
			FailureMessage:    "", // Empty - should use default
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.StepStatus != StepStatusFailed {
			t.Errorf("expected step status 'failed', got '%s'", result.StepStatus)
		}
	})

	t.Run("pipeline completed after prod_steady_state", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-postexec-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"prod_steady_state": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		result, err := client.RunPostExecution(ctx, PostExecutionOptions{
			CorrelationID:     "corr-postexec-4",
			StepName:          "prod_steady_state",
			WorkflowSucceeded: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !result.SlipCompleted {
			t.Error("expected slip to be marked completed")
		}
		if result.SlipStatus != SlipStatusCompleted {
			t.Errorf("expected slip status 'completed', got '%s'", result.SlipStatus)
		}
	})

	t.Run("slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		_, err := client.RunPostExecution(ctx, PostExecutionOptions{
			CorrelationID:     "nonexistent",
			StepName:          "dev_deploy",
			WorkflowSucceeded: true,
		})
		if err == nil {
			t.Fatal("expected error for nonexistent slip")
		}
	})
}

func TestClient_CheckPipelineCompletion(t *testing.T) {
	ctx := context.Background()

	t.Run("not complete", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-completion-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":       {Status: StepStatusCompleted},
				"dev_deploy":        {Status: StepStatusRunning},
				"prod_steady_state": {Status: StepStatusPending},
			},
		}
		store.AddSlip(slip)

		completed, status := client.checkPipelineCompletion(ctx, "corr-completion-1")
		if completed {
			t.Error("expected pipeline to not be complete")
		}
		if status != SlipStatusInProgress {
			t.Errorf("expected status 'in_progress', got '%s'", status)
		}
	})

	t.Run("completed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-completion-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"prod_steady_state": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		completed, status := client.checkPipelineCompletion(ctx, "corr-completion-2")
		if !completed {
			t.Error("expected pipeline to be complete")
		}
		if status != SlipStatusCompleted {
			t.Errorf("expected status 'completed', got '%s'", status)
		}
	})

	t.Run("failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-completion-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":       {Status: StepStatusCompleted},
				"builds_completed":  {Status: StepStatusFailed},
				"prod_steady_state": {Status: StepStatusPending},
			},
		}
		store.AddSlip(slip)

		completed, status := client.checkPipelineCompletion(ctx, "corr-completion-3")
		if !completed {
			t.Error("expected pipeline to be marked complete (failed)")
		}
		if status != SlipStatusFailed {
			t.Errorf("expected status 'failed', got '%s'", status)
		}
	})

	t.Run("slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		completed, status := client.checkPipelineCompletion(ctx, "nonexistent")
		if completed {
			t.Error("expected pipeline to not be marked complete for nonexistent slip")
		}
		if status != "" {
			t.Errorf("expected empty status, got '%s'", status)
		}
	})

	t.Run("aborted step marks pipeline failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-completion-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"dev_deploy":        {Status: StepStatusAborted},
				"prod_steady_state": {Status: StepStatusPending},
			},
		}
		store.AddSlip(slip)

		completed, status := client.checkPipelineCompletion(ctx, "corr-completion-4")
		if !completed {
			t.Error("expected pipeline to be marked complete (aborted)")
		}
		if status != SlipStatusFailed {
			t.Errorf("expected status 'failed', got '%s'", status)
		}
	})

	t.Run("timeout step marks pipeline failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-completion-5",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"dev_tests":         {Status: StepStatusTimeout},
				"prod_steady_state": {Status: StepStatusPending},
			},
		}
		store.AddSlip(slip)

		completed, status := client.checkPipelineCompletion(ctx, "corr-completion-5")
		if !completed {
			t.Error("expected pipeline to be marked complete (timeout)")
		}
		if status != SlipStatusFailed {
			t.Errorf("expected status 'failed', got '%s'", status)
		}
	})
}

func TestParsePrerequisites(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []string
		length int
	}{
		{
			name:   "empty string",
			input:  "",
			want:   nil,
			length: 0,
		},
		{
			name:   "single prereq",
			input:  "push_parsed",
			want:   []string{"push_parsed"},
			length: 1,
		},
		{
			name:   "multiple prereqs",
			input:  "push_parsed,builds_completed,dev_deploy",
			want:   []string{"push_parsed", "builds_completed", "dev_deploy"},
			length: 3,
		},
		{
			name:   "with spaces",
			input:  "push_parsed, builds_completed, dev_deploy",
			want:   []string{"push_parsed", "builds_completed", "dev_deploy"},
			length: 3,
		},
		{
			name:   "with extra whitespace",
			input:  "  push_parsed  ,  builds_completed  ",
			want:   []string{"push_parsed", "builds_completed"},
			length: 2,
		},
		{
			name:   "empty entries are skipped",
			input:  "push_parsed,,builds_completed,",
			want:   []string{"push_parsed", "builds_completed"},
			length: 2,
		},
		{
			name:   "only commas",
			input:  ",,,,",
			want:   []string{},
			length: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePrerequisites(tt.input)
			if len(got) != tt.length {
				t.Errorf("ParsePrerequisites(%q) length = %d, want %d", tt.input, len(got), tt.length)
			}
			if tt.want != nil && len(got) > 0 {
				for i, v := range tt.want {
					if i >= len(got) || got[i] != v {
						t.Errorf("ParsePrerequisites(%q)[%d] = %q, want %q", tt.input, i, got[i], v)
					}
				}
			}
		})
	}
}

func TestPreExecutionOutcome_Values(t *testing.T) {
	tests := []struct {
		outcome PreExecutionOutcome
		want    string
	}{
		{PreExecutionOutcomeProceed, "proceed"},
		{PreExecutionOutcomeAbort, "abort"},
		{PreExecutionOutcomeTimeout, "timeout"},
		{PreExecutionOutcomeNoSlip, "no_slip"},
	}

	for _, tt := range tests {
		if string(tt.outcome) != tt.want {
			t.Errorf("PreExecutionOutcome %s: expected '%s', got '%s'", tt.want, tt.want, string(tt.outcome))
		}
	}
}

func TestClient_RunPostExecution_Error(t *testing.T) {
	ctx := context.Background()

	t.Run("complete step error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-posterr-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)
		store.UpdateStepError = errors.New("update step failed")

		_, err := client.RunPostExecution(ctx, PostExecutionOptions{
			CorrelationID:     "corr-posterr-1",
			StepName:          "dev_deploy",
			WorkflowSucceeded: true,
		})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("fail step error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-posterr-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)
		store.UpdateStepError = errors.New("update step failed")

		_, err := client.RunPostExecution(ctx, PostExecutionOptions{
			CorrelationID:     "corr-posterr-2",
			StepName:          "dev_deploy",
			WorkflowSucceeded: false,
			FailureMessage:    "workflow failed",
		})
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
