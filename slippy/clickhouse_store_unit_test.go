package slippy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

	t.Run("version conflict no longer detected with epoch versioning", func(t *testing.T) {
		// With epoch-based versioning, each update has a unique timestamp-based version.
		// Updates no longer fail with ErrVersionConflict - "last write wins" semantics apply.
		// This test verifies that Update succeeds even when another "higher version" exists,
		// because we no longer do post-insert verification.
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(ctx context.Context, query string, args ...any) ch.Row {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*sql.NullInt64); ok {
								// Even if we return a higher version, Update should succeed
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

		// With epoch versioning and no verification, Update should succeed
		err := store.Update(context.Background(), slip)
		if err != nil {
			t.Errorf("expected no error with epoch versioning, got %v", err)
		}
		// Verify the insert was made
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf(
				"expected 1 ExecWithArgs call (atomic cancel + new via UNION ALL), got %d",
				len(mockSession.ExecWithArgsCalls),
			)
		}
	})
}

// TestClickHouseStore_UpdateStep tests the UpdateStep method.
func TestClickHouseStore_UpdateStep(t *testing.T) {
	t.Run("pure pipeline step succeeds without loading slip", func(t *testing.T) {
		// Under the event-sourcing design, pure pipeline steps (non-aggregate, no component)
		// only write to slip_component_states. No slip load is required; hydrateSlip derives
		// the status on every Load. UpdateStep must succeed even when the routing_slips row is
		// unavailable (e.g. before the slip exists or when the load would have failed).
		mockSession := &clickhousetest.MockSession{}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.UpdateStep(context.Background(), "test-corr-001", "push_parsed", "", StepStatusCompleted)
		if err != nil {
			t.Errorf("expected no error for pure pipeline step, got %v", err)
		}
		// One ExecWithArgs call: the slip_component_states insert.
		// Zero QueryRow calls: no slip load needed for non-aggregate pipeline steps.
		if len(mockSession.ExecWithArgsCalls) != 1 {
			t.Errorf(
				"expected 1 ExecWithArgs call (component_states insert), got %d",
				len(mockSession.ExecWithArgsCalls),
			)
		}
		if len(mockSession.QueryRowCalls) != 0 {
			t.Errorf(
				"expected 0 QueryRow calls (no load for pure pipeline step), got %d",
				len(mockSession.QueryRowCalls),
			)
		}
		// The single insert must target slip_component_states.
		if !strings.Contains(mockSession.ExecWithArgsCalls[0].Stmt, TableSlipComponentStates) {
			t.Errorf(
				"expected insert into %s, got query: %s",
				TableSlipComponentStates,
				mockSession.ExecWithArgsCalls[0].Stmt,
			)
		}
	})

	t.Run("insert error propagates for pure pipeline step", func(t *testing.T) {
		expectedErr := errors.New("insert failed")
		mockSession := &clickhousetest.MockSession{
			ExecWithArgsErr: expectedErr,
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		err := store.UpdateStep(context.Background(), "test-corr-001", "push_parsed", "", StepStatusCompleted)
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected insert error %v, got %v", expectedErr, err)
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
// 7: step_details (JSON), 8: state_history (JSON)
// 9: sign, 10: version
// 11-14: step statuses (push_parsed, builds_completed, unit_tests_completed, dev_deploy)
// 15: builds (aggregate JSON), 16: unit_tests (aggregate JSON)
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
			// Test config has 4 steps, 2 aggregates + sign + version = 17 columns
			// Column layout:
			// 0-6: core fields (correlation_id, repository, branch, commit_sha, created_at, updated_at, status)
			// 7-8: JSON fields (step_details, state_history)
			// 9: sign, 10: version
			// 11-14: step statuses
			// 15-16: aggregate JSON (builds, unit_tests)
			if len(dest) < 17 {
				return fmt.Errorf("not enough scan destinations: got %d, want 17", len(dest))
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
			// Set sign (index 9)
			if ptr, ok := dest[9].(*int8); ok {
				*ptr = 1
			}
			// Set version (index 10)
			if ptr, ok := dest[10].(*uint32); ok {
				*ptr = 1
			}
			// Set step statuses (4 steps, indices 11-14)
			for i := 11; i < 15; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = string(StepStatusPending)
				}
			}
			// Set builds aggregate JSON (index 15)
			if jsonPtr, ok := dest[15].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			} else if ptr, ok := dest[15].(*string); ok {
				data, _ := json.Marshal(buildsData)
				*ptr = string(data)
			}
			// Set unit_tests aggregate JSON (index 16)
			if jsonPtr, ok := dest[16].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			} else if ptr, ok := dest[16].(*string); ok {
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
// The mock tracks the version being inserted and returns it from subsequent max(version) and
// loadVersionFromDB queries, ensuring the post-write conflict checks succeed without retrying.
func createMockSessionForUpdates(
	correlationID, repository, branch, commitSHA string,
	status SlipStatus,
	version uint64,
) *clickhousetest.MockSession {
	slipRow := createMockScanRow(correlationID, repository, branch, commitSHA, status)

	// Track the last inserted version to return from max(version) and loadVersionFromDB queries.
	lastInsertedVersion := version

	return &clickhousetest.MockSession{
		QueryWithArgsRows: &clickhousetest.MockRows{CloseErr: nil}, // Handle hydration query
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			// getMaxVersion query
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
			// loadStateHistoryFromDB: SELECT state_history FROM ... (single-column; full Load starts with SELECT correlation_id)
			if strings.Contains(query, "SELECT state_history") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) == 0 {
							return fmt.Errorf("expected scan destination, got none")
						}
						jsonPtr, ok := dest[0].(*chcol.JSON)
						if !ok {
							return fmt.Errorf("expected dest[0] to be *chcol.JSON, got %T", dest[0])
						}
						return jsonPtr.Scan(map[string]any{"entries": []any{}})
					},
				}
			}
			// loadVersionFromDB: SELECT version FROM ... ORDER BY version DESC LIMIT 1
			if strings.Contains(query, "SELECT version FROM") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*uint64); ok {
								// Return the same version just written — no conflict.
								*v = lastInsertedVersion
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
			// Track the newVersion from INSERT statements for routing_slips.
			// In insertAtomicUpdateWithVersions, args[1] is the new timestamp version.
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
			if ptr, ok := dest[4].(*string); ok {
				*ptr = "" // image_tag
			}
			if ptr, ok := dest[5].(*time.Time); ok {
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
			if ptr, ok := dest[4].(*string); ok {
				*ptr = "" // image_tag
			}
			if ptr, ok := dest[5].(*time.Time); ok {
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
			if ptr, ok := dest[4].(*string); ok {
				*ptr = "" // image_tag
			}
			if ptr, ok := dest[5].(*time.Time); ok {
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
	// Under the event-sourcing design, a pure pipeline step (non-aggregate, no component)
	// only writes to slip_component_states. No Load + routing_slips Update is needed.
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateStep(context.Background(), "test-corr-001", "push_parsed", "", StepStatusCompleted)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	// Pure pipeline step: 0 QueryRow calls (no Load), 1 ExecWithArgs (slip_component_states insert).
	if len(mockSession.QueryRowCalls) != 0 {
		t.Errorf("expected 0 QueryRow calls (no load for pure pipeline step), got %d",
			len(mockSession.QueryRowCalls))
	}
	if len(mockSession.ExecWithArgsCalls) != 1 {
		t.Errorf(
			"expected 1 ExecWithArgs call (slip_component_states insert), got %d",
			len(mockSession.ExecWithArgsCalls),
		)
	}
}

// TestClickHouseStore_HydrateSlip_StepNameAliasMerge is a regression test verifying that
// component states stored under different step name aliases (e.g. "build" and "builds")
// that resolve to the same aggregate are merged correctly during hydrateSlip. Without
// normalization, applyComponentStatesToAggregate would be called twice for the same
// aggregate, making the final status dependent on Go map iteration order.
func TestClickHouseStore_HydrateSlip_StepNameAliasMerge(t *testing.T) {
	config := testPipelineConfig()

	olderTimestamp := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newerTimestamp := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)

	// Row 1: "build" / "api" with status running (older)
	// Row 2: "builds" / "api" with status completed (newer)
	// Both "build" and "builds" resolve to the same aggregate step via resolveAggregateStepName.
	callCount := 0
	mockRows := &clickhousetest.MockRows{
		NextData: []bool{true, true, false},
		ScanFunc: func(dest ...any) error {
			callCount++
			var step, component, status string
			var ts time.Time
			switch callCount {
			case 1:
				step = "build"
				component = "api"
				status = string(StepStatusRunning)
				ts = olderTimestamp
			case 2:
				step = "builds"
				component = "api"
				status = string(StepStatusCompleted)
				ts = newerTimestamp
			}
			if ptr, ok := dest[0].(*string); ok {
				*ptr = step
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = component
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = status
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = ""
			}
			if ptr, ok := dest[4].(*string); ok {
				*ptr = ""
			}
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = ts
			}
			return nil
		},
	}
	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(_ context.Context, _ string, _ ...any) (driver.Rows, error) {
			return mockRows, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, config, "ci")
	slip := &Slip{
		CorrelationID: "test-alias-merge",
		Steps: map[string]Step{
			"builds": {Status: StepStatusPending},
		},
		Aggregates: map[string][]ComponentStepData{},
	}

	err := store.hydrateSlip(context.Background(), slip)
	if err != nil {
		t.Fatalf("hydrateSlip returned unexpected error: %v", err)
	}

	// The newer "completed" state (from "builds") must win over the older "running" (from "build").
	comps := slip.Aggregates["builds"]
	if len(comps) != 1 {
		t.Fatalf("expected 1 component in Aggregates[builds], got %d: %+v", len(comps), comps)
	}
	if comps[0].Component != "api" {
		t.Errorf("expected component name 'api', got %q", comps[0].Component)
	}
	if comps[0].Status != StepStatusCompleted {
		t.Errorf("expected merged status completed (newer timestamp wins), got %s", comps[0].Status)
	}
}

// TestClickHouseStore_HydrateSlip_PipelineLevelEvent_EmptyComponent is a regression test
// for the concurrent lost-update fix. It verifies that pipeline-level step events stored
// with component="" in slip_component_states are applied to the slip's Steps map during
// hydrateSlip, and that these sentinel rows are NOT treated as real component entries
// in the aggregate computation loop.
func TestClickHouseStore_HydrateSlip_PipelineLevelEvent_EmptyComponent(t *testing.T) {
	eventTimestamp := time.Now()
	config := testPipelineConfig()

	// Simulate one pipeline-level event (component="") for "push_parsed"
	// and one real component event for "builds" / "api".
	callCount := 0
	mockRows := &clickhousetest.MockRows{
		NextData: []bool{true, true, false},
		ScanFunc: func(dest ...any) error {
			callCount++
			step := "push_parsed"
			component := ""
			status := string(StepStatusCompleted)
			if callCount == 2 {
				step = "builds"
				component = "api"
				status = string(StepStatusRunning)
			}
			if ptr, ok := dest[0].(*string); ok {
				*ptr = step
			}
			if ptr, ok := dest[1].(*string); ok {
				*ptr = component
			}
			if ptr, ok := dest[2].(*string); ok {
				*ptr = status
			}
			if ptr, ok := dest[3].(*string); ok {
				*ptr = ""
			}
			if ptr, ok := dest[4].(*string); ok {
				*ptr = "" // image_tag
			}
			if ptr, ok := dest[5].(*time.Time); ok {
				*ptr = eventTimestamp
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
		Steps: map[string]Step{
			"push_parsed": {Status: StepStatusPending},
			"builds":      {Status: StepStatusPending},
		},
		Aggregates: map[string][]ComponentStepData{
			"builds": {{Component: "api", Status: StepStatusPending}},
		},
	}

	err := store.hydrateSlip(context.Background(), slip)
	if err != nil {
		t.Fatalf("hydrateSlip returned unexpected error: %v", err)
	}

	// Pipeline-level event (component="") must update the step status directly.
	if slip.Steps["push_parsed"].Status != StepStatusCompleted {
		t.Errorf("expected push_parsed step status completed, got %s", slip.Steps["push_parsed"].Status)
	}

	// The component="" row must NOT appear as a component entry in the aggregate loop.
	// Only the real "api" component should be in Aggregates["builds"].
	comps := slip.Aggregates["builds"]
	for _, c := range comps {
		if c.Component == "" {
			t.Error("component= sentinel row must not appear in Aggregates after hydrateSlip")
		}
	}

	// The real component event (builds / api) must still be applied.
	found := false
	for _, c := range comps {
		if c.Component == "api" && c.Status == StepStatusRunning {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Aggregates[builds] to contain api=running; got %+v", comps)
	}
}

// TestClickHouseStore_UpdateStep_ConcurrentStepsNeitherLost is a regression test for the
// concurrent lost-update bug. It runs two UpdateStep calls in separate goroutines for
// different pipeline steps on the same slip and verifies that each produces exactly one
// slip_component_states insert with no shared routing_slips load. Under the old RMW design,
// both writers would race on routing_slips and one would silently overwrite the other's column.
func TestClickHouseStore_UpdateStep_ConcurrentStepsNeitherLost(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	var (
		errsMu sync.Mutex
		errs   []error
	)

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := store.UpdateStep(
			context.Background(),
			"test-corr-001",
			"push_parsed",
			"",
			StepStatusCompleted,
		); err != nil {
			errsMu.Lock()
			errs = append(errs, fmt.Errorf("push_parsed: %w", err))
			errsMu.Unlock()
		}
	})
	wg.Go(func() {
		if err := store.UpdateStep(
			context.Background(),
			"test-corr-001",
			"dev_deploy",
			"",
			StepStatusFailed,
		); err != nil {
			errsMu.Lock()
			errs = append(errs, fmt.Errorf("dev_deploy: %w", err))
			errsMu.Unlock()
		}
	})
	wg.Wait()

	for _, err := range errs {
		t.Error(err)
	}

	// Each concurrent call must have produced exactly one slip_component_states insert.
	if len(mockSession.ExecWithArgsCalls) != 2 {
		t.Errorf("expected 2 ExecWithArgs calls (one per step), got %d", len(mockSession.ExecWithArgsCalls))
	}
	// No routing_slips read (QueryRow) should have occurred — UpdateStep is a blind insert.
	if len(mockSession.QueryRowCalls) != 0 {
		t.Errorf("expected 0 QueryRow calls (no shared routing_slips load), got %d", len(mockSession.QueryRowCalls))
	}

	// Verify the two inserts target slip_component_states and carry the correct step names.
	steps := make(map[string]bool)
	for _, call := range mockSession.ExecWithArgsCalls {
		if !strings.Contains(call.Stmt, TableSlipComponentStates) {
			t.Errorf("expected insert into %s, got: %s", TableSlipComponentStates, call.Stmt)
		}
		for _, arg := range call.Args {
			if s, ok := arg.(string); ok && (s == "push_parsed" || s == "dev_deploy") {
				steps[s] = true
			}
		}
	}
	if !steps["push_parsed"] {
		t.Error("expected push_parsed step in slip_component_states inserts")
	}
	if !steps["dev_deploy"] {
		t.Error("expected dev_deploy step in slip_component_states inserts")
	}
}

// TestClickHouseStore_UpdateStep_WithComponent tests step update for a specific component.
func TestClickHouseStore_UpdateStep_WithComponent(t *testing.T) {
	execCalled := false
	insertToComponentStatesCalled := false
	slipRow := createMockScanRow("test-corr-001", "myorg/myrepo", "main", "abc123", SlipStatusPending)

	// Track the version being inserted so we can return it from max(version) and loadVersionFromDB.
	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
		// Handle Load, getMaxVersion, and loadVersionFromDB queries.
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
			// loadVersionFromDB: SELECT version ... ORDER BY version DESC LIMIT 1
			if strings.Contains(query, "SELECT version FROM") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*uint64); ok {
								*v = lastInsertedVersion
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

	// Track the version being inserted so we can return it from max(version) and loadVersionFromDB.
	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
		// Handle Load, getMaxVersion, and loadVersionFromDB queries.
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
			// loadVersionFromDB: SELECT version ... ORDER BY version DESC LIMIT 1
			if strings.Contains(query, "SELECT version FROM") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*uint64); ok {
								*v = lastInsertedVersion
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
// 7: step_details (JSON), 8: state_history (JSON)
// 9: sign, 10: version
// 11-14: step statuses (push_parsed, builds_completed, unit_tests_completed, dev_deploy)
// 15: builds (aggregate JSON), 16: unit_tests (aggregate JSON)
// 17: matched_commit
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
			// Test config has 4 steps, 2 aggregates + sign + version + matched_commit = 18 columns
			if len(dest) < 18 {
				return fmt.Errorf("not enough scan destinations for scanSlipWithMatch: got %d, want 18", len(dest))
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
			// Set sign (index 9)
			if ptr, ok := dest[9].(*int8); ok {
				*ptr = 1
			}
			// Set version (index 10)
			if ptr, ok := dest[10].(*uint32); ok {
				*ptr = 1
			}
			// Set step statuses (4 steps, indices 11-14)
			for i := 11; i < 15; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = string(StepStatusPending)
				}
			}
			// Set builds aggregate JSON (index 15)
			if jsonPtr, ok := dest[15].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			} else if ptr, ok := dest[15].(*string); ok {
				data, _ := json.Marshal(buildsData)
				*ptr = string(data)
			}
			// Set unit_tests aggregate JSON (index 16)
			if jsonPtr, ok := dest[16].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			} else if ptr, ok := dest[16].(*string); ok {
				data, _ := json.Marshal(unitTestsData)
				*ptr = string(data)
			}
			// Set matched_commit (index 17)
			if ptr, ok := dest[17].(*string); ok {
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
			// 7-8: JSON fields (step_details, state_history)
			// 9: sign, 10: version
			// 11-14: step statuses
			// 15-16: aggregate JSON
			// 17: matched_commit

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
			// Set sign (index 9)
			if ptr, ok := dest[9].(*int8); ok {
				*ptr = 1
			}
			// Set version (index 10)
			if ptr, ok := dest[10].(*uint32); ok {
				*ptr = 1
			}
			// Set step statuses (4 steps, indices 11-14)
			for i := 11; i < 15; i++ {
				if ptr, ok := dest[i].(*string); ok {
					*ptr = "pending"
				}
			}
			// Valid aggregate JSONs (indices 15-16)
			if jsonPtr, ok := dest[15].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(buildsData)
			}
			if jsonPtr, ok := dest[16].(*chcol.JSON); ok {
				_ = jsonPtr.Scan(unitTestsData)
			}
			// Matched commit (index 17)
			if ptr, ok := dest[17].(*string); ok {
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

func TestClickHouseStore_Ping_Success(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.Ping(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mockSession.PingCalls != 1 {
		t.Errorf("expected 1 Ping call, got %d", mockSession.PingCalls)
	}
}

func TestClickHouseStore_Ping_Error(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		PingErr: errors.New("connection refused"),
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.Ping(context.Background())

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "connection refused" {
		t.Errorf("expected 'connection refused', got '%s'", err.Error())
	}
}

// TestClickHouseStore_AppendHistory_DoesNotCallFullLoad is a regression test verifying that
// AppendHistory no longer performs a full Load (QueryWithArgs) to read the entire routing_slips
// row. After the Phase 2 fix it should use exactly one QueryRow (loadStateHistoryFromDB) and
// one ExecWithArgs (insertAtomicHistoryUpdate), keeping all step/aggregate columns in sync with
// the DB row rather than an in-memory snapshot.
func TestClickHouseStore_AppendHistory_DoesNotCallFullLoad(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			if strings.Contains(query, "SELECT state_history") {
				// Strict: destination must be *chcol.JSON and Scan error must propagate.
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) < 1 {
							return fmt.Errorf("expected at least 1 scan destination, got 0")
						}
						jsonPtr, ok := dest[0].(*chcol.JSON)
						if !ok {
							return fmt.Errorf("expected dest[0] to be *chcol.JSON, got %T", dest[0])
						}
						return jsonPtr.Scan(map[string]interface{}{"entries": []interface{}{}})
					},
				}
			}
			// loadVersionFromDB: SELECT version FROM ...
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if len(dest) > 0 {
						if v, ok := dest[0].(*uint64); ok {
							*v = 1
						}
					}
					return nil
				},
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "test-actor",
	}

	err := store.AppendHistory(context.Background(), "corr-append-regression", entry)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Exactly one QueryRow call (loadStateHistoryFromDB) + one for the pre-write
	// version floor (loadVersionFromDB) + one for the post-write version check
	// (loadVersionFromDB). No full QueryWithArgs (Load).
	if len(mockSession.QueryRowCalls) != 3 {
		t.Errorf(
			"expected 3 QueryRow calls (loadStateHistoryFromDB + 2x loadVersionFromDB), got %d",
			len(mockSession.QueryRowCalls),
		)
	}
	if len(mockSession.QueryWithArgsCalls) != 0 {
		t.Errorf("expected 0 QueryWithArgs calls (no full Load), got %d", len(mockSession.QueryWithArgsCalls))
	}
	// Exactly one ExecWithArgs call (insertAtomicHistoryUpdate).
	if len(mockSession.ExecWithArgsCalls) != 1 {
		t.Errorf("expected 1 ExecWithArgs call (insertAtomicHistoryUpdate), got %d", len(mockSession.ExecWithArgsCalls))
	}
}

