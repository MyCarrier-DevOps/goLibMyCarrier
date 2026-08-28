//go:build integration

package slippy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMigratedStore starts a Postgres container, runs the slippy migrations, and returns
// a PostgresStore over the resulting schema.
func newMigratedStore(t *testing.T) (*PostgresStore, *pgxpool.Pool, *PipelineConfig) {
	t.Helper()
	pool := newPGMigrationTestPool(t)
	cfg := pgTestPipelineConfig(t)
	_, err := RunPostgresMigrations(context.Background(), pool, PostgresMigrateOptions{PipelineConfig: cfg})
	require.NoError(t, err)
	store, err := NewPostgresStore(pool, cfg, nil)
	require.NoError(t, err)
	return store, pool, cfg
}

func TestPostgresStore_CRUD_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	slip := &Slip{
		CorrelationID: "c1",
		Repository:    "Owner/Repo",
		Branch:        "main",
		CommitSHA:     "sha1",
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"builds": {Status: StepStatusRunning, StartedAt: &started, Actor: "ci"},
		},
		Aggregates: map[string][]ComponentStepData{
			"builds": {{Component: "api", Status: StepStatusCompleted}},
		},
		StateHistory: []StateHistoryEntry{
			{Step: "builds", Status: StepStatusRunning, Actor: "ci", Timestamp: started},
		},
	}
	require.NoError(t, store.Create(ctx, slip))

	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusInProgress, got.Status)
	assert.Equal(t, "Owner/Repo", got.Repository)
	assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status)
	assert.Equal(t, "ci", got.Steps["builds"].Actor)
	require.NotNil(t, got.Steps["builds"].StartedAt)
	assert.Equal(t, StepStatusPending, got.Steps["unit_tests"].Status, "unset steps default pending")
	require.Len(t, got.Aggregates["builds"], 1)
	assert.Equal(t, "api", got.Aggregates["builds"][0].Component)
	require.Len(t, got.StateHistory, 1)

	// Update: promote status and complete the build.
	got.Status = SlipStatusCompleted
	b := got.Steps["builds"]
	b.Status = StepStatusCompleted
	got.Steps["builds"] = b
	require.NoError(t, store.Update(ctx, got))

	reloaded, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusCompleted, reloaded.Status)
	assert.Equal(t, StepStatusCompleted, reloaded.Steps["builds"].Status)

	// Case-insensitive commit lookup.
	byCommit, err := store.LoadByCommit(ctx, "owner/repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "c1", byCommit.CorrelationID)

	// Not-found paths.
	_, err = store.Load(ctx, "ghost")
	require.ErrorIs(t, err, ErrSlipNotFound)
	require.ErrorIs(t, store.Update(ctx, &Slip{CorrelationID: "ghost"}), ErrSlipNotFound)
}

func TestPostgresStore_CreateUpsert_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "c1", Repository: "r", Branch: "b", CommitSHA: "sha", Status: SlipStatusPending,
	}))
	// Create again with the same ID overwrites (last-write-wins), no error.
	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "c1", Repository: "r", Branch: "b", CommitSHA: "sha", Status: SlipStatusInProgress,
	}))

	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusInProgress, got.Status)
}

func TestPostgresStore_LoadLiveByCommit_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "c1", Repository: "r", Branch: "b", CommitSHA: "sha", Status: SlipStatusInProgress,
	}))

	live, err := store.LoadLiveByCommit(ctx, "r", "sha")
	require.NoError(t, err)
	assert.Equal(t, "c1", live.CorrelationID)

	// Abandon it: LoadLiveByCommit must now exclude it, LoadByCommit must still find it.
	live.Status = SlipStatusAbandoned
	require.NoError(t, store.Update(ctx, live))

	_, err = store.LoadLiveByCommit(ctx, "r", "sha")
	require.ErrorIs(t, err, ErrSlipNotFound)

	still, err := store.LoadByCommit(ctx, "r", "sha")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusAbandoned, still.Status)
}

func TestPostgresStore_Ping_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	require.NoError(t, store.Ping(context.Background()))
}

