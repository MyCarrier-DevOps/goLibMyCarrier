package slippy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectLock queues the FOR UPDATE existence check returning the slip.
func expectLock(mock pgxmock.PgxPoolIface, id string) {
	mock.ExpectQuery("SELECT correlation_id FROM routing_slips").
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"correlation_id"}).AddRow(id))
}

func TestComputeAggregateStatus(t *testing.T) {
	run := func(status StepStatus) ComponentStepData { return ComponentStepData{Status: status} }
	tests := []struct {
		name  string
		comps []ComponentStepData
		want  StepStatus
	}{
		{"empty->completed", nil, StepStatusCompleted},
		{"all completed", []ComponentStepData{run(StepStatusCompleted), run(StepStatusSkipped)}, StepStatusCompleted},
		{"any failed", []ComponentStepData{run(StepStatusCompleted), run(StepStatusFailed)}, StepStatusFailed},
		{"any running", []ComponentStepData{run(StepStatusRunning), run(StepStatusPending)}, StepStatusRunning},
		{"completed+pending", []ComponentStepData{run(StepStatusCompleted), run(StepStatusPending)}, StepStatusRunning},
		{"all pending", []ComponentStepData{run(StepStatusPending)}, StepStatusPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, computeAggregateStatus(tt.comps))
		})
	}
}

