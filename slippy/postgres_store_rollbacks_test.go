package slippy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rollbackInsertArgs is the bind list RecordRollback must send for validRollbackRecord(), in
// column order; the status goes out as plain text.
func rollbackInsertArgs(r RollbackRecord) []any {
	return []any{
		r.CorrelationID, r.Workflow, r.Environment, r.Reason, r.Source, r.RolloutPhase, r.PodHash,
		r.GitOpsRepo, r.GitOpsCommit, r.RevertCommit, string(r.Status), r.Forced, r.Message, r.Outcome,
	}
}

// insertedRow is the single-column result RecordRollback scans: RETURNING (xmax = 0).
func insertedRow(inserted bool) *pgxmock.Rows {
	return pgxmock.NewRows([]string{"inserted"}).AddRow(inserted)
}

func TestPostgresStore_RecordRollback_Inserted(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	mock.ExpectQuery("INSERT INTO slip_rollbacks").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnRows(insertedRow(true))

	inserted, err := store.RecordRollback(context.Background(), rec)
	require.NoError(t, err)
	assert.True(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The key is (correlation_id, workflow): a recycled generateName workflow name on another
// slip is a new row, not a conflict.
func TestPostgresStore_RecordRollback_ConflictsOnTheSlipAndWorkflow(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	mock.ExpectQuery("ON CONFLICT \\(correlation_id, workflow\\) DO UPDATE").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnRows(insertedRow(true))

	_, err := store.RecordRollback(context.Background(), rec)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An argo retry re-runs the exit handler under the same Workflow name. The retry's outcome
// replaces a failed record only; the update reports inserted=false.
func TestPostgresStore_RecordRollback_ARetryReplacesAFailedRecord(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	mock.ExpectQuery("WHERE slip_rollbacks.status = 'failed'\\s+RETURNING \\(xmax = 0\\)").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnRows(insertedRow(false))

	inserted, err := store.RecordRollback(context.Background(), rec)
	require.NoError(t, err)
	assert.False(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Over a record that did not fail, the guarded update matches nothing and RETURNING yields no
// row: that is a repeat, not an error.
func TestPostgresStore_RecordRollback_ARepeatOverAnyOtherRecordWritesNothing(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	mock.ExpectQuery("INSERT INTO slip_rollbacks").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnRows(pgxmock.NewRows([]string{"inserted"}))

	inserted, err := store.RecordRollback(context.Background(), rec)
	require.NoError(t, err, "a repeat of the same workflow is a success, not an error")
	assert.False(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_RecordRollback_UnknownSlip(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	mock.ExpectQuery("INSERT INTO slip_rollbacks").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnError(&pgconn.PgError{Code: "23503", ConstraintName: "fk_slip_rollbacks_slip"})

	_, err := store.RecordRollback(context.Background(), rec)
	require.ErrorIs(t, err, ErrSlipNotFound)
	assert.Contains(t, err.Error(), rec.CorrelationID)
}

func TestPostgresStore_RecordRollback_CheckViolationIsInvalid(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	mock.ExpectQuery("INSERT INTO slip_rollbacks").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnError(&pgconn.PgError{Code: "23514", ConstraintName: "ck_slip_rollbacks_status",
			Message: "new row violates check constraint"})

	_, err := store.RecordRollback(context.Background(), rec)
	require.ErrorIs(t, err, ErrInvalidRollback)
}

func TestPostgresStore_RecordRollback_OtherForeignKeyIsNotNotFound(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	mock.ExpectQuery("INSERT INTO slip_rollbacks").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnError(&pgconn.PgError{Code: "23503", ConstraintName: "some_other_fk"})

	_, err := store.RecordRollback(context.Background(), rec)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSlipNotFound, "only the slip FK means the slip is missing")
}

func TestPostgresStore_RecordRollback_DriverErrorIsWrapped(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	boom := errors.New("connection reset")
	mock.ExpectQuery("INSERT INTO slip_rollbacks").
		WithArgs(rollbackInsertArgs(rec)...).
		WillReturnError(boom)

	_, err := store.RecordRollback(context.Background(), rec)
	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrSlipNotFound)
	assert.NotErrorIs(t, err, ErrInvalidRollback)
}

func TestPostgresStore_RecordRollback_InvalidRecordNeverReachesTheDatabase(t *testing.T) {
	store, mock := newMockStore(t)
	rec := validRollbackRecord()
	rec.Status = "dry-run" // the artifact spelling; gitops-rollback maps it to dry_run first

	_, err := store.RecordRollback(context.Background(), rec)
	require.ErrorIs(t, err, ErrInvalidRollback)
	require.NoError(t, mock.ExpectationsWereMet(), "no statement may be sent for an invalid record")
}

func rollbackColumns() []string {
	return []string{
		"id", "correlation_id", "workflow", "recorded_at", "environment", "reason", "source",
		"rollout_phase", "pod_hash", "gitops_repo", "gitops_commit", "revert_commit", "status",
		"forced", "message", "outcome",
	}
}

func TestPostgresStore_ListRollbacks_OK(t *testing.T) {
	store, mock := newMockStore(t)
	t1 := time.Date(2026, 10, 14, 19, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	rows := pgxmock.NewRows(rollbackColumns()).
		AddRow(int64(1), "c1", "wf-1", t1, "prod", "canary_abort", "canary-coordinator", "dark",
			"5d8b9c7f6", "GitOps-prod", "a1b2c3d", "e4f5a6b", "success", false, "canary analysis failed", "reverted").
		AddRow(int64(2), "c1", "wf-2", t2, "prod", "canary_abort", "rollout-watch", "dark",
			"5d8b9c7f6", "GitOps-prod", "a1b2c3d", "", "noop", true, "", "")
	mock.ExpectQuery("FROM slip_rollbacks WHERE correlation_id = \\$1 ORDER BY recorded_at, id").
		WithArgs("c1").
		WillReturnRows(rows)

	got, err := store.ListRollbacks(context.Background(), "c1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, RollbackRecord{
		ID: 1, CorrelationID: "c1", Workflow: "wf-1", RecordedAt: t1, Environment: "prod",
		Reason: "canary_abort", Source: "canary-coordinator", RolloutPhase: "dark", PodHash: "5d8b9c7f6",
		GitOpsRepo: "GitOps-prod", GitOpsCommit: "a1b2c3d", RevertCommit: "e4f5a6b",
		Status: RollbackStatusSuccess, Message: "canary analysis failed", Outcome: "reverted",
	}, got[0])
	assert.Equal(t, RollbackStatusNoop, got[1].Status)
	assert.True(t, got[1].Forced)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ListRollbacks_EmptyIsANonNilSlice(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectQuery("FROM slip_rollbacks").WithArgs("nobody").
		WillReturnRows(pgxmock.NewRows(rollbackColumns()))

	got, err := store.ListRollbacks(context.Background(), "nobody")
	require.NoError(t, err)
	assert.NotNil(t, got, "callers serialise this; nil would become JSON null")
	assert.Empty(t, got)
}

func TestPostgresStore_ListRollbacks_QueryError(t *testing.T) {
	store, mock := newMockStore(t)
	boom := errors.New("timeout")
	mock.ExpectQuery("FROM slip_rollbacks").WithArgs("c1").WillReturnError(boom)

	_, err := store.ListRollbacks(context.Background(), "c1")
	require.ErrorIs(t, err, boom)
}

func TestPostgresStore_ListRollbacks_ScanError(t *testing.T) {
	store, mock := newMockStore(t)
	rows := pgxmock.NewRows(rollbackColumns()).
		AddRow("not-a-number", "c1", "wf-1", time.Now(), "prod", "", "", "", "", "", "", "", "success", false, "", "")
	mock.ExpectQuery("FROM slip_rollbacks").WithArgs("c1").WillReturnRows(rows)

	_, err := store.ListRollbacks(context.Background(), "c1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list rollbacks for slip c1")
}

func TestPostgresStore_ListRollbacks_RowsError(t *testing.T) {
	store, mock := newMockStore(t)
	boom := errors.New("stream broke")
	rows := pgxmock.NewRows(rollbackColumns()).
		AddRow(int64(1), "c1", "wf-1", time.Now(), "prod", "", "", "", "", "", "", "", "success", false, "", "").
		RowError(0, boom)
	mock.ExpectQuery("FROM slip_rollbacks").WithArgs("c1").WillReturnRows(rows)

	_, err := store.ListRollbacks(context.Background(), "c1")
	require.ErrorIs(t, err, boom)
}
