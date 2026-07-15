package slippy

import (
	"errors"
	"fmt"
)

// Sentinel errors for common error conditions.
// These can be used with errors.Is() for error handling.
var (
	// ErrSlipNotFound indicates no slip was found matching the query
	ErrSlipNotFound = errors.New("routing slip not found")

	// ErrHoldTimeout indicates the hold operation exceeded its time limit
	ErrHoldTimeout = errors.New("hold timeout exceeded")

	// ErrPrerequisiteFailed indicates one or more prerequisites failed
	ErrPrerequisiteFailed = errors.New("prerequisite failed")

	// ErrInvalidConfiguration indicates the configuration is incomplete or invalid
	ErrInvalidConfiguration = errors.New("invalid configuration")

	// ErrInvalidRepository indicates the repository format is invalid
	ErrInvalidRepository = errors.New("invalid repository format (expected owner/repo)")

	// ErrInvalidCorrelationID indicates the correlation ID format is invalid
	ErrInvalidCorrelationID = errors.New("invalid correlation ID format")

	// ErrStoreConnection indicates a storage connection error
	ErrStoreConnection = errors.New("storage connection error")

	// ErrGitHubAPI indicates a GitHub API error
	ErrGitHubAPI = errors.New("GitHub API error")

	// ErrNoInstallation indicates no GitHub App installation was found for the organization
	ErrNoInstallation = errors.New("no GitHub App installation found for organization")

	// ErrContextCancelled indicates the operation was cancelled via context
	ErrContextCancelled = errors.New("operation cancelled")

	// ErrAncestryResolutionFailed indicates ancestry resolution failed.
	// The slip is still created but without ancestry tracking.
	// This error wraps the underlying cause (e.g., GitHub API failure, no installation).
	ErrAncestryResolutionFailed = errors.New("ancestry resolution failed")

	// ErrAncestorUpdateFailed indicates updating an ancestor slip failed.
	// This includes both abandonment and promotion failures.
	ErrAncestorUpdateFailed = errors.New("failed to update ancestor slip status")

	// ErrHistoryAppendFailed indicates appending to state history failed.
	// The primary operation succeeded but audit trail is incomplete.
	//
	// Not returned by handlePushRetry (push.go): that call site routes through
	// UpdateStepWithHistory, whose best-effort write-back semantics already Warn-log and
	// swallow history-append failures internally — any error it does return there is a
	// terminal-freshness gate rejection (ErrTerminalAlreadyExists) or an event-insert
	// failure, not a history-append failure, so it is propagated via %w instead of this
	// sentinel (bd mycarrier-5dv5 review fix).
	ErrHistoryAppendFailed = errors.New("failed to append state history")

	// ErrSlipStatusUpdateFailed indicates updating slip status failed.
	// The underlying step update may have succeeded but overall status is stale.
	ErrSlipStatusUpdateFailed = errors.New("failed to update slip status")

	// ErrMaxRetriesExceeded indicates the maximum number of retry attempts was reached.
	// This occurs when a slip is not found after multiple retries.
	ErrMaxRetriesExceeded = errors.New("maximum retry attempts exceeded")

	// ErrTerminalAlreadyExists is returned by the terminal-freshness gate in
	// UpdateStep / UpdateStepWithHistory when a non-terminal incoming status would
	// overwrite a freshly-recorded terminal status in slip_component_states.
	//
	// The gate closes the I5 race (ADO #82468 / ADO #83405) by refusing
	// non-terminal-over-terminal writes that arrive within the freshness window
	// (default 750 ms, configurable via SLIPPY_I5_FRESHNESS_WINDOW_MS).
	//
	// Spec: standup-notes/2026/07/slip-state-ch-fix-spec-and-plan.md §2 D4
	//
	// The gate REFUSES:
	//   - non-terminal incoming status when the prior recorded status is terminal
	//     AND the prior event is younger than the freshness window.
	//     Exception: aborted → pending is always allowed (cascade-reset path).
	//
	// The gate ALLOWS unconditionally (SC-3 / DA-reviewed):
	//   - terminal incoming status — the race artifact class is exclusively
	//     non-terminal-over-terminal; terminal→terminal preserves v1 recovery
	//     semantics (failed → completed, etc.) with no timing probe needed.
	//   - no prior event for the (correlation_id, step, component) tuple.
	//   - prior non-terminal status.
	//   - aborted → pending (cascade-reset exception).
	//   - events older than the freshness window (genuine re-run / restart).
	//   - push_parsed with component="" (bypass — push-retry resets terminal
	//     within window without triggering the gate).
	//   - gate disabled via SLIPPY_I5_GATE_ENABLED=false.
	//
	// Callers should map this sentinel to HTTP 409 Conflict at the API boundary:
	//   errors.Is(err, slippy.ErrTerminalAlreadyExists)
	// It propagates through SlipError / StepError via errors.Unwrap so that
	// errors.Is continues to work at the outermost caller.
	ErrTerminalAlreadyExists = errors.New("terminal status already recorded for step")
)

// SlipError wraps an error with additional context about the slip operation.
type SlipError struct {
	// Op is the operation that failed (e.g., "create", "update", "load")
	Op string

	// CorrelationID is the unique identifier for the routing slip (if known).
	// This ID is used organization-wide to identify jobs across all systems.
	CorrelationID string

	// Err is the underlying error
	Err error
}

