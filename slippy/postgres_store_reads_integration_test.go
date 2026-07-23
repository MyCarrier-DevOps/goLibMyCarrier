//go:build integration

package slippy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresStore_FindByCommits_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "a", Repository: "owner/repo", Branch: "main",
		CommitSHA: "shaA", Status: SlipStatusInProgress,
	}))
	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "b", Repository: "owner/repo", Branch: "main",
		CommitSHA: "shaB", Status: SlipStatusAbandoned,
	}))

	// shaB is highest priority but abandoned (excluded); shaA wins.
	slip, matched, err := store.FindByCommits(ctx, "owner/repo", []string{"shaB", "shaA"})
	require.NoError(t, err)
	assert.Equal(t, "a", slip.CorrelationID)
	assert.Equal(t, "shaA", matched)

	// No match -> ErrSlipNotFound.
	_, _, err = store.FindByCommits(ctx, "owner/repo", []string{"missing"})
	require.ErrorIs(t, err, ErrSlipNotFound)

	// FindAllByCommits does NOT exclude terminal statuses and preserves priority order.
	all, err := store.FindAllByCommits(ctx, "owner/repo", []string{"shaB", "shaA"})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "b", all[0].Slip.CorrelationID, "shaB is priority 1")
	assert.Equal(t, "shaB", all[0].MatchedCommit)
	assert.Equal(t, "a", all[1].Slip.CorrelationID)
}

func TestPostgresStore_Ancestry_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	child := &Slip{CorrelationID: "child", Repository: "owner/repo", Branch: "feature"}
	parentSlip := &Slip{CorrelationID: "p", Repository: "owner/repo", Branch: "main"}

	// child (feature) -> p (main): cross-branch link.
	require.NoError(t, store.InsertAncestryLink(ctx, child, AncestryEntry{
		CorrelationID: "p", CommitSHA: "psha", Status: SlipStatusCompleted,
		Repository: "owner/repo", Branch: "main", CreatedAt: now,
	}))
	// p (main) -> gp (main)
	require.NoError(t, store.InsertAncestryLink(ctx, parentSlip, AncestryEntry{
		CorrelationID: "gp", CommitSHA: "gpsha", Status: SlipStatusFailed, FailedStep: "build",
		Repository: "owner/repo", Branch: "main", CreatedAt: now,
	}))

	chain, err := store.ResolveAncestry(ctx, "owner/repo", "feature", "child", 10)
	require.NoError(t, err)
	require.Len(t, chain, 2)
	assert.Equal(t, "p", chain[0].CorrelationID)
	assert.Equal(t, SlipStatusCompleted, chain[0].Status)
	assert.Equal(t, "gp", chain[1].CorrelationID)
	assert.Equal(t, "build", chain[1].FailedStep)

	// maxDepth caps the walk.
	capped, err := store.ResolveAncestry(ctx, "owner/repo", "feature", "child", 1)
	require.NoError(t, err)
	require.Len(t, capped, 1)
	assert.Equal(t, "p", capped[0].CorrelationID)

	// Re-linking upserts (last write wins).
	require.NoError(t, store.InsertAncestryLink(ctx, child, AncestryEntry{
		CorrelationID: "p2", CommitSHA: "p2sha", Status: SlipStatusCompleted,
		Repository: "owner/repo", Branch: "main", CreatedAt: now,
	}))
	relinked, err := store.ResolveAncestry(ctx, "owner/repo", "feature", "child", 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(relinked), 1)
	assert.Equal(t, "p2", relinked[0].CorrelationID, "re-link overwrote the direct parent")

	// No ancestry for an unknown slip -> empty chain, no error.
	empty, err := store.ResolveAncestry(ctx, "owner/repo", "feature", "orphan", 10)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
