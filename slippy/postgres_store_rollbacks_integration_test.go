//go:build integration

package slippy

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRollback(corr, workflow string) RollbackRecord {
	rec := validRollbackRecord()
	rec.CorrelationID = corr
	rec.Workflow = workflow
	return rec
}

func TestPostgresStore_Rollbacks_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("records once per workflow and reads back every field", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-1", "sha-rb-1", SlipStatusFailed)
		want := testRollback("rb-1", "gitops-rollback-abc12")

		inserted, err := store.RecordRollback(ctx, want)
		require.NoError(t, err)
		assert.True(t, inserted)

		inserted, err = store.RecordRollback(ctx, want)
		require.NoError(t, err)
		assert.False(t, inserted, "a repeat of the same workflow writes nothing")

		got, err := store.ListRollbacks(ctx, "rb-1")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Positive(t, got[0].ID)
		assert.False(t, got[0].RecordedAt.IsZero(), "the database stamps recorded_at")
		got[0].ID, got[0].RecordedAt = 0, want.RecordedAt
		assert.Equal(t, want, got[0])
	})

	t.Run("every status the CHECK allows is accepted and round-trips", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-status", "sha-rb-status", SlipStatusFailed)
		statuses := []RollbackStatus{
			RollbackStatusSuccess, RollbackStatusNoop, RollbackStatusSkipped,
			RollbackStatusFailed, RollbackStatusDryRun,
		}
		for _, status := range statuses {
			rec := testRollback("rb-status", "wf-status-"+string(status))
			rec.Status = status
			inserted, err := store.RecordRollback(ctx, rec)
			require.NoError(t, err, "status %q", status)
			assert.True(t, inserted, "status %q", status)
		}

		got, err := store.ListRollbacks(ctx, "rb-status")
		require.NoError(t, err)
		require.Len(t, got, len(statuses))
		for i, status := range statuses {
			assert.Equal(t, status, got[i].Status)
		}
	})

	t.Run("an unknown slip is ErrSlipNotFound and writes nothing", func(t *testing.T) {
		store, pool, _ := newMigratedStore(t)
		_, err := store.RecordRollback(ctx, testRollback("rb-missing", "wf-missing"))
		require.ErrorIs(t, err, ErrSlipNotFound)

		var n int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM slip_rollbacks").Scan(&n))
		assert.Zero(t, n)
	})

	// Review Focus 1: correlation IDs are case-sensitive here exactly as on every other slip
	// path, so an upper-cased ID does not reach a lower-case slip.
	t.Run("a differently-cased correlation id does not match the slip", func(t *testing.T) {
		store, pool, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-case", "sha-rb-case", SlipStatusFailed)
		_, err := store.RecordRollback(ctx, testRollback("RB-CASE", "wf-case"))
		require.ErrorIs(t, err, ErrSlipNotFound)

		var n int
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM slip_rollbacks").Scan(&n))
		assert.Zero(t, n)
	})

	// Review Focus 2: an argo retry re-runs the exit handler under the same Workflow name, and
	// two emits for one workflow may race; exactly one of them may write a new row.
	t.Run("concurrent emits for one workflow write exactly one row", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-race", "sha-rb-race", SlipStatusFailed)

		const writers = 8
		var wg sync.WaitGroup
		results := make([]bool, writers)
		errs := make([]error, writers)
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], errs[i] = store.RecordRollback(ctx, testRollback("rb-race", "wf-race"))
			}()
		}
		wg.Wait()

		wins := 0
		for i := range writers {
			require.NoError(t, errs[i])
			if results[i] {
				wins++
			}
		}
		assert.Equal(t, 1, wins)
		got, err := store.ListRollbacks(ctx, "rb-race")
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})

	// generateName workflow names come back after their Workflow is gone, so the key is the
	// pair: the same name on another slip is a new row, never a lost one.
	t.Run("a recycled workflow name on another slip is recorded", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-a", "sha-rb-a", SlipStatusFailed)
		claimTestSlip(t, store, "rb-b", "sha-rb-b", SlipStatusFailed)

		for _, corr := range []string{"rb-a", "rb-b"} {
			inserted, err := store.RecordRollback(ctx, testRollback(corr, "gitops-rollback-x7k2p"))
			require.NoError(t, err)
			assert.True(t, inserted, "slip %s", corr)
		}
	})

	// An argo retry of a failed rollback carries the newer outcome: it replaces the failed row.
	// A repeat over any other row changes nothing, so a recorded success is never overwritten.
	t.Run("a retry replaces a failed row and never a successful one", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-retry", "sha-rb-retry", SlipStatusFailed)

		failed := testRollback("rb-retry", "wf-retry")
		failed.Status, failed.Outcome, failed.RevertCommit = RollbackStatusFailed, "push rejected after 5 attempts", ""
		inserted, err := store.RecordRollback(ctx, failed)
		require.NoError(t, err)
		require.True(t, inserted)
		first, err := store.ListRollbacks(ctx, "rb-retry")
		require.NoError(t, err)
		require.Len(t, first, 1)

		retried := testRollback("rb-retry", "wf-retry")
		inserted, err = store.RecordRollback(ctx, retried)
		require.NoError(t, err)
		assert.False(t, inserted, "an update is not a new row")
		got, err := store.ListRollbacks(ctx, "rb-retry")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, RollbackStatusSuccess, got[0].Status)
		assert.Equal(t, retried.Outcome, got[0].Outcome)
		assert.Equal(t, retried.RevertCommit, got[0].RevertCommit)
		assert.Equal(t, failed.Message, got[0].Message, "why the rollback started does not change")
		assert.True(t, got[0].RecordedAt.After(first[0].RecordedAt), "recorded_at moves to the retry")

		_, err = store.RecordRollback(ctx, failed)
		require.NoError(t, err)
		got, err = store.ListRollbacks(ctx, "rb-retry")
		require.NoError(t, err)
		assert.Equal(t, RollbackStatusSuccess, got[0].Status, "a later failed emit never overwrites a success")
		assert.Equal(t, retried.Outcome, got[0].Outcome)
	})

	// Review Focus 3: the limit is characters, not bytes.
	t.Run("a 1000-character multibyte message round-trips and 1001 is refused", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-msg", "sha-rb-msg", SlipStatusFailed)

		ok := testRollback("rb-msg", "wf-msg-ok")
		ok.Message = strings.Repeat("é", 1000)
		_, err := store.RecordRollback(ctx, ok)
		require.NoError(t, err)

		tooLong := testRollback("rb-msg", "wf-msg-long")
		tooLong.Message = strings.Repeat("é", 1001)
		_, err = store.RecordRollback(ctx, tooLong)
		require.ErrorIs(t, err, ErrInvalidRollback)

		got, err := store.ListRollbacks(ctx, "rb-msg")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, ok.Message, got[0].Message)
	})

	// Review Focus 4: a repave hard-deletes the superseded run's slip; its rows go with it and
	// a late emit for it is a not-found, not a dangling row.
	t.Run("after the slip is deleted, its rows are gone and a late emit is not-found", func(t *testing.T) {
		store, pool, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-gone", "sha-rb-gone", SlipStatusFailed)
		_, err := store.RecordRollback(ctx, testRollback("rb-gone", "wf-gone-1"))
		require.NoError(t, err)

		_, err = pool.Exec(ctx, "DELETE FROM routing_slips WHERE correlation_id = $1", "rb-gone")
		require.NoError(t, err)

		got, err := store.ListRollbacks(ctx, "rb-gone")
		require.NoError(t, err)
		assert.Empty(t, got)
		_, err = store.RecordRollback(ctx, testRollback("rb-gone", "wf-gone-2"))
		require.ErrorIs(t, err, ErrSlipNotFound)
	})

	t.Run("lists in recorded order and only for the requested slip", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		claimTestSlip(t, store, "rb-a", "sha-rb-a", SlipStatusFailed)
		claimTestSlip(t, store, "rb-b", "sha-rb-b", SlipStatusFailed)
		for _, wf := range []string{"wf-a-1", "wf-a-2", "wf-a-3"} {
			_, err := store.RecordRollback(ctx, testRollback("rb-a", wf))
			require.NoError(t, err)
		}
		_, err := store.RecordRollback(ctx, testRollback("rb-b", "wf-b-1"))
		require.NoError(t, err)

		got, err := store.ListRollbacks(ctx, "rb-a")
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []string{"wf-a-1", "wf-a-2", "wf-a-3"},
			[]string{got[0].Workflow, got[1].Workflow, got[2].Workflow})
	})

	t.Run("a slip with no rollbacks lists an empty non-nil slice", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		got, err := store.ListRollbacks(ctx, "nobody")
		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}
