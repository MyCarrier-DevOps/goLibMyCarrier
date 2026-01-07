package slippy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse/clickhousetest"
)

// testPipelineConfig returns a minimal pipeline config for testing.
// The config is properly initialized with internal lookup maps.
func testPipelineConfig() *PipelineConfig {
	config := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline config",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push parsed"},
			{Name: "builds_completed", Description: "Builds completed", Aggregates: "build", Prerequisites: []string{"push_parsed"}},
			{Name: "unit_tests_completed", Description: "Unit tests completed", Aggregates: "unit_test", Prerequisites: []string{"builds_completed"}},
			{Name: "dev_deploy", Description: "Dev deploy", Prerequisites: []string{"unit_tests_completed"}},
		},
	}
	// Initialize internal lookup maps (same as what LoadPipelineConfig does)
	config.stepsByName = make(map[string]*StepConfig)
	config.aggregateMap = make(map[string]string)
	config.gateSteps = make([]string, 0)
	for i := range config.Steps {
		step := &config.Steps[i]
		step.order = i
		config.stepsByName[step.Name] = step
		if step.Aggregates != "" {
			config.aggregateMap[step.Aggregates] = step.Name
		}
		if step.IsGate {
			config.gateSteps = append(config.gateSteps, step.Name)
		}
	}
	return config
}

// TestNewClickHouseStoreFromSession tests creating a store from an existing session.
func TestNewClickHouseStoreFromSession(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	if store == nil {
		t.Fatal("expected store to be non-nil")
	}
	if store.Session() == nil {
		t.Fatal("expected session to be non-nil")
	}
}

// TestClickHouseStore_Session tests the Session accessor method.
func TestClickHouseStore_Session(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	session := store.Session()
	if session == nil {
		t.Fatal("expected session to be non-nil")
	}
}

// TestClickHouseStore_Conn tests the Conn accessor method.
func TestClickHouseStore_Conn(t *testing.T) {
	mockConn := &clickhousetest.MockConn{}
	mockSession := &clickhousetest.MockSession{
		ConnConn: mockConn,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	conn := store.Conn()
	if conn != mockConn {
		t.Fatal("expected Conn to return the mock connection")
	}
	if mockSession.ConnCalls != 1 {
		t.Errorf("expected 1 Conn call, got %d", mockSession.ConnCalls)
	}
}

// TestClickHouseStore_Close tests the Close method.
func TestClickHouseStore_Close(t *testing.T) {
	t.Run("successful close", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.Close()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if mockSession.CloseCalls != 1 {
			t.Errorf("expected 1 Close call, got %d", mockSession.CloseCalls)
		}
	})

	t.Run("close with error", func(t *testing.T) {
		expectedErr := errors.New("close error")
		mockSession := &clickhousetest.MockSession{
			CloseErr: expectedErr,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.Close()
		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

// TestClickHouseStore_Create tests the Create method.
func TestClickHouseStore_Create(t *testing.T) {
	t.Run("successful create", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Aggregates: map[string][]ComponentStepData{
				"builds": {{Component: "api", Status: StepStatusPending}},
			},
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusCompleted},
			},
		}

		err := store.Create(context.Background(), slip)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf("expected 1 ExecWithArgs call, got %d", len(mockSession.ExecWithArgsCalls))
		}
	})

	t.Run("create with exec error", func(t *testing.T) {
		expectedErr := errors.New("exec error")
		mockSession := &clickhousetest.MockSession{
			ExecWithArgsErr: expectedErr,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		err := store.Create(context.Background(), slip)
		if err == nil {
			t.Error("expected error, got nil")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error to wrap %v, got %v", expectedErr, err)
		}
	})

	t.Run("create with step timestamps", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		now := time.Now()
		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			CreatedAt:     now,
			UpdatedAt:     now,
			Steps: map[string]Step{
				"push_parsed": {
					Status:      StepStatusCompleted,
					StartedAt:   &now,
					CompletedAt: &now,
				},
				"builds_completed": {
					Status:    StepStatusRunning,
					StartedAt: &now,
				},
			},
		}

		err := store.Create(context.Background(), slip)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("create with state history", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		now := time.Now()
		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			CreatedAt:     now,
			UpdatedAt:     now,
			StateHistory: []StateHistoryEntry{
				{
					Timestamp: now,
					Step:      "push_parsed",
					Status:    StepStatusRunning,
					Actor:     "system",
				},
			},
		}

		err := store.Create(context.Background(), slip)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}