// TestClickHouseStore_SetComponentImageTag_PreservesCurrentStatus is a regression test verifying
// that SetComponentImageTag reads the current component status from the event log and preserves
// it when inserting the new image-tag event, preventing the "unknown status" data loss that the
// Phase 3 RMW approach could have caused if the component state was missing from an in-memory load.
func TestClickHouseStore_SetComponentImageTag_PreservesCurrentStatus(t *testing.T) {
	const wantStatus = "completed"
	const wantImageTag = "sha256:abc123deadbeef"

	mockSession := &clickhousetest.MockSession{
		// QueryRow returns the current status for argMax(status, timestamp) query.
		QueryRowRow: &clickhousetest.MockRow{
			ScanFunc: func(dest ...any) error {
				if len(dest) < 1 {
					return fmt.Errorf("expected at least 1 scan destination, got 0")
				}
				if ptr, ok := dest[0].(*string); ok {
					*ptr = wantStatus
				}
				return nil
			},
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.SetComponentImageTag(context.Background(), "corr-imagetag-regression", "build", "svc-a", wantImageTag)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Exactly one QueryRow call (argMax status read) and one ExecWithArgs (insertComponentState).
	if len(mockSession.QueryRowCalls) != 1 {
		t.Errorf("expected 1 QueryRow call, got %d", len(mockSession.QueryRowCalls))
	}
	if len(mockSession.ExecWithArgsCalls) != 1 {
		t.Errorf("expected 1 ExecWithArgs call (insertComponentState), got %d", len(mockSession.ExecWithArgsCalls))
	}

	// The insert must carry the preserved status and the new image tag.
	execArgs := mockSession.ExecWithArgsCalls[0].Args
	foundStatus := false
	foundImageTag := false
	for _, arg := range execArgs {
		if s, ok := arg.(string); ok {
			if s == wantStatus {
				foundStatus = true
			}
			if s == wantImageTag {
				foundImageTag = true
			}
		}
	}
	if !foundStatus {
		t.Errorf("expected ExecWithArgs to include preserved status %q in args %v", wantStatus, execArgs)
	}
	if !foundImageTag {
		t.Errorf("expected ExecWithArgs to include image tag %q in args %v", wantImageTag, execArgs)
	}
}

// TestClickHouseStore_NextVersion_MonotonicUnderConcurrency verifies that nextVersion
// returns strictly increasing values even when called concurrently from multiple goroutines.
func TestClickHouseStore_NextVersion_MonotonicUnderConcurrency(t *testing.T) {
	store := NewClickHouseStoreFromSession(&clickhousetest.MockSession{}, testPipelineConfig(), "ci")

	const goroutines = 10
	const versionsPerGoroutine = 100

	results := make([][]uint64, goroutines)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		results[g] = make([]uint64, versionsPerGoroutine)
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for i := 0; i < versionsPerGoroutine; i++ {
				results[idx][i] = store.nextVersion()
			}
		}(g)
	}
	wg.Wait()

	// Collect all versions and verify uniqueness.
	seen := make(map[uint64]bool, goroutines*versionsPerGoroutine)
	for g := 0; g < goroutines; g++ {
		for _, v := range results[g] {
			if seen[v] {
				t.Fatalf("duplicate version %d detected", v)
			}
			seen[v] = true
		}
		// Within each goroutine, versions must be strictly increasing.
		for i := 1; i < versionsPerGoroutine; i++ {
			if results[g][i] <= results[g][i-1] {
				t.Fatalf("goroutine %d: version[%d]=%d <= version[%d]=%d",
					g, i, results[g][i], i-1, results[g][i-1])
			}
		}
	}
}

