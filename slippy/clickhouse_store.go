package slippy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator"
)

const (
	// DefaultMaxUpdateRetries is the default number of retry attempts for updates
	// when a version conflict occurs due to concurrent modifications.
	DefaultMaxUpdateRetries = 10
)

// ClickHouseStore implements SlipStore using ClickHouse as the backend.
// The store uses correlation_id as the unique identifier for routing slips,
// consistent with MyCarrier's organization-wide use of correlation_id to
// identify jobs across all systems.
//
// The store is config-driven: the pipeline configuration determines which
// step columns exist and how they are queried.
type ClickHouseStore struct {
	session            ch.ClickhouseSessionInterface
	pipelineConfig     *PipelineConfig
	database           string
	queryBuilder       *SlipQueryBuilder
	scanner            *SlipScanner
	optimizeAfterWrite bool // If true, runs OPTIMIZE TABLE after each write operation
	maxUpdateRetries   int  // Maximum retry attempts for version conflicts
}

// ClickHouseStoreOptions configures the ClickHouse store.
type ClickHouseStoreOptions struct {
	// SkipMigrations if true, skips running migrations during initialization
	SkipMigrations bool

	// MigrateOptions configures migration behavior (only used if SkipMigrations is false)
	MigrateOptions MigrateOptions

	// PipelineConfig defines the pipeline steps (required for dynamic schema)
	PipelineConfig *PipelineConfig

	// Database is the ClickHouse database name (default: "ci")
	Database string

	// Logger for migration output
	Logger clickhousemigrator.Logger

	// OptimizeAfterWrite if true, runs OPTIMIZE TABLE after each write operation.
	// This ensures immediate deduplication with ReplacingMergeTree.
	// Default: true for normal operations, set to false for migrations/bulk operations.
	OptimizeAfterWrite *bool
}

// NewClickHouseStoreFromConfig creates a new ClickHouse-backed slip store from config.
// By default, this runs all pending migrations to ensure the schema is up to date.
func NewClickHouseStoreFromConfig(config *ch.ClickhouseConfig, opts ClickHouseStoreOptions) (*ClickHouseStore, error) {
	ctx := context.Background()
	startTime := time.Now()

	// Create ClickHouse connection
	connStart := time.Now()
	session, err := ch.NewClickhouseSession(config, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse session: %w", err)
	}
	connDuration := time.Since(connStart)

	// Log connection timing if logger is available
	if opts.Logger != nil {
		opts.Logger.Info(ctx, "ClickHouse connection established", map[string]interface{}{
			"connection_ms": connDuration.Milliseconds(),
		})
	}

	if opts.Database == "" {
		opts.Database = "ci"
	}

	// Default to true for OptimizeAfterWrite (normal operations)
	optimizeAfterWrite := true
	if opts.OptimizeAfterWrite != nil {
		optimizeAfterWrite = *opts.OptimizeAfterWrite
	}

	store := &ClickHouseStore{
		session:            session,
		pipelineConfig:     opts.PipelineConfig,
		database:           opts.Database,
		queryBuilder:       NewSlipQueryBuilder(opts.PipelineConfig, opts.Database),
		scanner:            NewSlipScanner(opts.PipelineConfig),
		optimizeAfterWrite: optimizeAfterWrite,
		maxUpdateRetries:   DefaultMaxUpdateRetries, // Default retry count for version conflicts
	}

	// Run migrations unless explicitly skipped
	if !opts.SkipMigrations {
		migrateStart := time.Now()
		migrateOpts := opts.MigrateOptions
		migrateOpts.Database = opts.Database
		migrateOpts.PipelineConfig = opts.PipelineConfig
		migrateOpts.Logger = opts.Logger

		if _, err := RunMigrations(ctx, session.Conn(), migrateOpts); err != nil {
			closeErr := session.Close()
			if closeErr != nil {
				return nil, fmt.Errorf("failed to run migrations: %w (also failed to close session: %w)", err, closeErr)
			}
			return nil, fmt.Errorf("failed to run migrations: %w", err)
		}

		// Log migration timing if logger is available
		if opts.Logger != nil {
			opts.Logger.Info(ctx, "Migrations completed", map[string]interface{}{
				"migration_ms":   time.Since(migrateStart).Milliseconds(),
				"total_store_ms": time.Since(startTime).Milliseconds(),
			})
		}
	}

	return store, nil
}

