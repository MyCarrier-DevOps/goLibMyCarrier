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

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

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

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

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

		// Now returns error - history errors are no longer swallowed
		result, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected error for history append failure")
		}
		if !errors.Is(err, ErrHistoryAppendFailed) {
			t.Errorf("expected ErrHistoryAppendFailed, got: %v", err)
		}
		// Result should be nil when there's an error
		if result != nil {
			t.Error("expected nil result on error")
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

func TestClient_resolveAndAbandonAncestors(t *testing.T) {
	ctx := context.Background()

	t.Run("no ancestor commits found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// No ancestry configured - GetCommitAncestry returns empty
		opts := PushOptions{
			CorrelationID: "corr-new-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, err := client.resolveAndAbandonAncestors(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if ancestry != nil {
			t.Errorf("expected nil ancestry, got %v", ancestry)
		}
	})

	t.Run("finds and abandons ancestor slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip exists at commit "parent123"
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusInProgress, // Non-terminal - should be abandoned
			Steps:         make(map[string]Step),
		}
		store.AddSlip(ancestorSlip)

		// Configure GitHub to return ancestry chain
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123", "grandparent456"})

		opts := PushOptions{
			CorrelationID: "corr-new-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, err := client.resolveAndAbandonAncestors(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify ancestry chain was built
		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}
		if ancestry[0].CorrelationID != "corr-ancestor-1" {
			t.Errorf("expected ancestor ID 'corr-ancestor-1', got '%s'", ancestry[0].CorrelationID)
		}

		// Verify ancestor was abandoned
		if len(store.UpdateCalls) != 1 {
			t.Fatalf("expected 1 Update call (abandon), got %d", len(store.UpdateCalls))
		}
		if store.UpdateCalls[0].Slip.Status != SlipStatusAbandoned {
			t.Errorf("expected ancestor to be abandoned, got status '%s'", store.UpdateCalls[0].Slip.Status)
		}
	})

	t.Run("inherits ancestry from parent slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip with its own ancestry chain
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-parent-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusCompleted, // Terminal - won't be abandoned
			Steps:         make(map[string]Step),
			Ancestry: []AncestryEntry{
				{
					CorrelationID: "corr-grandparent-1",
					CommitSHA:     "grandparent456",
					Status:        SlipStatusCompleted,
					CreatedAt:     now.Add(-20 * time.Minute),
				},
			},
		}
		store.AddSlip(ancestorSlip)

		// Configure GitHub to return ancestry chain
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-new-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, err := client.resolveAndAbandonAncestors(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify ancestry chain includes both direct parent and inherited ancestors
		if len(ancestry) != 2 {
			t.Fatalf("expected 2 ancestry entries (parent + inherited), got %d", len(ancestry))
		}
		if ancestry[0].CorrelationID != "corr-parent-1" {
			t.Errorf("expected first entry 'corr-parent-1', got '%s'", ancestry[0].CorrelationID)
		}
		if ancestry[1].CorrelationID != "corr-grandparent-1" {
			t.Errorf("expected second entry (inherited) 'corr-grandparent-1', got '%s'", ancestry[1].CorrelationID)
		}

		// Verify no abandonment (ancestor was terminal)
		if len(store.UpdateCalls) != 0 {
			t.Errorf("expected 0 Update calls (ancestor was terminal), got %d", len(store.UpdateCalls))
		}
	})

	t.Run("records failed step in ancestry", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip that failed at a specific step
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-failed-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "failed123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusFailed,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusCompleted},
				"unit_tests":  {Status: StepStatusFailed}, // This one failed
				"dev_deploy":  {Status: StepStatusPending},
			},
		}
		store.AddSlip(ancestorSlip)

		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "failed123"})

		opts := PushOptions{
			CorrelationID: "corr-new-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, err := client.resolveAndAbandonAncestors(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify failed step is recorded
		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}
		if ancestry[0].FailedStep != "unit_tests" {
			t.Errorf("expected FailedStep 'unit_tests', got '%s'", ancestry[0].FailedStep)
		}
		if ancestry[0].Status != SlipStatusFailed {
			t.Errorf("expected Status 'failed', got '%s'", ancestry[0].Status)
		}
	})

	t.Run("invalid repository format", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		opts := PushOptions{
			CorrelationID: "corr-invalid",
			Repository:    "invalid-repo-format", // Missing owner/repo separator
			CommitSHA:     "abc123",
		}

		_, err := client.resolveAndAbandonAncestors(ctx, opts)
		if err == nil {
			t.Fatal("expected error for invalid repository format")
		}
	})

	t.Run("GitHub API error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		github.GetCommitAncestryError = errors.New("GitHub API unavailable")
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		opts := PushOptions{
			CorrelationID: "corr-err-1",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		_, err := client.resolveAndAbandonAncestors(ctx, opts)
		if err == nil {
			t.Fatal("expected error from GitHub API")
		}
	})

	t.Run("store FindAllByCommits error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		store.FindAllByCommitsError = errors.New("database unavailable")
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Configure GitHub to return ancestry
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-err-2",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		_, err := client.resolveAndAbandonAncestors(ctx, opts)
		if err == nil {
			t.Fatal("expected error from store")
		}
	})
}

