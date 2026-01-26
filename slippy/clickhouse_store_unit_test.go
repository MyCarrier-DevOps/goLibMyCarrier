package slippy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/chcol"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

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
			{
				Name:          "builds",
				Description:   "Builds completed",
				Aggregates:    "build",
				Prerequisites: []string{"push_parsed"},
			},
			{
				Name:          "unit_tests",
				Description:   "Unit tests completed",
				Aggregates:    "unit_test",
				Prerequisites: []string{"builds"},
			},
			{Name: "dev_deploy", Description: "Dev deploy", Prerequisites: []string{"unit_tests"}},
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

// TestClickHouseStore_PipelineConfig tests the PipelineConfig accessor method.
func TestClickHouseStore_PipelineConfig(t *testing.T) {
	config := testPipelineConfig()
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, config, "ci")

	gotConfig := store.PipelineConfig()
	if gotConfig != config {
		t.Fatal("expected PipelineConfig to return the same config")
	}
	if gotConfig.Name != "test-pipeline" {
		t.Errorf("expected pipeline name 'test-pipeline', got '%s'", gotConfig.Name)
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
		// Track call count to return different versions on each call
		callCount := 0

		// Create a mock row factory for getMaxVersion
		// Post-insert verification: after insert, getMaxVersion should return our new version
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(ctx context.Context, query string, args ...any) ch.Row {
				callCount++
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*sql.NullInt64); ok {
								// The verification call should return a version that matches newVersion
								// Since we can't predict the exact nanosecond timestamp, we capture
								// and return it. In this test, we simulate success by returning
								// a high enough version that it appears to match.
								// We use a placeholder that the Update method will set.
								v.Int64 = 0 // Will be overwritten
								v.Valid = true
							}
						}
						return nil
					},
				}
			},
		}

		// Use a custom approach: make getMaxVersion return the slip's version after update
		// by using QueryRowRow which allows us to customize behavior
		var capturedNewVersion uint64
		mockSession.QueryRowFunc = func(ctx context.Context, query string, args ...any) ch.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if len(dest) > 0 {
						if v, ok := dest[0].(*sql.NullInt64); ok {
							// Return the captured version (set after ExecWithArgs is called)
							v.Int64 = int64(capturedNewVersion)
							v.Valid = true
						}
					}
					return nil
				},
			}
		}

		// Capture the new version when ExecWithArgs is called
		mockSession.ExecWithArgsFunc = func(ctx context.Context, query string, args ...any) error {
			// The new version is generated by Update() and passed to insertAtomicUpdateWithVersions
			// We need to capture it from the slip after the update sets it
			return nil
		}

		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Version:       1, // Set initial version
		}

		// Intercept the version after Update sets slip.Version
		// We need the mock to return the same version that Update generates
		// Since we can't predict it, we'll verify behavior differently:
		// The mock will succeed if QueryRow returns a version >= any value (we return MAX int)
		mockSession.QueryRowFunc = func(ctx context.Context, query string, args ...any) ch.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if len(dest) > 0 {
						if v, ok := dest[0].(*sql.NullInt64); ok {
							// Return the slip's new version (which was set by Update)
							// Since this is called after ExecWithArgs, slip.Version has been updated
							v.Int64 = int64(slip.Version)
							v.Valid = true
						}
					}
					return nil
				},
			}
		}

		err := store.Update(context.Background(), slip)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		// Update with VersionedCollapsingMergeTree inserts 2 rows atomically via UNION ALL
		// Plus OPTIMIZE TABLE call
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf(
				"expected 1 ExecWithArgs call (atomic cancel + new via UNION ALL), got %d",
				len(mockSession.ExecWithArgsCalls),
			)
		}
		// Verify slip.Version was updated to a timestamp-based version
		if slip.Version <= 1 {
			t.Errorf("expected slip.Version to be updated to a timestamp, got %d", slip.Version)
		}
	})

	t.Run("version conflict error", func(t *testing.T) {
		// With post-insert verification, version conflict is detected AFTER insert
		// The mock should return a different version than what Update generated
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(ctx context.Context, query string, args ...any) ch.Row {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*sql.NullInt64); ok {
								// Return a much higher version to simulate another update winning
								// Use max int64 value
								v.Int64 = int64(^uint64(0) >> 1) // Max int64
								v.Valid = true
							}
						}
						return nil
					},
				}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		slip := &Slip{
			CorrelationID: "test-corr-001",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Version:       1, // Slip has version 1
		}

		err := store.Update(context.Background(), slip)
		if !errors.Is(err, ErrVersionConflict) {
			t.Errorf("expected ErrVersionConflict, got %v", err)
		}
		// With post-insert verification, the insert happens BEFORE the conflict is detected
		// So we expect 1 ExecWithArgs call (the insert)
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf(
				"expected 1 ExecWithArgs call (insert before conflict detected), got %d",
				len(mockSession.ExecWithArgsCalls),
			)
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

		// Use a short timeout since slip-not-found retry logic waits 5+10+15 minutes
		// with production timeouts. The context timeout ensures the test completes quickly.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := store.UpdateStep(ctx, "test-corr-001", "push_parsed", "", StepStatusCompleted)
		// With short timeout, we expect context deadline exceeded OR slip not found error
		if !errors.Is(err, ErrSlipNotFound) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected ErrSlipNotFound or context.DeadlineExceeded, got %v", err)
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

		// Use a short timeout since slip-not-found retry logic waits 5+10+15 minutes
		// with production timeouts. The context timeout ensures the test completes quickly.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := store.UpdateComponentStatus(ctx, "test-corr-001", "api", "build", StepStatusCompleted)
		// With short timeout, we expect context deadline exceeded OR slip not found error
		if !errors.Is(err, ErrSlipNotFound) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected ErrSlipNotFound or context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("update component status - uses insert", func(t *testing.T) {
		execCalled := false
		insertToComponentStatesCalled := false
		slipRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)

		// Track the version being inserted so we can return it from max(version)
		// This prevents infinite retry loops in the post-insert verification
		var lastInsertedVersion uint64
		mockSession := &clickhousetest.MockSession{
			// Handle both Load and getMaxVersion queries
			QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
				if strings.Contains(query, "max(version)") {
					return &clickhousetest.MockRow{
						ScanFunc: func(dest ...any) error {
							if len(dest) > 0 {
								if v, ok := dest[0].(*sql.NullInt64); ok {
									// Return the version that was just inserted
									// This simulates the successful "winning" scenario
									v.Int64 = int64(lastInsertedVersion)
									v.Valid = true
								}
							}
							return nil
						},
					}
				}
				return slipRow
			},
			QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
				return &clickhousetest.MockRows{}, nil
			},
			ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
				execCalled = true
				if strings.Contains(stmt, "slip_component_states") {
					insertToComponentStatesCalled = true
				}
				// Capture the newVersion from the INSERT args for routing_slips
				// In insertAtomicUpdateWithVersions, the args are:
				// [0] = correlation_id, [1] = newVersion, [2...] = new row values
				if strings.Contains(stmt, "routing_slips") && strings.Contains(stmt, "INSERT") {
					if len(args) >= 2 {
						if v, ok := args[1].(uint64); ok {
							lastInsertedVersion = v
						}
					}
				}
				return nil
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.UpdateComponentStatus(context.Background(), "test-corr-001", "api", "build", StepStatusCompleted)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !execCalled {
			t.Error("expected ExecWithArgs to be called")
		}
		if !insertToComponentStatesCalled {
			t.Error("expected insert into slip_component_states")
		}
	})

	t.Run("update component status - uses insert", func(t *testing.T) {
		execCalled := false
		mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: mockRow,
			QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
				return &clickhousetest.MockRows{}, nil
			},
			ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
				execCalled = true
				if !strings.Contains(stmt, "slip_component_states") {
					return fmt.Errorf("expected insert into slip_component_states, got %s", stmt)
				}
				return nil
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.UpdateComponentStatus(context.Background(), "test-corr-001", "api", "build", StepStatusCompleted)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !execCalled {
			t.Error("expected ExecWithArgs to be called")
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

		// Use a short timeout since slip-not-found retry logic waits 5+10+15 minutes
		// with production timeouts. The context timeout ensures the test completes quickly.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := store.AppendHistory(ctx, "test-corr-001", entry)
		// With short timeout, we expect context deadline exceeded OR slip not found error
		if !errors.Is(err, ErrSlipNotFound) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected ErrSlipNotFound or context.DeadlineExceeded, got %v", err)
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
// 7: step_details (JSON), 8: state_history (JSON), 9: ancestry (JSON)
// 10-13: step statuses (push_parsed, builds_completed, unit_tests_completed, dev_deploy)
// 14: builds (aggregate JSON), 15: unit_tests (aggregate JSON)
func createMockScanRow(correlationID, repository, branch, commitSHA string, status SlipStatus) *clickhousetest.MockRow {
	now := time.Now()

	// Create step details data structure matching what ClickHouse JSON returns
	stepDetailsData := map[string]map[string]interface{}{
		"push_parsed": {
			"started_at":   now.Format(time.RFC3339Nano),
			"completed_at": now.Format(time.RFC3339Nano),
		},
	}
	// State history wrapped in object for ClickHouse JSON compatibility
	stateHistoryData := map[string]interface{}{
		"entries": []StateHistoryEntry{
			{Timestamp: now, Step: "push_parsed", Status: StepStatusCompleted},
		},
	}
	// Ancestry wrapped in object for ClickHouse JSON compatibility
	ancestryData := map[string]interface{}{
		"chain": []AncestryEntry{},
	}
	// Aggregates wrapped in object for ClickHouse JSON compatibility
	buildsData := map[string]interface{}{
		"items": []ComponentStepData{
			{Component: "api", Status: StepStatusCompleted},
		},
	}
	unitTestsData := map[string]interface{}{
		"items": []ComponentStepData{
			{Component: "api", Status: StepStatusPending},
		},
	}

	return &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Test config has 4 steps, 2 aggregates + sign + version = 18 columns
			// Column layout:
			// 0-6: core fields (correlation_id, repository, branch, commit_sha, created_at, updated_at, status)
			// 7-9: JSON fields (step_details, state_history, ancestry)
			// 10: sign, 11: version
			// 12-15: step statuses
			// 16-17: aggregate JSON (builds, unit_tests)
			if len(dest) < 18 {
				return fmt.Errorf("not enough scan destinations: got %d, want 18", len(dest))
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
			// Set step_details JSON - use Scan with map data for *chcol.JSON
			if jsonPtr, ok := dest[7].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(stepDetailsData)
			} else if ptr, ok := dest[7].(*string); ok {
				data, _ := json.Marshal(stepDetailsData)
				*ptr = string(data)
			}
			// Set state_history JSON - use Scan with map data for *chcol.JSON
			if jsonPtr, ok := dest[8].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(stateHistoryData)
			} else if ptr, ok := dest[8].(*string); ok {
				data, _ := json.Marshal(stateHistoryData)
				*ptr = string(data)
			}
			// Set ancestry JSON - use Scan with map data for *chcol.JSON
			if jsonPtr, ok := dest[9].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(ancestryData)
			} else if ptr, ok := dest[9].(*string); ok {
				data, _ := json.Marshal(ancestryData)
				*ptr = string(data)
			}
			// Set sign (index 10)
			if ptr, ok := dest[10].(*int8); ok {
				*ptr = 1
			}
			// Set version (index 11)
			if ptr, ok := dest[11].(*uint32); ok {
				*ptr = 1
			}
			// Set step statuses (4 steps, now at indices 12-15)
			for i := 12; i < 16; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = string(StepStatusPending)
				}
			}
			// Set builds aggregate JSON - use Scan with map data for *chcol.JSON (now at index 16)
			if jsonPtr, ok := dest[16].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			} else if ptr, ok := dest[16].(*string); ok {
				data, _ := json.Marshal(buildsData)
				*ptr = string(data)
			}
			// Set unit_tests aggregate JSON - use Scan with map data for *chcol.JSON (now at index 17)
			if jsonPtr, ok := dest[17].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			} else if ptr, ok := dest[17].(*string); ok {
				data, _ := json.Marshal(unitTestsData)
				*ptr = string(data)
			}
			return nil
		},
	}
}