// TestCalculateAggregateConflictBackoff verifies the exponential backoff sequence for
// aggregate write-back conflict retries.
func TestCalculateAggregateConflictBackoff(t *testing.T) {
	cases := []struct {
		retryNumber int
		wantMs      int64
	}{
		{1, 10},
		{2, 20},
		{3, 40},
		{4, 80},
		{5, 160},
	}
	for _, tc := range cases {
		got := calculateAggregateConflictBackoff(tc.retryNumber)
		if got.Milliseconds() != tc.wantMs {
			t.Errorf("retryNumber=%d: expected %dms, got %dms", tc.retryNumber, tc.wantMs, got.Milliseconds())
		}
	}

	// Below-minimum clamps to 1 → 10ms
	if got := calculateAggregateConflictBackoff(0); got.Milliseconds() != 10 {
		t.Errorf("retryNumber=0: expected 10ms, got %dms", got.Milliseconds())
	}
	if got := calculateAggregateConflictBackoff(-1); got.Milliseconds() != 10 {
		t.Errorf("retryNumber=-1: expected 10ms, got %dms", got.Milliseconds())
	}
	// Above-maximum clamps to 5 → 160ms
	if got := calculateAggregateConflictBackoff(6); got.Milliseconds() != 160 {
		t.Errorf("retryNumber=6: expected 160ms, got %dms", got.Milliseconds())
	}
}

// TestClickHouseStore_LoadVersionFromDB verifies the single-column version query helper.
func TestClickHouseStore_LoadVersionFromDB(t *testing.T) {
	t.Run("returns version on success", func(t *testing.T) {
		const wantVersion uint64 = 1234567890
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if len(dest) < 1 {
						return fmt.Errorf("expected 1 dest, got 0")
					}
					if v, ok := dest[0].(*uint64); ok {
						*v = wantVersion
					}
					return nil
				},
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		got, err := store.loadVersionFromDB(context.Background(), "corr-version-test")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if got != wantVersion {
			t.Errorf("expected version %d, got %d", wantVersion, got)
		}
	})

	t.Run("returns ErrSlipNotFound on sql.ErrNoRows", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: &clickhousetest.MockRow{ScanErr: sql.ErrNoRows},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		_, err := store.loadVersionFromDB(context.Background(), "corr-missing")
		if !errors.Is(err, ErrSlipNotFound) {
			t.Errorf("expected ErrSlipNotFound, got %v", err)
		}
	})

	t.Run("propagates scan errors", func(t *testing.T) {
		scanErr := errors.New("scan failure")
		mockSession := &clickhousetest.MockSession{
			QueryRowRow: &clickhousetest.MockRow{ScanErr: scanErr},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		_, err := store.loadVersionFromDB(context.Background(), "corr-scan-err")
		if !errors.Is(err, scanErr) {
			t.Errorf("expected wrapped scan error, got %v", err)
		}
	})
}

// TestClickHouseStore_UpdateAggregateStatus_ConflictRetry is a regression test verifying that
// when loadVersionFromDB returns a version that does not match slip.Version (simulating a
// concurrent write-back), updateAggregateStatusFromComponentStates re-Loads and re-writes
// until the version matches.
func TestClickHouseStore_UpdateAggregateStatus_ConflictRetry(t *testing.T) {
	slipRow := createMockScanRow("corr-conflict", "myorg/myrepo", "main", "abc123", SlipStatusInProgress)

	// queryRow call sequence:
	// 1st  → Load (returns slipRow, version field = 1 from createMockScanRow)
	// 2nd  → loadVersionFromDB (returns 999 — simulates concurrent write)
	// 3rd  → Load retry (returns slipRow again)
	// 4th  → loadVersionFromDB (returns lastInsertedVersion — our write won)
	var lastInsertedVersion uint64
	queryRowCall := 0
	mockSession := &clickhousetest.MockSession{
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
			if strings.Contains(query, "SELECT version FROM") {
				queryRowCall++
				call := queryRowCall
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*uint64); ok {
								if call == 1 {
									// First check: concurrent writer at version 999
									*v = 999
								} else {
									// Second check: our write won
									*v = lastInsertedVersion
								}
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

	// "build" has an aggregate step "builds" in testPipelineConfig.
	err := store.updateAggregateStatusFromComponentStates(context.Background(), "corr-conflict", "build")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Two Exec calls: one per Load+Update cycle (initial attempt + one retry).
	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	if routingSlipsExecs != 2 {
		t.Errorf("expected 2 routing_slips Update calls (initial + conflict retry), got %d", routingSlipsExecs)
	}

	// Two loadVersionFromDB calls (one per attempt).
	if queryRowCall != 2 {
		t.Errorf("expected 2 loadVersionFromDB calls, got %d", queryRowCall)
	}
}

// TestClickHouseStore_UpdateAggregateStatus_ConflictRetryExhausted verifies that when all
// aggregateConflictMaxRetries are exhausted, the function returns nil (not an error).
func TestClickHouseStore_UpdateAggregateStatus_ConflictRetryExhausted(t *testing.T) {
	slipRow := createMockScanRow("corr-exhausted", "myorg/myrepo", "main", "abc123", SlipStatusInProgress)

	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
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
			// loadVersionFromDB always returns a higher version than what we wrote.
			if strings.Contains(query, "SELECT version FROM") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*uint64); ok {
								// Always one higher than our last write.
								*v = lastInsertedVersion + 1
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

	// Should return nil even when all retries are exhausted.
	err := store.updateAggregateStatusFromComponentStates(context.Background(), "corr-exhausted", "build")
	if err != nil {
		t.Errorf("expected nil (best-effort outcome), got %v", err)
	}

	// Should have attempted (1 initial + aggregateConflictMaxRetries) Updates total.
	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	wantExecs := 1 + aggregateConflictMaxRetries
	if routingSlipsExecs != wantExecs {
		t.Errorf("expected %d routing_slips Update calls, got %d", wantExecs, routingSlipsExecs)
	}
}

// ---------------------------------------------------------------------------
// Quickest-win coverage additions
// ---------------------------------------------------------------------------

// TestClickHouseStore_UpdateAggregateStatusWithHistory_NoConflict verifies the happy path for
// updateAggregateStatusFromComponentStatesWithHistory: version matches after the first write,
// so no retry occurs and exactly one routing_slips Update is issued.
func TestClickHouseStore_UpdateAggregateStatusWithHistory_NoConflict(t *testing.T) {
	mockSession := createMockSessionForUpdates(
		"corr-withhistory-ok", "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1,
	)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "builds",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		context.Background(), "corr-withhistory-ok", "builds", entry,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	if routingSlipsExecs != 1 {
		t.Errorf("expected exactly 1 routing_slips Update (no conflict), got %d", routingSlipsExecs)
	}
}

// TestClickHouseStore_UpdateAggregateStatusWithHistory_ConflictRetry verifies that when
// loadVersionFromDB returns a version higher than the one just written,
// updateAggregateStatusFromComponentStatesWithHistory re-Loads and re-writes once before the
// version matches, producing exactly 2 routing_slips Update calls.
func TestClickHouseStore_UpdateAggregateStatusWithHistory_ConflictRetry(t *testing.T) {
	slipRow := createMockScanRow("corr-wh-retry", "myorg/myrepo", "main", "abc123", SlipStatusInProgress)

	var lastInsertedVersion uint64
	queryRowCall := 0
	mockSession := &clickhousetest.MockSession{
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
			if strings.Contains(query, "SELECT version FROM") {
				queryRowCall++
				call := queryRowCall
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*uint64); ok {
								if call == 1 {
									*v = lastInsertedVersion + 500 // concurrent writer
								} else {
									*v = lastInsertedVersion // our retry won
								}
							}
						}
						return nil
					},
				}
			}
			// loadStateHistoryFromDB: SELECT state_history FROM ... (single-column; full Load starts with SELECT correlation_id)
			if strings.Contains(query, "SELECT state_history") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) == 0 {
							return fmt.Errorf("expected scan destination, got none")
						}
						jsonPtr, ok := dest[0].(*chcol.JSON)
						if !ok {
							return fmt.Errorf("expected dest[0] to be *chcol.JSON, got %T", dest[0])
						}
						return jsonPtr.Scan(map[string]any{"entries": []any{}})
					},
				}
			}
			return slipRow
		},
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &clickhousetest.MockRows{}, nil
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
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

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "builds",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		context.Background(), "corr-wh-retry", "builds", entry,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	if routingSlipsExecs != 2 {
		t.Errorf("expected 2 routing_slips Update calls (initial + one conflict retry), got %d", routingSlipsExecs)
	}
	if queryRowCall != 2 {
		t.Errorf("expected 2 loadVersionFromDB calls, got %d", queryRowCall)
	}
}

