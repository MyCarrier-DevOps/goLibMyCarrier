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
	session        ch.ClickhouseSessionInterface
	pipelineConfig *PipelineConfig
	database       string
	queryBuilder   *SlipQueryBuilder
	scanner        *SlipScanner
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

	store := &ClickHouseStore{
		session:        session,
		pipelineConfig: opts.PipelineConfig,
		database:       opts.Database,
		queryBuilder:   NewSlipQueryBuilder(opts.PipelineConfig, opts.Database),
		scanner:        NewSlipScanner(opts.PipelineConfig),
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
func NewClickHouseStoreFromSession(
	session ch.ClickhouseSessionInterface,
	pipelineConfig *PipelineConfig,
	database string,
) *ClickHouseStore {
	if database == "" {
		database = "ci"
	}
	return &ClickHouseStore{
		session:        session,
		pipelineConfig: pipelineConfig,
		database:       database,
		queryBuilder:   NewSlipQueryBuilder(pipelineConfig, database),
		scanner:        NewSlipScanner(pipelineConfig),
	}
}

// NewClickHouseStoreFromConn creates a store from an existing driver connection.
// Migrations are NOT run automatically when using this constructor.
// This is provided for backward compatibility with existing code.
func NewClickHouseStoreFromConn(conn ch.Conn, pipelineConfig *PipelineConfig, database string) *ClickHouseStore {
	if database == "" {
		database = "ci"
	}
	return &ClickHouseStore{
		session:        ch.NewSessionFromConn(conn),
		pipelineConfig: pipelineConfig,
		database:       database,
		queryBuilder:   NewSlipQueryBuilder(pipelineConfig, database),
		scanner:        NewSlipScanner(pipelineConfig),
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
func (s *ClickHouseStore) Create(ctx context.Context, slip *Slip) error {
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

	// Serialize state history
	stateHistoryJSON, err := json.Marshal(slip.StateHistory)
	if err != nil {
		return fmt.Errorf("failed to marshal state history: %w", err)
	}

	// Build the INSERT query dynamically
	var columns []string
	var placeholders []string
	var values []interface{}

	// Core columns
	columns = append(columns, ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails, ColumnStateHistory)
	placeholders = append(placeholders, "?", "?", "?", "?", "?", "?", "?", "?", "?")
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
		return fmt.Errorf("failed to insert slip: %w", err)
	}

	return nil
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

// Update persists changes to an existing slip.
func (s *ClickHouseStore) Update(ctx context.Context, slip *Slip) error {
	// Update updated_at timestamp
	slip.UpdatedAt = time.Now()

	// Use the same insert logic (ReplacingMergeTree handles upserts)
	return s.Create(ctx, slip)
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
			// Component was updated, also update the aggregate
			aggregateStep := s.pipelineConfig.GetAggregateStep(stepName)
			if aggregateStep != "" {
				s.updateAggregateStatus(slip, aggregateStep, pluralize(stepName))
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
// Returns nil if successful, error if the step is not an aggregate step.
func (s *ClickHouseStore) updateComponentInAggregate(
	slip *Slip,
	stepName, componentName string,
	status StepStatus,
	now time.Time,
) error {
	aggregateStep := s.pipelineConfig.GetAggregateStep(stepName)
	if aggregateStep == "" {
		return fmt.Errorf("step %s is not a component step", stepName)
	}

	columnName := pluralize(stepName)
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