// TestPostgresStore_Repave_ReplacesRowAndChildren_Integration pins the core Repave
// contract against a real Postgres instance: the superseded run's row and children are
// gone, the successor's row exists, and the successor's own ancestry link is the one the
// caller supplied — all committed as a single unit.
func TestPostgresStore_Repave_ReplacesRowAndChildren_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	old := &Slip{
		CorrelationID: "corr-repave-old",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-repave",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	}
	require.NoError(t, store.Create(ctx, old))
	require.NoError(t, store.UpdateStep(ctx, old.CorrelationID, "builds", "api", StepStatusFailed))
	require.NoError(t, store.InsertAncestryLink(ctx, old, AncestryEntry{
		CorrelationID: "corr-grandparent", CommitSHA: "sha-grandparent",
		Repository: "owner/repo", Branch: "integration",
		Status: SlipStatusCompleted, CreatedAt: time.Now(),
	}))

	successor := &Slip{
		CorrelationID: "corr-repave-new",
		Repository:    old.Repository,
		Branch:        old.Branch,
		CommitSHA:     old.CommitSHA,
		Status:        SlipStatusPending,
		Steps:         map[string]Step{},
		StateHistory:  []StateHistoryEntry{},
	}
	parent := &AncestryEntry{
		CorrelationID: "corr-fresh-parent", CommitSHA: "sha-fresh-parent",
		Repository: "owner/repo", Branch: "integration",
		Status: SlipStatusCompleted, CreatedAt: time.Now(),
	}

	require.NoError(t, store.Repave(ctx, old.CorrelationID, successor, parent))

	_, err := store.Load(ctx, old.CorrelationID)
	assert.ErrorIs(t, err, ErrSlipNotFound, "the superseded row must be gone")

	got, loadErr := store.Load(ctx, successor.CorrelationID)
	require.NoError(t, loadErr,
		"the successor must exist in the same transaction that removed the superseded row")
	assert.Equal(t, old.CommitSHA, got.CommitSHA)

	for _, table := range []string{"slip_component_states", "slip_ancestry"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT count(*) FROM "+table+" WHERE correlation_id = $1", old.CorrelationID).Scan(&n))
		assert.Zero(t, n, table+" rows for the superseded run must be removed")
	}

	var linkedParent string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT parent_correlation_id FROM slip_ancestry WHERE correlation_id = $1",
		successor.CorrelationID).Scan(&linkedParent))
	assert.Equal(t, "corr-fresh-parent", linkedParent,
		"the caller-supplied parent link must be written for the successor")
}

// TestPostgresStore_Repave_RecordsPredecessorOnSuccessor_Integration pins the audit entry.
// After a repave the superseded row and its children are gone, so without this the
// successor carries no evidence a prior run for this commit ever existed — the only other
// link is a span attribute and a log line, neither of which is on any row.
func TestPostgresStore_Repave_RecordsPredecessorOnSuccessor_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	old := &Slip{
		CorrelationID: "corr-audit-old",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-audit",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, old))

	successor := &Slip{
		CorrelationID: "corr-audit-new",
		Repository:    old.Repository,
		Branch:        old.Branch,
		CommitSHA:     old.CommitSHA,
		Status:        SlipStatusPending,
		StateHistory:  []StateHistoryEntry{},
	}
	require.NoError(t, store.Repave(ctx, old.CorrelationID, successor, nil))

	got, err := store.Load(ctx, successor.CorrelationID)
	require.NoError(t, err)

	var found bool
	for _, e := range got.StateHistory {
		if strings.Contains(e.Message, old.CorrelationID) {
			found = true
		}
	}
	assert.True(t, found,
		"the successor's history must name the run it replaced, got %+v", got.StateHistory)
}

