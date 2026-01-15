package slippy

// SlipStatus represents the overall status of a routing slip.
// It tracks the high-level state of the entire pipeline execution.
type SlipStatus string

const (
	// SlipStatusPending indicates the slip has been created but no processing has started
	SlipStatusPending SlipStatus = "pending"

	// SlipStatusInProgress indicates the pipeline is actively executing
	SlipStatusInProgress SlipStatus = "in_progress"

	// SlipStatusCompleted indicates all pipeline steps completed successfully
	SlipStatusCompleted SlipStatus = "completed"

	// SlipStatusFailed indicates one or more pipeline steps failed
	SlipStatusFailed SlipStatus = "failed"

	// SlipStatusCompensating indicates compensation/rollback is in progress
	SlipStatusCompensating SlipStatus = "compensating"

	// SlipStatusCompensated indicates compensation completed
	SlipStatusCompensated SlipStatus = "compensated"

	// SlipStatusAbandoned indicates the slip was superseded by a newer slip
	SlipStatusAbandoned SlipStatus = "abandoned"
)

// String returns the string representation of the slip status.
func (s SlipStatus) String() string {
	return string(s)
}

// IsTerminal returns true if the slip status is a terminal state
// (completed, failed, compensated, or abandoned).
func (s SlipStatus) IsTerminal() bool {
	switch s {
	case SlipStatusCompleted, SlipStatusFailed, SlipStatusCompensated, SlipStatusAbandoned:
		return true
	case SlipStatusPending, SlipStatusInProgress, SlipStatusCompensating:
		return false
	default:
		return false
	}
}

// StepStatus represents the status of an individual pipeline step.
// Steps progress through various states during execution.
type StepStatus string

const (
	// StepStatusPending indicates the step has not yet started
	StepStatusPending StepStatus = "pending"

	// StepStatusHeld indicates the step is waiting for prerequisites
	StepStatusHeld StepStatus = "held"

	// StepStatusRunning indicates the step is actively executing
	StepStatusRunning StepStatus = "running"

	// StepStatusCompleted indicates the step completed successfully
	StepStatusCompleted StepStatus = "completed"

	// StepStatusFailed indicates the step failed during execution
	StepStatusFailed StepStatus = "failed"

	// StepStatusError indicates an unexpected error occurred
	StepStatusError StepStatus = "error"

	// StepStatusAborted indicates the step was aborted due to upstream failure
	StepStatusAborted StepStatus = "aborted"

	// StepStatusTimeout indicates the step exceeded its time limit
	StepStatusTimeout StepStatus = "timeout"

	// StepStatusSkipped indicates the step was intentionally skipped
	StepStatusSkipped StepStatus = "skipped"
)

// String returns the string representation of the step status.
func (s StepStatus) String() string {
	return string(s)
}

// IsTerminal returns true if the step status is a terminal state
// (completed, failed, error, aborted, timeout, or skipped).
func (s StepStatus) IsTerminal() bool {
	switch s {
	case StepStatusCompleted, StepStatusFailed, StepStatusError,
		StepStatusAborted, StepStatusTimeout, StepStatusSkipped:
		return true
	case StepStatusPending, StepStatusHeld, StepStatusRunning:
		return false
	default:
		return false
	}
}

// IsSuccess returns true if the step status indicates success
// (completed or skipped).
func (s StepStatus) IsSuccess() bool {
	return s == StepStatusCompleted || s == StepStatusSkipped
}

// IsFailure returns true if the step status indicates failure
// (failed, error, aborted, or timeout).
func (s StepStatus) IsFailure() bool {
	switch s {
	case StepStatusFailed, StepStatusError, StepStatusAborted, StepStatusTimeout:
		return true
	case StepStatusPending, StepStatusHeld, StepStatusRunning:
		return false
	case StepStatusCompleted, StepStatusSkipped:
		return false
	default:
		return false
	}
}

// IsRunning returns true if the step is actively executing or held.
func (s StepStatus) IsRunning() bool {
	return s == StepStatusRunning || s == StepStatusHeld
}

// IsPending returns true if the step has not started yet.
func (s StepStatus) IsPending() bool {
	return s == StepStatusPending
}

// PrereqStatus represents the aggregate prerequisite status.
// It indicates whether all prerequisites are satisfied.
type PrereqStatus string

const (
	// PrereqStatusCompleted indicates all prerequisites are satisfied
	PrereqStatusCompleted PrereqStatus = "completed"

	// PrereqStatusRunning indicates some prerequisites are still running
	PrereqStatusRunning PrereqStatus = "running"

	// PrereqStatusFailed indicates one or more prerequisites failed
	PrereqStatusFailed PrereqStatus = "failed"
)

// String returns the string representation of the prereq status.
func (p PrereqStatus) String() string {
	return string(p)
}