// NewClickHouseStoreFromSession creates a store from an existing session.
// Migrations are NOT run automatically when using this constructor.
// Use RunMigrations explicitly if needed.
// OptimizeAfterWrite defaults to true for normal operations.
func NewClickHouseStoreFromSession(
	session ch.ClickhouseSessionInterface,
	pipelineConfig *PipelineConfig,
	database string,
) *ClickHouseStore {
	if database == "" {
		database = "ci"
	}
	return &ClickHouseStore{
		session:            session,
		pipelineConfig:     pipelineConfig,
		database:           database,
		queryBuilder:       NewSlipQueryBuilder(pipelineConfig, database),
		scanner:            NewSlipScanner(pipelineConfig),
		optimizeAfterWrite: true,                    // Default to true for normal operations
		maxUpdateRetries:   DefaultMaxUpdateRetries, // Default retry count for version conflicts
	}
}

// NewClickHouseStoreFromConn creates a store from an existing driver connection.
// Migrations are NOT run automatically when using this constructor.
// This is provided for backward compatibility with existing code.
// OptimizeAfterWrite defaults to true for normal operations.
func NewClickHouseStoreFromConn(conn ch.Conn, pipelineConfig *PipelineConfig, database string) *ClickHouseStore {
	if database == "" {
		database = "ci"
	}
	return &ClickHouseStore{
		session:            ch.NewSessionFromConn(conn),
		pipelineConfig:     pipelineConfig,
		database:           database,
		queryBuilder:       NewSlipQueryBuilder(pipelineConfig, database),
		scanner:            NewSlipScanner(pipelineConfig),
		optimizeAfterWrite: true,                    // Default to true for normal operations
		maxUpdateRetries:   DefaultMaxUpdateRetries, // Default retry count for version conflicts
	}
}

// Session returns the underlying ClickHouse session interface.
// This can be used for running custom queries or migrations.
func (s *ClickHouseStore) Session() ch.ClickhouseSessionInterface {
	return s.session
}

// Conn returns the underlying ClickHouse driver connection.
// This can be used for running migrations or custom queries.
func (s *ClickHouseStore) Conn() ch.Conn {
	return s.session.Conn()
}

// PipelineConfig returns the pipeline configuration.
func (s *ClickHouseStore) PipelineConfig() *PipelineConfig {
	return s.pipelineConfig
}

// Create persists a new routing slip.
// The slip's CorrelationID is used as the unique identifier.
// For VersionedCollapsingMergeTree, this inserts a row with sign=1 and version=1.
func (s *ClickHouseStore) Create(ctx context.Context, slip *Slip) error {
	if s.pipelineConfig == nil {
		return fmt.Errorf("pipeline config is required for store operations")
	}

	// Set default sign and version for new slips
	if slip.Sign == 0 {
		slip.Sign = 1
	}
	if slip.Version == 0 {
		slip.Version = 1
	}

	if err := s.insertRow(ctx, slip); err != nil {
		return fmt.Errorf("failed to create slip: %w", err)
	}

	// Run OPTIMIZE TABLE if enabled
	if s.optimizeAfterWrite {
		if err := s.OptimizeTable(ctx); err != nil {
			return fmt.Errorf("failed to optimize table after insert: %w", err)
		}
	}

	return nil
}

// OptimizeTable runs OPTIMIZE TABLE to force immediate collapsing.
// This is necessary with VersionedCollapsingMergeTree to ensure reads don't return
// uncollapsed rows before background merges complete.
func (s *ClickHouseStore) OptimizeTable(ctx context.Context) error {
	query := fmt.Sprintf("OPTIMIZE TABLE %s.routing_slips FINAL", s.database)
	if err := s.session.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to optimize table: %w", err)
	}
	return nil
}

// SetOptimizeAfterWrite enables or disables automatic table optimization after writes.
// This is useful for bulk operations where optimization should be deferred.
func (s *ClickHouseStore) SetOptimizeAfterWrite(enabled bool) {
	s.optimizeAfterWrite = enabled
}