// TestClickHouseStore_Load tests the Load method.
func TestClickHouseStore_Load(t *testing.T) {
	t.Run("load not found", func(t *testing.T) {
		// Create a mock row that returns no data
		mockRow := &clickhousetest.MockRow{
			ScanErr: ErrSlipNotFound,
		}
		mockSession := &clickhousetest.MockSession{
			QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (ch.Rows, error) {
				return &clickhousetest.MockRows{
					NextData: []bool{false}, // No rows
				}, nil
			},
		}
		mockSession.QueryRowRow = mockRow
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		_, err := store.Load(context.Background(), "nonexistent")
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("expected ErrSlipNotFound, got %v", err)
		}
	})

	t.Run("load with query error", func(t *testing.T) {
		expectedErr := errors.New("query error")
		mockRow := &clickhousetest.MockRow{
			ScanErr: expectedErr,
		}
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: mockRow,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		_, err := store.Load(context.Background(), "test-corr-001")
		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}

// TestClickHouseStore_LoadByCommit tests the LoadByCommit method.
func TestClickHouseStore_LoadByCommit(t *testing.T) {
	t.Run("load by commit not found", func(t *testing.T) {
		mockRow := &clickhousetest.MockRow{
			ScanErr: ErrSlipNotFound,
		}
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: mockRow,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		_, err := store.LoadByCommit(context.Background(), "myorg/myrepo", "nonexistent")
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("expected ErrSlipNotFound, got %v", err)
		}
	})
}

// TestClickHouseStore_FindByCommits tests the FindByCommits method.
func TestClickHouseStore_FindByCommits(t *testing.T) {
	t.Run("find by commits empty list", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		_, _, err := store.FindByCommits(context.Background(), "myorg/myrepo", []string{})
		if err == nil {
			t.Error("expected error for empty commits list, got nil")
		}
	})

	t.Run("find by commits not found", func(t *testing.T) {
		mockRow := &clickhousetest.MockRow{
			ScanErr: ErrSlipNotFound,
		}
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: mockRow,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		_, _, err := store.FindByCommits(context.Background(), "myorg/myrepo", []string{"abc123", "def456"})
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("expected ErrSlipNotFound, got %v", err)
		}
	})
}

// TestClickHouseStore_Update tests the Update method.
func TestClickHouseStore_Update(t *testing.T) {
	t.Run("successful update", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		err := store.Update(context.Background(), slip)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		// Update should update the UpdatedAt timestamp and call Create
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf("expected 1 ExecWithArgs call, got %d", len(mockSession.ExecWithArgsCalls))
		}
	})
}

// TestClickHouseStore_UpdateStep tests the UpdateStep method.
func TestClickHouseStore_UpdateStep(t *testing.T) {
	t.Run("update step - load fails", func(t *testing.T) {
		mockRow := &clickhousetest.MockRow{
			ScanErr: ErrSlipNotFound,
		}
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: mockRow,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.UpdateStep(context.Background(), "test-corr-001", "push_parsed", "", StepStatusCompleted)
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("expected ErrSlipNotFound, got %v", err)
		}
	})
}

// TestClickHouseStore_UpdateComponentStatus tests the UpdateComponentStatus method.
func TestClickHouseStore_UpdateComponentStatus(t *testing.T) {
	t.Run("update component status - load fails", func(t *testing.T) {
		mockRow := &clickhousetest.MockRow{
			ScanErr: ErrSlipNotFound,
		}
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: mockRow,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.UpdateComponentStatus(context.Background(), "test-corr-001", "api", "build", StepStatusCompleted)
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("expected ErrSlipNotFound, got %v", err)
		}
	})
}