// createMockSessionForUpdates creates a mock session that can handle both Load queries (returning a full slip)
// and getMaxVersion queries (returning a version number). This is needed because Update methods
// now call getMaxVersion to implement optimistic locking (post-insert verification).
// The mock tracks the version being inserted and returns it from subsequent max(version) queries.
func createMockSessionForUpdates(
	correlationID, repository, branch, commitSHA string,
	status SlipStatus,
	version uint64,
) *clickhousetest.MockSession {
	slipRow := createMockScanRow(correlationID, repository, branch, commitSHA, status)

	// Track the last inserted version to return from max(version) queries
	// This is essential for the post-insert verification to succeed
	var lastInsertedVersion uint64 = version

	return &clickhousetest.MockSession{
		QueryWithArgsRows: &clickhousetest.MockRows{CloseErr: nil}, // Handle hydration query
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			// getMaxVersion query
			if strings.Contains(query, "max(version)") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*sql.NullInt64); ok {
								// Return the last inserted version (or initial version if no insert yet)
								v.Int64 = int64(lastInsertedVersion)
								v.Valid = true
							}
						}
						return nil
					},
				}
			}
			// Return the slip row for Load queries
			return slipRow
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
			// Track the newVersion from INSERT statements for routing_slips
			// In insertAtomicUpdateWithVersions, the args are:
			// [0] = correlation_id, [1] = newVersion, [2...] = new row values
			if strings.Contains(stmt, "routing_slips") && strings.Contains(stmt, "INSERT") {
				if len(args) >= 2 {
					if v, ok := args[1].(uint64); ok {
						lastInsertedVersion = v
					}
				}
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
		QueryWithArgsRows: &clickhousetest.MockRows{
			CloseErr: nil, // Handle Close() call in hydration
		},
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

// TestClickHouseStore_Load_HydratesComponentStates ensures component states hydrate aggregate columns and steps.
func TestClickHouseStore_Load_HydratesComponentStates(t *testing.T) {
	stateTimestamp := time.Now()
	config := testPipelineConfig()

	// Get the aggregate step name from config - the step that aggregates "build"
	buildAggregateStep := config.GetAggregateStep("build")
	if buildAggregateStep == "" {
		t.Fatal("expected config to have an aggregate step for 'build'")
	}

	mockRows := &clickhousetest.MockRows{
		NextData: []bool{true, false},
		ScanFunc: func(dest ...any) error {
			if ptr, ok := dest[0].(*string); ok {
				*ptr = "build"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = "api"
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = string(StepStatusCompleted)
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "ok"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = stateTimestamp
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return mockRows, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, config, "ci")
	slip := &Slip{
		CorrelationID: "test-corr-001",
		Aggregates: map[string][]ComponentStepData{
			// The aggregate column name is the step name from config
			buildAggregateStep: {{Component: "api", Status: StepStatusPending}},
		},
		Steps: map[string]Step{
			buildAggregateStep: {Status: StepStatusPending},
		},
	}

	err := store.hydrateSlip(context.Background(), slip)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	components := slip.Aggregates[buildAggregateStep]
	if len(components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(components))
	}
	if components[0].Status != StepStatusCompleted {
		t.Errorf("expected component status completed, got %s", components[0].Status)
	}

	aggregateStep := slip.Steps[buildAggregateStep]
	if aggregateStep.Status != StepStatusCompleted {
		t.Errorf("expected aggregate step completed, got %s", aggregateStep.Status)
	}
}

// TestClickHouseStore_Load_HydratesComponentStates_AggregateStepName tests hydration when
// component states use the aggregate step name directly (e.g., "builds" instead of "build").
// This is the scenario when the CLI passes --step builds --component api.
func TestClickHouseStore_Load_HydratesComponentStates_AggregateStepName(t *testing.T) {
	stateTimestamp := time.Now()
	config := testPipelineConfig()

	// The aggregate step name is "builds" (the step that aggregates "build")
	aggregateStepName := "builds"

	// Verify the config is set up correctly
	if !config.IsAggregateStep(aggregateStepName) {
		t.Fatalf("expected %q to be an aggregate step", aggregateStepName)
	}

	// Mock returns component state with step="builds" (aggregate step name, not component type)
	mockRows := &clickhousetest.MockRows{
		NextData: []bool{true, false},
		ScanFunc: func(dest ...any) error {
			if ptr, ok := dest[0].(*string); ok {
				*ptr = "builds" // Using aggregate step name, not "build"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = "mc.example.api" // Component name from workflow
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = string(StepStatusCompleted)
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "ok"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = stateTimestamp
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return mockRows, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, config, "ci")

	// Initial slip has different component names (from pipeline definition)
	slip := &Slip{
		CorrelationID: "test-corr-001",
		Aggregates: map[string][]ComponentStepData{
			aggregateStepName: {
				{Component: "ExampleApi", Status: StepStatusPending},
				{Component: "ExampleWorker", Status: StepStatusPending},
			},
		},
		Steps: map[string]Step{
			aggregateStepName: {Status: StepStatusPending},
		},
	}

	err := store.hydrateSlip(context.Background(), slip)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should have 3 components now: 2 original + 1 new from component states
	components := slip.Aggregates[aggregateStepName]
	if len(components) != 3 {
		t.Fatalf("expected 3 components (2 original + 1 new), got %d: %+v", len(components), components)
	}

	// Find the new component and verify its status
	var newComponent *ComponentStepData
	for i := range components {
		if components[i].Component == "mc.example.api" {
			newComponent = &components[i]
			break
		}
	}
	if newComponent == nil {
		t.Fatal("expected to find component 'mc.example.api' in aggregates")
	}
	if newComponent.Status != StepStatusCompleted {
		t.Errorf("expected new component status completed, got %s", newComponent.Status)
	}

	// Original components should still be pending (they are placeholders, not updated)
	for _, comp := range components {
		if comp.Component == "ExampleApi" || comp.Component == "ExampleWorker" {
			if comp.Status != StepStatusPending {
				t.Errorf("expected original component %q status pending, got %s", comp.Component, comp.Status)
			}
		}
	}

	// Aggregate step status should be "completed" because the ONLY component state
	// (mc.example.api) is completed. The original placeholder components (ExampleApi,
	// ExampleWorker) don't have component state entries, so they are not considered
	// when computing the aggregate status.
	aggregateStep := slip.Steps[aggregateStepName]
	if aggregateStep.Status != StepStatusCompleted {
		t.Errorf(
			"expected aggregate step completed (only active component state is completed), got %s",
			aggregateStep.Status,
		)
	}
}

// TestClickHouseStore_HydrateSlip_AllComponentStatesCompleted_AggregateCompleted tests
// the scenario where ALL component states in the event sourcing table are completed,
// but the original pipeline components have different names (e.g., "ExampleApi" vs "mc.example.api").
// The aggregate status should be COMPLETED because all actual work is done,
// regardless of the original placeholder components.
//
// This reproduces the production issue where:
// - Pipeline defines: ExampleApi, ExampleWorker, etc. (pending placeholders)
// - Workflows report: mc.example.api, mc.example.worker, etc. (completed)
// - Expected: builds_status = "completed" (all real work is done)
// - Actual bug: builds_status = "running" (mixed pending/completed)
func TestClickHouseStore_HydrateSlip_AllComponentStatesCompleted_AggregateCompleted(t *testing.T) {
	stateTimestamp := time.Now()
	config := testPipelineConfig()
	aggregateStepName := config.GetAggregateStep("build") // "builds"

	// Mock returns ALL component states as completed (6 components like production)
	componentNames := []string{
		"mc.example.api",
		"mc.example.inboundintegration.worker",
		"mc.example.migrationtool",
		"mc.example.outboundintegration.worker",
		"mc.example.sagas.worker",
		"tests",
	}
	currentIdx := 0

	mockRows := &clickhousetest.MockRows{
		NextFunc: func() bool {
			return currentIdx < len(componentNames)
		},
		ScanFunc: func(dest ...any) error {
			if ptr, ok := dest[0].(*string); ok {
				*ptr = aggregateStepName // step = "builds"
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = componentNames[currentIdx] // component name
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = string(StepStatusCompleted) // ALL are completed
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = "build succeeded"
			}
			if ptr, ok := dest[4].(*time.Time); ok {
				*ptr = stateTimestamp
			}
			currentIdx++
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return mockRows, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, config, "ci")

	// Initial slip has DIFFERENT component names (from pipeline definition)
	// These are placeholders that don't match the actual workflow component names
	slip := &Slip{
		CorrelationID: "test-corr-001",
		Aggregates: map[string][]ComponentStepData{
			aggregateStepName: {
				{Component: "ExampleApi", Status: StepStatusPending},
				{Component: "ExampleInboundIntegrationWorker", Status: StepStatusPending},
				{Component: "ExampleMigrationTool", Status: StepStatusPending},
				{Component: "ExampleOutboundIntegrationWorker", Status: StepStatusPending},
				{Component: "ExampleSagasWorker", Status: StepStatusPending},
				{Component: "testing.MCExampleAutomatedTests/Tests", Status: StepStatusPending},
			},
		},
		Steps: map[string]Step{
			aggregateStepName: {Status: StepStatusPending},
		},
	}

	err := store.hydrateSlip(context.Background(), slip)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should have 12 components: 6 original (pending) + 6 new (completed)
	components := slip.Aggregates[aggregateStepName]
	t.Logf("After hydration, %d components in aggregate:", len(components))
	for _, c := range components {
		t.Logf("  - %s: %s", c.Component, c.Status)
	}

	// Count completed components from event sourcing
	completedCount := 0
	for _, c := range components {
		if c.Status == StepStatusCompleted {
			completedCount++
		}
	}
	t.Logf("Completed components: %d", completedCount)

	// THE KEY ASSERTION: Since all 6 actual component states are completed,
	// the aggregate status SHOULD be "completed", not "running".
	// The original pending placeholders should not prevent completion.
	aggregateStep := slip.Steps[aggregateStepName]
	if aggregateStep.Status != StepStatusCompleted {
		t.Errorf("expected aggregate step COMPLETED (all component states are completed), got %s", aggregateStep.Status)
		t.Log("This is the production bug: aggregate remains 'running' because original")
		t.Log("placeholder components (ExampleApi) don't match workflow names (mc.example.api)")
	}
}

// TestClickHouseStore_LoadByCommit_Success tests successful slip loading by commit.
func TestClickHouseStore_LoadByCommit_Success(t *testing.T) {
	mockRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
		QueryWithArgsRows: &clickhousetest.MockRows{
			CloseErr: nil, // Handle Close() call in hydration
		},
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
	// Create data structures for valid JSON columns
	stateHistoryData := map[string]interface{}{
		"entries": []StateHistoryEntry{},
	}
	unitTestsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
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
			// Valid JSON for step_details (empty object)
			if jsonPtr, ok := dest[7].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{})
			}
			// Valid JSON for state_history
			if jsonPtr, ok := dest[8].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(stateHistoryData)
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Invalid JSON for builds aggregate - set object without "items" key
			if jsonPtr, ok := dest[13].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{"invalid_key": "invalid_value"})
			}
			// Valid JSON for unit_tests aggregate
			if jsonPtr, ok := dest[14].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			// Mock successful hydration query with no results
			return &clickhousetest.MockRows{}, nil
		},
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
// Invalid state_history JSON is gracefully handled - the slip is returned with empty StateHistory.
func TestClickHouseStore_Load_InvalidStateHistoryJSON(t *testing.T) {
	now := time.Now()
	// Create data structures for aggregates (wrapped in object)
	buildsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
	unitTestsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
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
			// Valid step_details JSON (empty object)
			if jsonPtr, ok := dest[7].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{})
			}
			// Invalid state history - set an empty object without "entries" key
			// This simulates malformed JSON that won't parse as expected
			if jsonPtr, ok := dest[8].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{"invalid_key": "invalid_value"})
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Valid aggregate JSONs
			if jsonPtr, ok := dest[13].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			}
			if jsonPtr, ok := dest[14].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			// Mock successful hydration query with no results
			return &clickhousetest.MockRows{}, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, err := store.Load(context.Background(), "test-corr-001")
	// Invalid state_history JSON is gracefully handled - no error returned
	if err != nil {
		t.Errorf("expected no error (graceful handling), got %v", err)
	}
	// The slip should be returned but with empty StateHistory
	if slip == nil {
		t.Fatal("expected slip to be returned")
	}
	if len(slip.StateHistory) != 0 {
		t.Errorf("expected empty state history due to malformed JSON, got %d entries", len(slip.StateHistory))
	}
}

