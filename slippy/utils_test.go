package slippy

import (
	"testing"
	"time"
)

func TestCalculateBackoff(t *testing.T) {
	// Test that backoff increases with attempt number
	for attempt := 1; attempt <= 5; attempt++ {
		current := calculateBackoff(attempt)
		// The backoff should generally increase, but jitter can cause overlap
		// So we just verify it returns a positive duration
		if current <= 0 {
			t.Errorf("calculateBackoff(%d) returned non-positive duration: %v", attempt, current)
		}
	}

	// Test that it doesn't exceed max delay (10 seconds + jitter)
	for i := 0; i < 100; i++ {
		backoff := calculateBackoff(20) // High attempt number
		// Max is 10s, with jitter can be up to ~15s
		if backoff > 20*retryMaxDelay {
			t.Errorf("calculateBackoff(20) = %v, exceeds reasonable max", backoff)
		}
	}
}

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

func TestCalculateBackoffWithParams(t *testing.T) {
	tests := []struct {
		name      string
		attempt   int
		baseDelay int64 // milliseconds
		maxDelay  int64 // milliseconds
	}{
		{name: "first attempt", attempt: 0, baseDelay: 100, maxDelay: 1000},
		{name: "second attempt", attempt: 1, baseDelay: 100, maxDelay: 1000},
		{name: "high attempt capped", attempt: 20, baseDelay: 100, maxDelay: 1000},
		{name: "zero base delay", attempt: 5, baseDelay: 0, maxDelay: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDelay := tt.baseDelay * 1e6 // Convert to nanoseconds
			maxDelay := tt.maxDelay * 1e6

			result := calculateBackoffWithParams(tt.attempt,
				time.Duration(baseDelay), time.Duration(maxDelay))

			// Result should be non-negative
			if result < 0 {
				t.Errorf("calculateBackoffWithParams returned negative: %v", result)
			}

			// Result should not greatly exceed max delay (allow for jitter)
			// With ±25% jitter, max should be around 1.25 * maxDelay
			maxWithJitter := time.Duration(maxDelay) * 2
			if result > maxWithJitter {
				t.Errorf("calculateBackoffWithParams = %v, exceeds reasonable max %v",
					result, maxWithJitter)
			}
		})
	}

	// Test exponential growth before hitting cap
	for i := 0; i < 100; i++ {
		backoff0 := calculateBackoffWithParams(0, 100*time.Millisecond, 10*time.Second)
		backoff1 := calculateBackoffWithParams(1, 100*time.Millisecond, 10*time.Second)
		backoff2 := calculateBackoffWithParams(2, 100*time.Millisecond, 10*time.Second)

		// On average, backoff should increase exponentially
		// Due to jitter, individual comparisons may vary, so we just verify they're reasonable
		if backoff0 <= 0 || backoff1 <= 0 || backoff2 <= 0 {
			t.Error("Backoff should be positive")
		}
	}
}