// Load retrieves a slip by its correlation ID.
// The correlation_id is the unique identifier for routing slips and is used
// organization-wide to identify jobs across all systems.
func (s *ClickHouseStore) Load(ctx context.Context, correlationID string) (*Slip, error) {
	if s.pipelineConfig == nil {
		return nil, fmt.Errorf("pipeline config is required for store operations")
	}

	query := s.queryBuilder.BuildSelectQuery("WHERE correlation_id = ?", "LIMIT 1")
	slip, err := s.scanSlip(ctx, query, correlationID)
	if err != nil {
		return nil, err
	}

	if err := s.hydrateSlip(ctx, slip); err != nil {
		return nil, fmt.Errorf("failed to hydrate slip with component states: %w", err)
	}

	return slip, nil
}

// LoadByCommit retrieves a slip by repository and commit SHA.
func (s *ClickHouseStore) LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error) {
	if s.pipelineConfig == nil {
		return nil, fmt.Errorf("pipeline config is required for store operations")
	}

	query := s.queryBuilder.BuildSelectQuery(
		"WHERE repository = ? AND commit_sha = ?",
		"ORDER BY created_at DESC LIMIT 1",
	)
	slip, err := s.scanSlip(ctx, query, repository, commitSHA)
	if err != nil {
		return nil, err
	}

	if err := s.hydrateSlip(ctx, slip); err != nil {
		return nil, fmt.Errorf("failed to hydrate slip with component states: %w", err)
	}

	return slip, nil
}

// FindByCommits finds a slip matching any commit in the ordered list.
// Returns the slip for the first (most recent) matching commit.
func (s *ClickHouseStore) FindByCommits(
	ctx context.Context,
	repository string,
	commits []string,
) (*Slip, string, error) {
	if s.pipelineConfig == nil {
		return nil, "", fmt.Errorf("pipeline config is required for store operations")
	}

	if len(commits) == 0 {
		return nil, "", fmt.Errorf("no commits provided")
	}

	query := s.queryBuilder.BuildFindByCommitsQuery()

	row := s.session.QueryRow(ctx, query,
		ch.Named("repository", repository),
		ch.Named("commits", commits),
	)

	slip, matchedCommit, err := s.scanner.ScanSlipWithMatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrSlipNotFound
		}
		return nil, "", fmt.Errorf("failed to query slip by commits: %w", err)
	}

	if err := s.hydrateSlip(ctx, slip); err != nil {
		return nil, "", fmt.Errorf("failed to hydrate slip with component states: %w", err)
	}

	return slip, matchedCommit, nil
}

// FindAllByCommits finds all slips matching any commit in the ordered list.
// Returns slips ordered by commit priority (first matching commit's slip first).
func (s *ClickHouseStore) FindAllByCommits(
	ctx context.Context,
	repository string,
	commits []string,
) (results []SlipWithCommit, err error) {
	if s.pipelineConfig == nil {
		return nil, fmt.Errorf("pipeline config is required for store operations")
	}

	if len(commits) == 0 {
		return nil, nil // No commits to search, return empty slice (not an error)
	}

	query := s.queryBuilder.BuildFindAllByCommitsQuery()

	rows, err := s.session.QueryWithArgs(ctx, query,
		ch.Named("repository", repository),
		ch.Named("commits", commits),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query slips by commits: %w", err)
	}
	defer func() {
		closeErr := rows.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close rows: %w", closeErr)
		}
	}()

	for rows.Next() {
		slip, matchedCommit, scanErr := s.scanner.ScanSlipWithMatchFromRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan slip from rows: %w", scanErr)
		}
		if err := s.hydrateSlip(ctx, slip); err != nil {
			return nil, fmt.Errorf("failed to hydrate slip with component states: %w", err)
		}
		results = append(results, SlipWithCommit{Slip: slip, MatchedCommit: matchedCommit})
	}

	return results, nil
}