// TestClickHouseStore_UpdateAggregateStatusWithHistory_ConflictRetryExhausted verifies that
// when all aggregateConflictMaxRetries are exhausted, the function returns nil (best-effort).
func TestClickHouseStore_UpdateAggregateStatusWithHistory_ConflictRetryExhausted(t *testing.T) {
	slipRow := createMockScanRow("corr-wh-exhausted", "myorg/myrepo", "main", "abc123", SlipStatusInProgress)

	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
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
			if strings.Contains(query, "SELECT version FROM") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) > 0 {
							if v, ok := dest[0].(*uint64); ok {
								*v = lastInsertedVersion + 1 // always a concurrent writer
							}
						}
						return nil
					},
				}
			}
			// loadStateHistoryFromDB: SELECT state_history FROM ... (single-column; full Load starts with SELECT correlation_id)
			if strings.Contains(query, "SELECT state_history") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) == 0 {
							return fmt.Errorf("expected scan destination, got none")
						}
						jsonPtr, ok := dest[0].(*chcol.JSON)
						if !ok {
							return fmt.Errorf("expected dest[0] to be *chcol.JSON, got %T", dest[0])
						}
						return jsonPtr.Scan(map[string]any{"entries": []any{}})
					},
				}
			}
			return slipRow
		},
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &clickhousetest.MockRows{}, nil
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
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

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "builds",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		context.Background(), "corr-wh-exhausted", "builds", entry,
	)
	if err != nil {
		t.Errorf("expected nil (best-effort outcome), got %v", err)
	}

	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	wantExecs := 1 + aggregateConflictMaxRetries
	if routingSlipsExecs != wantExecs {
		t.Errorf("expected %d routing_slips Update calls, got %d", wantExecs, routingSlipsExecs)
	}
}

// TestClickHouseStore_UpdateAggregateStatus_NoConflict verifies the vanilla happy path for
// updateAggregateStatusFromComponentStates: first write, version matches, no retry.
func TestClickHouseStore_UpdateAggregateStatus_NoConflict(t *testing.T) {
	mockSession := createMockSessionForUpdates(
		"corr-no-conflict", "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1,
	)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.updateAggregateStatusFromComponentStates(
		context.Background(), "corr-no-conflict", "build",
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	if routingSlipsExecs != 1 {
		t.Errorf("expected exactly 1 routing_slips Update (no conflict), got %d", routingSlipsExecs)
	}
}

// TestClickHouseStore_UpdateAggregateStatus_VersionCheckError verifies that when
// loadVersionFromDB returns an error (e.g. network hiccup), the function treats it as
// non-fatal and returns nil rather than propagating the error.
func TestClickHouseStore_UpdateAggregateStatus_VersionCheckError(t *testing.T) {
	slipRow := createMockScanRow("corr-vcheck-err", "myorg/myrepo", "main", "abc123", SlipStatusInProgress)

	var lastInsertedVersion uint64
	mockSession := &clickhousetest.MockSession{
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
			// loadVersionFromDB returns a scan error (simulates DB hiccup).
			if strings.Contains(query, "SELECT version FROM") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						return fmt.Errorf("network error")
					},
				}
			}
			return slipRow
		},
		QueryWithArgsFunc: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
			return &clickhousetest.MockRows{}, nil
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
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

	err := store.updateAggregateStatusFromComponentStates(
		context.Background(), "corr-vcheck-err", "build",
	)
	// loadVersionFromDB errors are non-fatal; function must return nil.
	if err != nil {
		t.Errorf("expected nil (non-fatal version check error), got %v", err)
	}

	// Write must still have been attempted once.
	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	if routingSlipsExecs != 1 {
		t.Errorf("expected 1 routing_slips Update call before version check error, got %d", routingSlipsExecs)
	}
}

// TestClickHouseStore_UpdateStepWithHistory_PurePipelineStep verifies that UpdateStepWithHistory
// for a pure pipeline step (no component, not an aggregate step) writes one event to
// slip_component_states and calls AppendHistory (1 QueryRow for state_history + 1 ExecWithArgs
// for the UNION ALL update) without issuing any full Load queries.
func TestClickHouseStore_UpdateStepWithHistory_PurePipelineStep(t *testing.T) {
	mockSession := createMockSessionForUpdates(
		"corr-uwh-pipeline", "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1,
	)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.UpdateStepWithHistory(
		context.Background(), "corr-uwh-pipeline", "push_parsed", "", StepStatusCompleted, entry,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Expect: 1 ExecWithArgs for slip_component_states (insertComponentState)
	// + 1 ExecWithArgs for the UNION ALL history update (insertAtomicHistoryUpdate).
	componentStateInserts := 0
	historyInserts := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, TableSlipComponentStates) {
			componentStateInserts++
		}
		if strings.Contains(call.Stmt, "INSERT INTO") && strings.Contains(call.Stmt, "routing_slips") {
			historyInserts++
		}
	}
	if componentStateInserts != 1 {
		t.Errorf("expected 1 slip_component_states insert, got %d", componentStateInserts)
	}
	// AppendHistory uses insertAtomicHistoryUpdate which inserts into routing_slips.
	if historyInserts < 1 {
		t.Errorf("expected at least 1 routing_slips INSERT (AppendHistory), got %d", historyInserts)
	}
}

// TestClickHouseStore_UpdateStepWithHistory_PurePipelineStep_StatusOverrideInSQL is a
// regression test for the unit_tests_status staleness bug (introduced in v1.3.70).
// It verifies that when UpdateStepWithHistory is called for a pure pipeline step
// (non-aggregate, componentName == ""), insertAtomicHistoryUpdate replaces the step-status
// column reference with a SQL literal (?) in the new-row SELECT instead of copying the
// verbatim DB column value — and that the override status is injected into ExecWithArgs
// args at the correct position.
//
// Without this fix, "running" written by the builds aggregate write-back would be copied
// verbatim, leaving unit_tests_status (or any pure pipeline step status) permanently stale.
func TestClickHouseStore_UpdateStepWithHistory_PurePipelineStep_StatusOverrideInSQL(t *testing.T) {
	const corrID = "corr-status-override-regression"
	mockSession := createMockSessionForUpdates(corrID, "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.UpdateStepWithHistory(
		context.Background(), corrID, "push_parsed", "", StepStatusCompleted, entry,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Find the routing_slips INSERT call (insertAtomicHistoryUpdate).
	// The other ExecWithArgs call is the slip_component_states insert.
	var historyStmt string
	var historyArgs []any
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			historyStmt = call.Stmt
			historyArgs = call.Args
			break
		}
	}
	if historyStmt == "" {
		t.Fatal("expected a routing_slips INSERT (insertAtomicHistoryUpdate), found none")
	}

	// The override path must replace the step-status column reference with ? in the
	// new-row SELECT. Without the fix, "push_parsed_status" appears 3 times in the query
	// (INSERT column list + cancel SELECT + new-row SELECT). With the fix it appears
	// exactly twice (INSERT column list + cancel SELECT only; new-row SELECT uses ?).
	const colName = "push_parsed_status"
	occurrences := strings.Count(historyStmt, colName)
	if occurrences != 2 {
		t.Errorf(
			"expected %q to appear 2 times in routing_slips INSERT (column list + cancel SELECT only),"+
				" got %d — new-row SELECT should use ? instead.\nQuery: %s",
			colName, occurrences, historyStmt,
		)
	}

	// Args order for insertAtomicHistoryUpdate with 1 override (push_parsed_status):
	//   [0] correlationID          (cancel WHERE)
	//   [1] newVersion (uint64)    (cancel WHERE)
	//   [2] newStateHistoryJSON    (new-row CAST(? AS JSON) literal)
	//   [3] newVersion (uint64)    (new-row version literal)
	//   [4] "completed"            (push_parsed_status override value)  ← THE FIX
	//   [5] correlationID          (new-row WHERE)
	const wantArgCount = 6
	if len(historyArgs) != wantArgCount {
		t.Fatalf("expected %d ExecWithArgs args (5 base + 1 step override), got %d: %v",
			wantArgCount, len(historyArgs), historyArgs)
	}
	overrideArg, ok := historyArgs[4].(string)
	if !ok {
		t.Fatalf("expected args[4] (step status override) to be string, got %T: %v",
			historyArgs[4], historyArgs[4])
	}
	if overrideArg != string(StepStatusCompleted) {
		t.Errorf("expected args[4] (step status override) = %q, got %q",
			string(StepStatusCompleted), overrideArg)
	}
}

// TestClickHouseStore_AppendHistory_NoStatusColumnOverride verifies the contrasting case:
// AppendHistory (public, no overrides) must NOT inject extra step-status args and must
// NOT replace the step-status column reference with ? — the verbatim DB column value is
// copied as-is. This ensures the override mechanism is scoped exclusively to
// UpdateStepWithHistory's pure-pipeline-step path and does not affect other callers.
func TestClickHouseStore_AppendHistory_NoStatusColumnOverride(t *testing.T) {
	const corrID = "corr-appendhistory-no-override"
	mockSession := createMockSessionForUpdates(corrID, "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.AppendHistory(context.Background(), corrID, entry)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// AppendHistory must produce exactly one ExecWithArgs call (insertAtomicHistoryUpdate).
	if len(mockSession.ExecWithArgsCalls) != 1 {
		t.Fatalf("expected 1 ExecWithArgs call, got %d", len(mockSession.ExecWithArgsCalls))
	}
	call := mockSession.ExecWithArgsCalls[0]

	// Without overrides, exactly 5 base args:
	//   [0] correlationID, [1] newVersion, [2] newStateHistoryJSON, [3] newVersion, [4] correlationID.
	// No extra step-status arg injected.
	const wantArgCount = 5
	if len(call.Args) != wantArgCount {
		t.Errorf("expected %d ExecWithArgs args (no override), got %d: %v",
			wantArgCount, len(call.Args), call.Args)
	}

	// push_parsed_status must appear 3 times verbatim (INSERT column list + cancel SELECT +
	// new-row SELECT), confirming the column is copied from DB and not replaced with ?.
	const colName = "push_parsed_status"
	occurrences := strings.Count(call.Stmt, colName)
	if occurrences != 3 {
		t.Errorf(
			"expected %q to appear 3 times in routing_slips INSERT (column list + cancel + new-row SELECT), got %d.\nQuery: %s",
			colName, occurrences, call.Stmt,
		)
	}
}


// status read (QueryRow) returns an error, SetComponentImageTag propagates it and does
// not attempt an insert.
func TestClickHouseStore_SetComponentImageTag_QueryRowError(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					return fmt.Errorf("connection reset")
				},
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.SetComponentImageTag(
		context.Background(), "corr-tag-err", "build", "api", "v1.2.3",
	)
	if err == nil {
		t.Fatal("expected an error when QueryRow fails, got nil")
	}
	if len(mockSession.ExecWithArgsCalls) != 0 {
		t.Errorf("expected no ExecWithArgs calls when status read fails, got %d",
			len(mockSession.ExecWithArgsCalls))
	}
}