// TestClickHouseStore_UpdateStep_Success tests successful step update.
func TestClickHouseStore_UpdateStep_Success(t *testing.T) {
	// Use the helper that handles both Load and getMaxVersion queries
	mockSession := createMockSessionForUpdates("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending, 1)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateStep(context.Background(), "test-corr-001", "push_parsed", "", StepStatusCompleted)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	// Should have called QueryRow twice (Load + getMaxVersion) and ExecWithArgs once (atomic cancel + new via UNION ALL)
	if len(mockSession.QueryRowCalls) != 2 {
		t.Errorf("expected 2 QueryRow calls (Load + getMaxVersion), got %d", len(mockSession.QueryRowCalls))
	}
	if len(mockSession.ExecWithArgsCalls) != 1 {
		t.Errorf(
			"expected 1 ExecWithArgs call (atomic cancel + new via UNION ALL), got %d",
			len(mockSession.ExecWithArgsCalls),
		)
	}
}

// TestClickHouseStore_UpdateStep_WithComponent tests step update for a specific component.
func TestClickHouseStore_UpdateStep_WithComponent(t *testing.T) {
	execCalled := false
	insertToComponentStatesCalled := false
	slipRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)

	// Track the version being inserted so we can return it from max(version)
	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
		// Handle both Load and getMaxVersion queries
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			if strings.Contains(query, "max(version)") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*sql.NullInt64); ok {
								v.Int64 = int64(lastInsertedVersion)
								v.Valid = true
							}
						}
						return nil
					},
				}
			}
			return slipRow
		},
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &clickhousetest.MockRows{}, nil
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
			execCalled = true
			if strings.Contains(stmt, "slip_component_states") {
				insertToComponentStatesCalled = true
			}
			// Track newVersion from routing_slips INSERT
			if strings.Contains(stmt, "routing_slips") && strings.Contains(stmt, "INSERT") {
				if len(args) >= 2 {
					if v, ok := args[1].(uint64); ok {
						lastInsertedVersion = v
					}
				}
			}
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateStep(context.Background(), "test-corr-001", "build", "api", StepStatusCompleted)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !execCalled {
		t.Error("expected ExecWithArgs to be called")
	}
	if !insertToComponentStatesCalled {
		t.Error("expected insert into slip_component_states")
	}
}

