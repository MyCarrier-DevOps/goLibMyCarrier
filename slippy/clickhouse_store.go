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
	session, err := ch.NewClickhouseSession(config, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse session: %w", err)
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
	}

	// Run migrations unless explicitly skipped
	if !opts.SkipMigrations {
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
		optimizeAfterWrite: true, // Default to true for normal operations
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
		optimizeAfterWrite: true, // Default to true for normal operations
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
	return s.scanSlip(ctx, query, correlationID)
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
	return s.scanSlip(ctx, query, repository, commitSHA)
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
		results = append(results, SlipWithCommit{Slip: slip, MatchedCommit: matchedCommit})
	}

	return results, nil
}

// Update persists changes to an existing slip using VersionedCollapsingMergeTree semantics.
// This inserts two rows: a cancel row (sign=-1) with the old version, and a new row (sign=1) with incremented version.
func (s *ClickHouseStore) Update(ctx context.Context, slip *Slip) error {
	// Update updated_at timestamp
	slip.UpdatedAt = time.Now()

	// Store the current version before incrementing
	oldVersion := slip.Version

	// First, insert a cancel row with the old version (sign=-1)
	cancelSlip := *slip // shallow copy
	cancelSlip.Sign = -1
	cancelSlip.Version = oldVersion

	if err := s.insertRow(ctx, &cancelSlip); err != nil {
		return fmt.Errorf("failed to insert cancel row: %w", err)
	}

	// Then, insert the new state with incremented version (sign=1)
	slip.Sign = 1
	slip.Version = oldVersion + 1

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

// UpdateStep updates a specific step's status.
// The correlationID is the unique identifier for the routing slip.
func (s *ClickHouseStore) UpdateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
) error {
	// Load the current slip
	slip, err := s.Load(ctx, correlationID)
	if err != nil {
		return err
	}

	now := time.Now()

	// Handle component-level updates for aggregate steps
	if componentName != "" {
		if err := s.updateComponentInAggregate(slip, stepName, componentName, status, now); err == nil {
			// Component was updated, also update the aggregate step status
			// The step's Aggregates field tells us which aggregate step to update
			stepConfig := s.pipelineConfig.GetStep(stepName)
			if stepConfig != nil && stepConfig.Aggregates != "" {
				// Column name is the step name (e.g., "builds")
				s.updateAggregateStatus(slip, stepConfig.Aggregates, stepName)
			}
		}
	}

	// Update pipeline-level steps
	s.updatePipelineStep(slip, stepName, status, now)

	return s.Update(ctx, slip)
}

// UpdateComponentStatus updates a component's step status.
// The correlationID is the unique identifier for the routing slip.
func (s *ClickHouseStore) UpdateComponentStatus(
	ctx context.Context,
	correlationID, componentName, stepType string,
	status StepStatus,
) error {
	return s.UpdateStep(ctx, correlationID, stepType, componentName, status)
}

// AppendHistory adds a state history entry to the slip.
// The correlationID is the unique identifier for the routing slip.
func (s *ClickHouseStore) AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error {
	slip, err := s.Load(ctx, correlationID)
	if err != nil {
		return err
	}

	slip.StateHistory = append(slip.StateHistory, entry)
	return s.Update(ctx, slip)
}

// Close releases any resources held by the store.
func (s *ClickHouseStore) Close() error {
	return s.session.Close()
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

// updateComponentInAggregate updates a component's status within an aggregate column.
// Returns nil if successful, error if the step is not a component-level step with aggregates.
func (s *ClickHouseStore) updateComponentInAggregate(
	slip *Slip,
	stepName, componentName string,
	status StepStatus,
	now time.Time,
) error {
	// Get the step config to check if it has aggregate data
	stepConfig := s.pipelineConfig.GetStep(stepName)
	if stepConfig == nil {
		return fmt.Errorf("step %s not found", stepName)
	}

	// Check if this step has component-level aggregates
	if stepConfig.Aggregates == "" {
		return fmt.Errorf("step %s does not have component aggregates", stepName)
	}

	// Column name is the step name itself (e.g., "builds")
	columnName := stepName

	if slip.Aggregates == nil {
		slip.Aggregates = make(map[string][]ComponentStepData)
	}

	componentData := slip.Aggregates[columnName]
	found := false
	for i := range componentData {
		if componentData[i].Component == componentName {
			componentData[i].ApplyStatusTransition(status, now)
			found = true
			break
		}
	}

	if !found {
		newData := ComponentStepData{Component: componentName}
		newData.ApplyStatusTransition(status, now)
		componentData = append(componentData, newData)
	}

	slip.Aggregates[columnName] = componentData
	return nil
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

// updateAggregateStatus updates the aggregate step status based on component statuses.
func (s *ClickHouseStore) updateAggregateStatus(slip *Slip, aggregateStepName, columnName string) {
	componentData := slip.Aggregates[columnName]
	if len(componentData) == 0 {
		return
	}

	// Determine aggregate status from components
	aggregateStatus := s.computeAggregateStatus(componentData)
	if aggregateStatus == StepStatusPending {
		return // No change needed
	}

	step := slip.Steps[aggregateStepName]
	step.ApplyStatusTransition(aggregateStatus, time.Now())
	slip.Steps[aggregateStepName] = step
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

// Ensure ClickHouseStore implements SlipStore.
var _ SlipStore = (*ClickHouseStore)(nil)
