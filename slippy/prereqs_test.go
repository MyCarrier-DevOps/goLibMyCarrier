package slippy

import (
	"context"
	"testing"
	"time"
)

func TestClient_CheckPrerequisites(t *testing.T) {
	ctx := context.Background()

	t.Run("all prerequisites completed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereq1",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusCompleted},
				"dev_deploy":       {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{"push_parsed", "builds_completed"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusCompleted {
			t.Errorf("expected status 'completed', got '%s'", result.Status)
		}
		if len(result.CompletedPrereqs) != 2 {
			t.Errorf("expected 2 completed prereqs, got %d", len(result.CompletedPrereqs))
		}
		if len(result.FailedPrereqs) != 0 {
			t.Errorf("expected 0 failed prereqs, got %d", len(result.FailedPrereqs))
		}
		if len(result.RunningPrereqs) != 0 {
			t.Errorf("expected 0 running prereqs, got %d", len(result.RunningPrereqs))
		}
	})

	t.Run("some prerequisites running", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereq2",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{"push_parsed", "builds_completed"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusRunning {
			t.Errorf("expected status 'running', got '%s'", result.Status)
		}
		if len(result.CompletedPrereqs) != 1 {
			t.Errorf("expected 1 completed prereq, got %d", len(result.CompletedPrereqs))
		}
		if len(result.RunningPrereqs) != 1 {
			t.Errorf("expected 1 running prereq, got %d", len(result.RunningPrereqs))
		}
	})

	t.Run("some prerequisites failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereq3",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{"push_parsed", "builds_completed"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusFailed {
			t.Errorf("expected status 'failed', got '%s'", result.Status)
		}
		if len(result.FailedPrereqs) != 1 {
			t.Errorf("expected 1 failed prereq, got %d", len(result.FailedPrereqs))
		}
		if result.FailedPrereqs[0] != "builds_completed" {
			t.Errorf("expected 'builds_completed' in failed, got '%s'", result.FailedPrereqs[0])
		}
	})

	t.Run("failure takes precedence over running", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereq4",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusRunning}, // Running
				"builds_completed": {Status: StepStatusFailed},  // Failed
			},
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{"push_parsed", "builds_completed"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Failed should take precedence over running
		if result.Status != PrereqStatusFailed {
			t.Errorf("expected status 'failed', got '%s'", result.Status)
		}
	})

	t.Run("skipped counts as success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-5",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereq5",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusSkipped},
			},
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{"push_parsed"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusCompleted {
			t.Errorf("expected status 'completed', got '%s'", result.Status)
		}
	})

	t.Run("empty prerequisites", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-6",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereq6",
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusCompleted {
			t.Errorf("expected status 'completed' for empty prereqs, got '%s'", result.Status)
		}
	})

	t.Run("whitespace-only prerequisites are skipped", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-7",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereq7",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{"  ", "push_parsed", ""}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusCompleted {
			t.Errorf("expected status 'completed', got '%s'", result.Status)
		}
		// Only push_parsed should be counted
		if len(result.CompletedPrereqs) != 1 {
			t.Errorf("expected 1 completed prereq, got %d", len(result.CompletedPrereqs))
		}
	})

	t.Run("component-specific build prerequisite", func(t *testing.T) {
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
			CorrelationID: "corr-prereq-comp-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereqcomp1",
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				buildAggregateStep: {{Component: "my-service", Status: StepStatusCompleted}},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		// Check build prereq for specific component
		result, err := client.CheckPrerequisites(ctx, slip, []string{"build"}, "my-service")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusCompleted {
			t.Errorf("expected status 'completed', got '%s'", result.Status)
		}
	})

	t.Run("component-specific unit_test prerequisite", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Get the aggregate step name from config for "unit_test" component type
		unitTestAggregateStep := config.GetAggregateStep("unit_test")
		if unitTestAggregateStep == "" {
			t.Fatal("expected config to have an aggregate step for 'unit_test'")
		}

		slip := &Slip{
			CorrelationID: "corr-prereq-comp-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prereqcomp2",
			Status:        SlipStatusInProgress,
			Aggregates: map[string][]ComponentStepData{
				unitTestAggregateStep: {{Component: "my-service", Status: StepStatusFailed}},
			},
			Steps: make(map[string]Step),
		}
		store.AddSlip(slip)

		// Check unit_test prereq for specific component
		result, err := client.CheckPrerequisites(ctx, slip, []string{"unit_test"}, "my-service")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Status != PrereqStatusFailed {
			t.Errorf("expected status 'failed', got '%s'", result.Status)
		}
	})

	t.Run("unknown prerequisite returns pending", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-prereq-unknown",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "prerequnk",
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		result, err := client.CheckPrerequisites(ctx, slip, []string{"unknown_step"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Unknown step should be treated as pending/running
		if result.Status != PrereqStatusRunning {
			t.Errorf("expected status 'running', got '%s'", result.Status)
		}
		if len(result.RunningPrereqs) != 1 {
			t.Errorf("expected 1 running prereq, got %d", len(result.RunningPrereqs))
		}
	})
}