func TestClickHouseStore_loadComponentStates_Err(t *testing.T) {
	expectedErr := errors.New("rows error")
	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &clickhousetest.MockRows{
				ErrErr: expectedErr,
			}, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	_, err := store.loadComponentStates(context.Background(), "test-corr-001")
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

// TestClickHouseStore_UpdateComponentStatus_Success tests successful component status update.
func TestClickHouseStore_UpdateComponentStatus_Success(t *testing.T) {
	execCalled := false
	insertToComponentStatesCalled := false
	slipRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)

	// Track the version being inserted so we can return it from max(version)
	// This prevents infinite retry loops in the post-insert verification
	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
		// Handle both Load and getMaxVersion queries
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			if strings.Contains(query, "max(version)") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*sql.NullInt64); ok {
								v.Int64 = int64(lastInsertedVersion)
								v.Valid = true
							}
						}
						return nil
					},
				}
			}
			return slipRow
		},
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &clickhousetest.MockRows{}, nil
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
			execCalled = true
			if strings.Contains(stmt, "slip_component_states") {
				insertToComponentStatesCalled = true
			}
			// Track newVersion from routing_slips INSERT
			if strings.Contains(stmt, "routing_slips") && strings.Contains(stmt, "INSERT") {
				if len(args) >= 2 {
					if v, ok := args[1].(uint64); ok {
						lastInsertedVersion = v
					}
				}
			}
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateComponentStatus(context.Background(), "test-corr-001", "api", "build", StepStatusCompleted)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !execCalled {
		t.Error("expected ExecWithArgs to be called")
	}
	if !insertToComponentStatesCalled {
		t.Error("expected insert into slip_component_states")
	}
}

