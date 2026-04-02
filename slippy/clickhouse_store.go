package slippy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator"
)

const (
	// slipNotFoundBaseDelay is the multiplier (in minutes) for linear backoff
	// when waiting for a slip to be created. Uses formula: base * retry * minute
	// Retry 1: 5min, Retry 2: 10min, Retry 3: 15min (30min total wait)
	slipNotFoundBaseDelay = 5

	// slipNotFoundMaxRetries is the maximum number of retries when slip doesn't exist.
	// With linear backoff (5min + 10min + 15min), this gives ~30 minutes total wait time.
	slipNotFoundMaxRetries = 3

	// aggregateConflictMaxRetries is the maximum number of retries when a concurrent
	// aggregate write-back supersedes our row before we can re-read and rewrite.
	// Uses exponential backoff: 10ms, 20ms, 40ms, 80ms, 160ms (310ms total).
	aggregateConflictMaxRetries = 5

	// aggregateConflictBaseDelayMs is the base delay in milliseconds for conflict retries.
	aggregateConflictBaseDelayMs = 10

	// historyConflictMaxRetries is the maximum number of retries when a concurrent writer
	// inserts a higher-version routing_slips row after our loadStateHistoryFromDB read,
	// causing our appended history entry to be superseded. Reuses the same exponential
	// backoff sequence as aggregateConflictMaxRetries. History is best-effort; exhaustion
	// returns nil rather than an error.
	historyConflictMaxRetries = 5
)

// calculateSlipNotFoundBackoff calculates the backoff duration for slip-not-found retries.
// Uses linear backoff: 5min for 1st retry, 10min for 2nd, 15min for 3rd.
// Formula: slipNotFoundBaseDelay * retryNumber * time.Minute
// where retryNumber is 1-indexed (1, 2, 3).
func calculateSlipNotFoundBackoff(retryNumber int) time.Duration {
	if retryNumber < 1 {
		retryNumber = 1
	}
	if retryNumber > slipNotFoundMaxRetries {
		retryNumber = slipNotFoundMaxRetries
	}
	return time.Duration(slipNotFoundBaseDelay*retryNumber) * time.Minute
}

// calculateAggregateConflictBackoff returns exponential backoff for aggregate write-back
// conflicts. retryNumber is 1-indexed; the sequence is 10ms, 20ms, 40ms, 80ms, 160ms.
func calculateAggregateConflictBackoff(retryNumber int) time.Duration {
	if retryNumber < 1 {
		retryNumber = 1
	}
	if retryNumber > aggregateConflictMaxRetries {
		retryNumber = aggregateConflictMaxRetries
	}
	shift := retryNumber - 1
	return time.Duration(aggregateConflictBaseDelayMs<<shift) * time.Millisecond
}

// ClickHouseStore implements SlipStore using ClickHouse as the backend.
// The store uses correlation_id as the unique identifier for routing slips,
// consistent with MyCarrier's organization-wide use of correlation_id to
// identify jobs across all systems.
//
// The store is config-driven: the pipeline configuration determines which
// step columns exist and how they are queried.
type ClickHouseStore struct {
	session           ch.ClickhouseSessionInterface
	pipelineConfig    *PipelineConfig
	database          string
	queryBuilder      *SlipQueryBuilder
	scanner           *SlipScanner
	logger            Logger        // Logger for operations (defaults to NopLogger)
	hasAncestryColumn atomic.Bool   // false after migration v10 drops the ancestry column
	hasImageTagColumn atomic.Bool   // false when slip_component_states lacks image_tag column (pre-v11)
	lastVersion       atomic.Uint64 // per-store monotonic version generator; guarantees increasing versions within this process, but not global uniqueness across processes/hosts
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

	// Default to NopLogger if no logger provided
	storeLogger := NopLogger()
	if opts.Logger != nil {
		storeLogger = opts.Logger
	}

	store := &ClickHouseStore{
		session:        session,
		pipelineConfig: opts.PipelineConfig,
		database:       opts.Database,
		queryBuilder:   NewSlipQueryBuilder(opts.PipelineConfig, opts.Database),
		scanner:        NewSlipScanner(opts.PipelineConfig),
		logger:         storeLogger,
	}
	store.hasAncestryColumn.Store(true) // conservative default; updated by detectAncestryColumn after migrations
	store.hasImageTagColumn.Store(true) // conservative default; updated by detectImageTagColumn after migrations

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

	// Detect whether the ancestry column is still present (dropped by migration v10).
	store.detectAncestryColumn(ctx)

	// Detect whether the image_tag column exists (added by migration v11).
	store.detectImageTagColumn(ctx)

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
	store := &ClickHouseStore{
		session:        session,
		pipelineConfig: pipelineConfig,
		database:       database,
		queryBuilder:   NewSlipQueryBuilder(pipelineConfig, database),
		scanner:        NewSlipScanner(pipelineConfig),
		logger:         NopLogger(),
	}
	// conservative default; insertAtomicHistoryUpdate self-heals on first call if v10 has already dropped the column
	store.hasAncestryColumn.Store(true)
	store.hasImageTagColumn.Store(true)
	return store
}

