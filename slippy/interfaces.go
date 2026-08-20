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

	// LoadByCommit retrieves a slip by repository and commit SHA.
	//
	// Error contract (DEVOPS-231 review D3.5): a clean miss — no row exists for this
	// (repository, commitSHA) — MUST return ErrSlipNotFound and a nil slip. Any other
	// non-nil error is treated by CreateSlipForPush (push.go) as a hard failure of the
	// push: it aborts and returns the error to the caller rather than proceeding as if
	// no slip existed, so Kafka redelivers the message. A store that signals absence with
	// a bespoke error (e.g. wrapping sql.ErrNoRows without translating it, or returning a
	// generic error from a degraded/partial read such as a hydration failure) makes that
	// miss look like a hard failure instead: the push then fails every redelivery forever
	// and no slip is ever created for that commit. Implementations must translate any
	// "no rows" condition to ErrSlipNotFound before returning, and must not use
	// ErrSlipNotFound for anything other than a genuine clean miss.
	LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error)

	// LoadLiveByCommit returns the LIVE (non-terminal) slip for the exact (repository, commitSHA).
	// Excludes status in {abandoned, promoted, compensated}. Returns ErrSlipNotFound when no live
	// slip exists. Use for in-flight dedup paths that require exact-SHA semantics. For
	// ancestry-aware lookups use FindByCommits/ResolveSlip.
	//
	// Error contract: identical to LoadByCommit above — a clean miss (no live row) MUST be
	// ErrSlipNotFound; any other error is a hard failure to callers on the push path. See
	// LoadByCommit's doc for the full rationale.
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
	Update(ctx context.Context, slip *Slip) error

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

	// DeleteSlip repaves an ended slip: it removes the routing_slips row and its child
	// rows (slip_component_states, slip_ancestry) for correlationID, but ONLY when the
	// row's status is ended (failed, completed, abandoned, promoted, compensated) — a
	// slip that has gone live again between the caller's repave decision and this call
	// is never destroyed. Used by the same-commit repave path (DEVOPS-231): a retrigger
	// of an ended slip deletes the prior run and creates a fresh one under
	// successorCorrelationID, the new run's correlation ID.
	//
	// successorCorrelationID identifies the slip that supersedes the deleted one: any
	// OTHER slip whose ancestry points at correlationID as its parent is repointed to
	// successorCorrelationID rather than left dangling (a dangling parent link would
	// silently truncate that descendant's ResolveAncestry walk). Pass "" when there is
	// no successor to point at — those descendant links are deleted instead.
	//
	// Returns ErrSlipWentLive if correlationID's row exists but its status is no longer
	// ended (the repave decision is now stale; the caller must not create a fresh slip).
	// Deleting a missing slip is not an error (idempotent).
	//
	// A store that cannot repave (delete-and-recreate) at all — e.g. ClickHouseStore,
	// which is not the operational slip store (DEVOPS-127) — MUST return an error
	// wrapping ErrDeleteSlipUnsupported rather than a plain error or nil. The push path
	// detects that sentinel with errors.Is and falls back to abandon semantics (marking
	// the superseded slip abandoned) instead of repaving it.
	DeleteSlip(ctx context.Context, correlationID, successorCorrelationID string) error

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