// Update persists changes to an existing slip using VersionedCollapsingMergeTree semantics.
// This inserts two rows: a cancel row (sign=-1) with the current max version, and a new row (sign=1) with incremented version.
//
// Optimistic Locking: If the version in ClickHouse differs from the slip's version (indicating
// another process modified the slip), this method returns ErrVersionConflict. Callers should
// reload the slip and retry their operation.
//
// Atomic Version Increment: The version is fetched atomically from ClickHouse using max(version)
// to prevent race conditions between concurrent updates.
func (s *ClickHouseStore) Update(ctx context.Context, slip *Slip) error {
	// Get the current max version atomically from ClickHouse
	currentVersion, err := s.getMaxVersion(ctx, slip.CorrelationID)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	// Optimistic locking: verify version hasn't changed since the slip was loaded
	if currentVersion != slip.Version {
		return fmt.Errorf("%w: expected version %d, but found version %d",
			ErrVersionConflict, slip.Version, currentVersion)
	}

	// Update updated_at timestamp
	slip.UpdatedAt = time.Now()

	// Create a deep copy for the cancel row to prevent shared map/slice references
	cancelSlip := deepCopySlip(slip)
	cancelSlip.Sign = -1
	cancelSlip.Version = currentVersion // Use the atomically fetched version

	if err := s.insertRow(ctx, cancelSlip); err != nil {
		return fmt.Errorf("failed to insert cancel row: %w", err)
	}

	// Insert the new state with atomically incremented version (sign=1)
	slip.Sign = 1
	slip.Version = currentVersion + 1

	if err := s.insertRow(ctx, slip); err != nil {
		return fmt.Errorf("failed to insert new row: %w", err)
	}

	// Run OPTIMIZE TABLE if enabled
	if s.optimizeAfterWrite {
		if err := s.OptimizeTable(ctx); err != nil {
			return fmt.Errorf("failed to optimize table after update: %w", err)
		}
	}

	return nil
}

// UpdateStep updates a specific step's status with automatic retry on version conflicts.
// The correlationID is the unique identifier for the routing slip.
// If a concurrent modification is detected, the slip is reloaded and the update is retried.
func (s *ClickHouseStore) UpdateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
) error {
	// Handle component-level updates via event sourcing to avoid write contention.
	// This writes to slip_component_states using ReplacingMergeTree.
	if componentName != "" {
		// First, insert the component state into the event sourcing table
		if err := s.insertComponentState(ctx, correlationID, stepName, componentName, status); err != nil {
			return err
		}

		// Now update the aggregate status in the routing_slips table.
		// This ensures the slip reflects the current state of all components.
		return s.updateAggregateStatusFromComponentStates(ctx, correlationID, stepName)
	}

	var lastErr error

	for attempt := 0; attempt <= s.maxUpdateRetries; attempt++ {
		// Load the current slip (reload on retry to get latest version)
		slip, err := s.Load(ctx, correlationID)
		if err != nil {
			return err
		}

		now := time.Now()

		// Update pipeline-level steps
		s.updatePipelineStep(slip, stepName, status, now)

		err = s.Update(ctx, slip)
		if err == nil {
			return nil // Success
		}

		// Check if this is a version conflict error
		if errors.Is(err, ErrVersionConflict) {
			lastErr = err
			continue // Retry with fresh data
		}

		// Non-retryable error
		return err
	}

	// All retries exhausted
	return fmt.Errorf("%w: last error: %w", ErrMaxRetriesExceeded, lastErr)
}

// UpdateComponentStatus updates a component's step status with automatic retry on version conflicts.
// The correlationID is the unique identifier for the routing slip.
func (s *ClickHouseStore) UpdateComponentStatus(
	ctx context.Context,
	correlationID, componentName, stepType string,
	status StepStatus,
) error {
	return s.UpdateStep(ctx, correlationID, stepType, componentName, status)
}

// AppendHistory adds a state history entry to the slip with automatic retry on version conflicts.
// The correlationID is the unique identifier for the routing slip.
func (s *ClickHouseStore) AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error {
	var lastErr error

	for attempt := 0; attempt <= s.maxUpdateRetries; attempt++ {
		slip, err := s.Load(ctx, correlationID)
		if err != nil {
			return err
		}

		slip.StateHistory = append(slip.StateHistory, entry)

		err = s.Update(ctx, slip)
		if err == nil {
			return nil // Success
		}

		// Check if this is a version conflict error
		if errors.Is(err, ErrVersionConflict) {
			lastErr = err
			continue // Retry with fresh data
		}

		// Non-retryable error
		return err
	}

	// All retries exhausted
	return fmt.Errorf("%w: last error: %w", ErrMaxRetriesExceeded, lastErr)
}

