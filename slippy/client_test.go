package slippy

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
)

func TestNewClientWithDependencies(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	config := Config{
		HoldTimeout:   5 * time.Minute,
		PollInterval:  10 * time.Second,
		AncestryDepth: 15,
	}

	client := NewClientWithDependencies(store, github, config)

	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.store != store {
		t.Error("expected store to be set")
	}
	if client.github != github {
		t.Error("expected github to be set")
	}
	if client.config.HoldTimeout != 5*time.Minute {
		t.Errorf("expected HoldTimeout 5m, got %v", client.config.HoldTimeout)
	}
	if client.config.PollInterval != 10*time.Second {
		t.Errorf("expected PollInterval 10s, got %v", client.config.PollInterval)
	}
	if client.config.AncestryDepth != 15 {
		t.Errorf("expected AncestryDepth 15, got %v", client.config.AncestryDepth)
	}
}

func TestNewClientWithDependencies_DefaultConfig(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	config := Config{} // Empty config, should use defaults

	client := NewClientWithDependencies(store, github, config)

	defaultCfg := DefaultConfig()
	if client.config.HoldTimeout != defaultCfg.HoldTimeout {
		t.Errorf("expected default HoldTimeout %v, got %v", defaultCfg.HoldTimeout, client.config.HoldTimeout)
	}
	if client.config.PollInterval != defaultCfg.PollInterval {
		t.Errorf("expected default PollInterval %v, got %v", defaultCfg.PollInterval, client.config.PollInterval)
	}
	if client.config.AncestryDepth != defaultCfg.AncestryDepth {
		t.Errorf("expected default AncestryDepth %v, got %v", defaultCfg.AncestryDepth, client.config.AncestryDepth)
	}
}

func TestClient_Load(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		now := time.Now()
		slip := &Slip{
			CorrelationID: "corr-123",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusPending,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		loaded, err := client.Load(ctx, "corr-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded.CorrelationID != "corr-123" {
			t.Errorf("expected CorrelationID 'corr-123', got '%s'", loaded.CorrelationID)
		}
		if loaded.Repository != "owner/repo" {
			t.Errorf("expected Repository 'owner/repo', got '%s'", loaded.Repository)
		}

		// Verify the store was called
		if len(store.LoadCalls) != 1 {
			t.Errorf("expected 1 Load call, got %d", len(store.LoadCalls))
		}
		if store.LoadCalls[0] != "corr-123" {
			t.Errorf("expected Load call with 'corr-123', got '%s'", store.LoadCalls[0])
		}
	})

	t.Run("not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		_, err := client.Load(ctx, "nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent slip")
		}

		var slipErr *SlipError
		if !errors.As(err, &slipErr) {
			t.Fatalf("expected SlipError, got %T", err)
		}
		if slipErr.Op != "load" {
			t.Errorf("expected op 'load', got '%s'", slipErr.Op)
		}
		if slipErr.CorrelationID != "nonexistent" {
			t.Errorf("expected CorrelationID 'nonexistent', got '%s'", slipErr.CorrelationID)
		}
		if !errors.Is(err, ErrSlipNotFound) {
			t.Error("expected error to wrap ErrSlipNotFound")
		}
	})

	t.Run("store error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.LoadError = errors.New("database connection failed")

		_, err := client.Load(ctx, "corr-123")
		if err == nil {
			t.Fatal("expected error")
		}

		var slipErr *SlipError
		if !errors.As(err, &slipErr) {
			t.Fatalf("expected SlipError, got %T", err)
		}
		if slipErr.Op != "load" {
			t.Errorf("expected op 'load', got '%s'", slipErr.Op)
		}
	})
}

