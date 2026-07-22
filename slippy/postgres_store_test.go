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

func newMockStore(t *testing.T) (*PostgresStore, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	store, err := newPostgresStoreWithPool(mock, pgTestPipelineConfig(t), nil)
	require.NoError(t, err)
	return store, mock
}

// anyArgs returns n pgxmock AnyArg matchers, for expectations where we assert the SQL
// shape but not the individual argument values.
func anyArgs(n int) []any {
	a := make([]any, n)
	for i := range a {
		a[i] = pgxmock.AnyArg()
	}
	return a
}

func TestNewPostgresStore_Validation(t *testing.T) {
	_, err := NewPostgresStore(nil, pgTestPipelineConfig(t), nil)
	require.Error(t, err, "nil pool must error")

	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	_, err = newPostgresStoreWithPool(mock, nil, nil)
	require.Error(t, err, "nil config must error")
}

func TestPostgresStore_slipColumns(t *testing.T) {
	store, _ := newMockStore(t)
	cols := store.slipColumns()
	// 9 core + 4 step-status + 1 aggregate.
	require.Len(t, cols, 14)
	assert.Equal(t, "correlation_id", cols[0])
	assert.Equal(t, []string{
		"push_parsed_status", "builds_status", "unit_tests_status", "dev_deploy_status",
	}, cols[9:13])
	assert.Equal(t, "builds", cols[13], "aggregate column comes last")
	assert.Equal(t, []string{"builds"}, store.aggregateColumns())
}

func TestPostgresStore_Create_Upsert(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectExec("INSERT INTO routing_slips .* ON CONFLICT \\(correlation_id\\) DO UPDATE SET").
		WithArgs(anyArgs(len(store.slipColumns()))...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	slip := &Slip{CorrelationID: "c1", Repository: "owner/repo", Branch: "main", CommitSHA: "sha1"}
	require.NoError(t, store.Create(context.Background(), slip))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Update_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectExec("UPDATE routing_slips SET").
		WithArgs(anyArgs(len(store.slipColumns()))...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err := store.Update(context.Background(), &Slip{CorrelationID: "missing"})
	require.ErrorIs(t, err, ErrSlipNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Update_OK(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectExec("UPDATE routing_slips SET").
		WithArgs(anyArgs(len(store.slipColumns()))...).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	require.NoError(t, store.Update(context.Background(), &Slip{
		CorrelationID: "c1", Status: SlipStatusInProgress,
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Load_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectQuery("SELECT .* FROM routing_slips WHERE correlation_id").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)

	_, err := store.Load(context.Background(), "nope")
	require.ErrorIs(t, err, ErrSlipNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Load_Hydrates(t *testing.T) {
	store, mock := newMockStore(t)

	stepDetails := []byte(`{"builds":{"actor":"ci","started_at":"2026-01-02T03:04:05Z"}}`)
	stateHistory := []byte(
		`{"entries":[{"step":"builds","status":"running","actor":"ci","timestamp":"2026-01-02T03:04:05Z"}]}`,
	)
	buildsAgg := []byte(`{"items":[{"component":"api","status":"completed"}]}`)
	created := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)

	rows := pgxmock.NewRows(store.slipColumns()).AddRow(
		"c1", "owner/repo", "main", "sha1", created, updated,
		"in_progress", stepDetails, stateHistory,
		"pending", "completed", "pending", "pending", // push_parsed, builds, unit_tests, dev_deploy
		buildsAgg,
	)
	mock.ExpectQuery("SELECT .* FROM routing_slips WHERE correlation_id").
		WithArgs("c1").WillReturnRows(rows)

	slip, err := store.Load(context.Background(), "c1")
	require.NoError(t, err)
	assert.Equal(t, "c1", slip.CorrelationID)
	assert.Equal(t, SlipStatusInProgress, slip.Status)
	assert.Equal(t, StepStatusCompleted, slip.Steps["builds"].Status)
	assert.Equal(t, "ci", slip.Steps["builds"].Actor, "actor merged from step_details")
	require.NotNil(t, slip.Steps["builds"].StartedAt)
	require.Len(t, slip.Aggregates["builds"], 1)
	assert.Equal(t, "api", slip.Aggregates["builds"][0].Component)
	require.Len(t, slip.StateHistory, 1)
	assert.Equal(t, "builds", slip.StateHistory[0].Step)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildStepDetailsMap(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	slip := &Slip{Steps: map[string]Step{
		"builds":     {Status: StepStatusRunning, StartedAt: &started, Actor: "ci"},
		"unit_tests": {Status: StepStatusFailed, Error: "boom"},
		"empty":      {Status: StepStatusPending}, // no detail fields -> omitted
	}}
	got := buildStepDetailsMap(slip)
	assert.Contains(t, got, "builds")
	assert.Contains(t, got, "unit_tests")
	assert.NotContains(t, got, "empty", "steps with no detail fields are omitted")

	builds := got["builds"].(map[string]any)
	assert.Equal(t, "ci", builds["actor"])
	assert.Equal(t, started.Format(time.RFC3339Nano), builds["started_at"])
	assert.Equal(t, "boom", got["unit_tests"].(map[string]any)["error"])
}

func TestMergeStepDetailsJSON(t *testing.T) {
	slip := &Slip{Steps: map[string]Step{"builds": {Status: StepStatusRunning}}}
	mergeStepDetailsJSON(slip, []byte(
		`{"builds":{"actor":"ci","error":"x","held_reason":"waiting","completed_at":"2026-01-02T03:04:05Z"},"ghost":{"actor":"y"}}`,
	))

	b := slip.Steps["builds"]
	assert.Equal(t, "ci", b.Actor)
	assert.Equal(t, "x", b.Error)
	assert.Equal(t, "waiting", b.HeldReason)
	require.NotNil(t, b.CompletedAt)
	_, hasGhost := slip.Steps["ghost"]
	assert.False(t, hasGhost, "details for unknown steps are ignored")

	// Empty / invalid payloads are no-ops.
	mergeStepDetailsJSON(slip, nil)
	mergeStepDetailsJSON(slip, []byte("not json"))
}
