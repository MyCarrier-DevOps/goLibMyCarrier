package slippy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxPool is the subset of *pgxpool.Pool the PostgresStore uses. Depending on an
// interface (rather than the concrete pool) lets unit tests substitute a pgxmock pool
// while production wires the real pool. *pgxpool.Pool and pgxmock's PgxPoolIface both
// satisfy it.
type pgxPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
	Ping(ctx context.Context) error
	Close()
}

// PostgresStore is a SlipStore backed by Postgres (pgx/pgxpool). It is the write and
// read-modify-write path for slip persistence; unlike the ClickHouse store it relies on
// atomic UPDATE + MVCC, so it carries none of the sign/version, argMax-dedup,
// write-fingerprint, clone-derive, or verification-retry machinery.
type PostgresStore struct {
	pool   pgxPool
	config *PipelineConfig
	logger Logger
}

// NOTE: the compile-time `var _ SlipStore = (*PostgresStore)(nil)` assertion is added in
// the final sub-milestone (2c), once every SlipStore method is implemented.

// NewPostgresStore creates a PostgresStore over the given pool and pipeline config.
func NewPostgresStore(pool *pgxpool.Pool, config *PipelineConfig, logger Logger) (*PostgresStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: nil pool", ErrStoreConnection)
	}
	return newPostgresStoreWithPool(pool, config, logger)
}

// newPostgresStoreWithPool is the interface-typed constructor used by tests (pgxmock).
func newPostgresStoreWithPool(pool pgxPool, config *PipelineConfig, logger Logger) (*PostgresStore, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: nil pipeline config", ErrInvalidConfiguration)
	}
	if logger == nil {
		logger = NopLogger()
	}
	return &PostgresStore{pool: pool, config: config, logger: logger}, nil
}

// Close releases the pool.
func (s *PostgresStore) Close() error { s.pool.Close(); return nil }

// Ping verifies the connection is alive.
func (s *PostgresStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Create upserts a slip. Matches ClickHouse last-write-wins (and the in-memory
// reference store): an existing correlation_id is overwritten rather than rejected.
func (s *PostgresStore) Create(ctx context.Context, slip *Slip) error {
	cols := s.slipColumns()
	vals, err := s.slipValues(slip, true)
	if err != nil {
		return err
	}

	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	// ON CONFLICT DO UPDATE for every non-PK column (cols[0] is correlation_id).
	sets := make([]string, 0, len(cols)-1)
	for _, col := range cols[1:] {
		sets = append(sets, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
	}

	query := fmt.Sprintf(
		"INSERT INTO routing_slips (%s) VALUES (%s) ON CONFLICT (correlation_id) DO UPDATE SET %s",
		strings.Join(cols, ", "), strings.Join(placeholders, ", "), strings.Join(sets, ", "))

	if _, err := s.pool.Exec(ctx, query, vals...); err != nil {
		return fmt.Errorf("failed to create slip %s: %w", slip.CorrelationID, err)
	}
	return nil
}

// Update writes the slip's current state to its existing row. Returns ErrSlipNotFound if
// no row exists for the correlation ID.
func (s *PostgresStore) Update(ctx context.Context, slip *Slip) error {
	cols := s.slipColumns()
	vals, err := s.slipValues(slip, false)
	if err != nil {
		return err
	}

	// SET every non-PK column; correlation_id (cols[0]) is the WHERE key.
	sets := make([]string, 0, len(cols)-1)
	args := make([]any, 0, len(cols))
	n := 1
	for i, col := range cols {
		if i == 0 {
			continue
		}
		sets = append(sets, fmt.Sprintf("%s = $%d", col, n))
		args = append(args, vals[i])
		n++
	}
	args = append(args, slip.CorrelationID)

	query := fmt.Sprintf("UPDATE routing_slips SET %s WHERE correlation_id = $%d",
		strings.Join(sets, ", "), n)

	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update slip %s: %w", slip.CorrelationID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSlipNotFound
	}
	return nil
}

// Load retrieves a slip by correlation ID.
func (s *PostgresStore) Load(ctx context.Context, correlationID string) (*Slip, error) {
	query := fmt.Sprintf("SELECT %s FROM routing_slips WHERE correlation_id = $1",
		strings.Join(s.slipColumns(), ", "))
	return s.queryOne(ctx, query, correlationID)
}

// LoadByCommit retrieves the most recently updated slip for (repository, commitSHA).
// Repository comparison is case-insensitive.
func (s *PostgresStore) LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error) {
	query := fmt.Sprintf(
		"SELECT %s FROM routing_slips WHERE lower(repository) = lower($1) AND commit_sha = $2 "+
			"ORDER BY updated_at DESC LIMIT 1",
		strings.Join(s.slipColumns(), ", "))
	return s.queryOne(ctx, query, repository, commitSHA)
}

// LoadLiveByCommit returns the most recent live slip for (repository, commitSHA),
// excluding terminal-superseded statuses (abandoned, promoted, compensated).
func (s *PostgresStore) LoadLiveByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error) {
	query := fmt.Sprintf(
		"SELECT %s FROM routing_slips WHERE lower(repository) = lower($1) AND commit_sha = $2 "+
			"AND status NOT IN ('abandoned', 'promoted', 'compensated') "+
			"ORDER BY updated_at DESC LIMIT 1",
		strings.Join(s.slipColumns(), ", "))
	return s.queryOne(ctx, query, repository, commitSHA)
}