// Close releases any resources held by the store.
func (s *ClickHouseStore) Close() error {
	return s.session.Close()
}

// getMaxVersion retrieves the current maximum version for a slip from ClickHouse.
// This is used for atomic version increment to prevent race conditions.
func (s *ClickHouseStore) getMaxVersion(ctx context.Context, correlationID string) (uint32, error) {
	query := fmt.Sprintf(
		"SELECT max(version) FROM %s.%s WHERE %s = ?",
		s.database, TableRoutingSlips, ColumnCorrelationID,
	)

	row := s.session.QueryRow(ctx, query, correlationID)

	var maxVersion sql.NullInt64
	if err := row.Scan(&maxVersion); err != nil {
		return 0, fmt.Errorf("failed to scan max version: %w", err)
	}

	if !maxVersion.Valid {
		return 0, fmt.Errorf("no rows found for correlation_id %s", correlationID)
	}

	return uint32(maxVersion.Int64), nil
}

// insertRow is an internal method that inserts a single row without OPTIMIZE.
// Used by both Create (for new slips) and Update (for cancel and new rows).
func (s *ClickHouseStore) insertRow(ctx context.Context, slip *Slip) error {
	if s.pipelineConfig == nil {
		return fmt.Errorf("pipeline config is required for store operations")
	}

	// Build dynamic column lists using query builder
	stepColumns, stepPlaceholders, stepValues := s.queryBuilder.BuildStepColumnsAndValues(slip.Steps)
	aggregateColumns, aggregatePlaceholders, aggregateValues := s.queryBuilder.BuildAggregateColumnsAndValues(
		slip.Aggregates,
	)

	// Serialize step details (timing, actor, errors)
	stepDetailsJSON, err := json.Marshal(s.buildStepDetails(slip))
	if err != nil {
		return fmt.Errorf("failed to marshal step details: %w", err)
	}

	// Serialize state history wrapped in object for ClickHouse JSON compatibility
	stateHistoryWrapper := map[string]interface{}{"entries": slip.StateHistory}
	stateHistoryJSON, err := json.Marshal(stateHistoryWrapper)
	if err != nil {
		return fmt.Errorf("failed to marshal state history: %w", err)
	}

	// Serialize ancestry wrapped in object for ClickHouse JSON compatibility
	ancestryWrapper := map[string]interface{}{"chain": slip.Ancestry}
	ancestryJSON, err := json.Marshal(ancestryWrapper)
	if err != nil {
		return fmt.Errorf("failed to marshal ancestry: %w", err)
	}

	// Build the INSERT query dynamically
	var columns []string
	var placeholders []string
	var values []interface{}

	// Core columns (order must match scanner expectations)
	columns = append(columns, ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails, ColumnStateHistory, ColumnAncestry,
		ColumnSign, ColumnVersion)
	placeholders = append(placeholders, "?", "?", "?", "?", "?", "?", "?", "?", "?", "?", "?", "?")
	values = append(values,
		slip.CorrelationID,
		slip.Repository,
		slip.Branch,
		slip.CommitSHA,
		slip.CreatedAt,
		slip.UpdatedAt,
		string(slip.Status),
		string(stepDetailsJSON),
		string(stateHistoryJSON),
		string(ancestryJSON),
		slip.Sign,
		slip.Version,
	)

	// Step status columns
	columns = append(columns, stepColumns...)
	placeholders = append(placeholders, stepPlaceholders...)
	values = append(values, stepValues...)

	// Aggregate JSON columns
	columns = append(columns, aggregateColumns...)
	placeholders = append(placeholders, aggregatePlaceholders...)
	values = append(values, aggregateValues...)

	query := s.queryBuilder.BuildInsertQuery(columns, placeholders)

	if err := s.session.ExecWithArgs(ctx, query, values...); err != nil {
		return fmt.Errorf("failed to insert slip row: %w", err)
	}

	return nil
}

