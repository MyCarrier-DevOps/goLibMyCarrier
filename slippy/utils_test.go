package slippy

import (
	"testing"
)

func TestCalculateSlipNotFoundBackoff(t *testing.T) {
	tests := []struct {
		retryNumber int
		expected    int // Expected minutes
	}{
		{retryNumber: 0, expected: slipNotFoundBaseDelay * 1},   // Clamped to 1
		{retryNumber: 1, expected: slipNotFoundBaseDelay * 1},   // 5 min
		{retryNumber: 2, expected: slipNotFoundBaseDelay * 2},   // 10 min
		{retryNumber: 3, expected: slipNotFoundBaseDelay * 3},   // 15 min
		{retryNumber: 4, expected: slipNotFoundBaseDelay * 3},   // Clamped to max
		{retryNumber: 100, expected: slipNotFoundBaseDelay * 3}, // Clamped to max
		{retryNumber: -5, expected: slipNotFoundBaseDelay * 1},  // Clamped to 1
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := calculateSlipNotFoundBackoff(tt.retryNumber)
			expectedDuration := tt.expected * int(60e9) // Convert minutes to nanoseconds
			if int(result) != expectedDuration {
				t.Errorf("calculateSlipNotFoundBackoff(%d) = %v, want %v minutes",
					tt.retryNumber, result, tt.expected)
			}
		})
	}
}