// NewClickHouseStoreFromConn creates a store from an existing driver connection.
// Migrations are NOT run automatically when using this constructor.
// This is provided for backward compatibility with existing code.
func NewClickHouseStoreFromConn(conn ch.Conn, pipelineConfig *PipelineConfig, database string) *ClickHouseStore {
	if database == "" {
		database = "ci"
	}
	store := &ClickHouseStore{
		session:        ch.NewSessionFromConn(conn),
		pipelineConfig: pipelineConfig,
		database:       database,
		queryBuilder:   NewSlipQueryBuilder(pipelineConfig, database),
		scanner:        NewSlipScanner(pipelineConfig),
		logger:         NopLogger(),
	}
	// conservative default; insertAtomicHistoryUpdate self-heals on first call if v10 has already dropped the column
	store.hasAncestryColumn.Store(true)
	store.hasImageTagColumn.Store(true)
	return store
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
// For VersionedCollapsingMergeTree, this inserts a row with sign=1 and version calculated atomically.
func (s *ClickHouseStore) Create(ctx context.Context, slip *Slip) error {
	if s.pipelineConfig == nil {
		return fmt.Errorf("pipeline config is required for store operations")
	}

	// Set default sign for new slips
	if slip.Sign == 0 {
		slip.Sign = 1
	}

	version := s.nextVersion()

	if err := s.insertRow(ctx, slip, version); err != nil {
		return fmt.Errorf("failed to create slip: %w", err)
	}

	// Update the slip's version to the generated value
	slip.Version = version

	return nil
}

// Load retrieves a slip by its correlation ID.
// The correlation_id is the unique identifier for routing slips and is used
// organization-wide to identify jobs across all systems.
func (s *ClickHouseStore) Load(ctx context.Context, correlationID string) (*Slip, error) {
	if s.pipelineConfig == nil {
		return nil, fmt.Errorf("pipeline config is required for store operations")
	}

	// Only select active rows (sign=1) to exclude orphaned cancel rows.
	// Order by version DESC to get the latest version in case of uncollapsed duplicates.
	query := s.queryBuilder.BuildSelectQuery("WHERE correlation_id = ? AND sign = 1", "ORDER BY version DESC LIMIT 1")
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

	// Only select active rows (sign=1) to exclude orphaned cancel rows.
	// Order by version DESC to get the latest version.
	query := s.queryBuilder.BuildSelectQuery(
		"WHERE repository = ? AND commit_sha = ? AND sign = 1",
		"ORDER BY version DESC LIMIT 1",
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
// This inserts a cancel row (sign=-1) for the slip's current version and a new row (sign=1)
// with a new nanosecond timestamp version.
//
// With epoch-based versioning (nanoseconds since epoch), each update generates a unique
// version that is naturally ordered by time. Reads use ORDER BY version DESC LIMIT 1
// to get the latest row, implementing "last write wins" semantics.
//
// No explicit version verification is needed because:
// - Each writer generates a unique timestamp-based version
// - Later writes naturally have higher versions and supersede earlier ones
// - This is not a "conflict" - it's the expected behavior for concurrent writes
// - The write itself always succeeds; it may just be superseded by a later write
func (s *ClickHouseStore) Update(ctx context.Context, slip *Slip) error {
	// Update updated_at timestamp
	slip.UpdatedAt = time.Now()
	slip.Sign = 1

	// Store the old version for the cancel row
	oldVersion := slip.Version

	newVersion := s.nextVersion()

	// Insert both cancel row and new row atomically
	if err := s.insertAtomicUpdateWithVersions(ctx, slip, oldVersion, newVersion); err != nil {
		return fmt.Errorf("failed to insert atomic update: %w", err)
	}

	// Update the slip's version to the new value
	slip.Version = newVersion

	return nil
}

// UpdateStep updates a specific step's status.
// The correlationID is the unique identifier for the routing slip.
//
// All updates — both component-level (componentName != "") and pipeline-level
// (componentName == "") — are written to slip_component_states via event sourcing.
// This eliminates the Read-Modify-Write race on routing_slips under concurrent writers:
// each insert into the append-only event log is conflict-free, and hydrateSlip derives
// the authoritative step status on every Load.
func (s *ClickHouseStore) UpdateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
) error {
	// Write the step event to the conflict-free event-sourcing table.
	// Pipeline-level steps use componentName="" as a sentinel value.
	if err := s.insertComponentState(ctx, correlationID, stepName, componentName, status, "", ""); err != nil {
		return err
	}

	// Trigger aggregate write-back to routing_slips when there is an aggregate to update:
	//   - componentName != "":  a component step that rolls up into an aggregate step.
	//   - IsAggregateStep:      this step IS the aggregate itself (write-back keeps the
	//                           routing_slips row consistent with the event store).
	// For pure pipeline steps (non-aggregate, no component), the event log is the sole
	// source of truth; hydrateSlip derives the step status on every Load, so no
	// write-back to routing_slips is needed.
	if componentName != "" || (s.pipelineConfig != nil && s.pipelineConfig.IsAggregateStep(stepName)) {
		return s.updateAggregateStatusFromComponentStates(ctx, correlationID, stepName)
	}

	return nil
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

// UpdateStepWithHistory updates a step's status AND appends a history entry.
//
// The step status is written to slip_component_states via the same conflict-free
// event-sourcing path used by UpdateStep (both component-level and pipeline-level).
// The history entry is persisted:
//   - atomically alongside the aggregate write-back for component steps and aggregate steps;
//   - via AppendHistory for pure pipeline steps with no aggregate.
func (s *ClickHouseStore) UpdateStepWithHistory(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
	entry StateHistoryEntry,
) error {
	// Store the step event in the conflict-free event-sourcing table.
	// The message from the history entry is co-located in the event record.
	if err := s.insertComponentState(
		ctx, correlationID, stepName, componentName, status, entry.Message, "",
	); err != nil {
		return err
	}

	// Persist history and (where applicable) the aggregate status to routing_slips.
	//   - componentName != "":  component step → aggregate write-back carries the history.
	//   - IsAggregateStep:      aggregate step itself → aggregate write-back carries the history.
	//   - otherwise:            pure pipeline step → call AppendHistory directly.
	if componentName != "" || (s.pipelineConfig != nil && s.pipelineConfig.IsAggregateStep(stepName)) {
		return s.updateAggregateStatusFromComponentStatesWithHistory(ctx, correlationID, stepName, entry)
	}

	// Pure pipeline step: persist only the history entry to routing_slips.
	return s.AppendHistory(ctx, correlationID, entry)
}

// AppendHistory adds a state history entry to the slip.
// The correlationID is the unique identifier for the routing slip.
// Will retry if the slip doesn't exist yet (waiting for creation).
//
// Unlike a full Load+Update cycle, AppendHistory reads only the existing
// state_history column from routing_slips and then re-inserts the row
// with all other columns copied verbatim from the latest DB row.
// This prevents a concurrent step-status update from being overwritten in the
// routing_slips cache by an in-flight AppendHistory that loaded a stale snapshot.
func (s *ClickHouseStore) AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error {
	// Start tracing span for the retry operation
	retrySpan := startRetrySpan(ctx, "AppendHistory", correlationID)
	retrySpan.AddAttribute("slippy.entry_step", entry.Step)
	retrySpan.AddAttribute("slippy.entry_status", string(entry.Status))

	slipNotFoundRetry := 0    // Counter for slip-not-found retries (1-indexed when used)
	historyConflictRetry := 0 // Counter for concurrent-writer conflict retries

	// Retry loop handles slip-not-found scenarios and post-write history-conflict retries.
	for {
		// Check for context cancellation first
		if ctx.Err() != nil {
			retrySpan.EndError(ctx.Err())
			return ctx.Err()
		}

		// Load only state_history to avoid a full hydrateSlip round-trip.
		// This keeps step-status columns in the re-inserted row aligned with the
		// current DB row rather than an in-memory view that might be stale.
		existingHistoryJSON, err := s.loadStateHistoryFromDB(retrySpan.Context(), correlationID)
		if err != nil {
			if errors.Is(err, ErrSlipNotFound) {
				slipNotFoundRetry++
				if slipNotFoundRetry > slipNotFoundMaxRetries {
					err := fmt.Errorf("%w: slip not found after %d retries: %w",
						ErrMaxRetriesExceeded, slipNotFoundMaxRetries, ErrSlipNotFound)
					retrySpan.EndError(err)
					return err
				}

				backoff := calculateSlipNotFoundBackoff(slipNotFoundRetry)
				retrySpan.RecordAttempt(backoff.Milliseconds())
				retrySpan.AddAttribute("slippy.waiting_for_slip_creation", true)
				retrySpan.AddAttribute("slippy.slip_not_found_retry", slipNotFoundRetry)
				select {
				case <-ctx.Done():
					retrySpan.EndError(ctx.Err())
					return ctx.Err()
				case <-time.After(backoff):
				}
				continue
			}
			retrySpan.EndError(err)
			return err
		}

		// Deserialize existing history, append the new entry, re-serialize.
		var wrapper struct {
			Entries []StateHistoryEntry `json:"entries"`
		}
		if err := json.Unmarshal([]byte(existingHistoryJSON), &wrapper); err != nil {
			// History JSON is malformed. Returning an error here avoids silently
			// discarding existing history. The caller's retry loop can surface this
			// to the operator rather than overwriting potentially valid data.
			parseErr := fmt.Errorf("state_history JSON is malformed for %s: %w", correlationID, err)
			retrySpan.AddAttribute("slippy.history_unmarshal_error", parseErr.Error())
			retrySpan.EndError(parseErr)
			return parseErr
		}
		wrapper.Entries = append(wrapper.Entries, entry)
		newHistoryJSON, err := json.Marshal(wrapper)
		if err != nil {
			retrySpan.EndError(err)
			return fmt.Errorf("failed to marshal updated state history: %w", err)
		}

		newVersion := s.nextVersion()
		if err := s.insertAtomicHistoryUpdate(
			retrySpan.Context(), correlationID, newVersion, string(newHistoryJSON),
		); err != nil {
			retrySpan.EndError(err)
			return err
		}

		// Post-write version check: if a concurrent writer inserted a higher-version
		// routing_slips row after our loadStateHistoryFromDB read, that row wins the
		// last-write-wins merge and our appended entry is dropped. Detect and retry.
		latestVersion, versionErr := s.loadVersionFromDB(retrySpan.Context(), correlationID)
		if versionErr != nil {
			// Non-fatal: accept potential history loss if the version check fails.
			retrySpan.AddAttribute("slippy.history_version_check_error", versionErr.Error())
			retrySpan.EndSuccess()
			return nil
		}
		if latestVersion > newVersion {
			historyConflictRetry++
			retrySpan.AddAttribute("slippy.history_conflict_retry", historyConflictRetry)
			if historyConflictRetry > historyConflictMaxRetries {
				retrySpan.AddAttribute("slippy.history_conflict_retries_exhausted", true)
				retrySpan.EndSuccess()
				return nil
			}
			backoff := calculateAggregateConflictBackoff(historyConflictRetry)
			select {
			case <-ctx.Done():
				retrySpan.EndError(ctx.Err())
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}

		retrySpan.EndSuccess()
		return nil
	}
}

// Close releases any resources held by the store.
func (s *ClickHouseStore) Close() error {
	return s.session.Close()
}

// Ping verifies the ClickHouse connection is alive.
func (s *ClickHouseStore) Ping(ctx context.Context) error {
	return s.session.Ping(ctx)
}

// InsertAncestryLink writes a single direct-parent link to the slip_ancestry table.
// Each slip has at most one parent entry, keeping storage O(1) per slip.
func (s *ClickHouseStore) InsertAncestryLink(ctx context.Context, slip *Slip, parent AncestryEntry) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.%s (
			%s, %s, %s,
			%s, %s, %s,
			%s, %s, %s,
			%s
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.database, TableSlipAncestry,
		ColumnRepository, ColumnBranch, ColumnCorrelationID,
		ColumnParentCorrelationID, ColumnParentCommitSHA, ColumnParentStatus,
		ColumnParentFailedStep, ColumnParentRepository, ColumnParentBranch,
		ColumnCreatedAt,
	)

	return s.session.ExecWithArgs(ctx, query,
		slip.Repository,
		slip.Branch,
		slip.CorrelationID,
		parent.CorrelationID,
		parent.CommitSHA,
		string(parent.Status),
		parent.FailedStep,
		parent.Repository,
		parent.Branch,
		parent.CreatedAt,
	)
}

// ResolveAncestry walks the slip_ancestry table iteratively to reconstruct
// the full ancestry chain for a given slip. Returns entries ordered from
// direct parent to oldest ancestor. Stops when no more parent links are found
// or maxDepth is reached.
// Supports cross-branch ancestry: each link stores the parent's repository and
// branch, which are used for the next hop rather than the original child's values.
func (s *ClickHouseStore) ResolveAncestry(
	ctx context.Context,
	repository, branch, correlationID string,
	maxDepth int,
) ([]AncestryEntry, error) {
	query := fmt.Sprintf(`
		SELECT %s, %s, %s, %s, %s, %s, %s
		FROM %s.%s
		WHERE %s = ? AND %s = ? AND %s = ?
		ORDER BY %s DESC
		LIMIT 1
	`,
		ColumnParentCorrelationID, ColumnParentCommitSHA, ColumnParentStatus,
		ColumnParentFailedStep, ColumnParentRepository, ColumnParentBranch,
		ColumnCreatedAt,
		s.database, TableSlipAncestry,
		ColumnRepository, ColumnBranch, ColumnCorrelationID,
		ColumnCreatedAt,
	)

	var chain []AncestryEntry
	currentID := correlationID
	currentRepo := repository
	currentBranch := branch

	for range maxDepth {
		row := s.session.QueryRow(ctx, query, currentRepo, currentBranch, currentID)

		var entry AncestryEntry
		var statusStr string
		if err := row.Scan(
			&entry.CorrelationID,
			&entry.CommitSHA,
			&statusStr,
			&entry.FailedStep,
			&entry.Repository,
			&entry.Branch,
			&entry.CreatedAt,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			return nil, fmt.Errorf("scanning ancestry row: %w", err)
		}
		entry.Status = SlipStatus(statusStr)

		chain = append(chain, entry)
		currentID = entry.CorrelationID
		currentRepo = entry.Repository
		currentBranch = entry.Branch
	}

	return chain, nil
}

// SetComponentImageTag records the container image tag for a component by inserting a new
// event-sourcing row that retains the component's current status and adds the image tag.
// This replaces the previous Load→modify→Update RMW pattern with a conflict-free append.
func (s *ClickHouseStore) SetComponentImageTag(
	ctx context.Context,
	correlationID, stepName, componentName, imageTag string,
) error {
	if !s.hasImageTagColumn.Load() {
		return fmt.Errorf(
			"image_tag column not available (migration v11 not yet applied): cannot set image tag for %s",
			componentName,
		)
	}

	originalStepName := stepName

	// Normalize stepName: the event log records rows with the component step type
	// (e.g. "build"), but callers may pass an aggregate step name (e.g. "builds_completed").
	// Translate aggregate step names to the component step type before querying.
	if s.pipelineConfig != nil && s.pipelineConfig.IsAggregateStep(stepName) {
		if componentStep := s.pipelineConfig.GetComponentStep(stepName); componentStep != "" {
			stepName = componentStep
		}
	}

	currentStatus, err := s.queryComponentStatus(ctx, correlationID, stepName, componentName)
	if err != nil {
		return err
	}

	// If normalized name found nothing and we did normalize, try the original name as fallback.
	if currentStatus == "" && originalStepName != stepName {
		currentStatus, err = s.queryComponentStatus(ctx, correlationID, originalStepName, componentName)
		if err != nil {
			return err
		}
	}

	if currentStatus == "" {
		return fmt.Errorf("component %s not found in event log for step %s",
			componentName, stepName)
	}

	return s.insertComponentState(ctx, correlationID, stepName, componentName,
		StepStatus(currentStatus), "", imageTag)
}

// nextVersion returns a monotonically increasing version based on the current nanosecond
// timestamp for this ClickHouseStore instance. Under concurrency, if two callers arrive in
// the same nanosecond, the second caller gets lastVersion+1 instead of a duplicate
// timestamp. This guarantees per-instance monotonic and unique versions suitable for
// VersionedCollapsingMergeTree ordering within a single writer, but does not provide
// global uniqueness across processes or hosts.
func (s *ClickHouseStore) nextVersion() uint64 {
	candidate := uint64(time.Now().UnixNano())
	for {
		prev := s.lastVersion.Load()
		if candidate <= prev {
			candidate = prev + 1
		}
		if s.lastVersion.CompareAndSwap(prev, candidate) {
			return candidate
		}
	}
}

// queryComponentStatus reads the latest status for a component from the event log.
// Returns empty string (not an error) when no matching rows exist.
func (s *ClickHouseStore) queryComponentStatus(
	ctx context.Context,
	correlationID, stepName, componentName string,
) (string, error) {
	query := fmt.Sprintf(`
		SELECT argMax(status, timestamp)
		FROM %s.%s
		WHERE correlation_id = ? AND step = ? AND component = ?
	`, s.database, TableSlipComponentStates)

	row := s.session.QueryRow(ctx, query, correlationID, stepName, componentName)
	var currentStatus string
	if err := row.Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read current status for component %s: %w", componentName, err)
	}
	return currentStatus, nil
}

// detectAncestryColumn queries system.columns to determine whether the ancestry column
// is still present in routing_slips. After migration v10 (drop_ancestry_column) is applied,
// the column no longer exists and insertAtomicHistoryUpdate must omit it from query column lists.
// On any query error the column is assumed present (conservative default avoids data loss).
func (s *ClickHouseStore) detectAncestryColumn(ctx context.Context) {
	query := fmt.Sprintf(
		"SELECT count() FROM system.columns WHERE database = ? AND table = 'routing_slips' AND name = '%s'",
		ColumnAncestry,
	)
	row := s.session.QueryRow(ctx, query, s.database)
	if row == nil {
		// Conservative default when the session returns no row descriptor.
		s.hasAncestryColumn.Store(true)
		return
	}
	var count uint64
	if err := row.Scan(&count); err != nil {
		// Conservative default: assume column exists to avoid data loss on transient errors.
		// Log so operators can investigate if ancestry queries fail later.
		s.logger.Warn(ctx, "ancestry column probe failed; assuming ancestry column exists", map[string]interface{}{
			"error": err.Error(),
		})
		s.hasAncestryColumn.Store(true)
		return
	}
	s.hasAncestryColumn.Store(count > 0)
}

// detectImageTagColumn queries system.columns to determine whether the image_tag column
// exists in slip_component_states. Before migration v11 (add_image_tag_to_component_states)
// the column does not exist and insertComponentState must omit it.
// On any query error the column is assumed present (conservative default).
func (s *ClickHouseStore) detectImageTagColumn(ctx context.Context) {
	query := "SELECT count() FROM system.columns WHERE database = ? AND table = 'slip_component_states' AND name = 'image_tag'"
	row := s.session.QueryRow(ctx, query, s.database)
	if row == nil {
		s.hasImageTagColumn.Store(true)
		return
	}
	var count uint64
	if err := row.Scan(&count); err != nil {
		s.logger.Warn(ctx, "image_tag column probe failed; assuming column exists", map[string]interface{}{
			"error": err.Error(),
		})
		s.hasImageTagColumn.Store(true)
		return
	}
	s.hasImageTagColumn.Store(count > 0)
}

// isAncestryColumnError reports whether err indicates ClickHouse does not recognise the ancestry
// column. This is used as a runtime fallback: when detectAncestryColumn conservatively assumed the
// column exists (e.g. on a transient probe error) but the actual INSERT/SELECT fails because
// migration v10 has already dropped it.
func isAncestryColumnError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, ColumnAncestry) &&
		(strings.Contains(msg, "Unknown identifier") ||
			strings.Contains(msg, "Missing columns") ||
			strings.Contains(msg, "UNKNOWN_IDENTIFIER"))
}