// TestClickHouseStore_SetComponentImageTag_InsertError verifies that when the status read
// succeeds but the subsequent insert fails, SetComponentImageTag propagates that error.
func TestClickHouseStore_SetComponentImageTag_InsertError(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if ptr, ok := dest[0].(*string); ok {
						*ptr = string(StepStatusRunning)
					}
					return nil
				},
			}
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
			return fmt.Errorf("insert failed: disk full")
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.SetComponentImageTag(
		context.Background(), "corr-tag-insert-err", "build", "api", "v1.2.3",
	)
	if err == nil {
		t.Fatal("expected an error when insert fails, got nil")
	}
}

// TestClickHouseStore_AppendHistory_LoadStateHistoryError verifies that when
// loadStateHistoryFromDB returns a non-retryable error, AppendHistory propagates it
// without attempting an insert.
func TestClickHouseStore_AppendHistory_LoadStateHistoryError(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					return fmt.Errorf("query error: table not found")
				},
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.AppendHistory(context.Background(), "corr-ah-err", entry)
	if err == nil {
		t.Fatal("expected error when loadStateHistoryFromDB fails, got nil")
	}
	if len(mockSession.ExecWithArgsCalls) != 0 {
		t.Errorf("expected no ExecWithArgs calls when state history load fails, got %d",
			len(mockSession.ExecWithArgsCalls))
	}
}

// TestClickHouseStore_AppendHistory_InsertAtomicHistoryUpdateError verifies that when
// insertAtomicHistoryUpdate (the UNION ALL write) fails, AppendHistory returns the error.
func TestClickHouseStore_AppendHistory_InsertAtomicHistoryUpdateError(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			// loadStateHistoryFromDB succeeds
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if len(dest) == 0 {
						return fmt.Errorf("expected scan destination, got none")
					}
					jsonPtr, ok := dest[0].(*chcol.JSON)
					if !ok {
						return fmt.Errorf("expected *chcol.JSON destination, got %T", dest[0])
					}
					return jsonPtr.Scan(map[string]any{"entries": []any{}})
				},
			}
		},
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
			return fmt.Errorf("insert error: cluster unavailable")
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "ci",
	}

	err := store.AppendHistory(context.Background(), "corr-ah-insert-err", entry)
	if err == nil {
		t.Fatal("expected error when insertAtomicHistoryUpdate fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// Critical gap tests — remaining uncovered branches
// ---------------------------------------------------------------------------

// --- updateAggregateStatusFromComponentStates ---

// TestClickHouseStore_UpdateAggregateStatus_ContextCancelled verifies that a pre-cancelled
// context is detected at the top of the retry loop and returned immediately.
func TestClickHouseStore_UpdateAggregateStatus_ContextCancelled(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	err := store.updateAggregateStatusFromComponentStates(ctx, "corr-ctx-cancel", "build")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	// No DB calls should have been made.
	if len(mockSession.QueryRowCalls) != 0 {
		t.Errorf("expected 0 QueryRow calls, got %d", len(mockSession.QueryRowCalls))
	}
}

// TestClickHouseStore_UpdateAggregateStatus_LoadError verifies that a non-ErrSlipNotFound
// Load failure is propagated as an error (no retry).
func TestClickHouseStore_UpdateAggregateStatus_LoadError(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					return fmt.Errorf("disk read error")
				},
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.updateAggregateStatusFromComponentStates(context.Background(), "corr-load-err", "build")
	if err == nil {
		t.Fatal("expected error from Load failure, got nil")
	}
	if len(mockSession.ExecWithArgsCalls) != 0 {
		t.Errorf("expected no ExecWithArgs calls after Load failure, got %d", len(mockSession.ExecWithArgsCalls))
	}
}

// TestClickHouseStore_UpdateAggregateStatus_ErrSlipNotFound_ContextCancelledDuringWait
// verifies that when Load returns ErrSlipNotFound and the context is cancelled during the
// backoff wait, the function returns context.Canceled rather than retrying indefinitely.
func TestClickHouseStore_UpdateAggregateStatus_ErrSlipNotFound_ContextCancelledDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(_ context.Context, query string, args ...any) driver.Row {
			// Load query — cancel the context so the sleep select triggers ctx.Done().
			if !strings.Contains(query, "max(version)") && !strings.Contains(query, "SELECT version FROM") {
				cancel()
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error { return sql.ErrNoRows },
				}
			}
			return &clickhousetest.MockRow{ScanFunc: func(dest ...any) error { return nil }}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.updateAggregateStatusFromComponentStates(ctx, "corr-slip-wait-cancel", "build")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled during ErrSlipNotFound wait, got %v", err)
	}
}

// TestClickHouseStore_UpdateAggregateStatus_NoAggregateStep verifies that when the step has
// no aggregate configured (e.g. "push_parsed"), the function returns nil immediately after
// the Load without issuing any Update.
func TestClickHouseStore_UpdateAggregateStatus_NoAggregateStep(t *testing.T) {
	mockSession := createMockSessionForUpdates(
		"corr-no-agg", "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1,
	)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	// "push_parsed" has no Aggregates field → resolveAggregateStepName returns "".
	err := store.updateAggregateStatusFromComponentStates(
		context.Background(), "corr-no-agg", "push_parsed",
	)
	if err != nil {
		t.Fatalf("expected nil for step with no aggregate, got %v", err)
	}
	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			routingSlipsExecs++
		}
	}
	if routingSlipsExecs != 0 {
		t.Errorf("expected no routing_slips Update for non-aggregate step, got %d", routingSlipsExecs)
	}
}

// TestClickHouseStore_UpdateAggregateStatus_UpdateFails verifies that when the routing_slips
// Update (ExecWithArgs) fails, the error is propagated.
func TestClickHouseStore_UpdateAggregateStatus_UpdateFails(t *testing.T) {
	slipRow := createMockScanRow("corr-update-fail", "myorg/myrepo", "main", "abc123", SlipStatusInProgress)

	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			if strings.Contains(query, "max(version)") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if v, ok := dest[0].(*sql.NullInt64); ok {
							v.Int64 = 1
							v.Valid = true
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
			if strings.Contains(stmt, "routing_slips") {
				return fmt.Errorf("write failed: quota exceeded")
			}
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.updateAggregateStatusFromComponentStates(
		context.Background(), "corr-update-fail", "build",
	)
	if err == nil {
		t.Fatal("expected error when Update fails, got nil")
	}
}

// --- updateAggregateStatusFromComponentStatesWithHistory ---

// TestClickHouseStore_UpdateAggregateStatusWithHistory_ContextCancelled verifies that a
// pre-cancelled context is detected at the top of the retry loop.
func TestClickHouseStore_UpdateAggregateStatusWithHistory_ContextCancelled(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entry := StateHistoryEntry{Step: "builds", Status: StepStatusCompleted}
	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		ctx, "corr-wh-ctx", "builds", entry,
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestClickHouseStore_UpdateAggregateStatusWithHistory_LoadError verifies that a
// non-ErrSlipNotFound Load failure is propagated as an error.
func TestClickHouseStore_UpdateAggregateStatusWithHistory_LoadError(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					return fmt.Errorf("network timeout")
				},
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{Step: "builds", Status: StepStatusCompleted}
	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		context.Background(), "corr-wh-load-err", "builds", entry,
	)
	if err == nil {
		t.Fatal("expected error from Load failure, got nil")
	}
}

// TestClickHouseStore_UpdateAggregateStatusWithHistory_NoAggregateStep verifies that when
// the step has no aggregate, the function delegates to AppendHistory instead of calling Update.
func TestClickHouseStore_UpdateAggregateStatusWithHistory_NoAggregateStep(t *testing.T) {
	mockSession := createMockSessionForUpdates(
		"corr-wh-no-agg", "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1,
	)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	// "push_parsed" has no aggregate → resolveAggregateStepName returns "" →
	// function calls AppendHistory (which does a state_history read + UNION ALL insert).
	entry := StateHistoryEntry{Step: "push_parsed", Status: StepStatusCompleted}
	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		context.Background(), "corr-wh-no-agg", "push_parsed", entry,
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	// AppendHistory should have issued a UNION ALL routing_slips insert.
	historyInserts := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		if strings.Contains(call.Stmt, "routing_slips") {
			historyInserts++
		}
	}
	if historyInserts < 1 {
		t.Errorf("expected at least 1 routing_slips INSERT from AppendHistory, got %d", historyInserts)
	}
}

// TestClickHouseStore_UpdateAggregateStatusWithHistory_ErrSlipNotFound_ContextCancelledDuringWait
// verifies that when Load returns ErrSlipNotFound and the context is cancelled during the
// backoff wait, the function returns context.Canceled.
func TestClickHouseStore_UpdateAggregateStatusWithHistory_ErrSlipNotFound_ContextCancelledDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(_ context.Context, query string, args ...any) driver.Row {
			// Load query — cancel the context so the sleep select triggers ctx.Done().
			if !strings.Contains(query, "max(version)") && !strings.Contains(query, "SELECT version FROM") {
				cancel()
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error { return sql.ErrNoRows },
				}
			}
			return &clickhousetest.MockRow{ScanFunc: func(dest ...any) error { return nil }}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{Step: "builds", Status: StepStatusCompleted}
	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		ctx, "corr-wh-slip-cancel", "builds", entry,
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled during ErrSlipNotFound wait, got %v", err)
	}
}

