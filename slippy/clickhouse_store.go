package slippy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
)

// ClickHouseStore implements SlipStore using ClickHouse as the backend.
// The store uses correlation_id as the unique identifier for routing slips,
// consistent with MyCarrier's organization-wide use of correlation_id to
// identify jobs across all systems.
type ClickHouseStore struct {
	session ch.ClickhouseSessionInterface
}

// ClickHouseStoreOptions configures the ClickHouse store.
type ClickHouseStoreOptions struct {
	// SkipMigrations if true, skips running migrations during initialization
	SkipMigrations bool

	// MigrateOptions configures migration behavior (only used if SkipMigrations is false)
	MigrateOptions MigrateOptions
}

// NewClickHouseStoreFromConfig creates a new ClickHouse-backed slip store from config.
// By default, this runs all pending migrations to ensure the schema is up to date.
func NewClickHouseStoreFromConfig(config *ch.ClickhouseConfig, opts ClickHouseStoreOptions) (*ClickHouseStore, error) {
	ctx := context.Background()
	session, err := ch.NewClickhouseSession(config, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClickHouse session: %w", err)
	}

	// Run migrations unless explicitly skipped
	if !opts.SkipMigrations {
		if _, err := RunMigrations(ctx, session.Conn(), opts.MigrateOptions); err != nil {
			session.Close()
			return nil, fmt.Errorf("failed to run migrations: %w", err)
		}
	}

	return &ClickHouseStore{session: session}, nil
}

// NewClickHouseStoreFromSession creates a store from an existing session.
// Migrations are NOT run automatically when using this constructor.
// Use RunMigrations explicitly if needed.
func NewClickHouseStoreFromSession(session ch.ClickhouseSessionInterface) *ClickHouseStore {
	return &ClickHouseStore{session: session}
}

