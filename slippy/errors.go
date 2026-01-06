package slippy

import "errors"

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