func TestClient_findAncestorSlipsWithProgressiveDepth(t *testing.T) {
	ctx := context.Background()

	t.Run("finds ancestor at initial depth", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip exists
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor-init",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(ancestorSlip)

		// Configure ancestry with just a few commits (within initial depth)
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-new-init",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Slip.CorrelationID != "corr-ancestor-init" {
			t.Errorf("expected 'corr-ancestor-init', got '%s'", results[0].Slip.CorrelationID)
		}

		// Verify only one GetCommitAncestry call (initial depth was sufficient)
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Errorf("expected 1 GetCommitAncestry call, got %d", len(github.GetCommitAncestryCalls))
		}
		if github.GetCommitAncestryCalls[0].Depth != 25 {
			t.Errorf("expected initial depth 25, got %d", github.GetCommitAncestryCalls[0].Depth)
		}
	})

	t.Run("expands to max depth when no ancestor at initial depth", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip exists at a commit far in the ancestry
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor-deep",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "deep123", // Far in ancestry
			CreatedAt:     now.Add(-60 * time.Minute),
			UpdatedAt:     now.Add(-60 * time.Minute),
			Status:        SlipStatusCompleted,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(ancestorSlip)

		// Create a commit chain that's longer than initial depth
		// Simulate: at depth 25, we only see commits without slips
		// At depth 100, we find the slip at "deep123"
		longCommitChain := make([]string, 50)
		longCommitChain[0] = "abc123" // Current commit
		for i := 1; i < 49; i++ {
			longCommitChain[i] = "intermediate" + string(rune('a'+i))
		}
		longCommitChain[49] = "deep123" // The ancestor with a slip

		github.SetAncestry("owner", "repo", "abc123", longCommitChain)

		opts := PushOptions{
			CorrelationID: "corr-new-deep",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(results) != 1 {
			t.Fatalf("expected 1 result (found at max depth), got %d", len(results))
		}
		if results[0].Slip.CorrelationID != "corr-ancestor-deep" {
			t.Errorf("expected 'corr-ancestor-deep', got '%s'", results[0].Slip.CorrelationID)
		}

		// Verify two GetCommitAncestry calls (initial + expanded)
		if len(github.GetCommitAncestryCalls) != 2 {
			t.Errorf("expected 2 GetCommitAncestry calls, got %d", len(github.GetCommitAncestryCalls))
		}
		if github.GetCommitAncestryCalls[0].Depth != 25 {
			t.Errorf("expected first call depth 25, got %d", github.GetCommitAncestryCalls[0].Depth)
		}
		if github.GetCommitAncestryCalls[1].Depth != 100 {
			t.Errorf("expected second call depth 100, got %d", github.GetCommitAncestryCalls[1].Depth)
		}
	})

	t.Run("no commits returns nil", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// No ancestry configured - returns empty
		opts := PushOptions{
			CorrelationID: "corr-no-commits",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if results != nil {
			t.Errorf("expected nil results for no commits, got %v", results)
		}

		// Should only call once - no point retrying with no commits
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Errorf("expected 1 GetCommitAncestry call, got %d", len(github.GetCommitAncestryCalls))
		}
	})

	t.Run("skips current commit in ancestry", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Slip exists at the CURRENT commit (should be skipped)
		now := time.Now()
		currentSlip := &Slip{
			CorrelationID: "corr-current",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123", // Same as current commit
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(currentSlip)

		// Parent slip
		parentSlip := &Slip{
			CorrelationID: "corr-parent",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusCompleted,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(parentSlip)

		// Ancestry includes current commit first
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-new-skip",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should find parent, not current
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Slip.CorrelationID != "corr-parent" {
			t.Errorf("expected 'corr-parent', got '%s'", results[0].Slip.CorrelationID)
		}
	})

	t.Run("does not expand if max depth equals initial", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 25, // Same as initial - no expansion
		})

		// Configure ancestry without matching slips
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123", "grandparent456"})

		opts := PushOptions{
			CorrelationID: "corr-no-expand",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// No slips found
		if results != nil {
			t.Errorf("expected nil results, got %v", results)
		}

		// Only one call - no expansion when max == initial
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Errorf("expected 1 GetCommitAncestry call (no expansion), got %d", len(github.GetCommitAncestryCalls))
		}
	})
}

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		expected      int
	}{
		{
			name:          "GitHub auto-generated squash merge",
			commitMessage: "Add new feature (#42)\n\nDetailed description here",
			expected:      42,
		},
		{
			name:          "explicit pull request reference",
			commitMessage: "Merge pull request #123 from feature-branch",
			expected:      123,
		},
		{
			name:          "no PR number",
			commitMessage: "Regular commit without PR reference",
			expected:      0,
		},
		{
			name:          "PR number in middle of message",
			commitMessage: "fix: resolve bug introduced in #789",
			expected:      789,
		},
		{
			name:          "multiple PR references returns first",
			commitMessage: "fix: resolve #45 and #67",
			expected:      45,
		},
		{
			name:          "empty commit message",
			commitMessage: "",
			expected:      0,
		},
		{
			name:          "number without hash not matched",
			commitMessage: "Fixed issue 42",
			expected:      0,
		},
		{
			name:          "hash at end of line",
			commitMessage: "Merged PR #999",
			expected:      999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPRNumber(tt.commitMessage)
			if result != tt.expected {
				t.Errorf("extractPRNumber(%q) = %d, want %d", tt.commitMessage, result, tt.expected)
			}
		})
	}
}