// TestClickHouseStore_AppendHistory_Success tests successful history append.
func TestClickHouseStore_AppendHistory_Success(t *testing.T) {
	// Use the helper that handles both Load and getMaxVersion queries
	mockSession := createMockSessionForUpdates("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending, 1)
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
// Column layout for test config (4 steps, 2 aggregates) + sign + version + matched_commit:
// 0: correlation_id, 1: repository, 2: branch, 3: commit_sha
// 4: created_at, 5: updated_at, 6: status
// 7: step_details (JSON), 8: state_history (JSON), 9: ancestry (JSON)
// 10: sign, 11: version
// 12-15: step statuses (push_parsed, builds_completed, unit_tests_completed, dev_deploy)
// 16: builds (aggregate JSON), 17: unit_tests (aggregate JSON)
// 18: matched_commit
func createMockScanRowWithMatch(
	correlationID, repository, branch, commitSHA, matchedCommit string,
	status SlipStatus,
) *clickhousetest.MockRow {
	now := time.Now()

	// Create data structures wrapped in objects for ClickHouse JSON compatibility
	stepDetailsData := map[string]map[string]interface{}{}
	stateHistoryData := map[string]interface{}{
		"entries": []StateHistoryEntry{},
	}
	ancestryData := map[string]interface{}{
		"chain": []AncestryEntry{},
	}
	buildsData := map[string]interface{}{
		"items": []ComponentStepData{
			{Component: "api", Status: StepStatusCompleted},
		},
	}
	unitTestsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}

	return &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Test config has 4 steps, 2 aggregates + sign + version + matched_commit = 19 columns
			if len(dest) < 19 {
				return fmt.Errorf("not enough scan destinations for scanSlipWithMatch: got %d, want 19", len(dest))
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
			if jsonPtr, ok := dest[7].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(stepDetailsData)
			} else if ptr, ok := dest[7].(*string); ok {
				data, _ := json.Marshal(stepDetailsData)
				*ptr = string(data)
			}
			// Set state_history JSON
			if jsonPtr, ok := dest[8].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(stateHistoryData)
			} else if ptr, ok := dest[8].(*string); ok {
				data, _ := json.Marshal(stateHistoryData)
				*ptr = string(data)
			}
			// Set ancestry JSON
			if jsonPtr, ok := dest[9].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(ancestryData)
			} else if ptr, ok := dest[9].(*string); ok {
				data, _ := json.Marshal(ancestryData)
				*ptr = string(data)
			}
			// Set sign (index 10)
			if ptr, ok := dest[10].(*int8); ok {
				*ptr = 1
			}
			// Set version (index 11)
			if ptr, ok := dest[11].(*uint32); ok {
				*ptr = 1
			}
			// Set step statuses (4 steps, now at indices 12-15)
			for i := 12; i < 16; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = string(StepStatusPending)
				}
			}
			// Set builds aggregate JSON (now at index 16)
			if jsonPtr, ok := dest[16].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			} else if ptr, ok := dest[16].(*string); ok {
				data, _ := json.Marshal(buildsData)
				*ptr = string(data)
			}
			// Set unit_tests aggregate JSON (now at index 17)
			if jsonPtr, ok := dest[17].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			} else if ptr, ok := dest[17].(*string); ok {
				data, _ := json.Marshal(unitTestsData)
				*ptr = string(data)
			}
			// Set matched_commit (now at index 18)
			if ptr, ok := dest[18].(*string); ok {
				*ptr = matchedCommit
			}
			return nil
		},
	}
}

