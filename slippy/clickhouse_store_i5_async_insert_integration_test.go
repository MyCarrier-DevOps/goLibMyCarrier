//go:build integration

// This file covers the I5 race fix end-to-end against a real ClickHouse container
// configured with the same async-insert profile used by production CH Cloud:
//
//	async_insert            = 1
//	wait_for_async_insert   = 1
//	async_insert_busy_timeout_ms = 10000
//
// These settings reproduce the production read-your-writes window where an INSERT
// into slip_component_states is acknowledged before being visible to subsequent
// SELECTs on the same connection pool. The Option D fix (caller-supplied
// StepStatusOverride on Update) eliminates the hazard by side-stepping the
// SELECT-then-INSERT cycle for the step being written.
//
// The negative control (without override) asserts the bug WOULD manifest if
// Option D were disabled — proving the override is what makes the positive case
// pass, not the test framework.

package slippy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
)

// setupClickHouseContainerWithAsyncInsert starts a ClickHouse container and
// connects with the production async-insert profile enabled.
func setupClickHouseContainerWithAsyncInsert(
	ctx context.Context, t *testing.T,
) (*clickhouseContainer, clickhouse.Conn, error) {
	t.Helper()
	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	req := testcontainers.ContainerRequest{
		Image:        "clickhouse/clickhouse-server:25.8",
		ExposedPorts: []string{"9000/tcp", "8123/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("9000/tcp"),
			wait.ForHTTP("/ping").WithPort("8123/tcp").WithStatusCodeMatcher(func(status int) bool {
				return status == 200
			}),
		).WithDeadline(120 * time.Second),
		Env: map[string]string{
			"CLICKHOUSE_USER":                      "default",
			"CLICKHOUSE_PASSWORD":                  "",
			"CLICKHOUSE_DB":                        "default",
			"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start clickhouse container: %w", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("host: %w", err)
	}
	mapped, err := container.MappedPort(ctx, "9000")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("port: %w", err)
	}
	time.Sleep(2 * time.Second)

	// Production async-insert profile applied at the connection level via Settings.
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%s", host, mapped.Port())},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
			Password: "",
		},
		DialTimeout:     10 * time.Second,
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Hour,
		Settings: clickhouse.Settings{
			"async_insert":                 1,
			"wait_for_async_insert":        1,
			"async_insert_busy_timeout_ms": 10000,
		},
	})
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	var pingErr error
	for i := 0; i < 5; i++ {
		if pingErr = conn.Ping(ctx); pingErr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if pingErr != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("ping after retries: %w", pingErr)
	}
	if err := conn.Exec(ctx, "SET allow_experimental_json_type = 1"); err != nil {
		_ = container.Terminate(ctx)
		return nil, nil, fmt.Errorf("set json type: %w", err)
	}

	return &clickhouseContainer{
		container: container,
		host:      host,
		port:      mapped.Port(),
	}, conn, nil
}

