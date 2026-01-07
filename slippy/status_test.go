package slippy

import "testing"

func TestSlipStatus_String(t *testing.T) {
	tests := []struct {
		status   SlipStatus
		expected string
	}{
		{SlipStatusPending, "pending"},
		{SlipStatusInProgress, "in_progress"},
		{SlipStatusCompleted, "completed"},
		{SlipStatusFailed, "failed"},
		{SlipStatusCompensating, "compensating"},
		{SlipStatusCompensated, "compensated"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("SlipStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSlipStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   SlipStatus
		expected bool
	}{
		{SlipStatusPending, false},
		{SlipStatusInProgress, false},
		{SlipStatusCompleted, true},
		{SlipStatusFailed, true},
		{SlipStatusCompensating, false},
		{SlipStatusCompensated, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("SlipStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_String(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected string
	}{
		{StepStatusPending, "pending"},
		{StepStatusHeld, "held"},
		{StepStatusRunning, "running"},
		{StepStatusCompleted, "completed"},
		{StepStatusFailed, "failed"},
		{StepStatusError, "error"},
		{StepStatusAborted, "aborted"},
		{StepStatusTimeout, "timeout"},
		{StepStatusSkipped, "skipped"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("StepStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, true},
		{StepStatusFailed, true},
		{StepStatusError, true},
		{StepStatusAborted, true},
		{StepStatusTimeout, true},
		{StepStatusSkipped, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsSuccess(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, true},
		{StepStatusFailed, false},
		{StepStatusError, false},
		{StepStatusAborted, false},
		{StepStatusTimeout, false},
		{StepStatusSkipped, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsSuccess(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsSuccess() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsFailure(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, false},
		{StepStatusFailed, true},
		{StepStatusError, true},
		{StepStatusAborted, true},
		{StepStatusTimeout, true},
		{StepStatusSkipped, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsFailure(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsFailure() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsRunning(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, true},
		{StepStatusRunning, true},
		{StepStatusCompleted, false},
		{StepStatusFailed, false},
		{StepStatusError, false},
		{StepStatusAborted, false},
		{StepStatusTimeout, false},
		{StepStatusSkipped, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsRunning(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsRunning() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsPending(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, true},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, false},
		{StepStatusFailed, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsPending(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsPending() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestPrereqStatus_String(t *testing.T) {
	tests := []struct {
		status   PrereqStatus
		expected string
	}{
		{PrereqStatusCompleted, "completed"},
		{PrereqStatusRunning, "running"},
		{PrereqStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("PrereqStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}