// TestClickHouseStore_FindByCommits_Success tests successful FindByCommits.
func TestClickHouseStore_FindByCommits_Success(t *testing.T) {
	mockRow := createMockScanRowWithMatch(
		"test-corr-001",
		"myorg/myrepo",
		"main",
		"abc123",
		"abc123",
		SlipStatusPending,
	)
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			// Mock successful hydration query with no results
			return &clickhousetest.MockRows{}, nil
		},
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
	// Create data structures for valid JSON columns
	stateHistoryData := map[string]interface{}{
		"entries": []StateHistoryEntry{},
	}
	unitTestsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
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
			// Valid step_details JSON (empty object)
			if jsonPtr, ok := dest[7].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{})
			}
			// Valid state_history JSON
			if jsonPtr, ok := dest[8].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(stateHistoryData)
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Invalid JSON for builds aggregate - set object without "items" key
			if jsonPtr, ok := dest[13].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{"invalid_key": "invalid_value"})
			}
			// Valid JSON for unit_tests aggregate
			if jsonPtr, ok := dest[14].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
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
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			// Mock successful hydration query with no results
			return &clickhousetest.MockRows{}, nil
		},
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
// Invalid state_history JSON is gracefully handled - the slip is returned with empty StateHistory.
func TestClickHouseStore_FindByCommits_InvalidStateHistoryJSON(t *testing.T) {
	now := time.Now()
	// Create data structures wrapped in objects for ClickHouse JSON compatibility
	ancestryData := map[string]interface{}{
		"chain": []AncestryEntry{},
	}
	buildsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
	unitTestsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
	mockRow := &clickhousetest.MockRow{
		ScanFunc: func(dest ...any) error {
			// Column layout for test config (4 steps, 2 aggregates) + sign + version + matched_commit:
			// 0-6: core fields
			// 7-9: JSON fields (step_details, state_history, ancestry)
			// 10: sign, 11: version
			// 12-15: step statuses
			// 16-17: aggregate JSON
			// 18: matched_commit

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
			// Valid step_details JSON (empty object)
			if jsonPtr, ok := dest[7].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{})
			}
			// Invalid state history - set an object without "entries" key
			if jsonPtr, ok := dest[8].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{"invalid_key": "invalid_value"})
			}
			// Valid ancestry JSON
			if jsonPtr, ok := dest[9].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(ancestryData)
			}
			// Set sign (index 10)
			if ptr, ok := dest[10].(*int8); ok {
				*ptr = 1
			}
			// Set version (index 11)
			if ptr, ok := dest[11].(*uint32); ok {
				*ptr = 1
			}
			// Set step statuses (4 steps, now at indices 12-15)
			for i := 12; i < 16; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Valid aggregate JSONs (now at indices 16-17)
			if jsonPtr, ok := dest[16].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			}
			if jsonPtr, ok := dest[17].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			}
			// Matched commit (now at index 18)
			if ptr, ok := dest[18].(*string); ok {
				*ptr = "abc123"
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			// Mock successful hydration query with no results
			return &clickhousetest.MockRows{}, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	slip, matchedCommit, err := store.FindByCommits(context.Background(), "myorg/myrepo", []string{"abc123"})
	// Invalid state_history JSON is gracefully handled - no error returned
	if err != nil {
		t.Errorf("expected no error (graceful handling), got %v", err)
	}
	// The slip should be returned but with empty StateHistory
	if slip == nil {
		t.Fatal("expected slip to be returned")
	}
	if len(slip.StateHistory) != 0 {
		t.Errorf("expected empty state history due to malformed JSON, got %d entries", len(slip.StateHistory))
	}
	if matchedCommit != "abc123" {
		t.Errorf("expected matched commit 'abc123', got '%s'", matchedCommit)
	}
}