// makeStoreFromConn wires a ClickHouseStore atop an already-connected
// clickhouse.Conn with a specified database name. It runs migrations.
func makeStoreFromConn(
	ctx context.Context, t *testing.T, conn clickhouse.Conn,
	dbName string, pipelineConfig *PipelineConfig,
) (*ClickHouseStore, error) {
	t.Helper()
	session := &testClickhouseSession{conn: conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, dbName)
	if _, err := RunMigrations(ctx, conn, MigrateOptions{
		Database:       dbName,
		PipelineConfig: pipelineConfig,
	}); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

// TestI5_OptionD_OverridePinsTerminalStatus_AsyncInsert exercises the I5 race
// fix end-to-end with the production async-insert profile.
//
// Scenario (positive case — override on):
//  1. Create a slip.
//  2. Insert a terminal `completed` event for `unit_tests` via insertComponentState.
//  3. Build an in-memory slip whose Steps[unit_tests].Status = "running" (stale).
//  4. Call Update with StepStatusOverride{unit_tests_status, completed}.
//  5. Re-read raw routing_slips columns + step_details JSON. Both MUST be
//     "completed" — the override is what makes that true.
//
// Negative control (override off):
//  6. Repeat steps 1–3 with a fresh slip.
//  7. Call Update WITHOUT override.
//  8. Assert routing_slips.unit_tests_status reflects the stale "running" — this
//     is the bug Option D fixes; presence proves the override is load-bearing.
func TestI5_OptionD_OverridePinsTerminalStatus_AsyncInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping I5 async-insert integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	container, conn, err := setupClickHouseContainerWithAsyncInsert(ctx, t)
	if err != nil {
		t.Fatalf("container setup: %v", err)
	}
	defer func() {
		_ = conn.Close()
		_ = container.terminate(ctx)
	}()

	pipelineConfig := integrationTestPipelineConfig()
	const dbName = "ci_i5_async"

	store, err := makeStoreFromConn(ctx, t, conn, dbName, pipelineConfig)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	// =========================================================================
	// Positive case: override pins event-log truth.
	// =========================================================================
	t.Run("positive_override_pins_terminal", func(t *testing.T) {
		corrID := fmt.Sprintf("i5-pos-%d", time.Now().UnixNano())

		// 1. Create slip.
		slip := &Slip{
			CorrelationID: corrID,
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     fmt.Sprintf("sha-%d", time.Now().UnixNano()),
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusRunning},
			},
		}
		if err := store.Create(ctx, slip); err != nil {
			t.Fatalf("create: %v", err)
		}

		// 2. Insert terminal completed event into the event log (pipeline-level).
		if err := store.insertComponentState(
			ctx, corrID, "unit_tests_completed", "", StepStatusCompleted, "", "",
		); err != nil {
			t.Fatalf("insertComponentState: %v", err)
		}

		// Sanity — confirm LatestStepStatusFromEvents observes the terminal status.
		// With wait_for_async_insert=1 the row is durable + visible on subsequent SELECTs.
		latest, found, err := store.LatestStepStatusFromEvents(ctx, corrID, "unit_tests_completed")
		if err != nil {
			t.Fatalf("LatestStepStatusFromEvents: %v", err)
		}
		if !found {
			t.Fatal("expected event row to be visible after wait_for_async_insert=1 INSERT")
		}
		if latest != StepStatusCompleted {
			t.Errorf("expected event-log status=completed, got %q", latest)
		}

		// 3. Build the stale snapshot — Steps says running.
		staleSlip := &Slip{
			CorrelationID: corrID,
			Repository:    slip.Repository,
			Branch:        slip.Branch,
			CommitSHA:     slip.CommitSHA,
			Status:        SlipStatusInProgress,
			CreatedAt:     slip.CreatedAt,
			UpdatedAt:     time.Now(),
			Version:       slip.Version,
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusRunning},
			},
		}

		// 4. Update WITH override pinning the column and JSON status to completed.
		if err := store.Update(ctx, staleSlip, StepStatusOverride{
			ColumnName: "unit_tests_completed_status",
			Status:     StepStatusCompleted,
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}

		// 5. Re-read raw column + JSON from routing_slips.
		colStatus, jsonStatus := readUnitTestsStatusFromRouting(t, ctx, conn, dbName, corrID)
		if colStatus != string(StepStatusCompleted) {
			t.Errorf(
				"routing_slips.unit_tests_completed_status: expected %q (from override), got %q — Option D regressed",
				StepStatusCompleted, colStatus,
			)
		}
		if jsonStatus != string(StepStatusCompleted) {
			t.Errorf(
				"step_details.unit_tests_completed.status: expected %q (JSON parity), got %q",
				StepStatusCompleted, jsonStatus,
			)
		}
	})

	// =========================================================================
	// Negative control: zero overrides → stale-snapshot wins → bug visible.
	// =========================================================================
	t.Run("negative_no_override_reproduces_bug", func(t *testing.T) {
		corrID := fmt.Sprintf("i5-neg-%d", time.Now().UnixNano())

		slip := &Slip{
			CorrelationID: corrID,
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     fmt.Sprintf("sha-%d", time.Now().UnixNano()),
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusRunning},
			},
		}
		if err := store.Create(ctx, slip); err != nil {
			t.Fatalf("create: %v", err)
		}

		// Write the terminal event log row.
		if err := store.insertComponentState(
			ctx, corrID, "unit_tests_completed", "", StepStatusCompleted, "", "",
		); err != nil {
			t.Fatalf("insertComponentState: %v", err)
		}

		// Build a stale snapshot — Steps says running (the bug pre-condition).
		staleSlip := &Slip{
			CorrelationID: corrID,
			Repository:    slip.Repository,
			Branch:        slip.Branch,
			CommitSHA:     slip.CommitSHA,
			Status:        SlipStatusInProgress,
			CreatedAt:     slip.CreatedAt,
			UpdatedAt:     time.Now(),
			Version:       slip.Version,
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusRunning},
			},
		}

		// Update WITHOUT override — falls back to slip.Steps[].Status = running.
		if err := store.Update(ctx, staleSlip); err != nil {
			t.Fatalf("Update: %v", err)
		}

		colStatus, _ := readUnitTestsStatusFromRouting(t, ctx, conn, dbName, corrID)
		if colStatus != string(StepStatusRunning) {
			// The bug DIDN'T manifest — that means the test framework is masking
			// the read-your-writes window. HARD FAIL (per prior reviewer Mod):
			// the positive case's signal is meaningless without this negative
			// control reproducing the bug. A silent t.Logf here lets the
			// async-insert window collapse on CI runners without anyone
			// noticing that the positive case stopped proving anything.
			t.Fatalf(
				"negative control did NOT reproduce the I5 bug — routing_slips.unit_tests_completed_status=%q (expected %q). The async-insert window is not wide enough on this runner; the positive case can no longer be trusted to prove the fix. Investigate test harness before re-enabling the positive case.",
				colStatus, StepStatusRunning,
			)
		}
	})
}

// readUnitTestsStatusFromRouting reads routing_slips.<unit_tests_completed_status>
// and step_details.unit_tests_completed.status for the latest sign=+1 row.
func readUnitTestsStatusFromRouting(
	t *testing.T, ctx context.Context, conn clickhouse.Conn,
	dbName, correlationID string,
) (colStatus string, jsonStatus string) {
	t.Helper()
	query := fmt.Sprintf(`
		SELECT unit_tests_completed_status, toString(step_details) AS sd
		FROM %s.routing_slips
		WHERE correlation_id = ? AND sign = 1
		ORDER BY version DESC
		LIMIT 1
	`, dbName)
	row := conn.QueryRow(ctx, query, correlationID)
	var sdRaw string
	if err := row.Scan(&colStatus, &sdRaw); err != nil {
		t.Fatalf("scan routing_slips: %v", err)
	}
	var sd map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(sdRaw), &sd); err != nil {
		t.Fatalf("unmarshal step_details %q: %v", sdRaw, err)
	}
	if ut, ok := sd["unit_tests_completed"]; ok {
		if v, ok := ut["status"]; ok {
			if s, ok := v.(string); ok {
				jsonStatus = s
			}
		}
	}
	return colStatus, jsonStatus
}

// Compile-time guard — ch package is referenced via the testClickhouseSession
// definition in clickhouse_store_integration_test.go; this assert keeps lints
// happy if the wrapper is otherwise unused in this file.
var _ = (ch.ClickhouseSessionInterface)(&testClickhouseSession{})