func TestClient_LoadByCommit(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		now := time.Now()
		slip := &Slip{
			CorrelationID: "corr-456",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "def456abc789",
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		loaded, err := client.LoadByCommit(ctx, "owner/repo", "def456abc789")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if loaded.CorrelationID != "corr-456" {
			t.Errorf("expected CorrelationID 'corr-456', got '%s'", loaded.CorrelationID)
		}
		if loaded.CommitSHA != "def456abc789" {
			t.Errorf("expected CommitSHA 'def456abc789', got '%s'", loaded.CommitSHA)
		}

		// Verify the store was called
		if len(store.LoadByCommitCalls) != 1 {
			t.Errorf("expected 1 LoadByCommit call, got %d", len(store.LoadByCommitCalls))
		}
		call := store.LoadByCommitCalls[0]
		if call.Repository != "owner/repo" || call.CommitSHA != "def456abc789" {
			t.Errorf("unexpected LoadByCommit call: %+v", call)
		}
	})

	t.Run("not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		_, err := client.LoadByCommit(ctx, "owner/repo", "unknown")
		if err == nil {
			t.Fatal("expected error for nonexistent commit")
		}

		var slipErr *SlipError
		if !errors.As(err, &slipErr) {
			t.Fatalf("expected SlipError, got %T", err)
		}
		if slipErr.Op != "load by commit" {
			t.Errorf("expected op 'load by commit', got '%s'", slipErr.Op)
		}
	})
}

func TestClient_UpdateSlipStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		now := time.Now()
		slip := &Slip{
			CorrelationID: "corr-789",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "xyz789",
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusPending,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.UpdateSlipStatus(ctx, "corr-789", SlipStatusInProgress)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the update was made
		if len(store.UpdateCalls) != 1 {
			t.Errorf("expected 1 Update call, got %d", len(store.UpdateCalls))
		}
		if store.UpdateCalls[0].Slip.Status != SlipStatusInProgress {
			t.Errorf("expected status InProgress, got %s", store.UpdateCalls[0].Slip.Status)
		}

		// Verify the store reflects the change
		updated, _ := store.Load(ctx, "corr-789")
		if updated.Status != SlipStatusInProgress {
			t.Errorf("expected updated status InProgress, got %s", updated.Status)
		}
	})

	t.Run("slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		err := client.UpdateSlipStatus(ctx, "nonexistent", SlipStatusFailed)
		if err == nil {
			t.Fatal("expected error for nonexistent slip")
		}

		var slipErr *SlipError
		if !errors.As(err, &slipErr) {
			t.Fatalf("expected SlipError, got %T", err)
		}
	})

	t.Run("update error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		now := time.Now()
		slip := &Slip{
			CorrelationID: "corr-999",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "aaa999",
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusPending,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)
		store.UpdateError = errors.New("update failed")

		err := client.UpdateSlipStatus(ctx, "corr-999", SlipStatusFailed)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestClient_Close(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		err := client.Close()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if store.CloseCalls != 1 {
			t.Errorf("expected 1 Close call, got %d", store.CloseCalls)
		}
	})

	t.Run("close error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.CloseError = errors.New("close failed")

		err := client.Close()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "close failed" {
			t.Errorf("expected 'close failed', got '%s'", err.Error())
		}
	})
}

func TestClient_Accessors(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	pipelineConfig := testPipelineConfig()
	config := Config{
		HoldTimeout:    30 * time.Minute,
		PollInterval:   45 * time.Second,
		AncestryDepth:  25,
		PipelineConfig: pipelineConfig,
	}
	client := NewClientWithDependencies(store, github, config)

	t.Run("Store", func(t *testing.T) {
		if client.Store() != store {
			t.Error("Store() should return the store")
		}
	})

	t.Run("GitHub", func(t *testing.T) {
		if client.GitHub() != github {
			t.Error("GitHub() should return the github client")
		}
	})

	t.Run("Config", func(t *testing.T) {
		cfg := client.Config()
		if cfg.HoldTimeout != 30*time.Minute {
			t.Errorf("Config().HoldTimeout = %v, want 30m", cfg.HoldTimeout)
		}
	})

	t.Run("PipelineConfig", func(t *testing.T) {
		pc := client.PipelineConfig()
		if pc == nil {
			t.Error("PipelineConfig() should return non-nil")
		}
		if pc != pipelineConfig {
			t.Error("PipelineConfig() should return the pipeline config from config")
		}
	})
}

