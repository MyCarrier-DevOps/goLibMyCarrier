//go:build integration

package slippy_test

import (
	"context"
	"testing"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy/slippytest"
)

// PostgresStore is the implementer the other two exist to imitate, so it runs the same
// contract against real Postgres. A case that passes here and fails against a double — or the
// reverse — is the divergence class three rounds of PR #87 review kept finding by hand.
func TestPostgresStore_SatisfiesTheClaimContract_Integration(t *testing.T) {
	slippytest.RunClaimContract(t, func(t *testing.T) (slippy.SlipStore, string) {
		t.Helper()
		store := slippy.NewMigratedStoreForContract(t)
		const id = "conformance-1"
		err := store.Create(context.Background(), &slippy.Slip{
			CorrelationID: id,
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "conformance-sha",
			Status:        slippy.SlipStatusFailed,
			Steps:         map[string]slippy.Step{"builds": {Status: slippy.StepStatusFailed}},
		})
		if err != nil {
			t.Fatalf("seeding the contract slip: %v", err)
		}
		return store, id
	})
}