// slipColumns returns the ordered routing_slips column list: core columns, then each
// step's status column, then each aggregate step's jsonb column. The order is shared by
// SELECT, INSERT, and the scan destinations so they never drift.
func (s *PostgresStore) slipColumns() []string {
	cols := []string{
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails, ColumnStateHistory,
	}
	for _, step := range s.config.Steps {
		cols = append(cols, step.Name+"_status")
	}
	for _, step := range s.config.Steps {
		if step.Aggregates != "" {
			cols = append(cols, step.Name)
		}
	}
	return cols
}

// aggregateColumns returns the ordered aggregate (jsonb) column names.
func (s *PostgresStore) aggregateColumns() []string {
	var cols []string
	for _, step := range s.config.Steps {
		if step.Aggregates != "" {
			cols = append(cols, step.Name)
		}
	}
	return cols
}

// newSlipScan builds a scan context whose scanDest matches slipColumns() order. Extra
// destinations (e.g. a matched_commit column) are appended last.
func (s *PostgresStore) newSlipScan(extra ...any) (sc *pgSlipScan, dest []any) {
	sc = &pgSlipScan{
		slip:          &Slip{},
		stepStatuses:  make([]string, len(s.config.Steps)),
		aggregateCols: s.aggregateColumns(),
	}
	dest = []any{
		&sc.slip.CorrelationID, &sc.slip.Repository, &sc.slip.Branch, &sc.slip.CommitSHA,
		&sc.slip.CreatedAt, &sc.slip.UpdatedAt, &sc.statusStr, &sc.stepDetails, &sc.stateHistory,
	}
	for i := range s.config.Steps {
		dest = append(dest, &sc.stepStatuses[i])
	}
	sc.aggregateBytes = make([][]byte, len(sc.aggregateCols))
	for i := range sc.aggregateBytes {
		dest = append(dest, &sc.aggregateBytes[i])
	}
	dest = append(dest, extra...)
	return sc, dest
}

// populate turns a scanned pgSlipScan into a fully hydrated Slip: the Steps map from the
// status columns, timing/actor/error merged from step_details, the aggregate component
// slices, and the state-history audit trail. Missing step timing is backfilled from the
// history, matching the ClickHouse scanner.
func (s *PostgresStore) populate(sc *pgSlipScan) *Slip {
	slip := sc.slip
	slip.Status = SlipStatus(sc.statusStr)

	slip.Steps = make(map[string]Step, len(s.config.Steps))
	for i, step := range s.config.Steps {
		slip.Steps[step.Name] = Step{Status: StepStatus(sc.stepStatuses[i])}
	}
	mergeStepDetailsJSON(slip, sc.stepDetails)

	slip.Aggregates = make(map[string][]ComponentStepData, len(sc.aggregateCols))
	for i, col := range sc.aggregateCols {
		var wrapper struct {
			Items []ComponentStepData `json:"items"`
		}
		if len(sc.aggregateBytes[i]) > 0 {
			if err := json.Unmarshal(sc.aggregateBytes[i], &wrapper); err != nil {
				// Leave this aggregate unset on malformed JSON, matching the CH scanner.
				continue
			}
		}
		slip.Aggregates[col] = wrapper.Items
	}

	if len(sc.stateHistory) > 0 {
		var wrapper struct {
			Entries []StateHistoryEntry `json:"entries"`
		}
		if json.Unmarshal(sc.stateHistory, &wrapper) == nil {
			slip.StateHistory = wrapper.Entries
		}
	}

	// Reuse the shared (backend-agnostic) timing reconstruction from the scanner.
	NewSlipScanner(s.config).reconstructStepTimingFromHistory(slip)
	return slip
}

// queryOne runs a single-row SELECT and hydrates the slip, mapping no-rows to
// ErrSlipNotFound.
func (s *PostgresStore) queryOne(ctx context.Context, query string, args ...any) (*Slip, error) {
	sc, dest := s.newSlipScan()
	if err := s.pool.QueryRow(ctx, query, args...).Scan(dest...); err != nil {
		if isNoRows(err) {
			return nil, ErrSlipNotFound
		}
		return nil, fmt.Errorf("failed to load slip: %w", err)
	}
	return s.populate(sc), nil
}

