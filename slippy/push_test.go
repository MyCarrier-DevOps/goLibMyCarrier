package slippy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPushOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    PushOptions
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid options",
			opts: PushOptions{
				CorrelationID: "corr-123",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "abc123",
				Components: []ComponentDefinition{
					{Name: "svc", DockerfilePath: "Dockerfile"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing correlation_id",
			opts: PushOptions{
				Repository: "owner/repo",
				CommitSHA:  "abc123",
			},
			wantErr: true,
			errMsg:  "correlation_id is required",
		},
		{
			name: "missing repository",
			opts: PushOptions{
				CorrelationID: "corr-123",
				CommitSHA:     "abc123",
			},
			wantErr: true,
			errMsg:  "repository is required",
		},
		{
			name: "missing commit_sha",
			opts: PushOptions{
				CorrelationID: "corr-123",
				Repository:    "owner/repo",
			},
			wantErr: true,
			errMsg:  "commit_sha is required",
		},
		{
			name: "empty components is valid",
			opts: PushOptions{
				CorrelationID: "corr-123",
				Repository:    "owner/repo",
				CommitSHA:     "abc123",
				Components:    []ComponentDefinition{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestClient_CreateSlipForPush(t *testing.T) {
	ctx := context.Background()

	t.Run("success - new slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		opts := PushOptions{
			CorrelationID: "corr-push-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123def456",
			Components: []ComponentDefinition{
				{Name: "svc-a", DockerfilePath: "services/a/Dockerfile"},
				{Name: "svc-b", DockerfilePath: "services/b/Dockerfile"},
			},
		}

		slip, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the returned slip
		if slip.CorrelationID != "corr-push-1" {
			t.Errorf("expected CorrelationID 'corr-push-1', got '%s'", slip.CorrelationID)
		}
		if slip.Repository != "owner/repo" {
			t.Errorf("expected Repository 'owner/repo', got '%s'", slip.Repository)
		}
		if slip.Branch != "main" {
			t.Errorf("expected Branch 'main', got '%s'", slip.Branch)
		}
		if slip.CommitSHA != "abc123def456" {
			t.Errorf("expected CommitSHA 'abc123def456', got '%s'", slip.CommitSHA)
		}
		if slip.Status != SlipStatusInProgress {
			t.Errorf("expected Status 'in_progress', got '%s'", slip.Status)
		}

		// Verify aggregates have component data
		if len(slip.Aggregates["builds"]) != 2 {
			t.Fatalf("expected 2 components in builds aggregate, got %d", len(slip.Aggregates["builds"]))
		}
		if slip.Aggregates["builds"][0].Component != "svc-a" {
			t.Errorf("expected first component 'svc-a', got '%s'", slip.Aggregates["builds"][0].Component)
		}
		if slip.Aggregates["builds"][0].Status != StepStatusPending {
			t.Errorf("expected build status 'pending', got '%s'", slip.Aggregates["builds"][0].Status)
		}

		// Verify steps were initialized
		if slip.Steps["push_parsed"].Status != StepStatusRunning {
			t.Errorf("expected push_parsed status 'running', got '%s'", slip.Steps["push_parsed"].Status)
		}
		if slip.Steps["dev_deploy"].Status != StepStatusPending {
			t.Errorf("expected dev_deploy status 'pending', got '%s'", slip.Steps["dev_deploy"].Status)
		}

		// Verify history was created
		if len(slip.StateHistory) == 0 {
			t.Error("expected state history to be initialized")
		}
		if slip.StateHistory[0].Step != "push_parsed" {
			t.Errorf("expected first history entry for 'push_parsed', got '%s'", slip.StateHistory[0].Step)
		}

		// Verify store was called
		if len(store.CreateCalls) != 1 {
			t.Errorf("expected 1 Create call, got %d", len(store.CreateCalls))
		}
	})

	t.Run("retry - existing slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Pre-create an existing slip
		existingSlip := &Slip{
			CorrelationID: "corr-push-retry",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "retry123",
			CreatedAt:     time.Now().Add(-5 * time.Minute),
			UpdatedAt:     time.Now().Add(-5 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusFailed}, // Previously failed
			},
			StateHistory: []StateHistoryEntry{},
		}
		store.AddSlip(existingSlip)

		opts := PushOptions{
			CorrelationID: "corr-push-retry-new", // Different correlation ID
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "retry123", // Same commit
			Components:    []ComponentDefinition{},
		}

		slip, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should return the existing slip (not create new)
		if slip.CorrelationID != "corr-push-retry" {
			t.Errorf("expected existing slip ID 'corr-push-retry', got '%s'", slip.CorrelationID)
		}

		// Verify no Create call (retry should update, not create)
		if len(store.CreateCalls) != 0 {
			t.Errorf("expected 0 Create calls (retry), got %d", len(store.CreateCalls))
		}

		// Verify UpdateStep was called to reset push_parsed
		var foundUpdateStep bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "push_parsed" && call.Status == StepStatusRunning {
				foundUpdateStep = true
				break
			}
		}
		if !foundUpdateStep {
			t.Error("expected push_parsed to be reset to running")
		}
	})

	t.Run("validation error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		opts := PushOptions{
			// Missing required fields
		}

		_, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected validation error")
		}
		// Error occurred as expected - validation failure
	})

	t.Run("store create error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.CreateError = errors.New("database unavailable")

		opts := PushOptions{
			CorrelationID: "corr-push-err",
			Repository:    "owner/repo",
			CommitSHA:     "errabc",
		}

		_, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("retry - UpdateStep error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Pre-create an existing slip
		existingSlip := &Slip{
			CorrelationID: "corr-push-retry-err",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "retryerr123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(existingSlip)
		store.UpdateStepError = errors.New("update step failed")

		opts := PushOptions{
			CorrelationID: "new-corr",
			Repository:    "owner/repo",
			CommitSHA:     "retryerr123", // Same commit
		}

		_, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected error from UpdateStep failure")
		}
	})

	t.Run("retry - history append error is non-fatal", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		existingSlip := &Slip{
			CorrelationID: "corr-push-hist-err",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "histerr123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(existingSlip)
		store.AppendHistoryError = errors.New("history append failed")

		opts := PushOptions{
			CorrelationID: "new-corr",
			Repository:    "owner/repo",
			CommitSHA:     "histerr123",
		}

		// Should succeed even though history append fails
		slip, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("expected success (history error is non-fatal), got: %v", err)
		}
		if slip == nil {
			t.Fatal("expected non-nil slip")
		}
	})
}