func TestExtractAllPRNumbers(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		expected      []int
	}{
		{
			name:          "single PR",
			commitMessage: "Add feature (#42)",
			expected:      []int{42},
		},
		{
			name:          "multiple PRs",
			commitMessage: "Merge dev (#45) which includes fix (#67)",
			expected:      []int{45, 67},
		},
		{
			name:          "duplicate PRs deduplicated",
			commitMessage: "Fix #45, closes #45",
			expected:      []int{45},
		},
		{
			name:          "no PRs",
			commitMessage: "Regular commit",
			expected:      nil,
		},
		{
			name:          "nested merge message",
			commitMessage: "Merge pull request #100\n\nContains:\n- Feature (#90)\n- Fix (#91)",
			expected:      []int{100, 90, 91},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAllPRNumbers(tt.commitMessage)
			if len(result) != len(tt.expected) {
				t.Errorf("extractAllPRNumbers(%q) returned %d PRs, want %d", tt.commitMessage, len(result), len(tt.expected))
				return
			}
			for i, pr := range tt.expected {
				if result[i] != pr {
					t.Errorf("extractAllPRNumbers(%q)[%d] = %d, want %d", tt.commitMessage, i, result[i], pr)
				}
			}
		})
	}
}

func TestIsCherryPick(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		expected      bool
	}{
		{
			name:          "cherry-pick with hyphen",
			commitMessage: "cherry-pick: fix from main",
			expected:      true,
		},
		{
			name:          "cherry pick with space",
			commitMessage: "cherry pick abc123",
			expected:      true,
		},
		{
			name:          "picked from",
			commitMessage: "Picked from release branch",
			expected:      true,
		},
		{
			name:          "backport",
			commitMessage: "Backport security fix",
			expected:      true,
		},
		{
			name:          "regular commit",
			commitMessage: "Add new feature",
			expected:      false,
		},
		{
			name:          "case insensitive",
			commitMessage: "CHERRY-PICK from v1.0",
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCherryPick(tt.commitMessage)
			if result != tt.expected {
				t.Errorf("isCherryPick(%q) = %v, want %v", tt.commitMessage, result, tt.expected)
			}
		})
	}
}

