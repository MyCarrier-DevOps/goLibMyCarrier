package slippytest

import (
	"testing"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// The published double must satisfy the same claim contract as PostgresStore. See
// RunClaimContract for why this exists.
func TestMockStore_SatisfiesTheClaimContract(t *testing.T) {
	RunClaimContract(t, func(t *testing.T) (slippy.SlipStore, string) {
		t.Helper()
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{
			CorrelationID: "conformance-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "conformance-sha",
			Status:        slippy.SlipStatusFailed,
			Steps:         map[string]slippy.Step{"builds": {Status: slippy.StepStatusFailed}},
		})
		return store, "conformance-1"
	})
}