// NewClickHouseStoreFromConn creates a store from an existing driver connection.
// Migrations are NOT run automatically when using this constructor.
// This is provided for backward compatibility with existing code.
func NewClickHouseStoreFromConn(conn ch.Conn) *ClickHouseStore {
	return &ClickHouseStore{session: ch.NewSessionFromConn(conn)}
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

// Create persists a new routing slip.
// The slip's CorrelationID is used as the unique identifier.
func (s *ClickHouseStore) Create(ctx context.Context, slip *Slip) error {
	// Serialize components to JSON
	componentsJSON, err := json.Marshal(slip.Components)
	if err != nil {
		return fmt.Errorf("failed to marshal components: %w", err)
	}

	// Serialize step timestamps
	stepTimestamps := make(map[string]interface{})
	for name, step := range slip.Steps {
		ts := make(map[string]interface{})
		if step.StartedAt != nil {
			ts["started_at"] = step.StartedAt.Format(time.RFC3339Nano)
		}
		if step.CompletedAt != nil {
			ts["completed_at"] = step.CompletedAt.Format(time.RFC3339Nano)
		}
		if len(ts) > 0 {
			stepTimestamps[name] = ts
		}
	}
	stepTimestampsJSON, err := json.Marshal(stepTimestamps)
	if err != nil {
		return fmt.Errorf("failed to marshal step timestamps: %w", err)
	}

	// Serialize state history
	stateHistoryJSON, err := json.Marshal(slip.StateHistory)
	if err != nil {
		return fmt.Errorf("failed to marshal state history: %w", err)
	}

	// Extract step statuses
	getStepStatus := func(name string) string {
		if step, ok := slip.Steps[name]; ok {
			return string(step.Status)
		}
		return string(StepStatusPending)
	}

	query := `
		INSERT INTO ci.routing_slips (
			correlation_id, repository, branch, commit_sha,
			created_at, updated_at, status, components,
			push_parsed_status, builds_completed_status, unit_tests_completed_status,
			secret_scan_completed_status, dev_deploy_status, dev_tests_status,
			preprod_deploy_status, preprod_tests_status, prod_release_created_status,
			prod_deploy_status, prod_tests_status, alert_gate_status, prod_steady_state_status,
			step_timestamps, state_history
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	err = s.session.ExecWithArgs(ctx, query,
		slip.CorrelationID,
		slip.Repository,
		slip.Branch,
		slip.CommitSHA,
		slip.CreatedAt,
		slip.UpdatedAt,
		string(slip.Status),
		string(componentsJSON),
		getStepStatus("push_parsed"),
		getStepStatus("builds_completed"),
		getStepStatus("unit_tests_completed"),
		getStepStatus("secret_scan_completed"),
		getStepStatus("dev_deploy"),
		getStepStatus("dev_tests"),
		getStepStatus("preprod_deploy"),
		getStepStatus("preprod_tests"),
		getStepStatus("prod_release_created"),
		getStepStatus("prod_deploy"),
		getStepStatus("prod_tests"),
		getStepStatus("alert_gate"),
		getStepStatus("prod_steady_state"),
		string(stepTimestampsJSON),
		string(stateHistoryJSON),
	)

	if err != nil {
		return fmt.Errorf("failed to insert slip: %w", err)
	}

	return nil
}

// Load retrieves a slip by its correlation ID.
// The correlation_id is the unique identifier for routing slips and is used
// organization-wide to identify jobs across all systems.
func (s *ClickHouseStore) Load(ctx context.Context, correlationID string) (*Slip, error) {
	query := `
		SELECT 
			correlation_id, repository, branch, commit_sha,
			created_at, updated_at, status, components,
			push_parsed_status, builds_completed_status, unit_tests_completed_status,
			secret_scan_completed_status, dev_deploy_status, dev_tests_status,
			preprod_deploy_status, preprod_tests_status, prod_release_created_status,
			prod_deploy_status, prod_tests_status, alert_gate_status, prod_steady_state_status,
			step_timestamps, state_history
		FROM ci.routing_slips FINAL
		WHERE correlation_id = ?
		LIMIT 1
	`

	return s.scanSlip(ctx, query, correlationID)
}

// LoadByCommit retrieves a slip by repository and commit SHA.
func (s *ClickHouseStore) LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error) {
	query := `
		SELECT 
			correlation_id, repository, branch, commit_sha,
			created_at, updated_at, status, components,
			push_parsed_status, builds_completed_status, unit_tests_completed_status,
			secret_scan_completed_status, dev_deploy_status, dev_tests_status,
			preprod_deploy_status, preprod_tests_status, prod_release_created_status,
			prod_deploy_status, prod_tests_status, alert_gate_status, prod_steady_state_status,
			step_timestamps, state_history
		FROM ci.routing_slips FINAL
		WHERE repository = ? AND commit_sha = ?
		ORDER BY created_at DESC
		LIMIT 1
	`

	return s.scanSlip(ctx, query, repository, commitSHA)
}

// FindByCommits finds a slip matching any commit in the ordered list.
// Returns the slip for the first (most recent) matching commit.
func (s *ClickHouseStore) FindByCommits(ctx context.Context, repository string, commits []string) (*Slip, string, error) {
	if len(commits) == 0 {
		return nil, "", fmt.Errorf("no commits provided")
	}

	// Build query with ordered commit matching using arrayJoin
	query := `
		WITH commits AS (
			SELECT 
				arrayJoin(range(1, length({commits:Array(String)}) + 1)) AS priority,
				{commits:Array(String)}[priority] AS commit_sha
		)
		SELECT 
			s.correlation_id, s.repository, s.branch, s.commit_sha,
			s.created_at, s.updated_at, s.status, s.components,
			s.push_parsed_status, s.builds_completed_status, s.unit_tests_completed_status,
			s.secret_scan_completed_status, s.dev_deploy_status, s.dev_tests_status,
			s.preprod_deploy_status, s.preprod_tests_status, s.prod_release_created_status,
			s.prod_deploy_status, s.prod_tests_status, s.alert_gate_status, s.prod_steady_state_status,
			s.step_timestamps, s.state_history,
			c.commit_sha AS matched_commit
		FROM ci.routing_slips s FINAL
		INNER JOIN commits c ON s.commit_sha = c.commit_sha
		WHERE s.repository = {repository:String}
		ORDER BY c.priority ASC
		LIMIT 1
	`

	row := s.session.QueryRow(ctx, query,
		ch.Named("repository", repository),
		ch.Named("commits", commits),
	)

	slip, matchedCommit, err := s.scanSlipWithMatch(row)
	if err != nil {
		if err == sql.ErrNoRows {
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
func (s *ClickHouseStore) UpdateStep(ctx context.Context, correlationID, stepName, componentName string, status StepStatus) error {
	// Load the current slip
	slip, err := s.Load(ctx, correlationID)
	if err != nil {
		return err
	}

	// Update the step
	now := time.Now()
	if componentName != "" {
		// Component-specific step (build or unit_test)
		for i := range slip.Components {
			if slip.Components[i].Name == componentName {
				switch stepName {
				case "build":
					slip.Components[i].BuildStatus = status
				case "unit_test":
					slip.Components[i].UnitTestStatus = status
				}
				break
			}
		}
	}

	// Always update the step map
	step := slip.Steps[stepName]
	step.Status = status
	if status == StepStatusRunning && step.StartedAt == nil {
		step.StartedAt = &now
	}
	if status.IsTerminal() && step.CompletedAt == nil {
		step.CompletedAt = &now
	}
	slip.Steps[stepName] = step

	return s.Update(ctx, slip)
}

// UpdateComponentStatus updates a component's build or test status.
// The correlationID is the unique identifier for the routing slip.
func (s *ClickHouseStore) UpdateComponentStatus(ctx context.Context, correlationID, componentName, stepType string, status StepStatus) error {
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

	var slip Slip
	var statusStr string
	var componentsJSON, stepTimestampsJSON, stateHistoryJSON string
	var stepStatuses [13]string

	err := row.Scan(
		&slip.CorrelationID,
		&slip.Repository,
		&slip.Branch,
		&slip.CommitSHA,
		&slip.CreatedAt,
		&slip.UpdatedAt,
		&statusStr,
		&componentsJSON,
		&stepStatuses[0],  // push_parsed
		&stepStatuses[1],  // builds_completed
		&stepStatuses[2],  // unit_tests_completed
		&stepStatuses[3],  // secret_scan_completed
		&stepStatuses[4],  // dev_deploy
		&stepStatuses[5],  // dev_tests
		&stepStatuses[6],  // preprod_deploy
		&stepStatuses[7],  // preprod_tests
		&stepStatuses[8],  // prod_release_created
		&stepStatuses[9],  // prod_deploy
		&stepStatuses[10], // prod_tests
		&stepStatuses[11], // alert_gate
		&stepStatuses[12], // prod_steady_state
		&stepTimestampsJSON,
		&stateHistoryJSON,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSlipNotFound
		}
		return nil, fmt.Errorf("failed to scan slip: %w", err)
	}

	// Parse status
	slip.Status = SlipStatus(statusStr)

	// Parse components JSON
	if err := json.Unmarshal([]byte(componentsJSON), &slip.Components); err != nil {
		return nil, fmt.Errorf("failed to unmarshal components: %w", err)
	}

	// Build steps map from denormalized columns
	slip.Steps = map[string]Step{
		"push_parsed":           {Status: StepStatus(stepStatuses[0])},
		"builds_completed":      {Status: StepStatus(stepStatuses[1])},
		"unit_tests_completed":  {Status: StepStatus(stepStatuses[2])},
		"secret_scan_completed": {Status: StepStatus(stepStatuses[3])},
		"dev_deploy":            {Status: StepStatus(stepStatuses[4])},
		"dev_tests":             {Status: StepStatus(stepStatuses[5])},
		"preprod_deploy":        {Status: StepStatus(stepStatuses[6])},
		"preprod_tests":         {Status: StepStatus(stepStatuses[7])},
		"prod_release_created":  {Status: StepStatus(stepStatuses[8])},
		"prod_deploy":           {Status: StepStatus(stepStatuses[9])},
		"prod_tests":            {Status: StepStatus(stepStatuses[10])},
		"alert_gate":            {Status: StepStatus(stepStatuses[11])},
		"prod_steady_state":     {Status: StepStatus(stepStatuses[12])},
	}

	// Parse step timestamps and merge into steps
	var stepTimestamps map[string]map[string]string
	if err := json.Unmarshal([]byte(stepTimestampsJSON), &stepTimestamps); err == nil {
		for name, ts := range stepTimestamps {
			if step, ok := slip.Steps[name]; ok {
				if startedStr, ok := ts["started_at"]; ok {
					if t, err := time.Parse(time.RFC3339Nano, startedStr); err == nil {
						step.StartedAt = &t
					}
				}
				if completedStr, ok := ts["completed_at"]; ok {
					if t, err := time.Parse(time.RFC3339Nano, completedStr); err == nil {
						step.CompletedAt = &t
					}
				}
				slip.Steps[name] = step
			}
		}
	}

	// Parse state history JSON
	if err := json.Unmarshal([]byte(stateHistoryJSON), &slip.StateHistory); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state history: %w", err)
	}

	return &slip, nil
}

// scanSlipWithMatch scans a slip with an additional matched_commit column.
func (s *ClickHouseStore) scanSlipWithMatch(row ch.Row) (*Slip, string, error) {
	var slip Slip
	var statusStr string
	var componentsJSON, stepTimestampsJSON, stateHistoryJSON string
	var stepStatuses [13]string
	var matchedCommit string

	err := row.Scan(
		&slip.CorrelationID,
		&slip.Repository,
		&slip.Branch,
		&slip.CommitSHA,
		&slip.CreatedAt,
		&slip.UpdatedAt,
		&statusStr,
		&componentsJSON,
		&stepStatuses[0],
		&stepStatuses[1],
		&stepStatuses[2],
		&stepStatuses[3],
		&stepStatuses[4],
		&stepStatuses[5],
		&stepStatuses[6],
		&stepStatuses[7],
		&stepStatuses[8],
		&stepStatuses[9],
		&stepStatuses[10],
		&stepStatuses[11],
		&stepStatuses[12],
		&stepTimestampsJSON,
		&stateHistoryJSON,
		&matchedCommit,
	)

	if err != nil {
		return nil, "", err
	}

	// Parse status
	slip.Status = SlipStatus(statusStr)

	// Parse components JSON
	if err := json.Unmarshal([]byte(componentsJSON), &slip.Components); err != nil {
		return nil, "", fmt.Errorf("failed to unmarshal components: %w", err)
	}

	// Build steps map
	slip.Steps = map[string]Step{
		"push_parsed":           {Status: StepStatus(stepStatuses[0])},
		"builds_completed":      {Status: StepStatus(stepStatuses[1])},
		"unit_tests_completed":  {Status: StepStatus(stepStatuses[2])},
		"secret_scan_completed": {Status: StepStatus(stepStatuses[3])},
		"dev_deploy":            {Status: StepStatus(stepStatuses[4])},
		"dev_tests":             {Status: StepStatus(stepStatuses[5])},
		"preprod_deploy":        {Status: StepStatus(stepStatuses[6])},
		"preprod_tests":         {Status: StepStatus(stepStatuses[7])},
		"prod_release_created":  {Status: StepStatus(stepStatuses[8])},
		"prod_deploy":           {Status: StepStatus(stepStatuses[9])},
		"prod_tests":            {Status: StepStatus(stepStatuses[10])},
		"alert_gate":            {Status: StepStatus(stepStatuses[11])},
		"prod_steady_state":     {Status: StepStatus(stepStatuses[12])},
	}

	// Parse step timestamps
	var stepTimestamps map[string]map[string]string
	if err := json.Unmarshal([]byte(stepTimestampsJSON), &stepTimestamps); err == nil {
		for name, ts := range stepTimestamps {
			if step, ok := slip.Steps[name]; ok {
				if startedStr, ok := ts["started_at"]; ok {
					if t, err := time.Parse(time.RFC3339Nano, startedStr); err == nil {
						step.StartedAt = &t
					}
				}
				if completedStr, ok := ts["completed_at"]; ok {
					if t, err := time.Parse(time.RFC3339Nano, completedStr); err == nil {
						step.CompletedAt = &t
					}
				}
				slip.Steps[name] = step
			}
		}
	}

	// Parse state history
	if err := json.Unmarshal([]byte(stateHistoryJSON), &slip.StateHistory); err != nil {
		return nil, "", fmt.Errorf("failed to unmarshal state history: %w", err)
	}

	return &slip, matchedCommit, nil
}

// Ensure ClickHouseStore implements SlipStore.
var _ SlipStore = (*ClickHouseStore)(nil)