func TestClient_FindAncestorViaSquashMerge(t *testing.T) {
	ctx := context.Background()

	t.Run("finds slip via PR head commit", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set up the feature branch slip that was created before the squash merge
		featureSlip := &Slip{
			CorrelationID: "corr-feature",
			Repository:    "owner/repo",
			CommitSHA:     "feature-commit-sha",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-feature"] = featureSlip
		store.CommitIndex["owner/repo:feature-commit-sha"] = "corr-feature"

		// Set up PR head commit lookup and its ancestry
		github.SetPRHeadCommit("owner", "repo", 42, "feature-commit-sha")
		github.SetAncestry("owner", "repo", "feature-commit-sha", []string{"feature-commit-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge",
			Repository:    "owner/repo",
			CommitSHA:     "merge-commit-sha",
			CommitMessage: "Add feature (#42)\n\nSquash merged",
		}

		result, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if !found {
			t.Fatal("expected to find ancestor via squash merge")
		}
		if result.Slip.CorrelationID != "corr-feature" {
			t.Errorf("expected correlation ID 'corr-feature', got '%s'", result.Slip.CorrelationID)
		}
		if result.MatchedCommit != "feature-commit-sha" {
			t.Errorf("expected matched commit 'feature-commit-sha', got '%s'", result.MatchedCommit)
		}

		// Verify PR head commit was looked up
		if len(github.GetPRHeadCommitCalls) != 1 {
			t.Fatalf("expected 1 GetPRHeadCommit call, got %d", len(github.GetPRHeadCommitCalls))
		}
		call := github.GetPRHeadCommitCalls[0]
		if call.Owner != "owner" || call.Repo != "repo" || call.PRNumber != 42 {
			t.Errorf("unexpected GetPRHeadCommit call: %+v", call)
		}
	})

	t.Run("finds slip when PR head is non-slip commit", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set up a slip from an earlier commit in the PR
		featureSlip := &Slip{
			CorrelationID: "corr-feature",
			Repository:    "owner/repo",
			CommitSHA:     "earlier-commit-sha",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-feature"] = featureSlip
		store.CommitIndex["owner/repo:earlier-commit-sha"] = "corr-feature"

		// PR head is a non-slip commit (e.g., docs change) that comes after the slip commit
		github.SetPRHeadCommit("owner", "repo", 99, "docs-commit-sha")
		// Ancestry from docs commit includes the earlier slip-creating commit
		github.SetAncestry("owner", "repo", "docs-commit-sha", []string{"docs-commit-sha", "earlier-commit-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge",
			Repository:    "owner/repo",
			CommitSHA:     "merge-commit-sha",
			CommitMessage: "Add feature (#99)",
		}

		result, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if !found {
			t.Fatal("expected to find ancestor via PR ancestry walk")
		}
		if result.Slip.CorrelationID != "corr-feature" {
			t.Errorf("expected correlation ID 'corr-feature', got '%s'", result.Slip.CorrelationID)
		}
		// Should match the slip's commit, not the PR head
		if result.MatchedCommit != "earlier-commit-sha" {
			t.Errorf("expected matched commit 'earlier-commit-sha', got '%s'", result.MatchedCommit)
		}
	})

	t.Run("returns false when no PR number in commit message", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		opts := PushOptions{
			CorrelationID: "corr-no-pr",
			Repository:    "owner/repo",
			CommitSHA:     "commit-sha",
			CommitMessage: "Regular commit without PR reference",
		}

		_, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if found {
			t.Error("expected not to find ancestor when no PR number in message")
		}
		if len(github.GetPRHeadCommitCalls) != 0 {
			t.Error("should not call GetPRHeadCommit when no PR number")
		}
	})

	t.Run("returns false when PR head commit lookup fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set error for PR lookup
		github.GetPRHeadCommitError = errors.New("PR not found")

		opts := PushOptions{
			CorrelationID: "corr-pr-error",
			Repository:    "owner/repo",
			CommitSHA:     "commit-sha",
			CommitMessage: "Fix (#99)",
		}

		_, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if found {
			t.Error("expected not to find ancestor when PR lookup fails")
		}
	})

	t.Run("returns false when no slip found for PR head commit", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// PR lookup succeeds but no slip exists in that commit's ancestry
		github.SetPRHeadCommit("owner", "repo", 50, "orphan-commit-sha")
		github.SetAncestry("owner", "repo", "orphan-commit-sha", []string{"orphan-commit-sha"})

		opts := PushOptions{
			CorrelationID: "corr-no-slip",
			Repository:    "owner/repo",
			CommitSHA:     "commit-sha",
			CommitMessage: "Merge (#50)",
		}

		_, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if found {
			t.Error("expected not to find ancestor when no slip exists in PR ancestry")
		}
	})

	t.Run("tries multiple PR numbers for nested merges", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set up slip from feature branch
		featureSlip := &Slip{
			CorrelationID: "corr-feature",
			Repository:    "owner/repo",
			CommitSHA:     "feature-sha",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-feature"] = featureSlip
		store.CommitIndex["owner/repo:feature-sha"] = "corr-feature"

		// First PR (#100) not found (use ErrorFor to be specific)
		github.GetPRHeadCommitErrorFor = map[string]error{
			"owner/repo:100": errors.New("PR not found"),
		}
		
		// Second PR (#90) has the slip
		github.SetPRHeadCommit("owner", "repo", 90, "feature-sha")
		github.SetAncestry("owner", "repo", "feature-sha", []string{"feature-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge",
			Repository:    "owner/repo",
			CommitSHA:     "merge-sha",
			CommitMessage: "Merge dev (#100) with feature (#90)",
		}

		result, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if !found {
			t.Fatal("expected to find ancestor via second PR")
		}
		if result.Slip.CorrelationID != "corr-feature" {
			t.Errorf("expected correlation ID 'corr-feature', got '%s'", result.Slip.CorrelationID)
		}
		
		// Should have tried both PRs
		if len(github.GetPRHeadCommitCalls) < 2 {
			t.Errorf("expected at least 2 GetPRHeadCommit calls, got %d", len(github.GetPRHeadCommitCalls))
		}
	})
}