func TestClient_InitializeSlipForPush(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	config := testPipelineConfig()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	opts := PushOptions{
		CorrelationID: "corr-init-1",
		Repository:    "owner/repo",
		Branch:        "feature/test",
		CommitSHA:     "init123",
		Components: []ComponentDefinition{
			{Name: "frontend", DockerfilePath: "frontend/Dockerfile"},
			{Name: "backend", DockerfilePath: "backend/Dockerfile"},
		},
	}

	slip := client.initializeSlipForPush(opts, nil)

	// Verify basic fields
	if slip.CorrelationID != "corr-init-1" {
		t.Errorf("expected CorrelationID 'corr-init-1', got '%s'", slip.CorrelationID)
	}
	if slip.Repository != "owner/repo" {
		t.Errorf("expected Repository 'owner/repo', got '%s'", slip.Repository)
	}
	if slip.Branch != "feature/test" {
		t.Errorf("expected Branch 'feature/test', got '%s'", slip.Branch)
	}
	if slip.CommitSHA != "init123" {
		t.Errorf("expected CommitSHA 'init123', got '%s'", slip.CommitSHA)
	}
	if slip.Status != SlipStatusInProgress {
		t.Errorf("expected Status 'in_progress', got '%s'", slip.Status)
	}

	// Verify timestamps are set
	if slip.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if slip.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}

	// Verify aggregates have component data (test config has builds_completed with "build" aggregate)
	if len(slip.Aggregates["builds"]) != 2 {
		t.Fatalf("expected 2 components in builds aggregate, got %d", len(slip.Aggregates["builds"]))
	}
	if slip.Aggregates["builds"][0].Component != "frontend" {
		t.Errorf("expected first component 'frontend', got '%s'", slip.Aggregates["builds"][0].Component)
	}
	if slip.Aggregates["builds"][1].Component != "backend" {
		t.Errorf("expected second component 'backend', got '%s'", slip.Aggregates["builds"][1].Component)
	}
	if slip.Aggregates["builds"][0].Status != StepStatusPending {
		t.Errorf("expected build status 'pending', got '%s'", slip.Aggregates["builds"][0].Status)
	}

	// Verify all pipeline steps from test config are initialized
	expectedSteps := []string{
		"push_parsed", "builds_completed", "unit_tests_completed", "dev_deploy",
	}
	for _, stepName := range expectedSteps {
		if _, ok := slip.Steps[stepName]; !ok {
			t.Errorf("expected step '%s' to be initialized", stepName)
		}
	}

	// Verify push_parsed is running, others pending
	if slip.Steps["push_parsed"].Status != StepStatusRunning {
		t.Errorf("expected push_parsed status 'running', got '%s'", slip.Steps["push_parsed"].Status)
	}
	if slip.Steps["push_parsed"].StartedAt == nil {
		t.Error("expected push_parsed StartedAt to be set")
	}
	if slip.Steps["dev_deploy"].Status != StepStatusPending {
		t.Errorf("expected dev_deploy status 'pending', got '%s'", slip.Steps["dev_deploy"].Status)
	}

	// Verify history
	if len(slip.StateHistory) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(slip.StateHistory))
	}
	if slip.StateHistory[0].Step != "push_parsed" {
		t.Errorf("expected history step 'push_parsed', got '%s'", slip.StateHistory[0].Step)
	}
	if slip.StateHistory[0].Status != StepStatusRunning {
		t.Errorf("expected history status 'running', got '%s'", slip.StateHistory[0].Status)
	}
	if slip.StateHistory[0].Actor != "slippy-library" {
		t.Errorf("expected history actor 'slippy-library', got '%s'", slip.StateHistory[0].Actor)
	}
}

func TestClient_InitializeSlipForPush_EmptyComponents(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	config := testPipelineConfig()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	opts := PushOptions{
		CorrelationID: "corr-init-empty",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "empty123",
		Components:    []ComponentDefinition{}, // Empty
	}

	slip := client.initializeSlipForPush(opts, nil)

	// Verify aggregates have empty component data
	if slip.Aggregates == nil {
		t.Error("expected Aggregates to be initialized (not nil)")
	}
	// With no components, the aggregates should have empty arrays
	if len(slip.Aggregates["builds"]) != 0 {
		t.Errorf("expected 0 components in builds aggregate, got %d", len(slip.Aggregates["builds"]))
	}
}