// isImageTagColumnError reports whether err indicates ClickHouse does not recognise the image_tag
// column. Used as a runtime fallback when detectImageTagColumn conservatively assumed the column
// exists but the INSERT fails because migration v11 has not yet been applied.
func isImageTagColumnError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "image_tag") &&
		(strings.Contains(msg, "Missing columns") ||
			strings.Contains(msg, "Unknown column") ||
			strings.Contains(msg, "No such column") ||
			strings.Contains(msg, "Unknown identifier") ||
			strings.Contains(msg, "UNKNOWN_IDENTIFIER"))
}

// loadStateHistoryFromDB reads only the state_history JSON column from the latest active
// routing_slips row for a given correlationID. Returns ErrSlipNotFound if no active row exists.
func (s *ClickHouseStore) loadStateHistoryFromDB(ctx context.Context, correlationID string) (string, error) {
	query := fmt.Sprintf(`
		SELECT state_history
		FROM %s.%s
		WHERE %s = ? AND %s = 1
		ORDER BY %s DESC
		LIMIT 1
	`, s.database, TableRoutingSlips, ColumnCorrelationID, ColumnSign, ColumnVersion)

	row := s.session.QueryRow(ctx, query, correlationID)
	var historyJSON string
	if err := row.Scan(&historyJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: correlation_id=%s", ErrSlipNotFound, correlationID)
		}
		return "", fmt.Errorf("failed to load state_history for %s: %w", correlationID, err)
	}
	return historyJSON, nil
}