func TestClient_AllPrerequisitesMet(t *testing.T) {
	ctx := context.Background()

	t.Run("all met", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-allmet-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "allmet1",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		met, err := client.AllPrerequisitesMet(ctx, "corr-allmet-1", []string{"push_parsed", "builds_completed"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !met {
			t.Error("expected all prerequisites to be met")
		}
	})

	t.Run("not all met", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-allmet-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "allmet2",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		met, err := client.AllPrerequisitesMet(ctx, "corr-allmet-2", []string{"push_parsed", "builds_completed"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if met {
			t.Error("expected prerequisites to NOT be met")
		}
	})

	t.Run("slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		_, err := client.AllPrerequisitesMet(ctx, "nonexistent", []string{"push_parsed"}, "")
		if err == nil {
			t.Fatal("expected error for nonexistent slip")
		}
	})
}

func TestClient_AnyPrerequisiteFailed(t *testing.T) {
	ctx := context.Background()

	t.Run("none failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-anyfail-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "anyfail1",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		failed, failedList, err := client.AnyPrerequisiteFailed(
			ctx,
			"corr-anyfail-1",
			[]string{"push_parsed", "builds_completed"},
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if failed {
			t.Error("expected no failures")
		}
		if len(failedList) != 0 {
			t.Errorf("expected empty failed list, got %v", failedList)
		}
	})

	t.Run("some failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-anyfail-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "anyfail2",
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusFailed},
				"unit_tests":       {Status: StepStatusError},
			},
		}
		store.AddSlip(slip)

		failed, failedList, err := client.AnyPrerequisiteFailed(
			ctx,
			"corr-anyfail-2",
			[]string{"push_parsed", "builds_completed", "unit_tests"},
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !failed {
			t.Error("expected failures")
		}
		if len(failedList) != 2 {
			t.Errorf("expected 2 failures, got %d: %v", len(failedList), failedList)
		}
	})

	t.Run("slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		_, _, err := client.AnyPrerequisiteFailed(ctx, "nonexistent", []string{"push_parsed"}, "")
		if err == nil {
			t.Fatal("expected error for nonexistent slip")
		}
	})
}

func TestClient_GetPendingPrerequisites(t *testing.T) {
	ctx := context.Background()

	t.Run("some pending", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-pending-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "pending1",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusRunning},
				"unit_tests":       {Status: StepStatusPending},
			},
		}
		store.AddSlip(slip)

		pending, err := client.GetPendingPrerequisites(
			ctx,
			"corr-pending-1",
			[]string{"push_parsed", "builds_completed", "unit_tests"},
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// builds_completed (running) and unit_tests (pending) should be pending
		if len(pending) != 2 {
			t.Errorf("expected 2 pending, got %d: %v", len(pending), pending)
		}
	})

	t.Run("none pending", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-pending-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "pending2",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusCompleted},
			},
		}
		store.AddSlip(slip)

		pending, err := client.GetPendingPrerequisites(
			ctx,
			"corr-pending-2",
			[]string{"push_parsed", "builds_completed"},
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(pending) != 0 {
			t.Errorf("expected 0 pending, got %d: %v", len(pending), pending)
		}
	})

	t.Run("includes failed", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-pending-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "pending3",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(slip)

		pending, err := client.GetPendingPrerequisites(
			ctx,
			"corr-pending-3",
			[]string{"push_parsed", "builds_completed"},
			"",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Failed also counts as "pending" (not completed)
		if len(pending) != 1 {
			t.Errorf("expected 1 pending (failed), got %d: %v", len(pending), pending)
		}
		if pending[0] != "builds_completed" {
			t.Errorf("expected 'builds_completed', got '%s'", pending[0])
		}
	})

	t.Run("slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		_, err := client.GetPendingPrerequisites(ctx, "nonexistent", []string{"push_parsed"}, "")
		if err == nil {
			t.Fatal("expected error for nonexistent slip")
		}
	})
}