// TestClickHouseStore_UpdateAggregateStatusWithHistory_UpdateFails verifies that when
// the routing_slips Update fails, the error is propagated.
func TestClickHouseStore_UpdateAggregateStatusWithHistory_UpdateFails(t *testing.T) {
	slipRow := createMockScanRow("corr-wh-upd-fail", "myorg/myrepo", "main", "abc123", SlipStatusInProgress)
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			if strings.Contains(query, "max(version)") {
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if v, ok := dest[0].(*sql.NullInt64); ok {
							v.Int64 = 1
							v.Valid = true
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
			if strings.Contains(stmt, "routing_slips") {
				return fmt.Errorf("write failed: disk full")
			}
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{Step: "builds", Status: StepStatusCompleted}
	err := store.updateAggregateStatusFromComponentStatesWithHistory(
		context.Background(), "corr-wh-upd-fail", "builds", entry,
	)
	if err == nil {
		t.Fatal("expected error when Update fails, got nil")
	}
}

// --- UpdateStepWithHistory ---

// TestClickHouseStore_UpdateStepWithHistory_InsertError verifies that when insertComponentState
// fails, UpdateStepWithHistory propagates the error and does not proceed to AppendHistory or
// the aggregate update.
func TestClickHouseStore_UpdateStepWithHistory_InsertError(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(ctx context.Context, stmt string, args ...any) error {
			return fmt.Errorf("insert failed: cluster unavailable")
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{Step: "push_parsed", Status: StepStatusCompleted}
	err := store.UpdateStepWithHistory(
		context.Background(), "corr-uwh-insert-err", "push_parsed", "", StepStatusCompleted, entry,
	)
	if err == nil {
		t.Fatal("expected error when insertComponentState fails, got nil")
	}
	// Only one ExecWithArgs call (the failing insert); no QueryRow (no Load, no history).
	if len(mockSession.ExecWithArgsCalls) != 1 {
		t.Errorf("expected 1 ExecWithArgs call (the failing insert), got %d",
			len(mockSession.ExecWithArgsCalls))
	}
}

// TestClickHouseStore_UpdateStepWithHistory_ComponentStep verifies that when componentName
// is non-empty, UpdateStepWithHistory routes through
// updateAggregateStatusFromComponentStatesWithHistory (not AppendHistory directly).
// This exercises the componentName != "" branch.
func TestClickHouseStore_UpdateStepWithHistory_ComponentStep(t *testing.T) {
	mockSession := createMockSessionForUpdates(
		"corr-uwh-comp", "myorg/myrepo", "main", "abc123", SlipStatusInProgress, 1,
	)
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{Step: "builds", Status: StepStatusCompleted, Actor: "ci"}
	err := store.UpdateStepWithHistory(
		context.Background(), "corr-uwh-comp", "builds", "api", StepStatusCompleted, entry,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// The path goes: insertComponentState (slip_component_states ExecWithArgs) →
	// updateAggregateStatusFromComponentStatesWithHistory → Load + Update (routing_slips ExecWithArgs).
	componentStateInserts := 0
	routingSlipsExecs := 0
	for _, call := range mockSession.ExecWithArgsCalls {
		switch {
		case strings.Contains(call.Stmt, TableSlipComponentStates):
			componentStateInserts++
		case strings.Contains(call.Stmt, "routing_slips"):
			routingSlipsExecs++
		}
	}
	if componentStateInserts != 1 {
		t.Errorf("expected 1 slip_component_states insert, got %d", componentStateInserts)
	}
	if routingSlipsExecs < 1 {
		t.Errorf("expected at least 1 routing_slips Update (aggregate write-back), got %d", routingSlipsExecs)
	}
}

// --- SetComponentImageTag ---

// TestClickHouseStore_SetComponentImageTag_NotFound verifies that when the status read
// returns sql.ErrNoRows, SetComponentImageTag returns an error indicating the component
// was not found (not ErrSlipNotFound, since the slip may exist but the component has no events).
func TestClickHouseStore_SetComponentImageTag_NotFound(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error { return sql.ErrNoRows },
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.SetComponentImageTag(context.Background(), "corr-tag-nf", "build", "api", "v1.0.0")
	if err == nil {
		t.Fatal("expected error for missing component, got nil")
	}
	if errors.Is(err, ErrSlipNotFound) {
		t.Errorf("error should not wrap ErrSlipNotFound for missing component state, got %v", err)
	}
	if !strings.Contains(err.Error(), "not found in event log") {
		t.Errorf("expected 'not found in event log' message, got %v", err)
	}
}

// TestClickHouseStore_SetComponentImageTag_EmptyStatus verifies that when the status scan
// succeeds but returns an empty string, SetComponentImageTag returns an error indicating
// the component was not found (not ErrSlipNotFound, since the slip may exist).
func TestClickHouseStore_SetComponentImageTag_EmptyStatus(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					// Scan succeeds but leaves the string pointer at zero value ("").
					return nil
				},
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.SetComponentImageTag(context.Background(), "corr-tag-empty", "build", "api", "v1.0.0")
	if err == nil {
		t.Fatal("expected error for empty component status, got nil")
	}
	if errors.Is(err, ErrSlipNotFound) {
		t.Errorf("error should not wrap ErrSlipNotFound for empty component status, got %v", err)
	}
	if !strings.Contains(err.Error(), "not found in event log") {
		t.Errorf("expected 'not found in event log' message, got %v", err)
	}
}

// --- AppendHistory ---

// TestClickHouseStore_AppendHistory_ContextCancelled verifies that a pre-cancelled context
// is detected at the top of the retry loop.
func TestClickHouseStore_AppendHistory_ContextCancelled(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entry := StateHistoryEntry{Step: "push_parsed", Status: StepStatusCompleted}
	err := store.AppendHistory(ctx, "corr-ah-ctx", entry)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if len(mockSession.QueryRowCalls) != 0 {
		t.Errorf("expected 0 QueryRow calls, got %d", len(mockSession.QueryRowCalls))
	}
}

// TestClickHouseStore_AppendHistory_MalformedHistoryJSON verifies that a malformed
// state_history JSON value returns an error instead of silently discarding existing history.
func TestClickHouseStore_AppendHistory_MalformedHistoryJSON(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(ctx context.Context, query string, args ...any) driver.Row {
			// Return a scan-time error simulating how clickhouse-go v2.44.0 rejects
			// malformed JSON at row.Scan, matching the real production failure mode.
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					return fmt.Errorf("clickhouse: failed to scan JSON column: invalid JSON data")
				},
			}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{Step: "push_parsed", Status: StepStatusCompleted}
	err := store.AppendHistory(context.Background(), "corr-ah-malformed", entry)
	if err == nil {
		t.Fatal("expected error for malformed history JSON, got nil")
	}
	// Must not have called ExecWithArgs (no write should follow a malformed read).
	if len(mockSession.ExecWithArgsCalls) != 0 {
		t.Errorf("expected no ExecWithArgs calls after malformed JSON, got %d",
			len(mockSession.ExecWithArgsCalls))
	}
}

// TestBuildComponentData_MessageOnlyForFailure verifies that state.Message is only
// mapped to Error when the component's status is a failure. Non-failure statuses
// (e.g. "running" with a progress note) must not produce an Error field.
func TestBuildComponentData_MessageOnlyForFailure(t *testing.T) {
	t.Run("failure with message sets Error", func(t *testing.T) {
		state := componentStateRow{
			Status:  string(StepStatusFailed),
			Message: "build timeout",
		}
		got := buildComponentData("api", state)
		if got.Error != "build timeout" {
			t.Errorf("expected Error='build timeout', got %q", got.Error)
		}
	})

	t.Run("running with message does not set Error", func(t *testing.T) {
		state := componentStateRow{
			Status:  string(StepStatusRunning),
			Message: "building layer 3/5",
		}
		got := buildComponentData("api", state)
		if got.Error != "" {
			t.Errorf("expected empty Error for running status, got %q", got.Error)
		}
	})

	t.Run("completed with message does not set Error", func(t *testing.T) {
		state := componentStateRow{
			Status:  string(StepStatusCompleted),
			Message: "image pushed",
		}
		got := buildComponentData("api", state)
		if got.Error != "" {
			t.Errorf("expected empty Error for completed status, got %q", got.Error)
		}
	})
}

// TestUpdateExistingComponent_ClearsErrorOnNonFailure verifies that transitioning a
// component away from a failure status clears any stale Error value.
func TestUpdateExistingComponent_ClearsErrorOnNonFailure(t *testing.T) {
	dest := ComponentStepData{
		Component: "api",
		Status:    StepStatusFailed,
		Error:     "previous error",
	}
	src := ComponentStepData{
		Component: "api",
		Status:    StepStatusCompleted,
		Error:     "", // retry succeeded — no new error
	}
	updateExistingComponent(&dest, src)
	if dest.Error != "" {
		t.Errorf("expected Error to be cleared on non-failure transition, got %q", dest.Error)
	}
	if dest.Status != StepStatusCompleted {
		t.Errorf("expected status=completed, got %s", dest.Status)
	}
}

// TestUpdateExistingComponent_PreservesErrorOnFailure verifies that a failure status
// propagates the error message.
func TestUpdateExistingComponent_PreservesErrorOnFailure(t *testing.T) {
	dest := ComponentStepData{
		Component: "api",
		Status:    StepStatusRunning,
	}
	src := ComponentStepData{
		Component: "api",
		Status:    StepStatusFailed,
		Error:     "out of memory",
	}
	updateExistingComponent(&dest, src)
	if dest.Error != "out of memory" {
		t.Errorf("expected Error='out of memory', got %q", dest.Error)
	}
}

// TestUpdateExistingComponent_CompletedAt_UpdatedOnRetry verifies that when a component
// transitions from a terminal failure to a terminal success (retry scenario), CompletedAt
// is updated to the later timestamp rather than keeping the stale failure timestamp.
func TestUpdateExistingComponent_CompletedAt_UpdatedOnRetry(t *testing.T) {
	failedTime := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	retryTime := time.Date(2026, 4, 2, 10, 5, 0, 0, time.UTC)

	dest := ComponentStepData{
		Component:   "api",
		Status:      StepStatusFailed,
		CompletedAt: &failedTime,
	}
	src := ComponentStepData{
		Component:   "api",
		Status:      StepStatusCompleted,
		CompletedAt: &retryTime,
	}
	updateExistingComponent(&dest, src)
	if dest.CompletedAt == nil {
		t.Fatal("CompletedAt should not be nil after retry")
	}
	if !dest.CompletedAt.Equal(retryTime) {
		t.Errorf("CompletedAt should be updated to retry time %v, got %v", retryTime, *dest.CompletedAt)
	}
	if dest.Status != StepStatusCompleted {
		t.Errorf("expected status=completed, got %s", dest.Status)
	}
}

// ---------------------------------------------------------------------------
// Gap 1: isAncestryColumnError — package-level function
// ---------------------------------------------------------------------------

// TestIsAncestryColumnError validates all branches of the isAncestryColumnError helper.
func TestIsAncestryColumnError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "ancestry + Unknown identifier",
			err:  fmt.Errorf("column %s: Unknown identifier in query", ColumnAncestry),
			want: true,
		},
		{
			name: "ancestry + Missing columns",
			err:  fmt.Errorf("Missing columns: %s is not found", ColumnAncestry),
			want: true,
		},
		{
			name: "ancestry + UNKNOWN_IDENTIFIER code",
			err:  fmt.Errorf("Code: UNKNOWN_IDENTIFIER, column %s", ColumnAncestry),
			want: true,
		},
		{
			name: "ancestry without sentinel string",
			err:  fmt.Errorf("error with column %s: timeout", ColumnAncestry),
			want: false,
		},
		{
			name: "sentinel string without ancestry",
			err:  errors.New("Unknown identifier in column foo"),
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("connection refused"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isAncestryColumnError(tc.err)
			if got != tc.want {
				t.Errorf("isAncestryColumnError(%q) = %v, want %v", tc.err.Error(), got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Gap 1b: isImageTagColumnError — package-level function
// ---------------------------------------------------------------------------

// TestIsImageTagColumnError validates all branches of the isImageTagColumnError helper.
func TestIsImageTagColumnError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "image_tag + Missing columns",
			err:  fmt.Errorf("Missing columns: image_tag is not found"),
			want: true,
		},
		{
			name: "image_tag + Unknown column",
			err:  fmt.Errorf("Unknown column image_tag in table"),
			want: true,
		},
		{
			name: "image_tag + No such column",
			err:  fmt.Errorf("No such column image_tag in schema"),
			want: true,
		},
		{
			name: "image_tag + Unknown identifier",
			err:  fmt.Errorf("Unknown identifier: image_tag in query"),
			want: true,
		},
		{
			name: "image_tag + UNKNOWN_IDENTIFIER code",
			err:  fmt.Errorf("Code: UNKNOWN_IDENTIFIER, column image_tag"),
			want: true,
		},
		{
			name: "image_tag without sentinel string",
			err:  fmt.Errorf("image_tag: connection timeout"),
			want: false,
		},
		{
			name: "sentinel string without image_tag",
			err:  errors.New("Unknown identifier in column foo"),
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("connection refused"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isImageTagColumnError(tc.err)
			if got != tc.want {
				t.Errorf("isImageTagColumnError(%q) = %v, want %v", tc.err.Error(), got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Gap 2: detectAncestryColumn — method
// ---------------------------------------------------------------------------

// TestClickHouseStore_DetectAncestryColumn tests the detectAncestryColumn method
// for column-exists, column-absent, nil-row, and scan-error scenarios.
func TestClickHouseStore_DetectAncestryColumn(t *testing.T) {
	t.Run("column exists", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
				if strings.Contains(query, "system.columns") {
					return &clickhousetest.MockRow{
						ScanFunc: func(dest ...any) error {
							if v, ok := dest[0].(*uint64); ok {
								*v = 1
							}
							return nil
						},
					}
				}
				return &clickhousetest.MockRow{}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		// Reset to false so we can verify detectAncestryColumn sets it true.
		store.hasAncestryColumn.Store(false)

		store.detectAncestryColumn(context.Background())

		if !store.hasAncestryColumn.Load() {
			t.Error("expected hasAncestryColumn=true when count=1")
		}
	})

	t.Run("column does not exist", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
				if strings.Contains(query, "system.columns") {
					return &clickhousetest.MockRow{
						ScanFunc: func(dest ...any) error {
							if v, ok := dest[0].(*uint64); ok {
								*v = 0
							}
							return nil
						},
					}
				}
				return &clickhousetest.MockRow{}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		store.detectAncestryColumn(context.Background())

		if store.hasAncestryColumn.Load() {
			t.Error("expected hasAncestryColumn=false when count=0")
		}
	})

	t.Run("nil row — conservative default", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
				if strings.Contains(query, "system.columns") {
					return nil
				}
				return &clickhousetest.MockRow{}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		store.hasAncestryColumn.Store(false)

		store.detectAncestryColumn(context.Background())

		if !store.hasAncestryColumn.Load() {
			t.Error("expected hasAncestryColumn=true (conservative) when row is nil")
		}
	})

	t.Run("scan error — conservative default", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
				if strings.Contains(query, "system.columns") {
					return &clickhousetest.MockRow{
						ScanFunc: func(_ ...any) error {
							return errors.New("scan failed")
						},
					}
				}
				return &clickhousetest.MockRow{}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		store.hasAncestryColumn.Store(false)

		store.detectAncestryColumn(context.Background())

		if !store.hasAncestryColumn.Load() {
			t.Error("expected hasAncestryColumn=true (conservative) on scan error")
		}
	})
}

// ---------------------------------------------------------------------------
// Gap 3: insertAtomicHistoryUpdate self-healing on ancestry column error
// ---------------------------------------------------------------------------

// TestClickHouseStore_InsertAtomicHistoryUpdate_AncestryColumnSelfHealing verifies that when
// ExecWithArgs returns an isAncestryColumnError, the function flips hasAncestryColumn to
// false and retries exactly once without ancestry columns.
func TestClickHouseStore_InsertAtomicHistoryUpdate_AncestryColumnSelfHealing(t *testing.T) {
	var mu sync.Mutex
	execCall := 0
	var capturedStmts []string

	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(_ context.Context, stmt string, _ ...any) error {
			mu.Lock()
			execCall++
			call := execCall
			capturedStmts = append(capturedStmts, stmt)
			mu.Unlock()

			if call == 1 {
				// First call: simulate ancestry column error
				return fmt.Errorf("Missing columns: '%s' while processing query", ColumnAncestry)
			}
			// Second call: succeed
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	// Start with ancestry column believed to exist.
	store.hasAncestryColumn.Store(true)

	err := store.insertAtomicHistoryUpdate(
		context.Background(), "corr-self-heal", uint64(time.Now().UnixNano()), `{"entries":[]}`,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(capturedStmts) != 2 {
		t.Fatalf("expected exactly 2 ExecWithArgs calls, got %d", len(capturedStmts))
	}

	// First call should include the ancestry column.
	if !strings.Contains(capturedStmts[0], ColumnAncestry) {
		t.Error("expected first ExecWithArgs call to contain ancestry column")
	}

	// Second call (retry) should NOT include the ancestry column.
	if strings.Contains(capturedStmts[1], ColumnAncestry) {
		t.Error("expected second ExecWithArgs call to omit ancestry column")
	}

	// hasAncestryColumn must now be false.
	if store.hasAncestryColumn.Load() {
		t.Error("expected hasAncestryColumn=false after self-healing")
	}
}

// ---------------------------------------------------------------------------
// Gap 4a: AppendHistory — post-write conflict retry (succeeds on second attempt)
// ---------------------------------------------------------------------------

// TestClickHouseStore_AppendHistory_ConflictRetry verifies that when loadVersionFromDB returns
// a version higher than newVersion (simulating a concurrent writer), AppendHistory retries and
// succeeds on the second attempt when the version matches.
func TestClickHouseStore_AppendHistory_ConflictRetry(t *testing.T) {
	var mu sync.Mutex
	loadHistoryCallCount := 0
	execCallCount := 0
	versionCheckCallCount := 0
	var lastInsertedVersion uint64

	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
			// loadStateHistoryFromDB: SELECT state_history FROM ...
			if strings.Contains(query, "SELECT state_history") {
				mu.Lock()
				loadHistoryCallCount++
				mu.Unlock()
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) == 0 {
							return fmt.Errorf("expected scan destination, got none")
						}
						jsonPtr, ok := dest[0].(*chcol.JSON)
						if !ok {
							return fmt.Errorf("expected dest[0] to be *chcol.JSON, got %T", dest[0])
						}
						return jsonPtr.Scan(map[string]any{"entries": []any{}})
					},
				}
			}
			// loadVersionFromDB: SELECT version FROM ...
			// With pre-write floor loading, even-numbered calls are post-write checks,
			// odd-numbered calls are pre-write floor loads.
			if strings.Contains(query, "SELECT version FROM") {
				mu.Lock()
				versionCheckCallCount++
				call := versionCheckCallCount
				mu.Unlock()
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if v, ok := dest[0].(*uint64); ok {
							mu.Lock()
							defer mu.Unlock()
							switch {
							case call%2 == 1:
								// Pre-write floor loads: return 0 (no clock skew in this test).
								*v = 0
							case call == 2:
								// First post-write check: concurrent writer at a much higher version.
								*v = lastInsertedVersion + 999999
							default:
								// Second post-write check: our write won.
								*v = lastInsertedVersion
							}
						}
						return nil
					},
				}
			}
			return &clickhousetest.MockRow{}
		},
		ExecWithArgsFunc: func(_ context.Context, stmt string, args ...any) error {
			if strings.Contains(stmt, "routing_slips") && strings.Contains(stmt, "INSERT") {
				mu.Lock()
				execCallCount++
				// Capture the version argument (state_history param + version param).
				// In insertAtomicHistoryUpdate, args layout:
				// [0]=correlationID, [1]=newVersion (cancel WHERE), [2]=newHistoryJSON, [3]=newVersion, [4]=correlationID
				if len(args) >= 4 {
					if v, ok := args[3].(uint64); ok {
						lastInsertedVersion = v
					}
				}
				mu.Unlock()
			}
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "test",
	}

	err := store.AppendHistory(context.Background(), "corr-conflict-history", entry)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Two loadStateHistoryFromDB calls: one initial + one retry.
	if loadHistoryCallCount != 2 {
		t.Errorf("expected 2 loadStateHistoryFromDB calls, got %d", loadHistoryCallCount)
	}

	// Two insertAtomicHistoryUpdate calls: one initial + one retry.
	if execCallCount != 2 {
		t.Errorf("expected 2 insertAtomicHistoryUpdate calls, got %d", execCallCount)
	}

	// Four loadVersionFromDB calls: one pre-write + one post-write per attempt.
	if versionCheckCallCount != 4 {
		t.Errorf("expected 4 loadVersionFromDB calls, got %d", versionCheckCallCount)
	}
}

