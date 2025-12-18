//go:build integration
// +build integration

package slippy

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ClickHouseContainer represents a running ClickHouse container for testing.
type ClickHouseContainer struct {
	testcontainers.Container
	DSN string
}

// setupClickHouseContainer creates a ClickHouse container for integration testing.
func setupClickHouseContainer(ctx context.Context) (*ClickHouseContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "clickhouse/clickhouse-server:latest",
		ExposedPorts: []string{"9000/tcp", "8123/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForLog("Ready for connections"),
			wait.ForListeningPort("9000/tcp"),
		),
		Env: map[string]string{
			"CLICKHOUSE_DB":       "ci",
			"CLICKHOUSE_USER":     "default",
			"CLICKHOUSE_PASSWORD": "",
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	port, err := container.MappedPort(ctx, "9000")
	if err != nil {
		return nil, fmt.Errorf("failed to get container port: %w", err)
	}

	dsn := fmt.Sprintf("clickhouse://default:@%s:%s/ci", host, port.Port())

	return &ClickHouseContainer{
		Container: container,
		DSN:       dsn,
	}, nil
}

// TestClickHouseStore_Integration performs integration tests with a real ClickHouse instance.
func TestClickHouseStore_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start ClickHouse container
	container, err := setupClickHouseContainer(ctx)
	if err != nil {
		t.Fatalf("Failed to start ClickHouse container: %v", err)
	}
	defer container.Terminate(ctx)

	// Create store (this also runs migrations)
	store, err := NewClickHouseStore(container.DSN)
	if err != nil {
		t.Fatalf("Failed to create ClickHouse store: %v", err)
	}
	defer store.Close()

	t.Run("CreateAndLoad", func(t *testing.T) {
		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123def456",
			Status:        SlipStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Components: []Component{
				{Name: "api", DockerfilePath: "services/api", BuildStatus: StepStatusPending, UnitTestStatus: StepStatusPending},
				{Name: "worker", DockerfilePath: "services/worker", BuildStatus: StepStatusPending, UnitTestStatus: StepStatusPending},
			},
			Steps: map[string]Step{
				"push_parsed":      {Status: StepStatusCompleted},
				"builds_completed": {Status: StepStatusPending},
			},
			StateHistory: []StateHistoryEntry{
				{Timestamp: time.Now(), Step: "status", Message: "Slip created"},
			},
		}

		// Create
		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Load by correlation ID
		loaded, err := store.Load(ctx, slip.CorrelationID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if loaded.CorrelationID != slip.CorrelationID {
			t.Errorf("CorrelationID = %q, want %q", loaded.CorrelationID, slip.CorrelationID)
		}
		if loaded.Repository != slip.Repository {
			t.Errorf("Repository = %q, want %q", loaded.Repository, slip.Repository)
		}
		if loaded.Status != slip.Status {
			t.Errorf("Status = %v, want %v", loaded.Status, slip.Status)
		}
		if len(loaded.Components) != 2 {
			t.Errorf("len(Components) = %d, want 2", len(loaded.Components))
		}
	})

	t.Run("LoadByCommit", func(t *testing.T) {
		slip := &Slip{
			CorrelationID: "test-corr-002",
			Repository:    "myorg/commit-test",
			Branch:        "feature",
			CommitSHA:     "commit123abc",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Components:    []Component{},
			Steps:         map[string]Step{},
			StateHistory:  []StateHistoryEntry{},
		}

		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Load by commit
		loaded, err := store.LoadByCommit(ctx, slip.Repository, slip.CommitSHA)
		if err != nil {
			t.Fatalf("LoadByCommit() error = %v", err)
		}

		if loaded.CorrelationID != slip.CorrelationID {
			t.Errorf("CorrelationID = %q, want %q", loaded.CorrelationID, slip.CorrelationID)
		}
	})

	t.Run("LoadNotFound", func(t *testing.T) {
		_, err := store.Load(ctx, "nonexistent-id")
		if err != ErrSlipNotFound {
			t.Errorf("Load() error = %v, want ErrSlipNotFound", err)
		}
	})

	t.Run("Update", func(t *testing.T) {
		slip := &Slip{
			CorrelationID: "test-corr-003",
			Repository:    "myorg/update-test",
			Branch:        "main",
			CommitSHA:     "update123",
			Status:        SlipStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Components:    []Component{},
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusPending},
			},
			StateHistory: []StateHistoryEntry{},
		}

		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Update status
		slip.Status = SlipStatusInProgress
		slip.Steps["push_parsed"] = Step{Status: StepStatusCompleted}
		err = store.Update(ctx, slip)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		// Verify update
		loaded, err := store.Load(ctx, slip.CorrelationID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if loaded.Status != SlipStatusInProgress {
			t.Errorf("Status = %v, want %v", loaded.Status, SlipStatusInProgress)
		}
		if loaded.Steps["push_parsed"].Status != StepStatusCompleted {
			t.Errorf("push_parsed status = %v, want %v", loaded.Steps["push_parsed"].Status, StepStatusCompleted)
		}
	})

	t.Run("UpdateStep", func(t *testing.T) {
		slip := &Slip{
			CorrelationID: "test-corr-004",
			Repository:    "myorg/step-test",
			Branch:        "main",
			CommitSHA:     "step123",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Components:    []Component{},
			Steps: map[string]Step{
				"dev_deploy": {Status: StepStatusPending},
			},
			StateHistory: []StateHistoryEntry{},
		}

		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Update specific step
		err = store.UpdateStep(ctx, slip.CorrelationID, "dev_deploy", "", StepStatusCompleted)
		if err != nil {
			t.Fatalf("UpdateStep() error = %v", err)
		}

		// Verify
		loaded, err := store.Load(ctx, slip.CorrelationID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if loaded.Steps["dev_deploy"].Status != StepStatusCompleted {
			t.Errorf("dev_deploy status = %v, want %v", loaded.Steps["dev_deploy"].Status, StepStatusCompleted)
		}
	})

	t.Run("UpdateComponentStatus", func(t *testing.T) {
		slip := &Slip{
			CorrelationID: "test-corr-005",
			Repository:    "myorg/component-test",
			Branch:        "main",
			CommitSHA:     "comp123",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Components: []Component{
				{Name: "api", DockerfilePath: "services/api", BuildStatus: StepStatusPending, UnitTestStatus: StepStatusPending},
			},
			Steps:        map[string]Step{},
			StateHistory: []StateHistoryEntry{},
		}

		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Update component build status
		err = store.UpdateComponentStatus(ctx, slip.CorrelationID, "api", "build", StepStatusCompleted)
		if err != nil {
			t.Fatalf("UpdateComponentStatus() error = %v", err)
		}

		// Verify
		loaded, err := store.Load(ctx, slip.CorrelationID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if len(loaded.Components) == 0 {
			t.Fatal("expected at least one component")
		}
		if loaded.Components[0].BuildStatus != StepStatusCompleted {
			t.Errorf("api BuildStatus = %v, want %v", loaded.Components[0].BuildStatus, StepStatusCompleted)
		}
	})

	t.Run("AppendHistory", func(t *testing.T) {
		slip := &Slip{
			CorrelationID: "test-corr-006",
			Repository:    "myorg/history-test",
			Branch:        "main",
			CommitSHA:     "hist123",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Components:    []Component{},
			Steps:         map[string]Step{},
			StateHistory:  []StateHistoryEntry{},
		}

		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Append history entry
		entry := StateHistoryEntry{
			Timestamp: time.Now(),
			Step:      "test",
			Message:   "Test history entry",
			Actor:     "integration-test",
		}
		err = store.AppendHistory(ctx, slip.CorrelationID, entry)
		if err != nil {
			t.Fatalf("AppendHistory() error = %v", err)
		}

		// Verify
		loaded, err := store.Load(ctx, slip.CorrelationID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if len(loaded.StateHistory) != 1 {
			t.Errorf("len(StateHistory) = %d, want 1", len(loaded.StateHistory))
		}
		if loaded.StateHistory[0].Message != "Test history entry" {
			t.Errorf("Message = %q, want 'Test history entry'", loaded.StateHistory[0].Message)
		}
	})

	t.Run("FindByCommits", func(t *testing.T) {
		// Create a slip
		slip := &Slip{
			CorrelationID: "test-corr-007",
			Repository:    "myorg/find-test",
			Branch:        "main",
			CommitSHA:     "target-commit-abc",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Components:    []Component{},
			Steps:         map[string]Step{},
			StateHistory:  []StateHistoryEntry{},
		}

		err := store.Create(ctx, slip)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Find by commits - target commit is third in ancestry
		commits := []string{"newer-commit", "middle-commit", "target-commit-abc", "older-commit"}
		found, matchedCommit, err := store.FindByCommits(ctx, slip.Repository, commits)
		if err != nil {
			t.Fatalf("FindByCommits() error = %v", err)
		}

		if found.CorrelationID != slip.CorrelationID {
			t.Errorf("CorrelationID = %q, want %q", found.CorrelationID, slip.CorrelationID)
		}
		if matchedCommit != "target-commit-abc" {
			t.Errorf("matchedCommit = %q, want 'target-commit-abc'", matchedCommit)
		}
	})

	t.Run("FindByCommits_NotFound", func(t *testing.T) {
		commits := []string{"nonexistent1", "nonexistent2"}
		_, _, err := store.FindByCommits(ctx, "myorg/nonexistent-repo", commits)
		if err != ErrSlipNotFound {
			t.Errorf("FindByCommits() error = %v, want ErrSlipNotFound", err)
		}
	})

	t.Run("FindByCommits_EmptyList", func(t *testing.T) {
		_, _, err := store.FindByCommits(ctx, "myorg/repo", []string{})
		if err == nil {
			t.Error("FindByCommits() with empty commits should return error")
		}
	})
}

// TestClickHouseStore_NewFromConn tests creating a store from an existing connection.
func TestClickHouseStore_NewFromConn(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	container, err := setupClickHouseContainer(ctx)
	if err != nil {
		t.Fatalf("Failed to start ClickHouse container: %v", err)
	}
	defer container.Terminate(ctx)

	// Open connection manually
	opts, err := clickhouse.ParseDSN(container.DSN)
	if err != nil {
		t.Fatalf("Failed to parse DSN: %v", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open connection: %v", err)
	}
	defer conn.Close()

	// Run migrations manually
	_, err = RunMigrations(ctx, conn, MigrateOptions{})
	if err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create store from existing connection
	store := NewClickHouseStoreFromConn(conn)

	// Verify it works
	if store.Conn() != conn {
		t.Error("Conn() should return the same connection")
	}

	// Create a slip
	slip := &Slip{
		CorrelationID: "from-conn-001",
		Repository:    "test/repo",
		Branch:        "main",
		CommitSHA:     "abc123",
		Status:        SlipStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Components:    []Component{},
		Steps:         map[string]Step{},
		StateHistory:  []StateHistoryEntry{},
	}

	err = store.Create(ctx, slip)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	loaded, err := store.Load(ctx, slip.CorrelationID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.CorrelationID != slip.CorrelationID {
		t.Errorf("CorrelationID = %q, want %q", loaded.CorrelationID, slip.CorrelationID)
	}
}

// TestClickHouseStore_SkipMigrations tests creating a store without running migrations.
func TestClickHouseStore_SkipMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	container, err := setupClickHouseContainer(ctx)
	if err != nil {
		t.Fatalf("Failed to start ClickHouse container: %v", err)
	}
	defer container.Terminate(ctx)

	// First, run migrations with a store
	store1, err := NewClickHouseStore(container.DSN)
	if err != nil {
		t.Fatalf("Failed to create first store: %v", err)
	}
	store1.Close()

	// Now create another store with SkipMigrations
	store2, err := NewClickHouseStoreWithOptions(container.DSN, ClickHouseStoreOptions{
		SkipMigrations: true,
	})
	if err != nil {
		t.Fatalf("Failed to create store with SkipMigrations: %v", err)
	}
	defer store2.Close()

	// Should be able to use the store (schema already exists from store1)
	slip := &Slip{
		CorrelationID: "skip-mig-001",
		Repository:    "test/skip",
		Branch:        "main",
		CommitSHA:     "skip123",
		Status:        SlipStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Components:    []Component{},
		Steps:         map[string]Step{},
		StateHistory:  []StateHistoryEntry{},
	}

	err = store2.Create(ctx, slip)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

// TestClickHouseStore_StepTimestamps verifies step timestamps are persisted correctly.
func TestClickHouseStore_StepTimestamps(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	container, err := setupClickHouseContainer(ctx)
	if err != nil {
		t.Fatalf("Failed to start ClickHouse container: %v", err)
	}
	defer container.Terminate(ctx)

	store, err := NewClickHouseStore(container.DSN)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	startTime := time.Now().Add(-time.Hour)
	completedTime := time.Now()

	slip := &Slip{
		CorrelationID: "timestamps-001",
		Repository:    "myorg/timestamp-test",
		Branch:        "main",
		CommitSHA:     "ts123",
		Status:        SlipStatusCompleted,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Components:    []Component{},
		Steps: map[string]Step{
			"push_parsed": {
				Status:      StepStatusCompleted,
				StartedAt:   &startTime,
				CompletedAt: &completedTime,
			},
		},
		StateHistory: []StateHistoryEntry{},
	}

	err = store.Create(ctx, slip)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	loaded, err := store.Load(ctx, slip.CorrelationID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	step := loaded.Steps["push_parsed"]
	if step.StartedAt == nil {
		t.Error("StartedAt should not be nil")
	} else if step.StartedAt.Unix() != startTime.Unix() {
		t.Errorf("StartedAt = %v, want %v", step.StartedAt, startTime)
	}

	if step.CompletedAt == nil {
		t.Error("CompletedAt should not be nil")
	} else if step.CompletedAt.Unix() != completedTime.Unix() {
		t.Errorf("CompletedAt = %v, want %v", step.CompletedAt, completedTime)
	}
}