// scanSlip executes a query and scans the result into a Slip.
func (s *ClickHouseStore) scanSlip(ctx context.Context, query string, args ...interface{}) (*Slip, error) {
	row := s.session.QueryRow(ctx, query, args...)
	return s.scanner.ScanSlipFromRow(row)
}

// buildStepDetails builds the step_details JSON from the slip.
func (s *ClickHouseStore) buildStepDetails(slip *Slip) map[string]interface{} {
	details := make(map[string]interface{})

	for stepName, step := range slip.Steps {
		stepDetail := make(map[string]interface{})

		if step.StartedAt != nil {
			stepDetail["started_at"] = step.StartedAt.Format(time.RFC3339Nano)
		}
		if step.CompletedAt != nil {
			stepDetail["completed_at"] = step.CompletedAt.Format(time.RFC3339Nano)
		}
		if step.Actor != "" {
			stepDetail["actor"] = step.Actor
		}
		if step.Error != "" {
			stepDetail["error"] = step.Error
		}
		if step.HeldReason != "" {
			stepDetail["held_reason"] = step.HeldReason
		}

		if len(stepDetail) > 0 {
			details[stepName] = stepDetail
		}
	}

	return details
}

// updatePipelineStep updates a pipeline step's status if it exists.
func (s *ClickHouseStore) updatePipelineStep(slip *Slip, stepName string, status StepStatus, now time.Time) {
	if s.pipelineConfig.GetStep(stepName) == nil {
		return
	}

	step := slip.Steps[stepName]
	step.ApplyStatusTransition(status, now)
	slip.Steps[stepName] = step
}

// deepCopySlip creates a deep copy of a Slip to prevent shared map/slice references.
// This is important for VersionedCollapsingMergeTree where we insert cancel rows
// that must be independent from the new rows.
func deepCopySlip(slip *Slip) *Slip {
	if slip == nil {
		return nil
	}

	cpy := &Slip{
		CorrelationID: slip.CorrelationID,
		Repository:    slip.Repository,
		Branch:        slip.Branch,
		CommitSHA:     slip.CommitSHA,
		CreatedAt:     slip.CreatedAt,
		UpdatedAt:     slip.UpdatedAt,
		Status:        slip.Status,
		PromotedTo:    slip.PromotedTo,
		Sign:          slip.Sign,
		Version:       slip.Version,
	}

	// Deep copy steps map
	if slip.Steps != nil {
		cpy.Steps = make(map[string]Step, len(slip.Steps))
		for k, v := range slip.Steps {
			cpy.Steps[k] = v
		}
	}

	// Deep copy aggregates
	if slip.Aggregates != nil {
		cpy.Aggregates = make(map[string][]ComponentStepData)
		for k, v := range slip.Aggregates {
			componentData := make([]ComponentStepData, len(v))
			copy(componentData, v)
			cpy.Aggregates[k] = componentData
		}
	}

	// Deep copy state history
	if slip.StateHistory != nil {
		cpy.StateHistory = make([]StateHistoryEntry, len(slip.StateHistory))
		copy(cpy.StateHistory, slip.StateHistory)
	}

	// Deep copy ancestry
	if slip.Ancestry != nil {
		cpy.Ancestry = make([]AncestryEntry, len(slip.Ancestry))
		copy(cpy.Ancestry, slip.Ancestry)
	}

	return cpy
}

// computeAggregateStatus determines the aggregate status from component statuses.
func (s *ClickHouseStore) computeAggregateStatus(componentData []ComponentStepData) StepStatus {
	allCompleted := true
	anyRunning := false
	anyFailed := false

	for _, comp := range componentData {
		if comp.Status.IsFailure() {
			anyFailed = true
		}
		if !comp.Status.IsSuccess() {
			allCompleted = false
		}
		if comp.Status.IsRunning() {
			anyRunning = true
		}
	}

	if anyFailed {
		return StepStatusFailed
	}
	if allCompleted {
		return StepStatusCompleted
	}
	if anyRunning {
		return StepStatusRunning
	}
	return StepStatusPending
}