func TestPostgresStore_UpdateSlipStatus(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectExec("UPDATE routing_slips SET status").
		WithArgs("completed", "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	require.NoError(t, store.UpdateSlipStatus(context.Background(), "c1", SlipStatusCompleted))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateSlipStatus_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectExec("UPDATE routing_slips SET status").
		WithArgs("completed", "nope").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	require.ErrorIs(t, store.UpdateSlipStatus(context.Background(), "nope", SlipStatusCompleted), ErrSlipNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_PipelineStep(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "unit_tests", "", "running", "", "", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE routing_slips SET unit_tests_status").
		WithArgs("running", "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.UpdateStep(context.Background(), "c1", "unit_tests", "", StepStatusRunning))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_TerminalGuardBlocks(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "unit_tests", "", "running", "", "", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 0)) // guard blocked
	mock.ExpectRollback()

	err := store.UpdateStep(context.Background(), "c1", "unit_tests", "", StepStatusRunning)
	require.ErrorIs(t, err, ErrTerminalAlreadyExists)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_PushParsedBypassesGate(t *testing.T) {
	store, mock := newMockStore(t)
	// push_parsed is a gate-bypass step: its pipeline-level (component="") write must pass
	// guarded=false (the 8th upsert arg) so a legitimate push-webhook retry can reset a
	// terminal push_parsed back to running regardless of the freshness window.
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "push_parsed", "", "running", "", "", pgxmock.AnyArg(), false).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE routing_slips SET push_parsed_status").
		WithArgs("running", "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.UpdateStep(context.Background(), "c1", "push_parsed", "", StepStatusRunning))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_NonBypassStepIsGuarded(t *testing.T) {
	store, mock := newMockStore(t)
	// A normal step's write passes guarded=true (8th arg), so the terminal-freshness guard
	// in the upsert WHERE is active for it.
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "unit_tests", "", "running", "", "", pgxmock.AnyArg(), true).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE routing_slips SET unit_tests_status").
		WithArgs("running", "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.UpdateStep(context.Background(), "c1", "unit_tests", "", StepStatusRunning))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT correlation_id FROM routing_slips").
		WithArgs("ghost").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	require.ErrorIs(
		t,
		store.UpdateStep(context.Background(), "ghost", "unit_tests", "", StepStatusRunning),
		ErrSlipNotFound,
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_ComponentAggregate(t *testing.T) {
	store, mock := newMockStore(t)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "component_builds", "api", "running", "", "", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// recompute: read current items, read component rows, write back.
	mock.ExpectQuery("SELECT builds FROM routing_slips").
		WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{"builds"}).AddRow([]byte(`{"items":[]}`)))
	mock.ExpectQuery("FROM slip_component_states").
		WithArgs("c1", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"component", "status", "message", "image_tag", "updated_at"}).
			AddRow("api", "running", "", "", ts))
	mock.ExpectExec("UPDATE routing_slips SET builds_status").
		WithArgs("running", pgxmock.AnyArg(), "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(
		t,
		store.UpdateComponentStatus(context.Background(), "c1", "api", "component_builds", StepStatusRunning),
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_InjectionSafe_UnknownStepSkipsColumn(t *testing.T) {
	store, mock := newMockStore(t)
	// A crafted / unknown pipeline step name must never be spliced into a column identifier.
	// The component-state event is still recorded, but no routing_slips column is written
	// (matching ClickHouse, which materializes only config-known columns). If the guard
	// regressed, the store would issue an UPDATE that pgxmock has no expectation for and the
	// test would fail.
	const evil = "unit_tests_status = 'skipped', builds_status"
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", evil, "", "running", "", "", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	// Deliberately no "UPDATE routing_slips SET ..." expectation.
	mock.ExpectCommit()

	require.NoError(t, store.UpdateStep(context.Background(), "c1", evil, "", StepStatusRunning))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStep_AggregateNoComponents_NotWritten(t *testing.T) {
	store, mock := newMockStore(t)
	// A pipeline-level StartStep on an aggregate step before any component reports leaves the
	// active component set empty; the aggregate must be left untouched rather than resolving
	// to a vacuous "completed". If the guard regressed, an UPDATE ... SET builds_status would
	// be issued and fail against the unexpected expectation below.
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "builds", "", "running", "", "", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT builds FROM routing_slips").
		WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{"builds"}).AddRow([]byte(`{"items":[]}`)))
	mock.ExpectQuery("FROM slip_component_states").
		WithArgs("c1", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"component", "status", "message", "image_tag", "updated_at"}))
	// Deliberately no "UPDATE routing_slips SET builds_status": empty active set.
	mock.ExpectCommit()

	require.NoError(t, store.UpdateStep(context.Background(), "c1", "builds", "", StepStatusRunning))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_UpdateStepWithHistory_Pipeline(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "unit_tests", "", "completed", "all green", "", pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE routing_slips SET unit_tests_status").
		WithArgs("completed", "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE routing_slips SET").
		WithArgs(pgxmock.AnyArg(), "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1)) // state_history append
	mock.ExpectCommit()

	entry := StateHistoryEntry{Step: "unit_tests", Status: StepStatusCompleted, Message: "all green"}
	require.NoError(
		t,
		store.UpdateStepWithHistory(context.Background(), "c1", "unit_tests", "", StepStatusCompleted, entry),
	)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_AppendHistory(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("UPDATE routing_slips SET").
		WithArgs(pgxmock.AnyArg(), "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.AppendHistory(context.Background(), "c1",
		StateHistoryEntry{Step: "builds", Status: StepStatusRunning}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_AppendHistory_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT correlation_id FROM routing_slips").
		WithArgs("ghost").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	require.ErrorIs(t, store.AppendHistory(context.Background(), "ghost",
		StateHistoryEntry{Step: "builds"}), ErrSlipNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_SetComponentImageTag(t *testing.T) {
	store, mock := newMockStore(t)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	// stepName "builds" is an aggregate -> normalized to component step "component_builds".
	mock.ExpectExec("UPDATE slip_component_states SET image_tag").
		WithArgs("tag123", "c1", "component_builds", "api").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SELECT builds FROM routing_slips").
		WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{"builds"}).AddRow([]byte(`{"items":[]}`)))
	mock.ExpectQuery("FROM slip_component_states").
		WithArgs("c1", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"component", "status", "message", "image_tag", "updated_at"}).
			AddRow("api", "completed", "", "tag123", ts))
	mock.ExpectExec("UPDATE routing_slips SET builds_status").
		WithArgs("completed", pgxmock.AnyArg(), "c1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.SetComponentImageTag(context.Background(), "c1", "builds", "api", "tag123"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_SetComponentImageTag_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	// stepName "component_builds" is not an aggregate step: no normalization, single UPDATE.
	mock.ExpectExec("UPDATE slip_component_states SET image_tag").
		WithArgs("tag123", "c1", "component_builds", "api").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := store.SetComponentImageTag(context.Background(), "c1", "component_builds", "api", "tag123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_DeleteSlip_EndedSlip_RepointsSuccessor pins the happy repave path: an
// ended slip (status IN the ended set) is deleted FIRST, and only because that guarded
// delete actually removed the row are descendants pointing at it repointed to the
// successor (clearing their stale parent_failed_step in the same UPDATE, D2.2) and this
// slip's own children cleaned up (D2.1: nothing here runs before the delete confirms it
// happened).
func TestPostgresStore_DeleteSlip_EndedSlip_RepointsSuccessor(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("UPDATE slip_ancestry SET parent_correlation_id").
		WithArgs("new-id", "old-id").
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.DeleteSlip(context.Background(), "old-id", "new-id"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_DeleteSlip_EndedSlip_RepointsSuccessor_ClearsFailedStep pins D2.2: the
// repoint UPDATE also clears parent_failed_step, since the deleted run's failed step is
// unambiguously wrong once the id beside it names the successor run instead.
func TestPostgresStore_DeleteSlip_EndedSlip_RepointsSuccessor_ClearsFailedStep(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`UPDATE slip_ancestry SET parent_correlation_id = \$1, parent_failed_step = '' `+
		`WHERE parent_correlation_id = \$2`).
		WithArgs("new-id", "old-id").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.DeleteSlip(context.Background(), "old-id", "new-id"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_DeleteSlip_EndedSlip_NoSuccessor_ClearsDescendants covers the "no
// successor" branch: with successorCorrelationID == "", descendant links are deleted
// rather than repointed (there is nothing to point them at) — and, per D2.1, only after
// the guarded delete confirms the row was actually removed.
func TestPostgresStore_DeleteSlip_EndedSlip_NoSuccessor_ClearsDescendants(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE parent_correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.DeleteSlip(context.Background(), "old-id", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_DeleteSlip_LiveSlip_RejectedWithErrSlipWentLive pins the TOCTOU guard:
// when the row still exists but its status is no longer ended (it recovered to live
// between the caller's repave decision and this call), the delete must be rejected with
// ErrSlipWentLive and the transaction rolled back — no children touched, no descendants
// repointed. Per D2.1/D2.2, the descendant statement is never even issued in this path:
// it sits after the guarded delete, which never reports RowsAffected() > 0 here.
func TestPostgresStore_DeleteSlip_LiveSlip_RejectedWithErrSlipWentLive(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 0)) // guard rejected: status no longer ended
	mock.ExpectQuery(`SELECT correlation_id FROM routing_slips WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnRows(pgxmock.NewRows([]string{"correlation_id"}).AddRow("old-id"))
	mock.ExpectRollback()

	err := store.DeleteSlip(context.Background(), "old-id", "")
	require.ErrorIs(t, err, ErrSlipWentLive)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_DeleteSlip_MissingSlip_ReturnsNil pins the pre-existing idempotent
// case: deleting a slip that does not exist at all is not an error. Critically (D2.1,
// DEVOPS-231 review), this must be a TRUE no-op: no descendant repoint/clear statement
// may be issued at all, since that would commit a real mutation (erasing or repointing
// other slips' ancestry links) under the guise of "nothing happened". pgxmock is in
// strict ordered mode, so if the code issues the descendant statement before the guarded
// delete confirms a row was actually removed, the first unexpected call below fails and
// DeleteSlip surfaces that as an error instead of returning nil.
func TestPostgresStore_DeleteSlip_MissingSlip_ReturnsNil(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("ghost").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectQuery(`SELECT correlation_id FROM routing_slips WHERE correlation_id = \$1`).
		WithArgs("ghost").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	require.NoError(t, store.DeleteSlip(context.Background(), "ghost", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_DeleteSlip_StatusGuard_CoversEndedStatuses pins the exact ended-status
// set the guard allows: failed, completed, abandoned, promoted, compensated.
func TestPostgresStore_DeleteSlip_StatusGuard_CoversEndedStatuses(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(
		`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN ` +
			`\('failed','completed','abandoned','promoted','compensated'\)`,
	).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE parent_correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.DeleteSlip(context.Background(), "old-id", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

// parseSQLStringSet parses a SQL "IN (...)" literal list of single-quoted strings (e.g.
// deletableSlipStatusesSQL's "'failed','completed'") into a set of the unquoted values.
// Used only by TestDeletableSlipStatusesSQL_MatchesIsLive so that test compares against
// the real deletableSlipStatusesSQL constant's actual contents, not a second hardcoded
// copy of the status list that could itself drift from the SQL.
func parseSQLStringSet(sqlSet string) map[string]bool {
	out := make(map[string]bool)
	for _, part := range strings.Split(sqlSet, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "'")
		out[part] = true
	}
	return out
}

// TestDeletableSlipStatusesSQL_MatchesIsLive asserts set equality between
// deletableSlipStatusesSQL (DeleteSlip's guard, expressed in SQL) and SlipStatus.IsLive
// (the repave decision predicate, expressed in Go) across every SlipStatus value —
// enumerated explicitly, the same exhaustiveness style TestSlipStatus_IsLive already
// uses, so a future ninth status forces a decision here too instead of silently drifting
// (D2.3, DEVOPS-231 review): if IsLive() ever disagrees with this SQL constant,
// CreateSlipForPush routes a push to repave but DeleteSlip's guard rejects the delete,
// returning ErrSlipWentLive for a slip that never actually went live — permanently
// wedging that commit's pushes onto the old, ended slip with no error surfaced anywhere.
func TestDeletableSlipStatusesSQL_MatchesIsLive(t *testing.T) {
	// Every SlipStatus value that exists today. Adding a ninth to the enum without adding
	// it here (and to the assertions below) leaves it silently unchecked by this test —
	// matching the enumeration style of TestSlipStatus_IsLive.
	allStatuses := []SlipStatus{
		SlipStatusPending,
		SlipStatusInProgress,
		SlipStatusCompleted,
		SlipStatusFailed,
		SlipStatusCompensating,
		SlipStatusCompensated,
		SlipStatusAbandoned,
		SlipStatusPromoted,
	}

	sqlSet := parseSQLStringSet(deletableSlipStatusesSQL)

	wantSet := make(map[string]bool, len(allStatuses))
	for _, status := range allStatuses {
		if !status.IsLive() {
			wantSet[string(status)] = true
		}
	}

	assert.Equal(t, wantSet, sqlSet,
		"deletableSlipStatusesSQL must contain exactly the SlipStatus values where IsLive() is false")

	for _, status := range allStatuses {
		t.Run(string(status), func(t *testing.T) {
			assert.Equal(t, !status.IsLive(), sqlSet[string(status)],
				"deletableSlipStatusesSQL and SlipStatus(%q).IsLive() disagree", status)
		})
	}
}