func TestClient_PromoteSlip(t *testing.T) {
	ctx := context.Background()

	t.Run("promotes active slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-to-promote",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-to-promote"] = slip

		err := client.PromoteSlip(ctx, "corr-to-promote", "corr-target")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify status was updated
		updated := store.Slips["corr-to-promote"]
		if updated.Status != SlipStatusPromoted {
			t.Errorf("expected status 'promoted', got '%s'", updated.Status)
		}
		if updated.PromotedTo != "corr-target" {
			t.Errorf("expected PromotedTo 'corr-target', got '%s'", updated.PromotedTo)
		}
	})

	t.Run("skips already terminal slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-completed",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
			Status:        SlipStatusCompleted,
		}
		store.Slips["corr-completed"] = slip

		err := client.PromoteSlip(ctx, "corr-completed", "corr-target")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should remain completed, not promoted
		updated := store.Slips["corr-completed"]
		if updated.Status != SlipStatusCompleted {
			t.Errorf("expected status to remain 'completed', got '%s'", updated.Status)
		}
	})

	t.Run("returns error when slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		err := client.PromoteSlip(ctx, "non-existent", "corr-target")
		if err == nil {
			t.Fatal("expected error for non-existent slip")
		}
	})

	t.Run("returns error when update fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-update-fail",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-update-fail"] = slip
		store.UpdateError = errors.New("database error")

		err := client.PromoteSlip(ctx, "corr-update-fail", "corr-target")
		if err == nil {
			t.Fatal("expected error when update fails")
		}
	})
}