// insertComponentState inserts a new state for a component into the event sourcing table.
func (s *ClickHouseStore) insertComponentState(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.%s (correlation_id, step, component, status, message, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
	`, s.database, TableSlipComponentStates)

	// Note: We don't have a message passed in UpdateStep/UpdateComponentStatus signature currently.
	// We pass empty string for message.
	err := s.session.ExecWithArgs(ctx, query,
		correlationID,
		stepName,
		componentName,
		string(status),
		"", // message
		time.Now(),
	)
	if err != nil {
		return err
	}

	// Force merge to deduplicate rows in ReplacingMergeTree immediately.
	// This ensures subsequent reads see the latest state without needing FINAL in SELECT.
	return s.optimizeComponentStatesTable(ctx)
}

// optimizeComponentStatesTable forces a merge on the slip_component_states table.
// This is necessary because ReplacingMergeTree deduplicates asynchronously during background merges.
// Running OPTIMIZE TABLE FINAL ensures the latest state is immediately visible to subsequent queries.
func (s *ClickHouseStore) optimizeComponentStatesTable(ctx context.Context) error {
	query := fmt.Sprintf(`OPTIMIZE TABLE %s.%s FINAL`, s.database, TableSlipComponentStates)
	return s.session.Exec(ctx, query)
}

// updateAggregateStatusFromComponentStates loads the slip, hydrates it with component states,
// and persists the updated aggregate status back to the routing_slips table.
// This is called after a component state update to ensure the slip reflects the current aggregate status.
func (s *ClickHouseStore) updateAggregateStatusFromComponentStates(
	ctx context.Context,
	correlationID, stepName string,
) error {
	var lastErr error

	for attempt := 0; attempt <= s.maxUpdateRetries; attempt++ {
		// Load the slip (this will hydrate component states automatically)
		slip, err := s.Load(ctx, correlationID)
		if err != nil {
			return fmt.Errorf("failed to load slip for aggregate update: %w", err)
		}

		// Determine the aggregate step name. The stepName could be either:
		// 1. The component step name (e.g., "build") - need to look up the aggregate step
		// 2. The aggregate step name itself (e.g., "builds_completed") - use directly
		aggregateStepName := ""
		if s.pipelineConfig != nil {
			// First, try to get aggregate step from component step name
			aggregateStepName = s.pipelineConfig.GetAggregateStep(stepName)
			if aggregateStepName == "" {
				// If not found, check if the step name IS an aggregate step
				if s.pipelineConfig.IsAggregateStep(stepName) {
					aggregateStepName = stepName
				}
			}
		}
		if aggregateStepName == "" {
			// No aggregate step configured for this step, nothing to update
			return nil
		}

		// The slip was already hydrated by Load(), so the step status should reflect
		// the computed aggregate from all component states.
		// Now persist this back to the database.
		err = s.Update(ctx, slip)
		if err == nil {
			return nil // Success
		}

		// Check if this is a version conflict error
		if errors.Is(err, ErrVersionConflict) {
			lastErr = err
			continue // Retry with fresh data
		}

		// Non-retryable error
		return err
	}

	return fmt.Errorf("%w: last error: %w", ErrMaxRetriesExceeded, lastErr)
}

// hydrateSlip fetches component states from the event sourcing table and merges them into the slip.
// This allows us to maintain accurate component state without updating the main slip row for every component change.
func (s *ClickHouseStore) hydrateSlip(ctx context.Context, slip *Slip) error {
	if slip == nil {
		return nil
	}

	states, err := s.loadComponentStates(ctx, slip.CorrelationID)
	if err != nil {
		return err
	}

	if len(states) == 0 {
		return nil
	}

	// Group states by component step -> component
	stateMap := make(map[string]map[string]componentStateRow)
	for _, state := range states {
		if _, ok := stateMap[state.Step]; !ok {
			stateMap[state.Step] = make(map[string]componentStateRow)
		}
		stateMap[state.Step][state.Component] = state
	}

	// Update aggregates in the slip
	for stepNameFromDB, stepStates := range stateMap {
		// Determine the aggregate step name. The step name from the database could be either:
		// 1. The component step name (e.g., "build") - need to look up the aggregate step
		// 2. The aggregate step name itself (e.g., "builds_completed") - use directly
		aggregateStepName := ""
		if s.pipelineConfig != nil {
			// First, try to get aggregate step from component step name
			aggregateStepName = s.pipelineConfig.GetAggregateStep(stepNameFromDB)
			if aggregateStepName == "" {
				// If not found, check if the step name IS an aggregate step
				if s.pipelineConfig.IsAggregateStep(stepNameFromDB) {
					aggregateStepName = stepNameFromDB
				}
			}
		}
		if aggregateStepName == "" {
			// No aggregate step configured for this step
			continue
		}

		// The aggregate JSON column name is the aggregate step name (e.g., "builds_completed")
		aggregateColumn := aggregateStepName
		componentDataList, ok := slip.Aggregates[aggregateColumn]
		if !ok {
			continue
		}

		updated := false
		var maxTime time.Time

		// Update each component's status if we have a newer state
		for i, comp := range componentDataList {
			newState, exists := stepStates[comp.Component]
			if !exists {
				continue
			}

			// Update component data
			slip.Aggregates[aggregateColumn][i].Status = StepStatus(newState.Status)
			if newState.Message != "" {
				slip.Aggregates[aggregateColumn][i].Error = newState.Message
			}

			// Infer timestamps
			// Since we only have the latest timestamp, we use it for both StartedAt (if running)
			// and CompletedAt (if terminal), essentially updating the "last transition time".
			ts := newState.Timestamp
			if ts.After(maxTime) {
				maxTime = ts
			}

			if StepStatus(newState.Status).IsRunning() && slip.Aggregates[aggregateColumn][i].StartedAt == nil {
				slip.Aggregates[aggregateColumn][i].StartedAt = &ts
			}
			if StepStatus(newState.Status).IsTerminal() && slip.Aggregates[aggregateColumn][i].CompletedAt == nil {
				slip.Aggregates[aggregateColumn][i].CompletedAt = &ts
			}

			updated = true
		}

		if updated {
			// Recompute the step status based on updated components
			newStatus := s.computeAggregateStatus(slip.Aggregates[aggregateColumn])

			// Update the step status only if the step exists.
			step, ok := slip.Steps[aggregateStepName]
			if !ok {
				continue
			}
			// We use the timestamp of the latest component update as the transition time
			step.ApplyStatusTransition(newStatus, maxTime)
			slip.Steps[aggregateStepName] = step
		}
	}

	return nil
}

type componentStateRow struct {
	Step      string    `ch:"step"`
	Component string    `ch:"component"`
	Status    string    `ch:"status"`
	Message   string    `ch:"message"`
	Timestamp time.Time `ch:"timestamp"`
}

// loadComponentStates fetches the latest state for all components of a slip.
func (s *ClickHouseStore) loadComponentStates(
	ctx context.Context,
	correlationID string,
) (results []componentStateRow, err error) {
	// We want the latest state for each component.
	// ReplacingMergeTree eventually deduplicates, but we use argMax to be sure given we might read unmerged parts.
	// Note: We alias the max(timestamp) column as 'latest_ts' to avoid conflict with the 'timestamp' column
	// used inside argMax functions. ClickHouse would otherwise interpret the alias as a nested aggregate.
	query := fmt.Sprintf(`
		SELECT
			step,
			component,
			argMax(status, timestamp) as status,
			argMax(message, timestamp) as message,
			max(timestamp) as latest_ts
		FROM %s.%s
		WHERE correlation_id = ?
		GROUP BY step, component
	`, s.database, TableSlipComponentStates)

	rows, err := s.session.QueryWithArgs(ctx, query, correlationID)
	if err != nil {
		return nil, fmt.Errorf("failed to query component states: %w", err)
	}
	defer func() {
		closeErr := rows.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close rows: %w", closeErr)
		}
	}()

	for rows.Next() {
		var row componentStateRow
		if err := rows.Scan(&row.Step, &row.Component, &row.Status, &row.Message, &row.Timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan component state: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate component states: %w", err)
	}
	return results, nil
}

// Ensure ClickHouseStore implements SlipStore.
var _ SlipStore = (*ClickHouseStore)(nil)
