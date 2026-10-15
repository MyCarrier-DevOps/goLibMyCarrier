package slippy

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Postgres SQLSTATEs and the constraint name RecordRollback maps (migration v7).
const (
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	fkSlipRollbacksSlip         = "fk_slip_rollbacks_slip"
)

// insertRollbackSQL writes one attempt. ON CONFLICT (workflow) DO NOTHING makes a retried emit
// for the same Argo Workflow a no-op rather than a second row (uq_slip_rollbacks_workflow).
const insertRollbackSQL = `INSERT INTO slip_rollbacks (
	correlation_id, workflow, environment, reason, source, rollout_phase, pod_hash,
	gitops_repo, gitops_commit, revert_commit, status, forced, message, outcome
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (workflow) DO NOTHING`

// listRollbacksSQL reads a slip's attempts in the order they were recorded; id breaks ties
// between rows recorded in the same microsecond.
const listRollbacksSQL = `SELECT id, correlation_id, workflow, recorded_at, environment, reason, source,
	rollout_phase, pod_hash, gitops_repo, gitops_commit, revert_commit, status, forced, message, outcome
FROM slip_rollbacks WHERE correlation_id = $1 ORDER BY recorded_at, id`

// RecordRollback writes one rollback attempt for a slip (DEVOPS-320). It returns true when this
// call wrote the row and false when a row for the same Workflow already existed, which is the
// normal outcome of a retried emit, not an error.
//
// The record is validated first, so an invalid one never reaches the database. A missing slip
// (never created, or repaved since) surfaces as ErrSlipNotFound through the cascade FK; a
// status the CHECK refuses surfaces as ErrInvalidRollback.
func (s *PostgresStore) RecordRollback(ctx context.Context, rec RollbackRecord) (bool, error) {
	if err := rec.Validate(); err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, insertRollbackSQL,
		rec.CorrelationID, rec.Workflow, rec.Environment, rec.Reason, rec.Source, rec.RolloutPhase,
		rec.PodHash, rec.GitOpsRepo, rec.GitOpsCommit, rec.RevertCommit, string(rec.Status), rec.Forced,
		rec.Message, rec.Outcome)
	if err != nil {
		return false, mapRollbackError(rec.CorrelationID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListRollbacks returns a slip's rollback attempts in the order they were recorded. A slip
// with none, or a correlation ID that names no slip, returns an empty, non-nil slice.
func (s *PostgresStore) ListRollbacks(ctx context.Context, correlationID string) ([]RollbackRecord, error) {
	rows, err := s.pool.Query(ctx, listRollbacksSQL, correlationID)
	if err != nil {
		return nil, fmt.Errorf("list rollbacks for slip %s: %w", correlationID, err)
	}
	defer rows.Close()

	records := make([]RollbackRecord, 0)
	for rows.Next() {
		var rec RollbackRecord
		var status string
		if scanErr := rows.Scan(&rec.ID, &rec.CorrelationID, &rec.Workflow, &rec.RecordedAt,
			&rec.Environment, &rec.Reason, &rec.Source, &rec.RolloutPhase, &rec.PodHash,
			&rec.GitOpsRepo, &rec.GitOpsCommit, &rec.RevertCommit, &status, &rec.Forced,
			&rec.Message, &rec.Outcome); scanErr != nil {
			return nil, fmt.Errorf("list rollbacks for slip %s: %w", correlationID, scanErr)
		}
		rec.Status = RollbackStatus(status)
		records = append(records, rec)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("list rollbacks for slip %s: %w", correlationID, rowsErr)
	}
	return records, nil
}

// mapRollbackError translates a slip_rollbacks insert failure into this package's sentinels.
func mapRollbackError(correlationID string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == sqlStateForeignKeyViolation && pgErr.ConstraintName == fkSlipRollbacksSlip:
			return fmt.Errorf("record rollback for slip %s: %w", correlationID, ErrSlipNotFound)
		case pgErr.Code == sqlStateCheckViolation:
			return fmt.Errorf("record rollback for slip %s: %w: %s", correlationID, ErrInvalidRollback,
				pgErr.Message)
		}
	}
	return fmt.Errorf("record rollback for slip %s: %w", correlationID, err)
}