func TestClient_CreateSlipForPush_SquashMergePromotion(t *testing.T) {
	ctx := context.Background()

	t.Run("promotes feature branch slip on squash merge", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Set up feature branch slip
		featureSlip := &Slip{
			CorrelationID: "corr-feature-branch",
			Repository:    "owner/repo",
			CommitSHA:     "feature-head-sha",
			Branch:        "feature/add-thing",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now().Add(-1 * time.Hour),
		}
		store.Slips["corr-feature-branch"] = featureSlip
		store.CommitIndex["owner/repo:feature-head-sha"] = "corr-feature-branch"

		// Set up PR head commit lookup (no git ancestry - simulates squash merge)
		github.SetPRHeadCommit("owner", "repo", 77, "feature-head-sha")
		// No ancestry from merge commit - squash merge creates new commit with no git parent link
		github.SetAncestry("owner", "repo", "squash-merge-sha", []string{"squash-merge-sha"})
		// But the PR head has its own ancestry that we can search
		github.SetAncestry("owner", "repo", "feature-head-sha", []string{"feature-head-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge-commit",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "squash-merge-sha",
			CommitMessage: "Add thing (#77)\n\n* First commit\n* Second commit",
			Components: []ComponentDefinition{
				{Name: "svc", DockerfilePath: "Dockerfile"},
			},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

		// Verify the new slip was created
		if slip.CorrelationID != "corr-merge-commit" {
			t.Errorf("expected correlation ID 'corr-merge-commit', got '%s'", slip.CorrelationID)
		}

		// Verify ancestry contains the promoted slip
		if len(slip.Ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(slip.Ancestry))
		}
		ancestryEntry := slip.Ancestry[0]
		if ancestryEntry.CorrelationID != "corr-feature-branch" {
			t.Errorf("expected ancestry correlation ID 'corr-feature-branch', got '%s'", ancestryEntry.CorrelationID)
		}

		// Verify feature slip was promoted (not abandoned)
		promotedSlip := store.Slips["corr-feature-branch"]
		if promotedSlip.Status != SlipStatusPromoted {
			t.Errorf("expected feature slip status 'promoted', got '%s'", promotedSlip.Status)
		}
		if promotedSlip.PromotedTo != "corr-merge-commit" {
			t.Errorf("expected PromotedTo 'corr-merge-commit', got '%s'", promotedSlip.PromotedTo)
		}
	})

	t.Run("falls back to git ancestry when no PR in message", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Set up ancestor slip
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor",
			Repository:    "owner/repo",
			CommitSHA:     "parent-sha",
			Branch:        "main",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now().Add(-1 * time.Hour),
		}
		store.Slips["corr-ancestor"] = ancestorSlip
		store.CommitIndex["owner/repo:parent-sha"] = "corr-ancestor"

		// Set up git ancestry
		github.SetAncestry("owner", "repo", "child-sha", []string{"child-sha", "parent-sha"})

		opts := PushOptions{
			CorrelationID: "corr-child",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "child-sha",
			CommitMessage: "Regular commit without PR reference",
			Components: []ComponentDefinition{
				{Name: "svc", DockerfilePath: "Dockerfile"},
			},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

		// Verify ancestry was resolved via git history
		if len(slip.Ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(slip.Ancestry))
		}

		// Verify ancestor slip was abandoned (regular push, not squash merge)
		abandonedSlip := store.Slips["corr-ancestor"]
		if abandonedSlip.Status != SlipStatusAbandoned {
			t.Errorf("expected ancestor slip status 'abandoned', got '%s'", abandonedSlip.Status)
		}
	})
}
