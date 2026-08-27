package slippy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// repaveSuccessor is the successor slip the Repave unit tests below replace "old-id" with.
func repaveSuccessor() *Slip {
	return &Slip{
		CorrelationID: "new-id",
		Repository:    "owner/repo",
		Branch:        "release",
		CommitSHA:     "sha1",
		Status:        SlipStatusPending,
	}
}

// expectRepaveSuccessorInsert expects the successor's routing_slips upsert.
func expectRepaveSuccessorInsert(store *PostgresStore, mock pgxmock.PgxPoolIface) *pgxmock.ExpectedExec {
	return mock.ExpectExec(`INSERT INTO routing_slips .* ON CONFLICT \(correlation_id\) DO UPDATE SET`).
		WithArgs(anyArgs(len(store.slipColumns()))...)
}

// TestPostgresStore_Repave_EndedSlip_OrdersStatementsForAtomicReplacement pins the whole
// happy path AND its ordering, which is the substance of the fix. pgxmock runs in strict
// ordered mode, so this test fails if any statement moves:
//
//	guarded delete -> superseded children -> SUCCESSOR INSERT -> descendant repoint -> link
//
// The successor insert sitting BEFORE the repoint is the load-bearing part: it is what
// stops a descendant from ever naming a correlation ID that has no row, and what makes a
// Phase B foreign key on slip_ancestry.parent_correlation_id possible at all.
func TestPostgresStore_Repave_EndedSlip_OrdersStatementsForAtomicReplacement(t *testing.T) {
	store, mock := newMockStore(t)
	successor := repaveSuccessor()

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectRepaveSuccessorInsert(store, mock).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE slip_ancestry SET parent_correlation_id = \$1, parent_branch = \$2, `+
		`parent_status = \$3, parent_failed_step = '' WHERE parent_correlation_id = \$4`).
		WithArgs("new-id", "release", "pending", "old-id").
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectExec(`INSERT INTO slip_ancestry`).
		WithArgs(anyArgs(10)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	parent := &AncestryEntry{CorrelationID: "parent-id", CommitSHA: "sha-parent", Status: SlipStatusCompleted}
	require.NoError(t, store.Repave(context.Background(), "old-id", successor, parent))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_RepointRewritesFullParentSnapshot pins that the repoint rewrites
// every column describing the parent, not just the id: branch and status name the successor
// and parent_failed_step is cleared (D2.2). parent_branch matters as much as the id —
// ResolveAncestry's next hop joins on it, so a stale branch truncated the walk for exactly
// the cross-branch repave the feature supports (the successor here is on "release" while
// the superseded run was elsewhere).
func TestPostgresStore_Repave_RepointRewritesFullParentSnapshot(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectRepaveSuccessorInsert(store, mock).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE slip_ancestry SET parent_correlation_id = \$1, parent_branch = \$2, `+
		`parent_status = \$3, parent_failed_step = '' WHERE parent_correlation_id = \$4`).
		WithArgs("new-id", "release", "pending", "old-id").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.Repave(context.Background(), "old-id", repaveSuccessor(), nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_CarriesForwardParentLinkWhenCallerHasNone pins the TR-4 fix: with
// parent == nil the superseded run's OWN link is read BEFORE its children are deleted, and
// re-inserted for the successor. The read has to precede the delete or there would be
// nothing left to carry; strict ordering here is what pins that.
func TestPostgresStore_Repave_CarriesForwardParentLinkWhenCallerHasNone(t *testing.T) {
	store, mock := newMockStore(t)
	created := time.Now()

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnRows(pgxmock.NewRows([]string{
			"parent_correlation_id", "parent_commit_sha", "parent_status", "parent_failed_step",
			"parent_repository", "parent_branch", "created_at",
		}).AddRow("carried-parent", "sha-carried", "completed", "", "owner/repo", "main", created))
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectRepaveSuccessorInsert(store, mock).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE slip_ancestry SET parent_correlation_id`).
		WithArgs("new-id", "release", "pending", "old-id").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	// The carried link is re-inserted for the successor: same parent id and SHA, but keyed
	// on the successor's own (repository, branch, correlation_id).
	mock.ExpectExec(`INSERT INTO slip_ancestry`).
		WithArgs("owner/repo", "release", "new-id",
			"carried-parent", "sha-carried", "completed", "", "owner/repo", "main", created).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, store.Repave(context.Background(), "old-id", repaveSuccessor(), nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_RollsBackWhenSuccessorInsertFails is the unit-level counterpart of
// the TR-2 integration test: a failing successor insert must roll the guarded delete back,
// so the superseded run is not destroyed with nothing to replace it.
func TestPostgresStore_Repave_RollsBackWhenSuccessorInsertFails(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	// 42703: the config named a step column the schema does not have yet.
	expectRepaveSuccessorInsert(store, mock).
		WillReturnError(&pgconn.PgError{Code: "42703", Message: `column "not_yet_migrated_status" does not exist`})
	mock.ExpectRollback()

	err := store.Repave(context.Background(), "old-id", repaveSuccessor(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not_yet_migrated_status")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_SuccessorDuplicateMapsToSentinel pins that a unique-index
// conflict on the successor surfaces as ErrDuplicateSlip (so the push path can route to its
// dedup backstop) rather than as an opaque failure — and that the delete rolls back with it.
func TestPostgresStore_Repave_SuccessorDuplicateMapsToSentinel(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectRepaveSuccessorInsert(store, mock).WillReturnError(&pgconn.PgError{
		Code:           "23505",
		ConstraintName: "uq_routing_slips_repo_sha",
	})
	mock.ExpectRollback()

	err := store.Repave(context.Background(), "old-id", repaveSuccessor(), nil)
	require.ErrorIs(t, err, ErrDuplicateSlip)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_RollsBackWhenLinkInsertFails pins the deliberate strictness of the
// repave path: unlike the fresh-create path (where a link failure is only a warning), a link
// failure inside a repave rolls the whole replacement back. Leaving a successor with a
// permanently missing parent hop is worse than failing the push and letting Kafka redeliver.
func TestPostgresStore_Repave_RollsBackWhenLinkInsertFails(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectRepaveSuccessorInsert(store, mock).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE slip_ancestry SET parent_correlation_id`).
		WithArgs("new-id", "release", "pending", "old-id").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectExec(`INSERT INTO slip_ancestry`).
		WithArgs(anyArgs(10)...).
		WillReturnError(errors.New("ancestry table unavailable"))
	mock.ExpectRollback()

	parent := &AncestryEntry{CorrelationID: "parent-id", CommitSHA: "sha-parent", Status: SlipStatusCompleted}
	err := store.Repave(context.Background(), "old-id", repaveSuccessor(), parent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ancestry table unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_LiveSlip_RejectedWithErrSlipWentLive pins the TOCTOU guard: when
// the row still exists but its status is no longer ended (it recovered to live between the
// caller's repave decision and this call), the repave must be rejected with ErrSlipWentLive
// and rolled back — nothing deleted, nothing repointed, and critically NO successor created,
// since that would leave two competing live runs for one commit. Strict ordered mode means
// any statement past the existence check would fail this test.
func TestPostgresStore_Repave_LiveSlip_RejectedWithErrSlipWentLive(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 0)) // guard rejected: status no longer ended
	mock.ExpectQuery(`SELECT correlation_id FROM routing_slips WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnRows(pgxmock.NewRows([]string{"correlation_id"}).AddRow("old-id"))
	mock.ExpectRollback()

	err := store.Repave(context.Background(), "old-id", repaveSuccessor(), nil)
	require.ErrorIs(t, err, ErrSlipWentLive)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_MissingSupersededRow_StillCreatesSuccessor pins the idempotent
// path. Two things must both hold: the successor IS created (so a Kafka redelivery of a push
// whose superseded row is already gone converges instead of failing forever), and NOTHING
// else is written — no carry-forward read, no child deletes, no descendant repoint — because
// this call did not remove the row and so has no licence to rewrite unrelated ancestry
// (D2.1, DEVOPS-231 review). pgxmock's strict ordering enforces the second half: any extra
// statement fails the test.
func TestPostgresStore_Repave_MissingSupersededRow_StillCreatesSuccessor(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("ghost").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectQuery(`SELECT correlation_id FROM routing_slips WHERE correlation_id = \$1`).
		WithArgs("ghost").
		WillReturnError(pgx.ErrNoRows)
	expectRepaveSuccessorInsert(store, mock).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, store.Repave(context.Background(), "ghost", repaveSuccessor(), nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_NilSuccessor_Rejected pins the precondition: Repave's entire
// contract is "replace this run WITH that one", so there is no meaningful behavior without a
// successor. Rejecting it before opening a transaction keeps a caller bug from being
// silently reinterpreted as a bare delete.
func TestPostgresStore_Repave_NilSuccessor_Rejected(t *testing.T) {
	store, mock := newMockStore(t)

	err := store.Repave(context.Background(), "old-id", nil, nil)
	require.ErrorIs(t, err, ErrInvalidConfiguration)
	require.NoError(t, mock.ExpectationsWereMet(), "no transaction may be opened for a nil successor")
}

// TestPostgresStore_Repave_CarryForwardReadFailureAborts pins that a failed carry-forward
// read is fatal rather than silently treated as "no link to carry" — the latter would
// quietly drop a lineage hop, which is the very loss the carry-forward exists to prevent.
func TestPostgresStore_Repave_CarryForwardReadFailureAborts(t *testing.T) {
	store, mock := newMockStore(t)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnError(errors.New("ancestry read failed"))
	mock.ExpectRollback()

	err := store.Repave(context.Background(), "old-id", repaveSuccessor(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading superseded ancestry link")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPostgresStore_Repave_RollsBackOnEveryStatementFailure walks each remaining statement
// in the transaction and asserts the same thing about all of them: a failure rolls the whole
// replacement back. Atomicity is only worth claiming if it holds at every step, not just at
// the successor insert, so each is exercised rather than argued.
func TestPostgresStore_Repave_RollsBackOnEveryStatementFailure(t *testing.T) {
	boom := errors.New("statement failed")

	tests := []struct {
		name       string
		wantErrMsg string
		// queue expects statements up to and including the failing one.
		queue func(store *PostgresStore, mock pgxmock.PgxPoolIface)
	}{
		{
			name:       "guarded delete of the superseded row",
			wantErrMsg: "deleting superseded row",
			queue: func(_ *PostgresStore, mock pgxmock.PgxPoolIface) {
				mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
					WithArgs("old-id").
					WillReturnError(boom)
			},
		},
		{
			name:       "existence check after the delete matched nothing",
			wantErrMsg: "checking superseded row",
			queue: func(_ *PostgresStore, mock pgxmock.PgxPoolIface) {
				mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
					WithArgs("old-id").
					WillReturnResult(pgxmock.NewResult("DELETE", 0))
				mock.ExpectQuery(`SELECT correlation_id FROM routing_slips WHERE correlation_id = \$1`).
					WithArgs("old-id").
					WillReturnError(boom)
			},
		},
		{
			name:       "deleting the superseded run's children",
			wantErrMsg: "deleting superseded children",
			queue: func(_ *PostgresStore, mock pgxmock.PgxPoolIface) {
				mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
					WithArgs("old-id").
					WillReturnResult(pgxmock.NewResult("DELETE", 1))
				mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
					WithArgs("old-id").
					WillReturnError(pgx.ErrNoRows)
				mock.ExpectExec("DELETE FROM slip_component_states").
					WithArgs("old-id").
					WillReturnError(boom)
			},
		},
		{
			name:       "repointing descendants onto the successor",
			wantErrMsg: "repointing descendants",
			queue: func(store *PostgresStore, mock pgxmock.PgxPoolIface) {
				mock.ExpectExec(`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN`).
					WithArgs("old-id").
					WillReturnResult(pgxmock.NewResult("DELETE", 1))
				mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
					WithArgs("old-id").
					WillReturnError(pgx.ErrNoRows)
				mock.ExpectExec("DELETE FROM slip_component_states").
					WithArgs("old-id").
					WillReturnResult(pgxmock.NewResult("DELETE", 1))
				mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
					WithArgs("old-id").
					WillReturnResult(pgxmock.NewResult("DELETE", 1))
				expectRepaveSuccessorInsert(store, mock).WillReturnResult(pgxmock.NewResult("INSERT", 1))
				mock.ExpectExec(`UPDATE slip_ancestry SET parent_correlation_id`).
					WithArgs("new-id", "release", "pending", "old-id").
					WillReturnError(boom)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := newMockStore(t)
			mock.ExpectBegin()
			tc.queue(store, mock)
			mock.ExpectRollback()

			err := store.Repave(context.Background(), "old-id", repaveSuccessor(), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErrMsg,
				"the error should name the statement that failed, for operator triage")
			assert.ErrorIs(t, err, boom, "the underlying store error must stay unwrapped-to")
			require.NoError(t, mock.ExpectationsWereMet(),
				"nothing past the failing statement may run, and the transaction must roll back")
		})
	}
}

// TestPostgresStore_Repave_StatusGuard_CoversEndedStatuses pins the exact ended-status set
// the guard allows: failed, completed, abandoned, promoted, compensated.
func TestPostgresStore_Repave_StatusGuard_CoversEndedStatuses(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectBegin()
	mock.ExpectExec(
		`DELETE FROM routing_slips WHERE correlation_id = \$1 AND status IN ` +
			`\('failed','completed','abandoned','promoted','compensated'\)`,
	).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectQuery(`SELECT parent_correlation_id, .* FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("DELETE FROM slip_component_states").
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec(`DELETE FROM slip_ancestry WHERE correlation_id = \$1`).
		WithArgs("old-id").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectRepaveSuccessorInsert(store, mock).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE slip_ancestry SET parent_correlation_id`).
		WithArgs("new-id", "release", "pending", "old-id").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectCommit()

	require.NoError(t, store.Repave(context.Background(), "old-id", repaveSuccessor(), nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// parseSQLStringSet parses a SQL "IN (...)" literal list of single-quoted strings (e.g.
// repaveableSlipStatusesSQL's "'failed','completed'") into a set of the unquoted values.
// Used only by TestRepaveableSlipStatusesSQL_MatchesIsLive so that test compares against
// the real repaveableSlipStatusesSQL constant's actual contents, not a second hardcoded
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

// TestRepaveableSlipStatusesSQL_MatchesIsLive asserts set equality between
// repaveableSlipStatusesSQL (Repave's guard, expressed in SQL) and SlipStatus.IsLive
// (the repave decision predicate, expressed in Go) across every SlipStatus value —
// enumerated explicitly, the same exhaustiveness style TestSlipStatus_IsLive already
// uses, so a future ninth status forces a decision here too instead of silently drifting
// (D2.3, DEVOPS-231 review): if IsLive() ever disagrees with this SQL constant,
// CreateSlipForPush routes a push to repave but Repave's guard rejects it, returning
// ErrSlipWentLive for a slip that never actually went live — permanently wedging that
// commit's pushes onto the old, ended slip with no error surfaced anywhere.
func TestRepaveableSlipStatusesSQL_MatchesIsLive(t *testing.T) {
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

	sqlSet := parseSQLStringSet(repaveableSlipStatusesSQL)

	wantSet := make(map[string]bool, len(allStatuses))
	for _, status := range allStatuses {
		if !status.IsLive() {
			wantSet[string(status)] = true
		}
	}

	assert.Equal(t, wantSet, sqlSet,
		"repaveableSlipStatusesSQL must contain exactly the SlipStatus values where IsLive() is false")

	for _, status := range allStatuses {
		t.Run(string(status), func(t *testing.T) {
			assert.Equal(t, !status.IsLive(), sqlSet[string(status)],
				"repaveableSlipStatusesSQL and SlipStatus(%q).IsLive() disagree", status)
		})
	}
}