// slipValues returns the column values for slip in slipColumns() order, marshaling jsonb
// columns to strings. When forCreate is true, created_at defaults to now for a zero
// timestamp; updated_at is always set to now.
func (s *PostgresStore) slipValues(slip *Slip, forCreate bool) ([]any, error) {
	now := time.Now().UTC()

	createdAt := slip.CreatedAt
	if forCreate && createdAt.IsZero() {
		createdAt = now
	}

	status := slip.Status
	if status == "" {
		status = SlipStatusPending
	}

	stepDetailsJSON, err := json.Marshal(buildStepDetailsMap(slip))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal step_details for %s: %w", slip.CorrelationID, err)
	}
	// Marshal a nil history as [] (not JSON null) so the entries array is always a JSON
	// array — appendHistoryTx concatenates onto it.
	entries := slip.StateHistory
	if entries == nil {
		entries = []StateHistoryEntry{}
	}
	stateHistoryJSON, err := json.Marshal(struct {
		Entries []StateHistoryEntry `json:"entries"`
	}{Entries: entries})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal state_history for %s: %w", slip.CorrelationID, err)
	}

	vals := []any{
		slip.CorrelationID, slip.Repository, slip.Branch, slip.CommitSHA,
		createdAt, now, string(status), string(stepDetailsJSON), string(stateHistoryJSON),
	}

	for _, step := range s.config.Steps {
		st := StepStatusPending
		if v, ok := slip.Steps[step.Name]; ok && v.Status != "" {
			st = v.Status
		}
		vals = append(vals, string(st))
	}

	for _, col := range s.aggregateColumns() {
		items := slip.Aggregates[col]
		if items == nil {
			items = []ComponentStepData{}
		}
		aggJSON, err := json.Marshal(struct {
			Items []ComponentStepData `json:"items"`
		}{Items: items})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal aggregate %s for %s: %w", col, slip.CorrelationID, err)
		}
		vals = append(vals, string(aggJSON))
	}

	return vals, nil
}

// pgSlipScan holds scan destinations and intermediate values for a routing_slips row.
type pgSlipScan struct {
	slip           *Slip
	statusStr      string
	stepDetails    []byte
	stateHistory   []byte
	stepStatuses   []string
	aggregateCols  []string
	aggregateBytes [][]byte
}

// buildStepDetailsMap builds the step_details JSON object from a slip. Mirrors
// ClickHouseStore.buildStepDetails so the serialized shape is identical across backends
// (the reporting layer and the data-copy job depend on shape parity).
func buildStepDetailsMap(slip *Slip) map[string]any {
	details := make(map[string]any)
	for stepName, step := range slip.Steps {
		d := make(map[string]any)
		if step.StartedAt != nil {
			d["started_at"] = step.StartedAt.Format(time.RFC3339Nano)
		}
		if step.CompletedAt != nil {
			d["completed_at"] = step.CompletedAt.Format(time.RFC3339Nano)
		}
		if step.Actor != "" {
			d["actor"] = step.Actor
		}
		if step.Error != "" {
			d["error"] = step.Error
		}
		if step.HeldReason != "" {
			d["held_reason"] = step.HeldReason
		}
		if len(d) > 0 {
			details[stepName] = d
		}
	}
	return details
}

// mergeStepDetailsJSON merges the step_details jsonb payload into slip.Steps: timing,
// actor, error, and held reason for each step present in both.
func mergeStepDetailsJSON(slip *Slip, data []byte) {
	if len(data) == 0 {
		return
	}
	var details map[string]struct {
		StartedAt   string `json:"started_at"`
		CompletedAt string `json:"completed_at"`
		Actor       string `json:"actor"`
		Error       string `json:"error"`
		HeldReason  string `json:"held_reason"`
	}
	if err := json.Unmarshal(data, &details); err != nil {
		return
	}
	for name, d := range details {
		step, ok := slip.Steps[name]
		if !ok {
			continue
		}
		if d.StartedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, d.StartedAt); err == nil {
				step.StartedAt = &t
			}
		}
		if d.CompletedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, d.CompletedAt); err == nil {
				step.CompletedAt = &t
			}
		}
		if d.Actor != "" {
			step.Actor = d.Actor
		}
		if d.Error != "" {
			step.Error = d.Error
		}
		if d.HeldReason != "" {
			step.HeldReason = d.HeldReason
		}
		slip.Steps[name] = step
	}
}

// isNoRows reports whether err is pgx's no-rows sentinel.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