// TestClickHouseStore_Load_WithStepTimestamps tests parsing step timestamps from JSON.
// NOTE: This test verifies the basic structure but cannot fully test step_details parsing
// because chcol.JSON.Scan() expects ClickHouse's binary protocol format, not Go maps.
// The step_details timestamp parsing is verified through integration tests against real ClickHouse.
func TestClickHouseStore_Load_WithStepTimestamps(t *testing.T) {
	now := time.Now()
	config := testPipelineConfig()

	// Create data structures wrapped in objects for ClickHouse JSON compatibility
	buildsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
	unitTestsData := map[string]interface{}{
		"items": []ComponentStepData{},
	}
	stateHistoryData := map[string]interface{}{
		"entries": []StateHistoryEntry{},
	}

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
			// Set step_details JSON - cannot mock chcol.JSON internals for timestamp parsing
			// The step_details parsing is verified through integration tests
			if jsonPtr, ok := dest[7].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(map[string]interface{}{})
			}
			// Set state_history JSON
			if jsonPtr, ok := dest[8].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(stateHistoryData)
			}
			// Set step statuses (4 steps)
			for i := 9; i < 13; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Set aggregate JSONs
			if jsonPtr, ok := dest[13].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			}
			if jsonPtr, ok := dest[14].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryRowRow: mockRow,
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			// Mock successful hydration query with no results
			return &clickhousetest.MockRows{}, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, config, "ci")

	slip, err := store.Load(context.Background(), "test-corr-001")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify basic slip structure is populated
	if slip.CorrelationID != "test-corr-001" {
		t.Errorf("expected correlation_id 'test-corr-001', got '%s'", slip.CorrelationID)
	}
	if slip.Repository != "myorg/myrepo" {
		t.Errorf("expected repository 'myorg/myrepo', got '%s'", slip.Repository)
	}

	// Verify steps exist (timestamps cannot be verified in unit tests due to chcol.JSON limitations)
	// Check all steps from config exist
	for _, step := range config.Steps {
		if _, ok := slip.Steps[step.Name]; !ok {
			t.Errorf("expected %s step to exist", step.Name)
		}
	}
}

