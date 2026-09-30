package slippy

import (
	"context"
	"errors"
	"testing"
	"time"
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

// TestNewClientWithDependencies_NormalisesConfig pins what the constructor does with a Config's
// hold and ancestry settings: a HoldTimeout, PollInterval or AncestryDepth of 0 or less is unset
// and gets DefaultConfig's value, and AncestryMaxDepth is raised to at least AncestryDepth, never
// to DefaultConfig's 100. Left alone, a negative PollInterval re-polls the store with no delay
// while a hold waits, a negative HoldTimeout times the step out at its first poll, and an
// AncestryMaxDepth of 0 makes ResolveAncestry return an empty chain.
func TestNewClientWithDependencies_NormalisesConfig(t *testing.T) {
	d := DefaultConfig()
	type settings struct {
		HoldTimeout, PollInterval       time.Duration
		AncestryDepth, AncestryMaxDepth int
	}
	tests := []struct {
		name string
		in   Config
		want settings
	}{
		{"all unset", Config{}, settings{d.HoldTimeout, d.PollInterval, d.AncestryDepth, d.AncestryDepth}},
		{
			"negative durations and depths are unset",
			Config{
				HoldTimeout: -5 * time.Minute, PollInterval: -30 * time.Second,
				AncestryDepth: -1, AncestryMaxDepth: -1,
			},
			settings{d.HoldTimeout, d.PollInterval, d.AncestryDepth, d.AncestryDepth},
		},
		{
			"positive values are kept",
			Config{
				HoldTimeout: 5 * time.Minute, PollInterval: 10 * time.Second,
				AncestryDepth: 15, AncestryMaxDepth: 40,
			},
			settings{5 * time.Minute, 10 * time.Second, 15, 40},
		},
		{
			"a max depth below the depth is raised to it",
			Config{AncestryDepth: 50, AncestryMaxDepth: 20},
			settings{d.HoldTimeout, d.PollInterval, 50, 50},
		},
		{
			// slippy-api builds exactly this: AncestryDepth set, AncestryMaxDepth left at 0.
			"an unset max depth is raised to the depth, not to 100",
			Config{AncestryDepth: 25},
			settings{d.HoldTimeout, d.PollInterval, 25, 25},
		},
		{
			// 20 is below DefaultConfig's 25: above it, max() would hide a constructor that used the default depth.
			"an unset max depth is raised to a non-default depth",
			Config{AncestryDepth: 20},
			settings{d.HoldTimeout, d.PollInterval, 20, 20},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClientWithDependencies(NewMockStore(), NewMockGitHubAPI(), tt.in).Config()
			got := settings{c.HoldTimeout, c.PollInterval, c.AncestryDepth, c.AncestryMaxDepth}
			if got != tt.want {
				t.Errorf("hold timeout, poll interval, depth, max depth = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestNewClientWithDependencies_NormalisesConfigFromEnv pins the flow the README's Quick Start
// relies on: ConfigFromEnv keeps a negative SLIPPY_HOLD_TIMEOUT or SLIPPY_POLL_INTERVAL and a
// SLIPPY_ANCESTRY_MAX_DEPTH below SLIPPY_ANCESTRY_DEPTH, and the constructor corrects them. It
// blanks SLIPPY_GITHUB_APP_PRIVATE_KEY and prints only the fields it checks, so a failure cannot
// log a private key ConfigFromEnv read from the process environment.
func TestNewClientWithDependencies_NormalisesConfigFromEnv(t *testing.T) {
	t.Setenv("SLIPPY_GITHUB_APP_PRIVATE_KEY", "")
	t.Setenv("SLIPPY_HOLD_TIMEOUT", "-5m")
	t.Setenv("SLIPPY_POLL_INTERVAL", "-30s")
	t.Setenv("SLIPPY_ANCESTRY_DEPTH", "50")
	t.Setenv("SLIPPY_ANCESTRY_MAX_DEPTH", "20")
	cfg := ConfigFromEnv()
	if cfg.HoldTimeout != -5*time.Minute || cfg.PollInterval != -30*time.Second || cfg.AncestryMaxDepth != 20 {
		t.Fatalf("precondition: ConfigFromEnv should keep the values as given, got hold timeout %v, "+
			"poll interval %v, depth %d, max depth %d",
			cfg.HoldTimeout, cfg.PollInterval, cfg.AncestryDepth, cfg.AncestryMaxDepth)
	}

	c := NewClientWithDependencies(NewMockStore(), NewMockGitHubAPI(), cfg).Config()
	if c.HoldTimeout != 60*time.Minute || c.PollInterval != 60*time.Second {
		t.Errorf("hold timeout, poll interval = %v, %v; want the defaults 1h0m0s, 1m0s", c.HoldTimeout, c.PollInterval)
	}
	if c.AncestryDepth != 50 || c.AncestryMaxDepth != 50 {
		t.Errorf("depth, max depth = %d, %d; want 50, 50", c.AncestryDepth, c.AncestryMaxDepth)
	}
}

// TestClient_ApplyHoldDefaults pins the per-call form of the constructor's rule, which
// WaitForPrerequisites and RunPreExecution both apply: a Timeout or PollInterval of 0 or less is
// unset and gets the client's HoldTimeout or PollInterval. Left alone, a negative PollInterval
// re-polls the store with no delay while a hold waits, and a negative Timeout times the step out
// at its first poll.
func TestClient_ApplyHoldDefaults(t *testing.T) {
	client := NewClientWithDependencies(NewMockStore(), NewMockGitHubAPI(), Config{
		HoldTimeout: 7 * time.Minute, PollInterval: 3 * time.Second,
	})
	type durations struct{ Timeout, PollInterval time.Duration }
	configured := durations{7 * time.Minute, 3 * time.Second}
	tests := []struct {
		name     string
		in, want durations
	}{
		{"unset", durations{}, configured},
		{"negative values are unset", durations{-5 * time.Minute, -30 * time.Second}, configured},
		{"positive values are kept", durations{5 * time.Minute, 10 * time.Second},
			durations{5 * time.Minute, 10 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got durations
			got.Timeout, got.PollInterval = client.applyHoldDefaults(tt.in.Timeout, tt.in.PollInterval)
			if got != tt.want {
				t.Errorf("timeout, poll interval = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// ancestryDepthStore records the maxDepth Client.ResolveAncestry hands the store.
type ancestryDepthStore struct {
	*MockStore
	maxDepth int
}

func (s *ancestryDepthStore) ResolveAncestry(_ context.Context, _, _, _ string, maxDepth int) ([]AncestryEntry, error) {
	s.maxDepth = maxDepth
	return nil, nil
}

// TestClient_ResolveAncestry_UnsetMaxDepthWalksAncestryDepth pins the consequence of raising an
// unset AncestryMaxDepth: ResolveAncestry walks up to AncestryDepth links, where a maxDepth of 0
// returned an empty chain for a slip that has parents.
func TestClient_ResolveAncestry_UnsetMaxDepthWalksAncestryDepth(t *testing.T) {
	store := &ancestryDepthStore{MockStore: NewMockStore()}
	// 20 is below DefaultConfig's 25: above it, max() would hide a constructor that used the default depth.
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{AncestryDepth: 20})

	if _, err := client.ResolveAncestry(context.Background(), "owner/repo", "main", "corr-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.maxDepth != 20 {
		t.Errorf("ResolveAncestry passed maxDepth %d to the store, want 20 (AncestryDepth)", store.maxDepth)
	}
}

// TestNewClientWithDependencies_UnsetMaxDepthKeepsThePushSearchAtDepth pins that raising an unset
// AncestryMaxDepth to AncestryDepth leaves both push-path searches where they were: one GitHub
// ancestry query, at AncestryDepth. That is slippy-api's Config; giving AncestryMaxDepth
// DefaultConfig's 100 instead would add a second query at 100 to every push that finds no slip.
func TestNewClientWithDependencies_UnsetMaxDepthKeepsThePushSearchAtDepth(t *testing.T) {
	ctx := context.Background()
	searches := map[string]func(c *Client) ([]SlipWithCommit, error){
		"ancestor search": func(c *Client) ([]SlipWithCommit, error) {
			return c.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo",
				PushOptions{CorrelationID: "corr-new", Repository: "owner/repo", CommitSHA: "abc123"})
		},
		"PR branch search": func(c *Client) ([]SlipWithCommit, error) {
			return c.findSlipsInPRBranchHistory(ctx, "owner", "repo", "owner/repo", "abc123")
		},
	}
	for name, search := range searches {
		t.Run(name, func(t *testing.T) {
			github := NewMockGitHubAPI()
			github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123", "grandparent456"})
			client := NewClientWithDependencies(NewMockStore(), github, Config{AncestryDepth: 25})

			if _, err := search(client); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			calls := github.GetCommitAncestryCalls
			if len(calls) != 1 || calls[0].Depth != 25 {
				t.Errorf("GetCommitAncestry calls = %+v, want exactly one, at depth 25", calls)
			}
		})
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

		// Verify the update was routed through store.UpdateSlipStatus (atomic path).
		if len(store.UpdateSlipStatusCalls) != 1 {
			t.Errorf("expected 1 UpdateSlipStatus call, got %d", len(store.UpdateSlipStatusCalls))
		}
		if store.UpdateSlipStatusCalls[0].Status != SlipStatusInProgress {
			t.Errorf("expected status InProgress, got %s", store.UpdateSlipStatusCalls[0].Status)
		}
		// No legacy Load+Update round-trip.
		if len(store.UpdateCalls) != 0 {
			t.Errorf("expected 0 Update calls (atomic path), got %d", len(store.UpdateCalls))
		}

		// Verify the store reflects the change.
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
		store.UpdateSlipStatusError = errors.New("update failed")

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

		// Verify the update was routed through the atomic status-only path.
		if len(store.UpdateSlipStatusCalls) != 1 {
			t.Errorf("expected 1 UpdateSlipStatus call, got %d", len(store.UpdateSlipStatusCalls))
		}
		if store.UpdateSlipStatusCalls[0].Status != SlipStatusAbandoned {
			t.Errorf("expected status Abandoned, got %s", store.UpdateSlipStatusCalls[0].Status)
		}
		if len(store.UpdateCalls) != 0 {
			t.Errorf("expected 0 full-row Update calls, got %d", len(store.UpdateCalls))
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
		if len(store.UpdateSlipStatusCalls) != 0 {
			t.Errorf("expected 0 UpdateSlipStatus calls for terminal slip, got %d", len(store.UpdateSlipStatusCalls))
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
		store.UpdateSlipStatusError = errors.New("database error")

		err := client.AbandonSlip(ctx, "corr-abandon-3", "corr-new-slip")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestClient_Ping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		err := client.Ping(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.PingCalls != 1 {
			t.Errorf("expected 1 Ping call, got %d", store.PingCalls)
		}
	})

	t.Run("error propagation", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.PingError = errors.New("connection dead")

		err := client.Ping(context.Background())
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "connection dead" {
			t.Errorf("expected 'connection dead', got '%s'", err.Error())
		}
	})

	t.Run("nil store", func(t *testing.T) {
		client := &Client{store: nil}

		err := client.Ping(context.Background())
		if err == nil {
			t.Fatal("expected error for nil store")
		}
		if !errors.Is(err, err) {
			t.Error("expected non-nil error")
		}
	})
}
