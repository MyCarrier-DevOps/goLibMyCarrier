package slippytest

import (
	"context"
	"errors"
	"testing"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// ClaimContractCase is one step of the shared claim-contract sequence: a named operation to
// run against a store, and the observable state the store must be in afterwards.
type ClaimContractCase struct {
	Name string
	// Run performs the operation. Returning an error is part of the expected outcome, so it
	// is handed to Check rather than failing the case.
	Run func(ctx context.Context, store slippy.SlipStore) error
	// Check asserts on the error and on the row as the store reports it.
	Check func(t *testing.T, err error, slip *slippy.Slip)
}

// RunClaimContract runs the claim, release, reset and terminal-write sequence against any
// SlipStore and asserts the observable state after each step.
//
// It exists because three separate rounds of PR #87 review found the same class of defect: a
// fix landed on PostgresStore and one or both test doubles kept the old behaviour. Create and
// Repave preserving a caller's ClaimedFrom, GuardReservedStepWrite reaching 5 of 16 entry
// points, and the slip_released marker on a terminal write were each caught by a human
// reading the diff, because nothing in the suite asserted the three implementers agree. Every
// one of them would have failed here at the commit that introduced it.
//
// The point is that it is STORE-AGNOSTIC: run it against slippytest.MockStore, against the
// in-package double from a package slippy_test file, and against PostgresStore from the
// integration suite, and a divergence fails on whichever implementer drifted. A downstream
// SlipStore implementation can run it too — that is why it is exported from slippytest rather
// than kept in the library's own tests.
//
// newStore must return an empty store and a seeded slip's correlation ID. The slip must exist,
// be unclaimed, and carry a non-terminal status.
func RunClaimContract(t *testing.T, newStore func(t *testing.T) (slippy.SlipStore, string)) {
	t.Helper()
	ctx := context.Background()

	for _, tc := range claimContractSequence() {
		t.Run(tc.Name, func(t *testing.T) {
			store, id := newStore(t)
			err := tc.Run(ctx, store)
			slip, loadErr := store.Load(ctx, id)
			if loadErr != nil {
				t.Fatalf("loading the slip after %q: %v", tc.Name, loadErr)
			}
			tc.Check(t, err, slip)
		})
	}
}

// claimContractSequence is the contract itself, as data, so the cases are the same for every
// implementer by construction.
func claimContractSequence() []ClaimContractCase {
	const id = "conformance-1"

	return []ClaimContractCase{
		{
			Name: "a claim sets claimed_from and appends a marker naming the claimant",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				_, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", "")
				return err
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "claim")
				if slip.ClaimedFrom == "" {
					t.Error("claimed_from must be set by a successful claim")
				}
				if n := contractCountStep(slip, slippy.ClaimMarkerStep); n != 1 {
					t.Errorf("expected exactly 1 %s marker, got %d", slippy.ClaimMarkerStep, n)
				}
				if a := contractLastActor(slip, slippy.ClaimMarkerStep); a != "conformance/claimant" {
					t.Errorf("the marker must name the claimant, got %q", a)
				}
			},
		},
		{
			Name: "a terminal status ends the claim AND records the release",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				return s.UpdateSlipStatus(ctx, id, slippy.SlipStatusCompleted)
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "terminal write")
				if slip.ClaimedFrom != "" {
					t.Error("a terminal status must clear claimed_from")
				}
				// The column and the markers are read by different consumers, so clearing one
				// without recording the other is a divergence a marker-reading consumer sees.
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 1 {
					t.Errorf("a terminal write that ended a claim must append exactly 1 %s marker, got %d",
						slippy.ReleaseMarkerStep, n)
				}
			},
		},
		{
			Name: "a terminal status on an UNCLAIMED slip records no release",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				return s.UpdateSlipStatus(ctx, id, slippy.SlipStatusCompleted)
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "terminal write")
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 0 {
					t.Errorf("no claim was held, so no release marker belongs on the row, got %d", n)
				}
			},
		},
		{
			Name: "a release on a quiescent claim clears it and appends a release marker",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				_, err := s.ReleaseClaim(ctx, id, "conformance/releaser", "")
				return err
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "release")
				if slip.ClaimedFrom != "" {
					t.Error("a release with nothing in flight must clear claimed_from")
				}
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 1 {
					t.Errorf("expected exactly 1 %s marker, got %d", slippy.ReleaseMarkerStep, n)
				}
			},
		},
		{
			Name: "an in-place reset is REFUSED on a claimed row and writes nothing",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				// Quiescent: no step has been reported, which is the shape that used to be
				// allowed through and wiped a dispatched run.
				return s.ResetSlipInPlace(ctx, &slippy.Slip{
					CorrelationID: id, Repository: "owner/repo", Branch: "main",
					CommitSHA: "conformance-sha", Status: slippy.SlipStatusPending,
				})
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				if err == nil {
					t.Fatal("a reset onto a claimed row must be refused")
				}
				if !isClaimedRefusal(err) {
					t.Errorf("the refusal must wrap ErrSlipClaimed, got %v", err)
				}
				if slip.ClaimedFrom == "" {
					t.Error("a refused reset writes nothing, so the claim must survive")
				}
				if n := contractCountStep(slip, slippy.ClaimMarkerStep); n != 1 {
					t.Errorf("nothing written means exactly the claim's own marker, got %d", n)
				}
			},
		},
		{
			Name: "a caller may not write a step under a reserved marker name",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				return s.UpdateStepWithHistory(ctx, id, "builds", "", slippy.StepStatusRunning,
					slippy.StateHistoryEntry{Step: slippy.ReleaseMarkerStep, Actor: "impostor"})
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				if err == nil {
					t.Fatal("a caller-supplied entry under a marker name must be refused")
				}
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 0 {
					t.Errorf("the refusal must write nothing, got %d forged markers", n)
				}
			},
		},
	}
}

// isClaimedRefusal keeps the sentinel check in one place, so a store that wraps it differently
// still satisfies the contract as long as errors.Is reaches it.
func isClaimedRefusal(err error) bool {
	return errors.Is(err, slippy.ErrSlipClaimed)
}

func contractCountStep(slip *slippy.Slip, step string) int {
	n := 0
	for _, e := range slip.StateHistory {
		if e.Step == step {
			n++
		}
	}
	return n
}

func contractLastActor(slip *slippy.Slip, step string) string {
	actor := ""
	for _, e := range slip.StateHistory {
		if e.Step == step {
			actor = e.Actor
		}
	}
	return actor
}

func contractRequireNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", what, err)
	}
}