// TestClickHouseStore_AppendHistory tests the AppendHistory method.
func TestClickHouseStore_AppendHistory(t *testing.T) {
	t.Run("append history - load fails", func(t *testing.T) {
		mockRow := &clickhousetest.MockRow{
			ScanErr: ErrSlipNotFound,
		}
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: mockRow,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		entry := StateHistoryEntry{
			Timestamp: time.Now(),
			Step:      "push_parsed",
			Status:    StepStatusCompleted,
		}

		err := store.AppendHistory(context.Background(), "test-corr-001", entry)
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("expected ErrSlipNotFound, got %v", err)
		}
	})
}

// TestNewClickHouseStoreFromConn tests creating a store from an existing connection.
func TestNewClickHouseStoreFromConn(t *testing.T) {
	mockConn := &clickhousetest.MockConn{}
	store := NewClickHouseStoreFromConn(mockConn, testPipelineConfig(), "ci")

	if store == nil {
		t.Fatal("expected store to be non-nil")
	}
	if store.Session() == nil {
		t.Fatal("expected session to be non-nil")
	}
	// Verify the connection is accessible through Session().Conn()
	conn := store.Conn()
	if conn != mockConn {
		t.Error("expected Conn to return the wrapped connection")
	}
}

// createMockScanRow creates a mock row that returns valid slip data for the test config.
// Column layout for test config (4 steps, 2 aggregates):
// 0: correlation_id, 1: repository, 2: branch, 3: commit_sha
// 4: created_at, 5: updated_at, 6: status
// 7: step_details (JSON), 8: state_history (JSON)
// 9-12: step statuses (push_parsed, builds_completed, unit_tests_completed, dev_deploy)
// 13: builds (aggregate JSON), 14: unit_tests (aggregate JSON)
func createMockScanRow(correlationID, repository, branch, commitSHA string, status SlipStatus) *clickhousetest.MockRow {
	now := time.Now()
	stepDetailsJSON, _ := json.Marshal(map[string]map[string]interface{}{
		"push_parsed": {
			"started_at":   now.Format(time.RFC3339Nano),
			"completed_at": now.Format(time.RFC3339Nano),
		},
	})
	stateHistoryJSON, _ := json.Marshal([]StateHistoryEntry{
		{Timestamp: now, Step: "push_parsed", Status: StepStatusCompleted},
	})
	buildsJSON, _ := json.Marshal([]ComponentStepData{
		{Component: "api", Status: StepStatusCompleted},
	})
	unitTestsJSON, _ := json.Marshal([]ComponentStepData{
		{Component: "api", Status: StepStatusPending},
	})

	return &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Test config has 4 steps, 2 aggregates = 15 columns
			if len(dest) < 15 {
				return fmt.Errorf("not enough scan destinations: got %d, want 15", len(dest))
			}
			// Set correlation_id
			if ptr, ok := dest[0].(*string); ok {
				*ptr = correlationID
			}
			// Set repository
			if ptr, ok := dest[1].(*string); ok {
				*ptr = repository
			}
			// Set branch
			if ptr, ok := dest[2].(*string); ok {
				*ptr = branch
			}
			// Set commit_sha
			if ptr, ok := dest[3].(*string); ok {
				*ptr = commitSHA
			}
			// Set created_at
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = now
			}
			// Set updated_at
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = now
			}
			// Set status
			if ptr, ok := dest[6].(*string); ok {
				*ptr = string(status)
			}
			// Set step_details JSON
			if ptr, ok := dest[7].(*string); ok {
				*ptr = string(stepDetailsJSON)
			}
			// Set state_history JSON
			if ptr, ok := dest[8].(*string); ok {
				*ptr = string(stateHistoryJSON)
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = string(StepStatusPending)
				}
			}
			// Set builds aggregate JSON
			if ptr, ok := dest[13].(*string); ok {
				*ptr = string(buildsJSON)
			}
			// Set unit_tests aggregate JSON
			if ptr, ok := dest[14].(*string); ok {
				*ptr = string(unitTestsJSON)
			}
			return nil
		},
	}
}

