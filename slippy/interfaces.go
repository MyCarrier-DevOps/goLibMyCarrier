package slippy

import (
	"context"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// Logger is an alias for the unified logger.Logger interface.
// This provides structured, context-aware logging throughout the slippy package.
type Logger = logger.Logger

// SlipStore defines the interface for slip persistence operations.
// Implementations provide storage backends (e.g., ClickHouse, in-memory for testing).
//
// All methods that identify a slip use correlationID as the unique identifier.
// The correlationID is the single, canonical identifier for a routing slip
// throughout its entire lifecycle.
type SlipStore interface {
	// Create persists a new routing slip
	Create(ctx context.Context, slip *Slip) error

	// Load retrieves a slip by its correlation ID (the unique slip identifier)
	Load(ctx context.Context, correlationID string) (*Slip, error)

	// LoadByCommit retrieves a slip by repository and commit SHA
	LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error)

	// LoadLiveByCommit returns the LIVE (non-terminal) slip for the exact (repository, commitSHA).
	// Excludes status in {abandoned, promoted, compensated}. Returns ErrSlipNotFound when no live
	// slip exists. Use for in-flight dedup paths that require exact-SHA semantics. For
	// ancestry-aware lookups use FindByCommits/ResolveSlip.
	LoadLiveByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error)

	// FindByCommits finds a slip matching any commit in the ordered list.
	// Returns the slip for the first (most recent) matching commit.
	// The third return value is the matched commit SHA.
	FindByCommits(ctx context.Context, repository string, commits []string) (*Slip, string, error)

	// FindAllByCommits finds all slips matching any commit in the ordered list.
	// Returns slips ordered by commit priority (first matching commit's slip first).
	// Each result includes the slip and its matched commit SHA.
	FindAllByCommits(ctx context.Context, repository string, commits []string) ([]SlipWithCommit, error)

	// Update persists changes to an existing slip.
	// With timestamp-based versioning, each update gets a unique nanosecond timestamp,
	// so there are no version conflicts.
	//
	// Optional StepStatusOverride values pin specific <step>_status columns (and their
	// step_details.<step>.status JSON counterpart) to caller-supplied truth, bypassing
	// slip.Steps[name].Status for those steps. Zero overrides preserves the historical
	// behavior — every step column is sourced from slip.Steps[name].Status — and is the
	// expected mode for callers like AbandonSlip / PromoteSlip that do not transition
	// individual step states. The override hook exists to defeat the stale-Load race in
	// slippy-api's hydrateAndPersist (see goLibMyCarrier I5 fix / ADO #82468).
	Update(ctx context.Context, slip *Slip, overrides ...StepStatusOverride) error

	// UpdateStep updates a specific step's status
	UpdateStep(ctx context.Context, correlationID, stepName, componentName string, status StepStatus) error

	// UpdateStepWithHistory updates a step's status AND appends a history entry in a single atomic operation.
	// This prevents race conditions between separate UpdateStep and AppendHistory calls.
	UpdateStepWithHistory(
		ctx context.Context,
		correlationID, stepName, componentName string,
		status StepStatus,
		entry StateHistoryEntry,
	) error

	// UpdateComponentStatus updates a component's build or test status
	UpdateComponentStatus(ctx context.Context, correlationID, componentName, stepType string, status StepStatus) error

	// AppendHistory adds a state history entry to the slip
	AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error

	// UpdateSlipStatus atomically updates the slip's top-level status without a full Load+Update
	// round-trip. Uses INSERT SELECT to copy the current DB row and override only the status
	// column, preventing concurrent history appends from being lost under last-write-wins.
	UpdateSlipStatus(ctx context.Context, correlationID string, status SlipStatus) error

	// SetComponentImageTag records the built container image tag for a component in the event log.
	// stepName is the component step type (e.g. "build"); componentName is the service name.
	SetComponentImageTag(ctx context.Context, correlationID, stepName, componentName, imageTag string) error

	// InsertAncestryLink writes a single direct-parent link to the ancestry table.
	InsertAncestryLink(ctx context.Context, slip *Slip, parent AncestryEntry) error

	// ResolveAncestry walks parent links to reconstruct the full ancestry chain.
	// Returns entries ordered from direct parent to oldest ancestor, capped at maxDepth.
	ResolveAncestry(
		ctx context.Context,
		repository, branch, correlationID string,
		maxDepth int,
	) ([]AncestryEntry, error)

	// LatestStepStatusFromEvents returns the latest pipeline-level (component="")
	// status for the given step from the event log (slip_component_states),
	// derived via the same argMax + sort-key formula used by hydrateSlip /
	// loadComponentStates. Returns (status, true, nil) when at least one event
	// row exists; returns ("", false, nil) when none exist (caller treats this
	// as "no event yet, do not block"). Returns ("", false, err) on query
	// failure — callers should treat this as fail-open (log + continue) so
	// transient ClickHouse outages do not block CI traffic.
	//
	// This method exists to support the I5 race fix in slippy-api's
	// overlayPipelineStep: callers need to know event-log truth for a step
	// (not the in-memory snapshot from Load) when deciding whether to apply
	// a non-terminal overlay on top of a terminal status.
	LatestStepStatusFromEvents(
		ctx context.Context, correlationID, step string,
	) (StepStatus, bool, error)

	// Close releases any resources held by the store
	Close() error

	// Ping verifies the underlying database connection is alive.
	// Returns nil if the connection is healthy, or an error if it is stale/dead.
	Ping(ctx context.Context) error
}

// GitHubAPI defines the interface for GitHub operations.
// This allows for mocking in tests and supports different GitHub implementations.
type GitHubAPI interface {
	// GetCommitAncestry retrieves the commit ancestry for a given ref.
	// Returns a slice of commit SHAs in order from newest to oldest.
	GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error)

	// GetPRHeadCommit retrieves the head commit SHA for a pull request.
	// This is used to link squash merge commits back to the original feature branch slip.
	// Returns the SHA of the PR's head commit before merging.
	GetPRHeadCommit(ctx context.Context, owner, repo string, prNumber int) (string, error)

	// ClearCache clears any cached data (useful for testing)
	ClearCache()
}

// PrereqResult represents the result of a prerequisite check.
// It provides details about which prerequisites are completed, running, or failed.
type PrereqResult struct {
	// Status is the aggregate prerequisite status
	Status PrereqStatus

	// FailedPrereqs lists prerequisites that have failed
	FailedPrereqs []string

	// RunningPrereqs lists prerequisites still in progress
	RunningPrereqs []string

	// CompletedPrereqs lists prerequisites that completed successfully
	CompletedPrereqs []string
}

// NopLogger returns a no-op logger that discards all messages.
// This is the default logger when none is provided.
func NopLogger() Logger {
	return &logger.NopLogger{}
}

// NewStdLogger creates a simple standard output logger.
// Set debug to true to enable debug-level logging.
func NewStdLogger(debug bool) Logger {
	return logger.NewStdLogger(debug)
}