// TestPostgresStore_Repave_LinkFailureDoesNotVetoReplacement_Integration pins the SAVEPOINT
// against real Postgres: a link write that violates a constraint must roll back only itself,
// leaving the replacement committed. Without the savepoint the failed statement aborts the
// whole transaction, and Postgres refuses every subsequent statement in it.
//
// The failure is induced with an over-long parent_status: slip_ancestry.parent_status is the
// slip_status enum, so a value outside it is rejected by the type itself.
func TestPostgresStore_Repave_LinkFailureDoesNotVetoReplacement_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	old := &Slip{
		CorrelationID: "corr-sp-old",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-savepoint",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, old))

	successor := &Slip{
		CorrelationID: "corr-sp-new",
		Repository:    old.Repository,
		Branch:        old.Branch,
		CommitSHA:     old.CommitSHA,
		Status:        SlipStatusPending,
	}
	// parent_status is the slip_status enum; "not-a-status" cannot be cast to it.
	badParent := &AncestryEntry{
		CorrelationID: "corr-sp-parent",
		CommitSHA:     "sha-sp-parent",
		Repository:    "owner/repo",
		Branch:        "main",
		Status:        SlipStatus("not-a-status"),
		CreatedAt:     time.Now(),
	}

	require.NoError(t, store.Repave(ctx, old.CorrelationID, successor, badParent),
		"a failing link write must not fail the repave")

	_, oldErr := store.Load(ctx, old.CorrelationID)
	assert.ErrorIs(t, oldErr, ErrSlipNotFound, "the replacement must still have committed")
	_, newErr := store.Load(ctx, successor.CorrelationID)
	require.NoError(t, newErr, "the successor must exist despite the link failure")

	var links int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM slip_ancestry WHERE correlation_id = $1",
		successor.CorrelationID).Scan(&links))
	assert.Zero(t, links, "only the link itself should have been rolled back")
}

// TestPostgresStore_Repave_CarriesForwardParentLink_Integration pins the TR-4 fix: when
// the caller has no resolved ancestry to supply (parent == nil, e.g. a GitHub outage made
// resolveAndAbandonAncestors return no entries), the superseded run's OWN parent link is
// carried forward to the successor rather than deleted with it. Before Repave, that hop
// was destroyed and never replaced, permanently truncating any descendant's walk.
func TestPostgresStore_Repave_CarriesForwardParentLink_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	old := &Slip{
		CorrelationID: "corr-carry-old",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-carry",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, old))
	require.NoError(t, store.InsertAncestryLink(ctx, old, AncestryEntry{
		CorrelationID: "corr-carried-parent", CommitSHA: "sha-carried-parent",
		Repository: "owner/repo", Branch: "main",
		Status: SlipStatusCompleted, FailedStep: "", CreatedAt: time.Now(),
	}))

	successor := &Slip{
		CorrelationID: "corr-carry-new",
		Repository:    old.Repository,
		Branch:        old.Branch,
		CommitSHA:     old.CommitSHA,
		Status:        SlipStatusPending,
	}

	require.NoError(t, store.Repave(ctx, old.CorrelationID, successor, nil))

	var carried, carriedSHA string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT parent_correlation_id, parent_commit_sha FROM slip_ancestry WHERE correlation_id = $1",
		successor.CorrelationID).Scan(&carried, &carriedSHA))
	assert.Equal(t, "corr-carried-parent", carried,
		"the superseded run's own parent link must be carried forward when the caller supplies none")
	assert.Equal(t, "sha-carried-parent", carriedSHA)
}

// TestPostgresStore_Repave_MissingOldRow_Integration pins the idempotent path: a repave
// whose superseded row is already gone still creates the successor (so a Kafka redelivery
// converges) and must NOT repoint anything, since it did not remove the row itself.
func TestPostgresStore_Repave_MissingOldRow_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	// A descendant that points at the already-gone correlation ID. Repave must leave it
	// alone: this call did not delete that row, so it has no licence to rewrite unrelated
	// ancestry (the D2.1 no-op contract).
	bystander := &Slip{
		CorrelationID: "corr-bystander",
		Repository:    "owner/repo",
		Branch:        "feature",
		CommitSHA:     "sha-bystander",
		Status:        SlipStatusInProgress,
	}
	require.NoError(t, store.Create(ctx, bystander))
	require.NoError(t, store.InsertAncestryLink(ctx, bystander, AncestryEntry{
		CorrelationID: "corr-never-existed", CommitSHA: "sha-gone",
		Repository: "owner/repo", Branch: "main",
		Status: SlipStatusFailed, CreatedAt: time.Now(),
	}))

	successor := &Slip{
		CorrelationID: "corr-missing-old-new",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-missing-old",
		Status:        SlipStatusPending,
	}
	require.NoError(t, store.Repave(ctx, "corr-never-existed", successor, nil))

	_, err := store.Load(ctx, successor.CorrelationID)
	require.NoError(t, err, "the successor must be created even when the superseded row was already gone")

	var stillDangling string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT parent_correlation_id FROM slip_ancestry WHERE correlation_id = $1",
		bystander.CorrelationID).Scan(&stillDangling))
	assert.Equal(t, "corr-never-existed", stillDangling,
		"a repave that deleted nothing must not repoint descendants")
}

