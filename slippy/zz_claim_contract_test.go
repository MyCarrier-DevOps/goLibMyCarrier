package slippy_test

import (
	"testing"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy/slippytest"
)

// The in-package double must satisfy the same claim contract as PostgresStore and as the
// published double. It lives in package slippy_test because slippytest imports slippy, so an
// in-package file could not reach the contract without a cycle.
//
// This is the third implementer of that contract, and the one with no consumer to notice when
// it drifts — every test in package slippy runs against it. See slippytest.RunClaimContract.
func TestInPackageMockStore_SatisfiesTheClaimContract(t *testing.T) {
	slippytest.RunClaimContract(t, func(t *testing.T) (slippy.SlipStore, string) {
		t.Helper()
		store := slippy.NewMockStore()
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
