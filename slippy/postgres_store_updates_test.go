package slippy

import (
	"context"
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

func TestPgComputeAggregateStatus(t *testing.T) {
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
			assert.Equal(t, tt.want, pgComputeAggregateStatus(tt.comps))
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
		WithArgs("c1", "unit_tests", "", "running", "", "").
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
		WithArgs("c1", "unit_tests", "", "running", "", "").
		WillReturnResult(pgxmock.NewResult("INSERT", 0)) // guard blocked
	mock.ExpectRollback()

	err := store.UpdateStep(context.Background(), "c1", "unit_tests", "", StepStatusRunning)
	require.ErrorIs(t, err, ErrTerminalAlreadyExists)
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
		WithArgs("c1", "component_builds", "api", "running", "", "").
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

func TestPostgresStore_UpdateStepWithHistory_Pipeline(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	expectLock(mock, "c1")
	mock.ExpectExec("INSERT INTO slip_component_states").
		WithArgs("c1", "unit_tests", "", "completed", "all green", "").
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