// TestPostgresStore_Repave_RepointsDescendants_Integration pins FIX 3 against a real
// Postgres instance: another slip whose slip_ancestry row points at the repaved slip as
// its parent must be repointed to the successor, not left dangling (a dangling link would
// silently truncate that descendant's ResolveAncestry walk at this hop). It also pins D2.2
// (DEVOPS-231 review): the repoint clears parent_failed_step, since the deleted run's
// failed step is unambiguously wrong once the id beside it names the successor run.
//
// The parent_branch assertion pins the TR-3 fix. ResolveAncestry's next hop looks up
// (repository, branch, correlation_id) using the branch recorded BESIDE the parent id, so
// repointing the id while leaving parent_branch describing the deleted run truncated the
// walk for exactly the cross-branch repave this feature supports. Repave knows the
// successor's branch (it inserts that row in the same transaction), so it writes it.
func TestPostgresStore_Repave_RepointsDescendants_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	old := &Slip{
		CorrelationID: "corr-parent-repave",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-parent-repave",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, old))

	child := &Slip{
		CorrelationID: "corr-child-of-repaved",
		Repository:    "owner/repo",
		Branch:        "feature",
		CommitSHA:     "sha-child-of-repaved",
		Status:        SlipStatusInProgress,
	}
	require.NoError(t, store.Create(ctx, child))
	require.NoError(t, store.InsertAncestryLink(ctx, child, AncestryEntry{
		CorrelationID: old.CorrelationID, CommitSHA: old.CommitSHA,
		Repository: old.Repository, Branch: old.Branch,
		Status: SlipStatusFailed, FailedStep: "unit_tests", CreatedAt: time.Now(),
	}))

	// Cross-branch repave: the successor lands on a different branch than the run it
	// supersedes, which is the case that exposed the stale parent_branch.
	successor := &Slip{
		CorrelationID: "corr-successor",
		Repository:    old.Repository,
		Branch:        "release",
		CommitSHA:     old.CommitSHA,
		Status:        SlipStatusPending,
	}
	require.NoError(t, store.Repave(ctx, old.CorrelationID, successor, nil))

	_, err := store.Load(ctx, old.CorrelationID)
	assert.ErrorIs(t, err, ErrSlipNotFound, "the repaved slip itself must be gone")

	var newParent, failedStep, parentBranch, parentStatus string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT parent_correlation_id, parent_failed_step, parent_branch, parent_status "+
			"FROM slip_ancestry WHERE repository = $1 AND branch = $2 AND correlation_id = $3",
		child.Repository, child.Branch, child.CorrelationID).
		Scan(&newParent, &failedStep, &parentBranch, &parentStatus))
	assert.Equal(t, successor.CorrelationID, newParent,
		"descendant must be repointed to the successor, not dangling")
	assert.Empty(t, failedStep,
		"parent_failed_step must be cleared on repoint: the pre-repave run's failed step is wrong for the successor")
	assert.Equal(t, "release", parentBranch,
		"parent_branch must name the successor's branch — it is ResolveAncestry's next-hop join key")
	assert.Equal(t, string(SlipStatusPending), parentStatus,
		"parent_status must describe the successor, which Repave creates in this same transaction")
}