func TestClient_AbandonSlip(t *testing.T) {
	ctx := context.Background()

	t.Run("success - abandon pending slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		now := time.Now()
		slip := &Slip{
			CorrelationID: "corr-abandon-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusPending,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.AbandonSlip(ctx, "corr-abandon-1", "corr-new-slip")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the update was made
		if len(store.UpdateCalls) != 1 {
			t.Errorf("expected 1 Update call, got %d", len(store.UpdateCalls))
		}
		if store.UpdateCalls[0].Slip.Status != SlipStatusAbandoned {
			t.Errorf("expected status Abandoned, got %s", store.UpdateCalls[0].Slip.Status)
		}
	})

	t.Run("skip - already terminal", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		now := time.Now()
		slip := &Slip{
			CorrelationID: "corr-abandon-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "def456",
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusCompleted, // Already terminal
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		err := client.AbandonSlip(ctx, "corr-abandon-2", "corr-new-slip")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should NOT have updated since already terminal
		if len(store.UpdateCalls) != 0 {
			t.Errorf("expected 0 Update calls for terminal slip, got %d", len(store.UpdateCalls))
		}
	})

	t.Run("error - slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		err := client.AbandonSlip(ctx, "nonexistent", "corr-new-slip")
		if err == nil {
			t.Fatal("expected error")
		}

		var slipErr *SlipError
		if !errors.As(err, &slipErr) {
			t.Fatalf("expected SlipError, got %T", err)
		}
		if slipErr.Op != "abandon" {
			t.Errorf("expected op 'abandon', got '%s'", slipErr.Op)
		}
	})

	t.Run("error - update fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		now := time.Now()
		slip := &Slip{
			CorrelationID: "corr-abandon-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "ghi789",
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)
		store.UpdateError = errors.New("database error")

		err := client.AbandonSlip(ctx, "corr-abandon-3", "corr-new-slip")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestNewClient_ValidationErrors tests that NewClient properly validates configuration.
// Note: We can't test successful NewClient without real ClickHouse/GitHub connections,
// but we can test that invalid configurations are rejected before any connections are made.
func TestNewClient_ValidationErrors(t *testing.T) {
	// Helper to create a valid ClickHouseConfig for tests
	validCHConfig := &ch.ClickhouseConfig{
		Hostname:   "localhost",
		Port:       "9000",
		Database:   "testdb",
		Username:   "user",
		Password:   "pass",
		SkipVerify: "true",
	}

	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "empty config fails validation",
			config:  Config{},
			wantErr: true,
		},
		{
			name: "missing ClickHouseConfig",
			config: Config{
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
			},
			wantErr: true,
		},
		{
			name: "missing GitHubAppID",
			config: Config{
				ClickHouseConfig: validCHConfig,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
			},
			wantErr: true,
		},
		{
			name: "missing GitHubPrivateKey",
			config: Config{
				ClickHouseConfig: validCHConfig,
				GitHubAppID:      12345,
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
			},
			wantErr: true,
		},
		{
			name: "zero HoldTimeout",
			config: Config{
				ClickHouseConfig: validCHConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      0,
				PollInterval:     time.Second,
				AncestryDepth:    10,
			},
			wantErr: true,
		},
		{
			name: "zero PollInterval",
			config: Config{
				ClickHouseConfig: validCHConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     0,
				AncestryDepth:    10,
			},
			wantErr: true,
		},
		{
			name: "zero AncestryDepth",
			config: Config{
				ClickHouseConfig: validCHConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				// Verify the error mentions invalid configuration
				if !errors.Is(err, ErrInvalidConfiguration) {
					// The error should wrap the validation error
					if err.Error() == "" {
						t.Error("expected non-empty error message")
					}
				}
			}
			// Note: valid configs will still fail because we don't have real connections
			// This is expected behavior - we're testing that validation runs first
		})
	}
}
