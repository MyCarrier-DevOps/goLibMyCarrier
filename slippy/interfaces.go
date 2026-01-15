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

	// FindByCommits finds a slip matching any commit in the ordered list.
	// Returns the slip for the first (most recent) matching commit.
	// The third return value is the matched commit SHA.
	FindByCommits(ctx context.Context, repository string, commits []string) (*Slip, string, error)

	// FindAllByCommits finds all slips matching any commit in the ordered list.
	// Returns slips ordered by commit priority (first matching commit's slip first).
	// Each result includes the slip and its matched commit SHA.
	FindAllByCommits(ctx context.Context, repository string, commits []string) ([]SlipWithCommit, error)

	// Update persists changes to an existing slip
	Update(ctx context.Context, slip *Slip) error

	// UpdateStep updates a specific step's status
	UpdateStep(ctx context.Context, correlationID, stepName, componentName string, status StepStatus) error

	// UpdateComponentStatus updates a component's build or test status
	UpdateComponentStatus(ctx context.Context, correlationID, componentName, stepType string, status StepStatus) error

	// AppendHistory adds a state history entry to the slip
	AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error

	// Close releases any resources held by the store
	Close() error
}

// GitHubAPI defines the interface for GitHub operations.
// This allows for mocking in tests and supports different GitHub implementations.
type GitHubAPI interface {
	// GetCommitAncestry retrieves the commit ancestry for a given ref.
	// Returns a slice of commit SHAs in order from newest to oldest.
	GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error)

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