// TestClickHouseStore_Load_Success tests successful slip loading.
func TestClickHouseStore_Load_Success(t *testing.T) {
	mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, err := store.Load(context.Background(), "test-corr-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if slip == nil {
		t.Fatal("expected slip to be non-nil")
	}
	if slip.CorrelationID != "test-corr-001" {
		t.Errorf("expected correlation_id 'test-corr-001', got '%s'", slip.CorrelationID)
	}
	if slip.Repository != "myorg/myrepo" {
		t.Errorf("expected repository 'myorg/myrepo', got '%s'", slip.Repository)
	}
	if slip.Branch != "main" {
		t.Errorf("expected branch 'main', got '%s'", slip.Branch)
	}
	// Test pipeline config has 4 steps
	if len(slip.Steps) != 4 {
		t.Errorf("expected 4 steps, got %d", len(slip.Steps))
	}
	if len(slip.StateHistory) != 1 {
		t.Errorf("expected 1 state history entry, got %d", len(slip.StateHistory))
	}
}

// TestClickHouseStore_LoadByCommit_Success tests successful slip loading by commit.
func TestClickHouseStore_LoadByCommit_Success(t *testing.T) {
	mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, err := store.LoadByCommit(context.Background(), "myorg/myrepo", "abc123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if slip == nil {
		t.Fatal("expected slip to be non-nil")
	}
	if slip.CommitSHA != "abc123" {
		t.Errorf("expected commit_sha 'abc123', got '%s'", slip.CommitSHA)
	}
}