// ---------------------------------------------------------------------------
// Gap 4b: AppendHistory — conflict retry exhausted
// ---------------------------------------------------------------------------

// TestClickHouseStore_AppendHistory_ConflictRetryExhausted verifies that when loadVersionFromDB
// always returns a higher version, AppendHistory returns nil (best-effort) after exhausting
// historyConflictMaxRetries.
func TestClickHouseStore_AppendHistory_ConflictRetryExhausted(t *testing.T) {
	var mu sync.Mutex
	loadHistoryCallCount := 0
	execCallCount := 0
	versionCallCount := 0

	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
			if strings.Contains(query, "SELECT state_history") {
				mu.Lock()
				loadHistoryCallCount++
				mu.Unlock()
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if len(dest) == 0 {
							return fmt.Errorf("expected scan destination, got none")
						}
						jsonPtr, ok := dest[0].(*chcol.JSON)
						if !ok {
							return fmt.Errorf("expected dest[0] to be *chcol.JSON, got %T", dest[0])
						}
						return jsonPtr.Scan(map[string]any{"entries": []any{}})
					},
				}
			}
			if strings.Contains(query, "SELECT version FROM") {
				mu.Lock()
				versionCallCount++
				call := versionCallCount
				mu.Unlock()
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if v, ok := dest[0].(*uint64); ok {
							if call%2 == 1 {
								// Odd calls: pre-write floor load — return 0 (no clock skew).
								*v = 0
							} else {
								// Even calls: post-write version check — always higher
								// than any written version to simulate perpetual conflict.
								*v = ^uint64(0) - 1
							}
						}
						return nil
					},
				}
			}
			return &clickhousetest.MockRow{}
		},
		ExecWithArgsFunc: func(_ context.Context, stmt string, _ ...any) error {
			if strings.Contains(stmt, "routing_slips") && strings.Contains(stmt, "INSERT") {
				mu.Lock()
				execCallCount++
				mu.Unlock()
			}
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	entry := StateHistoryEntry{
		Timestamp: time.Now(),
		Step:      "push_parsed",
		Status:    StepStatusCompleted,
		Actor:     "test",
	}

	err := store.AppendHistory(context.Background(), "corr-exhausted-history", entry)
	if err != nil {
		t.Errorf("expected nil (best-effort), got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// 1 initial + historyConflictMaxRetries re-reads = historyConflictMaxRetries + 1 total.
	wantIterations := 1 + historyConflictMaxRetries
	if loadHistoryCallCount != wantIterations {
		t.Errorf("expected %d loadStateHistoryFromDB calls, got %d", wantIterations, loadHistoryCallCount)
	}
	if execCallCount != wantIterations {
		t.Errorf("expected %d insertAtomicHistoryUpdate calls, got %d", wantIterations, execCallCount)
	}
}

// ---------------------------------------------------------------------------
// Image tag column detection and backward compatibility
// ---------------------------------------------------------------------------

// TestClickHouseStore_DetectImageTagColumn verifies detectImageTagColumn for
// column-exists, column-absent, nil-row, and scan-error scenarios.
func TestClickHouseStore_DetectImageTagColumn(t *testing.T) {
	t.Run("column exists", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
				if strings.Contains(query, "system.columns") && strings.Contains(query, "image_tag") {
					return &clickhousetest.MockRow{
						ScanFunc: func(dest ...any) error {
							if v, ok := dest[0].(*uint64); ok {
								*v = 1
							}
							return nil
						},
					}
				}
				return &clickhousetest.MockRow{}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		store.hasImageTagColumn.Store(false)

		store.detectImageTagColumn(context.Background())

		if !store.hasImageTagColumn.Load() {
			t.Error("expected hasImageTagColumn=true when count=1")
		}
	})

	t.Run("column missing", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
				if strings.Contains(query, "system.columns") && strings.Contains(query, "image_tag") {
					return &clickhousetest.MockRow{
						ScanFunc: func(dest ...any) error {
							if v, ok := dest[0].(*uint64); ok {
								*v = 0
							}
							return nil
						},
					}
				}
				return &clickhousetest.MockRow{}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

		store.detectImageTagColumn(context.Background())

		if store.hasImageTagColumn.Load() {
			t.Error("expected hasImageTagColumn=false when count=0")
		}
	})

	t.Run("query error — conservative default", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			QueryRowFunc: func(_ context.Context, query string, _ ...any) driver.Row {
				if strings.Contains(query, "system.columns") && strings.Contains(query, "image_tag") {
					return &clickhousetest.MockRow{
						ScanFunc: func(_ ...any) error {
							return errors.New("scan failed")
						},
					}
				}
				return &clickhousetest.MockRow{}
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		store.hasImageTagColumn.Store(false)

		store.detectImageTagColumn(context.Background())

		if !store.hasImageTagColumn.Load() {
			t.Error("expected hasImageTagColumn=true (conservative) on scan error")
		}
	})
}