// TestPostgresStore_Repave_RollsBackWhenSuccessorInsertFails_Integration pins the TR-2
// fix, the finding that motivated Repave: a deterministic insert failure must leave the
// superseded run intact instead of destroying it with no replacement.
//
// The failure is the real production hazard, not a synthetic one: slipColumns() derives the
// INSERT column list from the pipeline config, so deploying a config carrying a new step
// BEFORE the migration that adds that step's _status column makes every insert reference a
// nonexistent column (Postgres 42703). Under the old delete-then-create sequence the delete
// had already committed, so the commit was left with no slip at all and each Kafka
// redelivery failed identically — permanently. Here the whole thing rolls back.
func TestPostgresStore_Repave_RollsBackWhenSuccessorInsertFails_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	old := &Slip{
		CorrelationID: "corr-rollback-old",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-rollback",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, old))
	require.NoError(t, store.InsertAncestryLink(ctx, old, AncestryEntry{
		CorrelationID: "corr-rollback-parent", CommitSHA: "sha-rollback-parent",
		Repository: "owner/repo", Branch: "main",
		Status: SlipStatusCompleted, CreatedAt: time.Now(),
	}))

	// A store whose config declares a step the migrated schema has no column for —
	// config-ahead-of-migration, the ordinary deploy-ordering hazard.
	aheadCfg, err := ParsePipelineConfig([]byte(`{
		"version": "1.0",
		"name": "pg-test-ahead",
		"steps": [
			{"name": "push_parsed", "description": "push received"},
			{"name": "builds", "description": "container builds", "aggregates": "component_builds", "prerequisites": ["push_parsed"]},
			{"name": "unit_tests", "description": "unit tests", "prerequisites": ["builds"], "is_gate": true},
			{"name": "dev_deploy", "description": "deploy to dev", "prerequisites": ["unit_tests"]},
			{"name": "not_yet_migrated", "description": "step whose column does not exist yet"}
		]
	}`))
	require.NoError(t, err)
	aheadStore, err := NewPostgresStore(pool, aheadCfg, nil)
	require.NoError(t, err)

	successor := &Slip{
		CorrelationID: "corr-rollback-new",
		Repository:    old.Repository,
		Branch:        old.Branch,
		CommitSHA:     old.CommitSHA,
		Status:        SlipStatusPending,
	}

	repaveErr := aheadStore.Repave(ctx, old.CorrelationID, successor, nil)
	require.Error(t, repaveErr, "an insert against a nonexistent column must fail the repave")

	survived, loadErr := store.Load(ctx, old.CorrelationID)
	require.NoError(t, loadErr,
		"TR-2: the superseded run must survive a failed successor insert, not be destroyed with no replacement")
	assert.Equal(t, SlipStatusFailed, survived.Status)

	var linkCount int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM slip_ancestry WHERE correlation_id = $1", old.CorrelationID).Scan(&linkCount))
	assert.Equal(t, 1, linkCount, "the superseded run's own ancestry link must be rolled back into place too")

	_, successorErr := store.Load(ctx, successor.CorrelationID)
	assert.ErrorIs(t, successorErr, ErrSlipNotFound, "no half-created successor may be left behind")
}

// TestPostgresStore_Repave_WentLive_Integration pins FIX 2's TOCTOU guard against a real
// Postgres instance: a slip that recovers to live between the caller's repave decision and
// the Repave call must be rejected, not destroyed — and no successor may be created, since
// that would leave two competing live runs for one commit.
func TestPostgresStore_Repave_WentLive_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	slip := &Slip{
		CorrelationID: "corr-went-live",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-went-live",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, slip))

	// Simulate executor.go's recovery branch: the failed slip recovers to in_progress
	// after the caller already decided (from an earlier snapshot) to repave it.
	require.NoError(t, store.UpdateSlipStatus(ctx, slip.CorrelationID, SlipStatusInProgress))

	successor := &Slip{
		CorrelationID: "corr-should-not-exist",
		Repository:    slip.Repository,
		Branch:        slip.Branch,
		CommitSHA:     slip.CommitSHA,
		Status:        SlipStatusPending,
	}
	err := store.Repave(ctx, slip.CorrelationID, successor, nil)
	require.ErrorIs(t, err, ErrSlipWentLive)

	got, loadErr := store.Load(ctx, slip.CorrelationID)
	require.NoError(t, loadErr, "the now-live slip must survive the rejected repave")
	assert.Equal(t, SlipStatusInProgress, got.Status)

	_, successorErr := store.Load(ctx, successor.CorrelationID)
	assert.ErrorIs(t, successorErr, ErrSlipNotFound,
		"a rejected repave must not create the successor: two live runs for one commit is the outcome being prevented")
}