// insertAtomicHistoryUpdate cancels all active routing_slips rows for correlationID and inserts
// a new row that is identical to the latest active row except for state_history, updated_at,
// sign, and version. All step-status and aggregate columns are copied verbatim from the DB row,
// preventing a stale in-memory snapshot from overwriting a concurrently-written step status.
func (s *ClickHouseStore) insertAtomicHistoryUpdate(
	ctx context.Context,
	correlationID string,
	newVersion uint64,
	newStateHistoryJSON string,
) error {
	if s.pipelineConfig == nil {
		return fmt.Errorf("pipeline config is required for store operations")
	}

	// Column names for the INSERT target list and SELECT expressions.
	stepColumns := s.queryBuilder.BuildStepColumns()
	aggregateColumns := s.queryBuilder.BuildAggregateColumns()

	// Snapshot the ancestry flag once so all three column lists are consistent even if
	// a concurrent caller triggers the self-healing Store(false) between list builds.
	hasAncestry := s.hasAncestryColumn.Load()

	// Build INSERT column list: fixed columns + dynamic step/aggregate columns.
	// ColumnAncestry is excluded when migration v10 has dropped it from routing_slips.
	var allCols []string
	allCols = append(allCols,
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails,
		ColumnStateHistory,
	)
	if hasAncestry {
		allCols = append(allCols, ColumnAncestry)
	}
	allCols = append(allCols, ColumnSign, ColumnVersion)
	allCols = append(allCols, stepColumns...)
	allCols = append(allCols, aggregateColumns...)

	// Cancel SELECT: re-select all columns from existing active rows with sign flipped to -1.
	cancelSelectCols := []string{
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails,
		ColumnStateHistory,
	}
	if hasAncestry {
		cancelSelectCols = append(cancelSelectCols, ColumnAncestry)
	}
	cancelSelectCols = append(cancelSelectCols,
		"-1",          // sign
		ColumnVersion, // keep original version for proper VCollapsingMergeTree collapsing
	)
	cancelSelectCols = append(cancelSelectCols, stepColumns...)
	cancelSelectCols = append(cancelSelectCols, aggregateColumns...)

	cancelQuery := fmt.Sprintf(
		"SELECT %s FROM %s.%s WHERE %s = ? AND %s < ? AND %s = 1",
		strings.Join(cancelSelectCols, ", "),
		s.database, TableRoutingSlips,
		ColumnCorrelationID, ColumnVersion, ColumnSign,
	)

	// New-row SELECT: read all columns from the latest active DB row verbatim, except:
	//   - updated_at → now64(6)
	//   - state_history → provided literal (CAST to JSON)
	//   - sign → 1
	//   - version → new timestamp-based literal
	newRowSelectCols := []string{
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, "now64(6)", // updated_at
		ColumnStatus, ColumnStepDetails,
		"CAST(? AS JSON)", // state_history override
	}
	if hasAncestry {
		newRowSelectCols = append(newRowSelectCols, ColumnAncestry) // ancestry passthrough
	}
	newRowSelectCols = append(newRowSelectCols, "1", "?") // sign=1, version=newVersion
	newRowSelectCols = append(newRowSelectCols, stepColumns...)
	newRowSelectCols = append(newRowSelectCols, aggregateColumns...)

	newRowQuery := fmt.Sprintf(
		"SELECT %s FROM %s.%s WHERE %s = ? AND %s = 1 ORDER BY %s DESC LIMIT 1",
		strings.Join(newRowSelectCols, ", "),
		s.database, TableRoutingSlips,
		ColumnCorrelationID, ColumnSign, ColumnVersion,
	)

	query := fmt.Sprintf(`
		INSERT INTO %s.%s (%s)
		%s
		UNION ALL
		%s
	`, s.database, TableRoutingSlips, strings.Join(allCols, ", "), cancelQuery, newRowQuery)

	err := s.session.ExecWithArgs(ctx, query,
		// Cancel SELECT WHERE params
		correlationID, newVersion,
		// New row SELECT literal params (state_history, version)
		newStateHistoryJSON, newVersion,
		// New row SELECT WHERE param
		correlationID,
	)
	if err != nil && hasAncestry && isAncestryColumnError(err) {
		// The probe assumed ancestry exists but ClickHouse reports an unknown column —
		// migration v10 has been applied despite the probe not seeing it (transient failure).
		// Update the cached state atomically and retry once without ancestry columns.
		s.logger.Warn(ctx, "ancestry column unknown to ClickHouse; retrying without it", map[string]interface{}{
			"error":          err.Error(),
			"correlation_id": correlationID,
		})
		s.hasAncestryColumn.Store(false)
		return s.insertAtomicHistoryUpdate(ctx, correlationID, newVersion, newStateHistoryJSON)
	}
	return err
}