// TestClickHouseStore_InsertComponentState_NoImageTagColumn verifies that when
// hasImageTagColumn is false the INSERT query omits the image_tag column.
func TestClickHouseStore_InsertComponentState_NoImageTagColumn(t *testing.T) {
	var capturedQuery string
	var capturedArgCount int

	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(_ context.Context, stmt string, args ...any) error {
			capturedQuery = stmt
			capturedArgCount = len(args)
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	store.hasImageTagColumn.Store(false)

	err := store.insertComponentState(
		context.Background(), "corr-1", "build", "svc-a", StepStatusRunning, "", "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(capturedQuery, "image_tag") {
		t.Error("INSERT query should NOT include image_tag when hasImageTagColumn=false")
	}
	// 6 args: correlation_id, step, component, status, message, timestamp
	if capturedArgCount != 6 {
		t.Errorf("expected 6 args (no image_tag), got %d", capturedArgCount)
	}
}

// TestClickHouseStore_InsertComponentState_ImageTagColumnSelfHealing verifies that when
// hasImageTagColumn is true but INSERT fails with a missing-column error, the store
// flips hasImageTagColumn to false and retries without the image_tag column for status-only
// events (imageTag=""), but returns an error for explicit image tag events to avoid silent data loss.
func TestClickHouseStore_InsertComponentState_ImageTagColumnSelfHealing(t *testing.T) {
	t.Run("status-only event retries without image_tag", func(t *testing.T) {
		var mu sync.Mutex
		execCall := 0
		var capturedStmts []string

		mockSession := &clickhousetest.MockSession{
			ExecWithArgsFunc: func(_ context.Context, stmt string, _ ...any) error {
				mu.Lock()
				execCall++
				call := execCall
				capturedStmts = append(capturedStmts, stmt)
				mu.Unlock()

				if call == 1 {
					return fmt.Errorf("Missing columns: 'image_tag' while processing query")
				}
				return nil
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		store.hasImageTagColumn.Store(true)

		err := store.insertComponentState(
			context.Background(), "corr-heal", "build", "svc-a", StepStatusRunning, "", "",
		)
		if err != nil {
			t.Fatalf("expected self-healing retry to succeed, got %v", err)
		}

		mu.Lock()
		defer mu.Unlock()

		if len(capturedStmts) != 2 {
			t.Fatalf("expected 2 ExecWithArgs calls, got %d", len(capturedStmts))
		}

		if !strings.Contains(capturedStmts[0], "image_tag") {
			t.Error("first INSERT should include image_tag")
		}
		if strings.Contains(capturedStmts[1], "image_tag") {
			t.Error("retry INSERT should NOT include image_tag")
		}

		if store.hasImageTagColumn.Load() {
			t.Error("expected hasImageTagColumn=false after self-healing")
		}
	})

	t.Run("explicit image tag returns error instead of dropping", func(t *testing.T) {
		mockSession := &clickhousetest.MockSession{
			ExecWithArgsFunc: func(_ context.Context, _ string, _ ...any) error {
				return fmt.Errorf("Missing columns: 'image_tag' while processing query")
			},
		}
		store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
		store.hasImageTagColumn.Store(true)

		err := store.insertComponentState(
			context.Background(), "corr-heal", "build", "svc-a", StepStatusRunning, "", "v1.0.0",
		)
		if err == nil {
			t.Fatal("expected error when image_tag is non-empty and column is missing")
		}
		if !strings.Contains(err.Error(), "migration v11 not yet applied") {
			t.Errorf("expected migration-not-applied error, got: %v", err)
		}
		if store.hasImageTagColumn.Load() {
			t.Error("expected hasImageTagColumn=false after detection")
		}
	})
}

// TestClickHouseStore_SetComponentImageTag_FallbackToOriginalStepName verifies that when
// the normalized step name query returns empty, the original step name is tried as fallback.
func TestClickHouseStore_SetComponentImageTag_FallbackToOriginalStepName(t *testing.T) {
	queryRowCallCount := 0

	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(_ context.Context, query string, args ...any) driver.Row {
			if strings.Contains(query, "argMax(status") {
				queryRowCallCount++
				call := queryRowCallCount
				return &clickhousetest.MockRow{
					ScanFunc: func(dest ...any) error {
						if ptr, ok := dest[0].(*string); ok {
							if call == 1 {
								// First call (normalized name "build") returns empty — component not found.
								*ptr = ""
							} else {
								// Second call (original name "builds") returns a real status.
								*ptr = "completed"
							}
						}
						return nil
					},
				}
			}
			return &clickhousetest.MockRow{}
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	// "builds" is the aggregate step; it normalizes to "build" (the component step).
	err := store.SetComponentImageTag(
		context.Background(), "corr-fallback", "builds", "svc-a", "v2.0.0",
	)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}

	if queryRowCallCount != 2 {
		t.Errorf("expected 2 QueryRow calls (normalized + original fallback), got %d", queryRowCallCount)
	}
}

// TestClickHouseStore_SetComponentImageTag_NoImageTagColumn verifies that when
// hasImageTagColumn is false, SetComponentImageTag returns a clear error message.
func TestClickHouseStore_SetComponentImageTag_NoImageTagColumn(t *testing.T) {
	mockSession := &clickhousetest.MockSession{}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	store.hasImageTagColumn.Store(false)

	err := store.SetComponentImageTag(
		context.Background(), "corr-no-col", "build", "svc-a", "v1.0.0",
	)
	if err == nil {
		t.Fatal("expected error when hasImageTagColumn=false, got nil")
	}
	if !strings.Contains(err.Error(), "image_tag column not available") {
		t.Errorf("expected 'image_tag column not available' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// loadComponentStates — image_tag column self-healing
// ---------------------------------------------------------------------------

// TestClickHouseStore_LoadComponentStates_WithImageTag verifies that loadComponentStates
// includes the image_tag column when hasImageTagColumn is true.
func TestClickHouseStore_LoadComponentStates_WithImageTag(t *testing.T) {
	var capturedQuery string
	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
			capturedQuery = query
			return &clickhousetest.MockRows{}, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	store.hasImageTagColumn.Store(true)

	_, _ = store.loadComponentStates(context.Background(), "corr-img-tag")

	if !strings.Contains(capturedQuery, "image_tag") {
		t.Error("expected query to include image_tag when hasImageTagColumn=true")
	}
}

// TestClickHouseStore_LoadComponentStates_WithoutImageTag verifies that loadComponentStates
// omits the image_tag column when hasImageTagColumn is false.
func TestClickHouseStore_LoadComponentStates_WithoutImageTag(t *testing.T) {
	var capturedQuery string
	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
			capturedQuery = query
			return &clickhousetest.MockRows{}, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	store.hasImageTagColumn.Store(false)

	_, _ = store.loadComponentStates(context.Background(), "corr-no-img-tag")

	if strings.Contains(capturedQuery, "image_tag") {
		t.Error("expected query to omit image_tag when hasImageTagColumn=false")
	}
}

// TestClickHouseStore_LoadComponentStates_SelfHealOnImageTagError verifies that when
// loadComponentStates fails because ClickHouse doesn't recognise image_tag, it sets
// hasImageTagColumn=false and retries without the column.
func TestClickHouseStore_LoadComponentStates_SelfHealOnImageTagError(t *testing.T) {
	var mu sync.Mutex
	queryCallCount := 0
	var capturedQueries []string

	mockSession := &clickhousetest.MockSession{
		QueryWithArgsFunc: func(_ context.Context, query string, _ ...any) (driver.Rows, error) {
			mu.Lock()
			queryCallCount++
			call := queryCallCount
			capturedQueries = append(capturedQueries, query)
			mu.Unlock()

			if call == 1 {
				// First call: simulate image_tag column error
				return nil, fmt.Errorf("Missing columns: 'image_tag' while processing query")
			}
			// Second call: succeed with empty result
			return &clickhousetest.MockRows{}, nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	store.hasImageTagColumn.Store(true)

	results, err := store.loadComponentStates(context.Background(), "corr-self-heal-load")
	if err != nil {
		t.Fatalf("expected no error after self-healing, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	mu.Lock()
	defer mu.Unlock()

	if queryCallCount != 2 {
		t.Fatalf("expected 2 QueryWithArgs calls (initial + retry), got %d", queryCallCount)
	}
	// First query should include image_tag.
	if !strings.Contains(capturedQueries[0], "image_tag") {
		t.Error("expected first query to include image_tag")
	}
	// Second query (retry) should NOT include image_tag.
	if strings.Contains(capturedQueries[1], "image_tag") {
		t.Error("expected second query to omit image_tag")
	}
	// hasImageTagColumn should now be false.
	if store.hasImageTagColumn.Load() {
		t.Error("expected hasImageTagColumn=false after self-healing")
	}
}