// TestClickHouseStore_Load_ErrNoRows tests sql.ErrNoRows handling.
func TestClickHouseStore_Load_ErrNoRows(t *testing.T) {
	mockRow := &clickhousetest.MockRow{
		ScanErr: sql.ErrNoRows,
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	_, err := store.Load(context.Background(), "nonexistent")
	if !errors.Is(err, ErrSlipNotFound) {
		t.Errorf("expected ErrSlipNotFound, got %v", err)
	}
}

// TestClickHouseStore_Load_InvalidAggregateJSON tests that invalid aggregate JSON is handled gracefully.
// Invalid aggregate JSON should not cause an error - it just results in empty aggregates.
func TestClickHouseStore_Load_InvalidAggregateJSON(t *testing.T) {
	now := time.Now()
	mockRow := &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Set required fields
			if ptr, ok := dest[0].(*string); ok {
				*ptr = "test-corr-001"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = "myorg/myrepo"
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = "main"
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "abc123"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[6].(*string); ok {
				*ptr = "pending"
			}
			// Valid JSON for step_details
			if ptr, ok := dest[7].(*string); ok {
				*ptr = "{}"
			}
			// Valid JSON for state_history
			if ptr, ok := dest[8].(*string); ok {
				*ptr = "[]"
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Invalid JSON for builds aggregate - should be handled gracefully
			if ptr, ok := dest[13].(*string); ok {
				*ptr = "not valid json"
			}
			// Valid JSON for unit_tests aggregate
			if ptr, ok := dest[14].(*string); ok {
				*ptr = "[]"
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, err := store.Load(context.Background(), "test-corr-001")
	// Invalid aggregate JSON should not cause an error
	if err != nil {
		t.Errorf("expected no error for invalid aggregate JSON, got %v", err)
	}
	// The builds aggregate should be empty due to invalid JSON
	if slip != nil && len(slip.Aggregates["builds"]) != 0 {
		t.Errorf("expected empty builds aggregate due to invalid JSON, got %d items", len(slip.Aggregates["builds"]))
	}
}

// TestClickHouseStore_Load_InvalidStateHistoryJSON tests invalid state history JSON handling.
func TestClickHouseStore_Load_InvalidStateHistoryJSON(t *testing.T) {
	now := time.Now()
	buildsJSON, _ := json.Marshal([]ComponentStepData{})
	unitTestsJSON, _ := json.Marshal([]ComponentStepData{})
	mockRow := &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Set required fields
			if ptr, ok := dest[0].(*string); ok {
				*ptr = "test-corr-001"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = "myorg/myrepo"
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = "main"
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "abc123"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[6].(*string); ok {
				*ptr = "pending"
			}
			// Valid step_details JSON
			if ptr, ok := dest[7].(*string); ok {
				*ptr = "{}"
			}
			// Invalid state history JSON
			if ptr, ok := dest[8].(*string); ok {
				*ptr = "not valid json"
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Valid aggregate JSONs
			if ptr, ok := dest[13].(*string); ok {
				*ptr = string(buildsJSON)
			}
			if ptr, ok := dest[14].(*string); ok {
				*ptr = string(unitTestsJSON)
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	_, err := store.Load(context.Background(), "test-corr-001")
	if err == nil {
		t.Error("expected error for invalid state history JSON, got nil")
	}
}

// TestClickHouseStore_UpdateStep_Success tests successful step update.
func TestClickHouseStore_UpdateStep_Success(t *testing.T) {
	mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateStep(context.Background(), "test-corr-001", "push_parsed", "", StepStatusCompleted)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	// Should have called QueryRow (for Load) and ExecWithArgs (for Create/Update)
	if len(mockSession.QueryRowCalls) != 1 {
		t.Errorf("expected 1 QueryRow call, got %d", len(mockSession.QueryRowCalls))
	}
	if len(mockSession.ExecWithArgsCalls) != 1 {
		t.Errorf("expected 1 ExecWithArgs call, got %d", len(mockSession.ExecWithArgsCalls))
	}
}

// TestClickHouseStore_UpdateStep_WithComponent tests step update for a specific component.
func TestClickHouseStore_UpdateStep_WithComponent(t *testing.T) {
	mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateStep(context.Background(), "test-corr-001", "builds_completed", "api", StepStatusCompleted)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestClickHouseStore_UpdateComponentStatus_Success tests successful component status update.
func TestClickHouseStore_UpdateComponentStatus_Success(t *testing.T) {
	mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateComponentStatus(context.Background(), "test-corr-001", "api", "build", StepStatusCompleted)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestClickHouseStore_AppendHistory_Success tests successful history append.
func TestClickHouseStore_AppendHistory_Success(t *testing.T) {
	mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "test",
	}

	err := store.AppendHistory(context.Background(), "test-corr-001", entry)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// createMockScanRowWithMatch creates a mock row for FindByCommits that includes matchedCommit.
// Column layout for test config (4 steps, 2 aggregates) + matched_commit:
// 0: correlation_id, 1: repository, 2: branch, 3: commit_sha
// 4: created_at, 5: updated_at, 6: status
// 7: step_details (JSON), 8: state_history (JSON)
// 9-12: step statuses (push_parsed, builds_completed, unit_tests_completed, dev_deploy)
// 13: builds (aggregate JSON), 14: unit_tests (aggregate JSON)
// 15: matched_commit
func createMockScanRowWithMatch(correlationID, repository, branch, commitSHA, matchedCommit string, status SlipStatus) *clickhousetest.MockRow {
	now := time.Now()
	stepDetailsJSON, _ := json.Marshal(map[string]map[string]interface{}{})
	stateHistoryJSON, _ := json.Marshal([]StateHistoryEntry{})
	buildsJSON, _ := json.Marshal([]ComponentStepData{
		{Component: "api", Status: StepStatusCompleted},
	})
	unitTestsJSON, _ := json.Marshal([]ComponentStepData{})

	return &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Test config has 4 steps, 2 aggregates + matched_commit = 16 columns
			if len(dest) < 16 {
				return fmt.Errorf("not enough scan destinations for scanSlipWithMatch: got %d, want 16", len(dest))
			}
			// Set correlation_id
			if ptr, ok := dest[0].(*string); ok {
				*ptr = correlationID
			}
			// Set repository
			if ptr, ok := dest[1].(*string); ok {
				*ptr = repository
			}
			// Set branch
			if ptr, ok := dest[2].(*string); ok {
				*ptr = branch
			}
			// Set commit_sha
			if ptr, ok := dest[3].(*string); ok {
				*ptr = commitSHA
			}
			// Set created_at
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = now
			}
			// Set updated_at
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = now
			}
			// Set status
			if ptr, ok := dest[6].(*string); ok {
				*ptr = string(status)
			}
			// Set step_details JSON
			if ptr, ok := dest[7].(*string); ok {
				*ptr = string(stepDetailsJSON)
			}
			// Set state_history JSON
			if ptr, ok := dest[8].(*string); ok {
				*ptr = string(stateHistoryJSON)
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = string(StepStatusPending)
				}
			}
			// Set builds aggregate JSON
			if ptr, ok := dest[13].(*string); ok {
				*ptr = string(buildsJSON)
			}
			// Set unit_tests aggregate JSON
			if ptr, ok := dest[14].(*string); ok {
				*ptr = string(unitTestsJSON)
			}
			// Set matched_commit
			if ptr, ok := dest[15].(*string); ok {
				*ptr = matchedCommit
			}
			return nil
		},
	}
}

// TestClickHouseStore_FindByCommits_Success tests successful FindByCommits.
func TestClickHouseStore_FindByCommits_Success(t *testing.T) {
	mockRow := createMockScanRowWithMatch("test-corr-001", "myorg/myrepo", "main", "abc123", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, matchedCommit, err := store.FindByCommits(context.Background(), "myorg/myrepo", []string{"abc123", "def456"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if slip == nil {
		t.Fatal("expected slip to be non-nil")
	}
	if matchedCommit != "abc123" {
		t.Errorf("expected matched_commit 'abc123', got '%s'", matchedCommit)
	}
}

// TestClickHouseStore_FindByCommits_InvalidAggregateJSON tests that invalid aggregate JSON is handled gracefully in FindByCommits.
func TestClickHouseStore_FindByCommits_InvalidAggregateJSON(t *testing.T) {
	now := time.Now()
	mockRow := &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Set required fields
			if ptr, ok := dest[0].(*string); ok {
				*ptr = "test-corr-001"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = "myorg/myrepo"
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = "main"
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "abc123"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[6].(*string); ok {
				*ptr = "pending"
			}
			// Valid step_details JSON
			if ptr, ok := dest[7].(*string); ok {
				*ptr = "{}"
			}
			// Valid state_history JSON
			if ptr, ok := dest[8].(*string); ok {
				*ptr = "[]"
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Invalid JSON for builds aggregate - should be handled gracefully
			if ptr, ok := dest[13].(*string); ok {
				*ptr = "not valid json"
			}
			// Valid JSON for unit_tests aggregate
			if ptr, ok := dest[14].(*string); ok {
				*ptr = "[]"
			}
			// Matched commit
			if ptr, ok := dest[15].(*string); ok {
				*ptr = "abc123"
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, _, err := store.FindByCommits(context.Background(), "myorg/myrepo", []string{"abc123"})
	// Invalid aggregate JSON should not cause an error
	if err != nil {
		t.Errorf("expected no error for invalid aggregate JSON, got %v", err)
	}
	// The builds aggregate should be empty due to invalid JSON
	if slip != nil && len(slip.Aggregates["builds"]) != 0 {
		t.Errorf("expected empty builds aggregate due to invalid JSON, got %d items", len(slip.Aggregates["builds"]))
	}
}

// TestClickHouseStore_FindByCommits_InvalidStateHistoryJSON tests invalid state history JSON in FindByCommits.
func TestClickHouseStore_FindByCommits_InvalidStateHistoryJSON(t *testing.T) {
	now := time.Now()
	buildsJSON, _ := json.Marshal([]ComponentStepData{})
	unitTestsJSON, _ := json.Marshal([]ComponentStepData{})
	mockRow := &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Set required fields
			if ptr, ok := dest[0].(*string); ok {
				*ptr = "test-corr-001"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = "myorg/myrepo"
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = "main"
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "abc123"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[6].(*string); ok {
				*ptr = "pending"
			}
			// Valid step_details JSON
			if ptr, ok := dest[7].(*string); ok {
				*ptr = "{}"
			}
			// Invalid state history JSON
			if ptr, ok := dest[8].(*string); ok {
				*ptr = "not valid json"
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Valid aggregate JSONs
			if ptr, ok := dest[13].(*string); ok {
				*ptr = string(buildsJSON)
			}
			if ptr, ok := dest[14].(*string); ok {
				*ptr = string(unitTestsJSON)
			}
			// Matched commit
			if ptr, ok := dest[15].(*string); ok {
				*ptr = "abc123"
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	_, _, err := store.FindByCommits(context.Background(), "myorg/myrepo", []string{"abc123"})
	if err == nil {
		t.Error("expected error for invalid state history JSON, got nil")
	}
}

// TestClickHouseStore_Load_WithStepTimestamps tests parsing step timestamps from JSON.
func TestClickHouseStore_Load_WithStepTimestamps(t *testing.T) {
	now := time.Now()
	buildsJSON, _ := json.Marshal([]ComponentStepData{})
	unitTestsJSON, _ := json.Marshal([]ComponentStepData{})
	// Create step details with both started_at and completed_at
	stepDetailsJSON, _ := json.Marshal(map[string]map[string]interface{}{
		"push_parsed": {
			"started_at":   now.Format(time.RFC3339Nano),
			"completed_at": now.Format(time.RFC3339Nano),
		},
		"builds_completed": {
			"started_at": now.Format(time.RFC3339Nano),
		},
	})
	stateHistoryJSON, _ := json.Marshal([]StateHistoryEntry{})

	mockRow := &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			if ptr, ok := dest[0].(*string); ok {
				*ptr = "test-corr-001"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = "myorg/myrepo"
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = "main"
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "abc123"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = now
			}
			if ptr, ok := dest[6].(*string); ok {
				*ptr = "pending"
			}
			// Set step_details JSON
			if ptr, ok := dest[7].(*string); ok {
				*ptr = string(stepDetailsJSON)
			}
			// Set state_history JSON
			if ptr, ok := dest[8].(*string); ok {
				*ptr = string(stateHistoryJSON)
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Set aggregate JSONs
			if ptr, ok := dest[13].(*string); ok {
				*ptr = string(buildsJSON)
			}
			if ptr, ok := dest[14].(*string); ok {
				*ptr = string(unitTestsJSON)
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, err := store.Load(context.Background(), "test-corr-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify push_parsed has both timestamps
	pushParsed := slip.Steps["push_parsed"]
	if pushParsed.StartedAt == nil {
		t.Error("expected push_parsed.StartedAt to be set")
	}
	if pushParsed.CompletedAt == nil {
		t.Error("expected push_parsed.CompletedAt to be set")
	}

	// Verify builds_completed has only started_at
	buildsCompleted := slip.Steps["builds_completed"]
	if buildsCompleted.StartedAt == nil {
		t.Error("expected builds_completed.StartedAt to be set")
	}
	if buildsCompleted.CompletedAt != nil {
		t.Error("expected builds_completed.CompletedAt to be nil")
	}
}

// TestClickHouseStore_Update_UpdatesTimestamp tests that Update sets UpdatedAt.
func TestClickHouseStore_Update_UpdatesTimestamp(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	originalTime := time.Now().Add(-1 * time.Hour)
	slip := &Slip{
		CorrelationID: "test-corr-001",
		Repository:    "myorg/myrepo",
		Branch:        "main",
		CommitSHA:     "abc123",
		Status:        SlipStatusInProgress,
		CreatedAt:     originalTime,
		UpdatedAt:     originalTime,
	}

	err := store.Update(context.Background(), slip)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Verify UpdatedAt was updated (should be after original time)
	if !slip.UpdatedAt.After(originalTime) {
		t.Error("expected UpdatedAt to be updated to a later time")
	}
}

// TestClickHouseStore_FindByCommits_QueryError tests query error handling in FindByCommits.
func TestClickHouseStore_FindByCommits_QueryError(t *testing.T) {
	expectedErr := errors.New("query error")
	mockRow := &clickhousetest.MockRow{
		ScanErr: expectedErr,
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	_, _, err := store.FindByCommits(context.Background(), "myorg/myrepo", []string{"abc123"})
	if err == nil {
		t.Error("expected error, got nil")
	}
}