// insertRow inserts a single row into the routing_slips table.
// If version is 0, the version is calculated atomically as COALESCE(MAX(version), 0) + 1.
// If version is non-zero, that specific version is used (for cancel rows in VersionedCollapsingMergeTree).
//
// VersionedCollapsingMergeTree requires:
// - Cancel rows (sign=-1) must have the SAME version as the row they cancel
// - New state rows (sign=1) get the next version
func (s *ClickHouseStore) insertRow(ctx context.Context, slip *Slip, version uint64) error {
	if s.pipelineConfig == nil {
		return fmt.Errorf("pipeline config is required for store operations")
	}

	// Build dynamic column lists using query builder
	stepColumns, _, stepValues := s.queryBuilder.BuildStepColumnsAndValues(slip.Steps)
	aggregateColumns, _, aggregateValues := s.queryBuilder.BuildAggregateColumnsAndValues(
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

	// Build column list for INSERT
	var columns []string
	columns = append(columns, ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails, ColumnStateHistory,
		ColumnSign, ColumnVersion)
	columns = append(columns, stepColumns...)
	columns = append(columns, aggregateColumns...)

	// Build SELECT expressions - all literals except version which may use a subquery
	var selectExprs []string
	var values []interface{}

	// Core columns as literals (using positional parameters)
	selectExprs = append(selectExprs, "?", "?", "?", "?", "?", "?", "?", "?", "?")
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

	// Sign as literal
	selectExprs = append(selectExprs, "?")
	values = append(values, slip.Sign)

	// Version: use provided version or calculate atomically
	if version > 0 {
		// Use the specific version provided (for cancel rows)
		selectExprs = append(selectExprs, "?")
		values = append(values, version)
	} else {
		// Calculate atomically from subquery: COALESCE(MAX(version), 0) + 1
		// This works for all cases:
		// - New slip (no existing rows): returns 0 + 1 = 1
		// - Existing slip: returns current max + 1
		// Note: This is only used by Create(). Updates use insertAtomicUpdate() which
		// handles both cancel and new rows in a single UNION ALL statement.
		selectExprs = append(selectExprs, fmt.Sprintf(
			"COALESCE((SELECT max(version) FROM %s.%s WHERE %s = ?), 0) + 1",
			s.database, TableRoutingSlips, ColumnCorrelationID,
		))
		values = append(values, slip.CorrelationID)
	}

	// Step status columns as literals
	for _, stepVal := range stepValues {
		selectExprs = append(selectExprs, "?")
		values = append(values, stepVal)
	}

	// Aggregate JSON columns as literals
	for _, aggVal := range aggregateValues {
		selectExprs = append(selectExprs, "?")
		values = append(values, aggVal)
	}

	// Build the INSERT...SELECT query
	query := fmt.Sprintf(`
		INSERT INTO %s.%s (%s)
		SELECT %s
	`, s.database, TableRoutingSlips, strings.Join(columns, ", "), strings.Join(selectExprs, ", "))

	if err := s.session.ExecWithArgs(ctx, query, values...); err != nil {
		return fmt.Errorf("failed to insert slip row: %w", err)
	}

	return nil
}

// insertAtomicUpdateWithVersions inserts cancel rows for all existing active rows and a new state row
// in a single atomic INSERT statement.
//
// This uses INSERT...SELECT...UNION ALL to:
// 1. Select all existing rows with sign=1 and version < newVersion, re-insert them with sign=-1
// 2. Insert the new row with sign=1 and the new timestamp version
//
// The query structure is:
//
//	INSERT INTO routing_slips (columns...)
//	SELECT columns..., -1 as sign, version FROM routing_slips
//	    WHERE correlation_id = ? AND version < ? AND sign = 1
//	UNION ALL
//	SELECT <new_data>, 1 as sign, <new_version>
//
// This approach:
// - Cancels ALL existing active rows (handles any uncollapsed duplicates)
// - Uses timestamp-based versions to guarantee uniqueness
// - Is fully atomic - either all rows are inserted or none
func (s *ClickHouseStore) insertAtomicUpdateWithVersions(
	ctx context.Context,
	slip *Slip,
	_, newVersion uint64,
) error {
	if s.pipelineConfig == nil {
		return fmt.Errorf("pipeline config is required for store operations")
	}

	// Build dynamic column lists using query builder
	stepColumns, _, stepValues := s.queryBuilder.BuildStepColumnsAndValues(slip.Steps)

	aggregateColumns, _, aggregateValues := s.queryBuilder.BuildAggregateColumnsAndValues(
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

	// Build column list for INSERT (must match SELECT order exactly)
	var columns []string
	columns = append(columns, ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails, ColumnStateHistory,
		ColumnSign, ColumnVersion)
	columns = append(columns, stepColumns...)
	columns = append(columns, aggregateColumns...)

	// Build the SELECT for cancel rows - select existing rows with sign=1, re-insert with sign=-1
	// We keep the original version so VersionedCollapsingMergeTree can collapse them properly
	cancelSelectColumns := []string{
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails, ColumnStateHistory,
		"-1",          // Flip sign to -1
		ColumnVersion, // Keep original version for proper collapsing
	}
	cancelSelectColumns = append(cancelSelectColumns, stepColumns...)
	cancelSelectColumns = append(cancelSelectColumns, aggregateColumns...)

	cancelQuery := fmt.Sprintf(
		"SELECT %s FROM %s.%s WHERE %s = ? AND %s < ? AND %s = 1",
		strings.Join(cancelSelectColumns, ", "),
		s.database, TableRoutingSlips,
		ColumnCorrelationID, ColumnVersion, ColumnSign,
	)

	// Build SELECT expressions for new row (sign=1, new timestamp version)
	var newSelectExprs []string
	var newValues []interface{}

	// Core columns (9 columns) - need to cast JSON columns to match existing table schema
	// ClickHouse requires explicit casting when UNION ALL combines JSON columns from table
	// with string literals
	newSelectExprs = append(newSelectExprs, "?", "?", "?", "?", "?", "?", "?",
		"CAST(? AS JSON)", // step_details - cast to JSON
		"CAST(? AS JSON)") // state_history - cast to JSON
	newValues = append(newValues,
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

	// Sign for new row: 1, Version for new row: new timestamp
	newSelectExprs = append(newSelectExprs, "1", "?")
	newValues = append(newValues, newVersion)

	// Step columns (Enum8 - no casting needed)
	for _, stepVal := range stepValues {
		newSelectExprs = append(newSelectExprs, "?")
		newValues = append(newValues, stepVal)
	}

	// Aggregate columns (JSON type - need casting to match table schema in UNION ALL)
	for _, aggVal := range aggregateValues {
		newSelectExprs = append(newSelectExprs, "CAST(? AS JSON)")
		newValues = append(newValues, aggVal)
	}

	// Combine: cancel query params (correlation_id, newVersion) + new row values
	var values []interface{}
	values = append(values, slip.CorrelationID, newVersion) // For cancel SELECT WHERE clause
	values = append(values, newValues...)

	// Build the INSERT...SELECT...UNION ALL query
	query := fmt.Sprintf(`
		INSERT INTO %s.%s (%s)
		%s
		UNION ALL
		SELECT %s
	`, s.database, TableRoutingSlips, strings.Join(columns, ", "),
		cancelQuery,
		strings.Join(newSelectExprs, ", "))

	if err := s.session.ExecWithArgs(ctx, query, values...); err != nil {
		return fmt.Errorf("failed to insert atomic update: %w", err)
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

// computeAggregateStatus determines the aggregate status from component statuses.
// The aggregate is:
// - "failed" if any component has failed
// - "completed" if all components are completed
// - "running" if any component is running OR completed (work is in progress)
// - "pending" if all components are pending (no work has started)
func (s *ClickHouseStore) computeAggregateStatus(componentData []ComponentStepData) StepStatus {
	allCompleted := true
	anyRunning := false
	anyCompleted := false
	anyFailed := false

	for _, comp := range componentData {
		if comp.Status.IsFailure() {
			anyFailed = true
		}
		if comp.Status.IsSuccess() {
			anyCompleted = true
		} else {
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
	// If any component is running or completed, the aggregate is "running" (in progress)
	if anyRunning || anyCompleted {
		return StepStatusRunning
	}
	return StepStatusPending
}

// insertComponentState inserts a new state into the event sourcing table.
// Both component-level steps (componentName != "") and pipeline-level steps
// (componentName == "") are stored here, making all step updates conflict-free
// under concurrent writers. The message is preserved for history reconstruction.
// imageTag is an optional container image tag; pass "" when not applicable.
func (s *ClickHouseStore) insertComponentState(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
	message, imageTag string,
) error {
	hasImageTag := s.hasImageTagColumn.Load()

	// Capture timestamp once so retries preserve causal ordering in the event log.
	timestamp := time.Now()

	var query string
	var args []interface{}

	if hasImageTag {
		query = fmt.Sprintf(`
			INSERT INTO %s.%s (correlation_id, step, component, status, message, image_tag, timestamp)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, s.database, TableSlipComponentStates)
		args = []interface{}{correlationID, stepName, componentName, string(status), message, imageTag, timestamp}
	} else {
		query = fmt.Sprintf(`
			INSERT INTO %s.%s (correlation_id, step, component, status, message, timestamp)
			VALUES (?, ?, ?, ?, ?, ?)
		`, s.database, TableSlipComponentStates)
		args = []interface{}{correlationID, stepName, componentName, string(status), message, timestamp}
	}

	err := s.session.ExecWithArgs(ctx, query, args...)
	if err != nil && hasImageTag && isImageTagColumnError(err) {
		s.hasImageTagColumn.Store(false)
		// If the caller explicitly set an image tag, do not silently drop it.
		// Return an error so SetComponentImageTag surfaces a clear migration-not-applied message.
		if imageTag != "" {
			return fmt.Errorf(
				"image_tag column not available (migration v11 not yet applied): "+
					"cannot persist image tag for %s", componentName,
			)
		}
		s.logger.Warn(ctx, "image_tag column missing; retrying without it", map[string]interface{}{
			"error": err.Error(),
		})
		query = fmt.Sprintf(`
			INSERT INTO %s.%s (correlation_id, step, component, status, message, timestamp)
			VALUES (?, ?, ?, ?, ?, ?)
		`, s.database, TableSlipComponentStates)
		return s.session.ExecWithArgs(ctx, query,
			correlationID, stepName, componentName, string(status), message, timestamp)
	}
	return err
}

// loadVersionFromDB fetches the current latest version of a slip from routing_slips.
// Used after an aggregate write-back to detect whether a concurrent writer superseded our row.
// This is a minimal single-column query — no hydration, no full row scan.
func (s *ClickHouseStore) loadVersionFromDB(ctx context.Context, correlationID string) (uint64, error) {
	// SELECT version FROM ... (kept on one line so test discriminators using
	// strings.Contains(query, "SELECT version FROM") still match).
	query := fmt.Sprintf(
		"SELECT %s FROM %s.%s WHERE %s = ? AND %s = 1 ORDER BY %s DESC LIMIT 1",
		ColumnVersion, s.database, TableRoutingSlips, ColumnCorrelationID, ColumnSign, ColumnVersion,
	)

	row := s.session.QueryRow(ctx, query, correlationID)

	var version uint64
	if err := row.Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrSlipNotFound
		}
		return 0, fmt.Errorf("failed to load version for conflict check: %w", err)
	}

	return version, nil
}

// updateAggregateStatusFromComponentStates loads the slip, hydrates it with component states,
// and persists the updated aggregate status back to the routing_slips table.
// This is called after a component state update to ensure the slip reflects the current aggregate status.
func (s *ClickHouseStore) updateAggregateStatusFromComponentStates(
	ctx context.Context,
	correlationID, stepName string,
) error {
	// Start tracing span for the retry operation
	retrySpan := startRetrySpan(ctx, "updateAggregateStatusFromComponentStates", correlationID)
	retrySpan.AddAttribute("slippy.step_name", stepName)

	slipNotFoundRetry := 0 // Counter for slip-not-found retries (1-indexed when used)
	conflictRetry := 0     // Counter for concurrent write-back conflict retries

	// Retry loop handles two scenarios:
	// 1. Slip not yet created (slipNotFoundRetry) — waits with linear backoff up to 30 minutes.
	// 2. Concurrent write-back conflict (conflictRetry) — a concurrent writer superseded our
	//    Update; re-Load and re-write with exponential backoff (10ms–160ms, max 5 retries).
	for {
		// Check for context cancellation first
		if ctx.Err() != nil {
			retrySpan.EndError(ctx.Err())
			return ctx.Err()
		}

		// Load the slip (this will hydrate component states automatically)
		slip, err := s.Load(retrySpan.Context(), correlationID)
		if err != nil {
			// If slip doesn't exist yet, wait for it to be created (max 3 retries)
			if errors.Is(err, ErrSlipNotFound) {
				slipNotFoundRetry++
				if slipNotFoundRetry > slipNotFoundMaxRetries {
					err := fmt.Errorf("%w: slip not found after %d retries: %w",
						ErrMaxRetriesExceeded, slipNotFoundMaxRetries, ErrSlipNotFound)
					retrySpan.EndError(err)
					return err
				}

				backoff := calculateSlipNotFoundBackoff(slipNotFoundRetry)
				retrySpan.RecordAttempt(backoff.Milliseconds())
				retrySpan.AddAttribute("slippy.waiting_for_slip_creation", true)
				retrySpan.AddAttribute("slippy.slip_not_found_retry", slipNotFoundRetry)
				// Use select to respect context cancellation during sleep
				select {
				case <-ctx.Done():
					retrySpan.EndError(ctx.Err())
					return ctx.Err()
				case <-time.After(backoff):
				}
				continue
			}
			err = fmt.Errorf("failed to load slip for aggregate update: %w", err)
			retrySpan.EndError(err)
			return err
		}

		aggregateStepName := s.resolveAggregateStepName(stepName)
		if aggregateStepName == "" {
			// No aggregate step configured for this step, nothing to update
			retrySpan.EndSuccess()
			return nil
		}

		// The slip was already hydrated by Load(), so the step status should reflect
		// the computed aggregate from all component states.
		// Now persist this back to the database.
		if err = s.Update(retrySpan.Context(), slip); err != nil {
			retrySpan.EndError(err)
			return err
		}

		// Post-write conflict check: verify that our row was not immediately superseded
		// by a concurrent aggregate write-back. ClickHouse has no atomic CAS, so we detect
		// the conflict after the fact and retry from Load if needed.
		latestVersion, err := s.loadVersionFromDB(retrySpan.Context(), correlationID)
		if err != nil {
			// Version check failure is non-fatal: the write succeeded; we just cannot
			// confirm whether it was superseded. Log and treat as success.
			retrySpan.AddAttribute("slippy.version_check_error", err.Error())
			retrySpan.EndSuccess()
			return nil
		}

		if latestVersion == slip.Version {
			// Our row is current — done.
			retrySpan.EndSuccess()
			return nil
		}

		// A concurrent writer superseded our row. Retry from Load so the final
		// write includes all component completions seen so far.
		conflictRetry++
		if conflictRetry > aggregateConflictMaxRetries {
			// Retries exhausted. The latest row in the DB was written by a concurrent
			// writer that also re-Loaded from the conflict-free event log, so it reflects
			// an up-to-date aggregate. This is a best-effort outcome, not data corruption.
			retrySpan.AddAttribute("slippy.aggregate_conflict_retries_exhausted", true)
			retrySpan.EndSuccess()
			return nil
		}

		backoff := calculateAggregateConflictBackoff(conflictRetry)
		retrySpan.RecordAttempt(backoff.Milliseconds())
		retrySpan.AddAttribute("slippy.aggregate_conflict_retry", conflictRetry)
		select {
		case <-ctx.Done():
			retrySpan.EndError(ctx.Err())
			return ctx.Err()
		case <-time.After(backoff):
		}
		// continue → re-Load, re-compute, re-write
	}
}

// updateAggregateStatusFromComponentStatesWithHistory updates the aggregate status AND appends a history entry
// in a single atomic operation. This is the component-level equivalent of UpdateStepWithHistory.
func (s *ClickHouseStore) updateAggregateStatusFromComponentStatesWithHistory(
	ctx context.Context,
	correlationID, stepName string,
	entry StateHistoryEntry,
) error {
	// Start tracing span for the retry operation
	retrySpan := startRetrySpan(ctx, "updateAggregateStatusFromComponentStatesWithHistory", correlationID)
	retrySpan.AddAttribute("slippy.step_name", stepName)
	retrySpan.AddAttribute("slippy.entry_step", entry.Step)
	retrySpan.AddAttribute("slippy.entry_status", string(entry.Status))

	slipNotFoundRetry := 0 // Counter for slip-not-found retries (1-indexed when used)
	conflictRetry := 0     // Counter for concurrent write-back conflict retries

	// Retry loop handles two scenarios:
	// 1. Slip not yet created (slipNotFoundRetry) — waits with linear backoff up to 30 minutes.
	// 2. Concurrent write-back conflict (conflictRetry) — a concurrent writer superseded our
	//    Update; re-Load and re-write with exponential backoff (10ms–160ms, max 5 retries).
	for {
		// Check for context cancellation first
		if ctx.Err() != nil {
			retrySpan.EndError(ctx.Err())
			return ctx.Err()
		}

		// Load the slip (this will hydrate component states automatically)
		slip, err := s.Load(retrySpan.Context(), correlationID)
		if err != nil {
			// If slip doesn't exist yet, wait for it to be created (max 3 retries)
			if errors.Is(err, ErrSlipNotFound) {
				slipNotFoundRetry++
				if slipNotFoundRetry > slipNotFoundMaxRetries {
					err := fmt.Errorf("%w: slip not found after %d retries: %w",
						ErrMaxRetriesExceeded, slipNotFoundMaxRetries, ErrSlipNotFound)
					retrySpan.EndError(err)
					return err
				}

				backoff := calculateSlipNotFoundBackoff(slipNotFoundRetry)
				retrySpan.RecordAttempt(backoff.Milliseconds())
				retrySpan.AddAttribute("slippy.waiting_for_slip_creation", true)
				retrySpan.AddAttribute("slippy.slip_not_found_retry", slipNotFoundRetry)
				// Use select to respect context cancellation during sleep
				select {
				case <-ctx.Done():
					retrySpan.EndError(ctx.Err())
					return ctx.Err()
				case <-time.After(backoff):
				}
				continue
			}
			err = fmt.Errorf("failed to load slip for aggregate update with history: %w", err)
			retrySpan.EndError(err)
			return err
		}

		aggregateStepName := s.resolveAggregateStepName(stepName)
		if aggregateStepName == "" {
			// No aggregate step configured for this step. The step event is already
			// persisted in slip_component_states. Append the history entry to routing_slips
			// so the audit trail is preserved. AppendHistory now performs a
			// state_history-only read and an atomic append (e.g., via UNION ALL
			// re-insert), so it does not rely on a full Load + Update RMW and
			// therefore cannot lose another step's status.
			if appendErr := s.AppendHistory(retrySpan.Context(), correlationID, entry); appendErr != nil {
				retrySpan.EndError(appendErr)
				return appendErr
			}
			retrySpan.EndSuccess()
			return nil
		}

		// Append history entry to the same slip (atomic with aggregate update)
		slip.StateHistory = append(slip.StateHistory, entry)

		// The slip was already hydrated by Load(), so the step status should reflect
		// the computed aggregate from all component states.
		// Now persist this back to the database with the history entry.
		if err = s.Update(retrySpan.Context(), slip); err != nil {
			retrySpan.EndError(err)
			return err
		}

		// Post-write conflict check: verify our row was not immediately superseded
		// by a concurrent aggregate write-back.
		latestVersion, err := s.loadVersionFromDB(retrySpan.Context(), correlationID)
		if err != nil {
			retrySpan.AddAttribute("slippy.version_check_error", err.Error())
			retrySpan.EndSuccess()
			return nil
		}

		if latestVersion == slip.Version {
			retrySpan.EndSuccess()
			return nil
		}

		// A concurrent writer superseded our row. Retry from Load.
		conflictRetry++
		if conflictRetry > aggregateConflictMaxRetries {
			retrySpan.AddAttribute("slippy.aggregate_conflict_retries_exhausted", true)
			retrySpan.EndSuccess()
			return nil
		}

		backoff := calculateAggregateConflictBackoff(conflictRetry)
		retrySpan.RecordAttempt(backoff.Milliseconds())
		retrySpan.AddAttribute("slippy.aggregate_conflict_retry", conflictRetry)
		select {
		case <-ctx.Done():
			retrySpan.EndError(ctx.Err())
			return ctx.Err()
		case <-time.After(backoff):
		}
		// continue → re-Load, re-compute, re-write
	}
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

	// Ensure Aggregates map is initialized
	if slip.Aggregates == nil {
		slip.Aggregates = make(map[string][]ComponentStepData)
	}

	// First pass: apply pipeline-level step events (component="" sentinel).
	// These come from UpdateStep/UpdateStepWithHistory calls for non-component steps
	// and represent the authoritative step status stored in the event log.
	// Applying them before the aggregate loop prevents the aggregate loop from
	// treating component="" as an actual component name.
	for _, state := range states {
		if state.Component != "" {
			continue
		}
		step, ok := slip.Steps[state.Step]
		if !ok {
			continue
		}
		step.ApplyStatusTransition(StepStatus(state.Status), state.Timestamp)
		slip.Steps[state.Step] = step
	}

	// Second pass: group component-level states (component != "") by resolved aggregate step -> component.
	// Pipeline-level events (component="") are excluded here; they were handled above.
	// Normalize step names to their aggregate column name so that historical data written
	// under different aliases (e.g. "build" vs "builds_completed") is merged into a single
	// aggregate entry and applyComponentStatesToAggregate runs exactly once per aggregate.
	stateMap := make(map[string]map[string]componentStateRow)
	for _, state := range states {
		if state.Component == "" {
			continue // already applied in first pass
		}
		aggregateKey := s.resolveAggregateStepName(state.Step)
		if aggregateKey == "" {
			continue // no aggregate configured for this step
		}
		if _, ok := stateMap[aggregateKey]; !ok {
			stateMap[aggregateKey] = make(map[string]componentStateRow)
		}
		// Keep the latest state for each component. If the same component appears
		// under multiple step name aliases, the row with the latest timestamp wins.
		if existing, exists := stateMap[aggregateKey][state.Component]; exists {
			if state.Timestamp.After(existing.Timestamp) {
				stateMap[aggregateKey][state.Component] = state
			}
		} else {
			stateMap[aggregateKey][state.Component] = state
		}
	}

	// Update aggregates in the slip — stateMap is keyed by resolved aggregate
	// step name so each aggregate is computed exactly once.
	for aggregateStepName, stepStates := range stateMap {
		aggregateColumn := aggregateStepName
		s.applyComponentStatesToAggregate(slip, aggregateColumn, aggregateStepName, stepStates)
	}

	return nil
}

// resolveAggregateStepName maps a database step name to its aggregate step name.
// It handles both the component step type (e.g. "build") and the aggregate step name
// itself (e.g. "builds_completed"), returning "" if no aggregate is configured.
func (s *ClickHouseStore) resolveAggregateStepName(stepNameFromDB string) string {
	if s.pipelineConfig == nil {
		return ""
	}
	// First, try to get aggregate step from component step name
	aggregateStepName := s.pipelineConfig.GetAggregateStep(stepNameFromDB)
	if aggregateStepName != "" {
		return aggregateStepName
	}
	// If not found, check if the step name IS an aggregate step
	if s.pipelineConfig.IsAggregateStep(stepNameFromDB) {
		return stepNameFromDB
	}
	return ""
}

// applyComponentStatesToAggregate updates the aggregate data in slip for the given
// aggregateColumn (step key in slip.Aggregates) using the provided component states.
// It updates existing component entries or adds new ones, then recomputes the aggregate
// step status from active components only.
func (s *ClickHouseStore) applyComponentStatesToAggregate(
	slip *Slip,
	aggregateColumn, aggregateStepName string,
	stepStates map[string]componentStateRow,
) {
	// Get or create the component data list for this aggregate
	componentDataList := slip.Aggregates[aggregateColumn]

	// Build a map of existing components for quick lookup
	existingComponents := make(map[string]int) // component name -> index
	for i, comp := range componentDataList {
		existingComponents[comp.Component] = i
	}

	updated := false
	var maxTime time.Time

	// Track the components that have actual state from the event sourcing table.
	// These are the source of truth for aggregate status calculation.
	// Original placeholder components (from pipeline config) that have no matching
	// state entries should NOT be considered when computing aggregate status.
	activeComponents := make([]ComponentStepData, 0, len(stepStates))

	// Process each component state
	for componentName, state := range stepStates {
		ts := state.Timestamp
		if ts.After(maxTime) {
			maxTime = ts
		}

		compData := buildComponentData(componentName, state)

		// Track this component for aggregate status calculation
		activeComponents = append(activeComponents, compData)

		if idx, exists := existingComponents[componentName]; exists {
			updateExistingComponent(&slip.Aggregates[aggregateColumn][idx], compData)
		} else {
			// Add new component entry — handles the case where the aggregate was empty
			// ({"items":[]}) but component states exist in the event sourcing table.
			slip.Aggregates[aggregateColumn] = append(slip.Aggregates[aggregateColumn], compData)
		}
		updated = true
	}

	if !updated {
		return
	}

	// Recompute the step status based on ACTIVE components only.
	// Active components are those with entries in the component_states table.
	// This excludes original placeholder components that have different names
	// from the actual workflow component names.
	newStatus := s.computeAggregateStatus(activeComponents)

	step, ok := slip.Steps[aggregateStepName]
	if !ok {
		return
	}
	// We use the timestamp of the latest component update as the transition time
	step.ApplyStatusTransition(newStatus, maxTime)
	slip.Steps[aggregateStepName] = step
}

// buildComponentData constructs a ComponentStepData value from a raw componentStateRow.
func buildComponentData(componentName string, state componentStateRow) ComponentStepData {
	ts := state.Timestamp
	compData := ComponentStepData{
		Component: componentName,
		Status:    StepStatus(state.Status),
		ImageTag:  state.ImageTag,
	}
	// Only populate Error for failure statuses; non-failure messages (e.g. progress
	// notes on a running step) should not appear as errors in downstream consumers.
	if state.Message != "" && StepStatus(state.Status).IsFailure() {
		compData.Error = state.Message
	}
	if StepStatus(state.Status).IsRunning() {
		compData.StartedAt = &ts
	}
	if StepStatus(state.Status).IsTerminal() {
		compData.CompletedAt = &ts
	}
	return compData
}

// updateExistingComponent merges updated fields from src into dest, preserving
// existing non-zero values where the src field is zero.
func updateExistingComponent(dest *ComponentStepData, src ComponentStepData) {
	dest.Status = src.Status
	// When the new status is a failure, propagate the error message.
	// When transitioning away from failure (e.g. a retry succeeds), clear any
	// stale error so observers do not see incorrect error information.
	if src.Status.IsFailure() {
		if src.Error != "" {
			dest.Error = src.Error
		}
	} else {
		dest.Error = ""
	}
	if src.ImageTag != "" {
		dest.ImageTag = src.ImageTag
	}
	if src.StartedAt != nil && dest.StartedAt == nil {
		dest.StartedAt = src.StartedAt
	}
	if src.CompletedAt != nil {
		if dest.CompletedAt == nil || src.CompletedAt.After(*dest.CompletedAt) {
			dest.CompletedAt = src.CompletedAt
		}
	}
}

type componentStateRow struct {
	Step      string    `ch:"step"`
	Component string    `ch:"component"`
	Status    string    `ch:"status"`
	Message   string    `ch:"message"`
	ImageTag  string    `ch:"image_tag"`
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
			argMax(image_tag, timestamp) as image_tag,
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
		if err := rows.Scan(
			&row.Step, &row.Component, &row.Status, &row.Message, &row.ImageTag, &row.Timestamp,
		); err != nil {
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