// NewSlipError creates a new SlipError with the given operation and underlying error.
// The correlationID is the unique identifier for the routing slip.
func NewSlipError(op, correlationID string, err error) *SlipError {
	return &SlipError{
		Op:            op,
		CorrelationID: correlationID,
		Err:           err,
	}
}

// Error returns the error message.
func (e *SlipError) Error() string {
	if e.CorrelationID != "" {
		return e.Op + " slip " + e.CorrelationID + ": " + e.Err.Error()
	}
	return e.Op + ": " + e.Err.Error()
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e *SlipError) Unwrap() error {
	return e.Err
}

// StepError wraps an error with additional context about a step operation.
type StepError struct {
	// Op is the operation that failed
	Op string

	// CorrelationID is the unique identifier for the routing slip.
	// This ID is used organization-wide to identify jobs across all systems.
	CorrelationID string

	// StepName is the step that failed
	StepName string

	// ComponentName is the component involved (if applicable)
	ComponentName string

	// Err is the underlying error
	Err error
}

// NewStepError creates a new StepError.
// The correlationID is the unique identifier for the routing slip.
func NewStepError(op, correlationID, stepName, componentName string, err error) *StepError {
	return &StepError{
		Op:            op,
		CorrelationID: correlationID,
		StepName:      stepName,
		ComponentName: componentName,
		Err:           err,
	}
}

// Error returns the error message.
func (e *StepError) Error() string {
	msg := e.Op + " step " + e.StepName
	if e.ComponentName != "" {
		msg += " (component: " + e.ComponentName + ")"
	}
	if e.CorrelationID != "" {
		msg += " on slip " + e.CorrelationID
	}
	return msg + ": " + e.Err.Error()
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e *StepError) Unwrap() error {
	return e.Err
}

// ResolveError wraps an error that occurred during slip resolution.
type ResolveError struct {
	// Repository is the repository being resolved
	Repository string

	// Ref is the git ref being resolved
	Ref string

	// Err is the underlying error
	Err error
}

// NewResolveError creates a new ResolveError.
func NewResolveError(repository, ref string, err error) *ResolveError {
	return &ResolveError{
		Repository: repository,
		Ref:        ref,
		Err:        err,
	}
}

// Error returns the error message.
func (e *ResolveError) Error() string {
	return "resolve slip for " + e.Repository + "@" + e.Ref + ": " + e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *ResolveError) Unwrap() error {
	return e.Err
}

// AncestryError provides detailed information about ancestry resolution failures.
// This error type helps operators quickly understand what went wrong and how to fix it.
type AncestryError struct {
	// Repository is the repository being processed
	Repository string

	// CommitSHA is the commit being processed
	CommitSHA string

	// Phase indicates where the failure occurred:
	// "setup" - Failed during initial setup (e.g., invalid repository format)
	// "github_api" - Failed to query GitHub for commit ancestry
	// "slip_lookup" - Failed to query database for ancestor slips
	// "abandon" - Failed to abandon a superseded ancestor slip
	// "promote" - Failed to promote a feature branch slip
	Phase string

	// AncestorCorrelationID is set when updating an ancestor slip fails
	AncestorCorrelationID string

	// Err is the underlying error
	Err error
}

// NewAncestryError creates a new AncestryError.
func NewAncestryError(repository, commitSHA, phase string, err error) *AncestryError {
	return &AncestryError{
		Repository: repository,
		CommitSHA:  commitSHA,
		Phase:      phase,
		Err:        err,
	}
}

// NewAncestorUpdateError creates an AncestryError for ancestor update failures.
func NewAncestorUpdateError(repository, commitSHA, phase, ancestorID string, err error) *AncestryError {
	return &AncestryError{
		Repository:            repository,
		CommitSHA:             commitSHA,
		Phase:                 phase,
		AncestorCorrelationID: ancestorID,
		Err:                   err,
	}
}

// Error returns a detailed error message with actionable guidance.
func (e *AncestryError) Error() string {
	shortSHA := e.CommitSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}

	switch e.Phase {
	case "setup":
		return fmt.Sprintf(
			"ancestry resolution failed for %s@%s: setup error - %v",
			e.Repository, shortSHA, e.Err,
		)
	case "github_api":
		return fmt.Sprintf(
			"ancestry resolution failed for %s@%s: GitHub API error - %v "+
				"(check GitHub App installation and credentials)",
			e.Repository, shortSHA, e.Err,
		)
	case "slip_lookup":
		return fmt.Sprintf(
			"ancestry resolution failed for %s@%s: database query failed - %v",
			e.Repository, shortSHA, e.Err,
		)
	case "abandon":
		return fmt.Sprintf(
			"failed to abandon superseded slip %s for %s@%s: %v",
			e.AncestorCorrelationID, e.Repository, shortSHA, e.Err,
		)
	case "promote":
		return fmt.Sprintf(
			"failed to promote feature branch slip %s for %s@%s: %v",
			e.AncestorCorrelationID, e.Repository, shortSHA, e.Err,
		)
	default:
		return fmt.Sprintf("ancestry error for %s@%s: %v", e.Repository, shortSHA, e.Err)
	}
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e *AncestryError) Unwrap() error {
	switch e.Phase {
	case "abandon", "promote":
		return ErrAncestorUpdateFailed
	case "setup", "github_api", "slip_lookup":
		return ErrAncestryResolutionFailed
	default:
		// Fallback for any future phases - resolution failed is the safe default
		return ErrAncestryResolutionFailed
	}
}