// TestClickHouseStore_Update_UpdatesTimestamp tests that Update sets UpdatedAt.
func TestClickHouseStore_Update_UpdatesTimestamp(t *testing.T) {
	// Track the version being inserted so we can return it from max(version)
	// This prevents infinite retry loops in the post-insert verification
	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if len(dest) > 0 {
						if v, ok := dest[0].(*sql.NullInt64); ok {
							v.Int64 = int64(lastInsertedVersion)
							v.Valid = true
						}
					}
					return nil
				},
			}
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
			// Track newVersion from routing_slips INSERT
			if strings.Contains(stmt, "routing_slips") && strings.Contains(stmt, "INSERT") {
				if len(args) >= 2 {
					if v, ok := args[1].(uint64); ok {
						lastInsertedVersion = v
					}
				}
			}
			return nil
		},
	}
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
		Version:       1, // Initial version (will be replaced with timestamp)
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

// TestClickHouseStore_OptimizeTable tests the OptimizeTable method.
func TestClickHouseStore_OptimizeTable(t *testing.T) {
	t.Run("successful optimize", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.OptimizeTable(context.Background())
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(mockSession.ExecCalls) != 1 {
			t.Errorf("expected 1 Exec call, got %d", len(mockSession.ExecCalls))
		}
		expectedQuery := "OPTIMIZE TABLE ci.routing_slips FINAL"
		if mockSession.ExecCalls[0].Stmt != expectedQuery {
			t.Errorf("expected query %q, got %q", expectedQuery, mockSession.ExecCalls[0].Stmt)
		}
	})

	t.Run("optimize with error", func(t *testing.T) {
		expectedErr := errors.New("optimize error")
		mockSession := &clickhousetest.MockSession{
			ExecErr: expectedErr,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.OptimizeTable(context.Background())
		if err == nil {
			t.Error("expected error, got nil")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error to wrap %v, got %v", expectedErr, err)
		}
	})
}

// TestClickHouseStore_SetOptimizeAfterWrite tests the SetOptimizeAfterWrite method.
func TestClickHouseStore_SetOptimizeAfterWrite(t *testing.T) {
	t.Run("disable optimize after write", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		// Disable optimize after write
		store.SetOptimizeAfterWrite(false)

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
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		// Should have 1 ExecWithArgs call (INSERT) but no Exec calls (OPTIMIZE)
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf("expected 1 ExecWithArgs call, got %d", len(mockSession.ExecWithArgsCalls))
		}
		if len(mockSession.ExecCalls) != 0 {
			t.Errorf("expected 0 Exec calls (optimize disabled), got %d", len(mockSession.ExecCalls))
		}
	})

	t.Run("enable optimize after write", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		// Ensure it's enabled (default)
		store.SetOptimizeAfterWrite(true)

		slip := &Slip{
			CorrelationID: "test-corr-002",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "def456",
			Status:        SlipStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		err := store.Create(context.Background(), slip)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		// Should have 1 ExecWithArgs call (INSERT) and 1 Exec call (OPTIMIZE)
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf("expected 1 ExecWithArgs call, got %d", len(mockSession.ExecWithArgsCalls))
		}
		if len(mockSession.ExecCalls) != 1 {
			t.Errorf("expected 1 Exec call (optimize enabled), got %d", len(mockSession.ExecCalls))
		}
	})

	t.Run("optimize failure after insert", func(t *testing.T) {
		expectedErr := errors.New("optimize error")
		mockSession := &clickhousetest.MockSession{
			ExecErr: expectedErr, // OPTIMIZE will fail
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		slip := &Slip{
			CorrelationID: "test-corr-003",
			Repository:    "myorg/myrepo",
			Branch:        "main",
			CommitSHA:     "ghi789",
			Status:        SlipStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		err := store.Create(context.Background(), slip)
		if err == nil {
			t.Error("expected error, got nil")
		}
		// INSERT should succeed but OPTIMIZE should fail
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf("expected 1 ExecWithArgs call, got %d", len(mockSession.ExecWithArgsCalls))
		}
		if len(mockSession.ExecCalls) != 1 {
			t.Errorf("expected 1 Exec call (optimize attempted), got %d", len(mockSession.ExecCalls))
		}
	})
}
