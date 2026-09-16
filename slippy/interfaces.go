package slippy

import (
	"context"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// Logger is an alias for the unified logger.Logger interface.
// This provides structured, context-aware logging throughout the slippy package.
type Logger = logger.Logger

// SlipStore defines the interface for slip persistence operations.
// Implementations provide storage backends (e.g., ClickHouse, in-memory for testing).
//
// All methods that identify a slip use correlationID as the unique identifier.
// The correlationID is the single, canonical identifier for a routing slip
// throughout its entire lifecycle.
type SlipStore interface {
	// Create persists a new routing slip.
	//
	// Implementations upsert rather than reject an existing correlation ID (the Kafka
	// redelivery path), and claimed_from is NOT among the columns the conflict arm writes:
	// the Postgres SET list is slipColumns(), which excludes it. So a redelivered Create
	// carrying a TERMINAL status over a claimed row resets the row's own columns and leaves
	// the claim standing — a terminal status in a caller's snapshot never ends a claim,
	// only UpdateSlipStatus does (DEVOPS-367). That is the safe direction: the snapshot
	// cannot clear a claim taken after it was read. If a claim is left behind by a run that
	// will never release it, the way out is the recovery route documented on ErrNotClaimed
	// in errors.go — resolve the step that holds it, then release.
	Create(ctx context.Context, slip *Slip) error

	// Load retrieves a slip by its correlation ID (the unique slip identifier).
	//
	// Error contract, same shape as LoadByCommit's below: a clean miss MUST return
	// ErrSlipNotFound and a nil slip. In particular an implementation MUST NOT return
	// (nil, nil) — every in-repo caller checks only `err != nil` before dereferencing, and
	// (nil, nil) is not an error, so an error check does not screen it. The push path relies
	// on this at three sites: repaveExistingSlip's went-live reload feeds the result straight
	// into handlePushRetry, the duplicate-create backstop assigns it to result.Slip, and
	// handlePushRetry's own trailing Load becomes result.Slip. None of them nil-check, and
	// adding checks at 3 of 17 call sites would be worse than none.
	//
	// This is a contract-completeness requirement rather than a live hazard: all three
	// in-repo implementations return ErrSlipNotFound on a miss, nilnil is enabled in
	// .golangci.yml, and there is no out-of-repo SlipStore implementation today.
	Load(ctx context.Context, correlationID string) (*Slip, error)

	// LoadByCommit retrieves a slip by repository and commit SHA.
	//
	// Error contract (DEVOPS-231 review D3.5): a clean miss — no row exists for this
	// (repository, commitSHA) — MUST return ErrSlipNotFound and a nil slip. Any other
	// non-nil error is treated by CreateSlipForPush (push.go) as a hard failure of the
	// push: it aborts and returns the error to the caller rather than proceeding as if
	// no slip existed, so Kafka redelivers the message. A store that signals absence with
	// a bespoke error (e.g. wrapping sql.ErrNoRows without translating it, or returning a
	// generic error from a degraded/partial read such as a hydration failure) makes that
	// miss look like a hard failure instead: the push then fails every redelivery forever
	// and no slip is ever created for that commit. Implementations must translate any
	// "no rows" condition to ErrSlipNotFound before returning, and must not use
	// ErrSlipNotFound for anything other than a genuine clean miss.
	//
	// Selection contract: wherever the Phase B cleanup has not run and migration v5's
	// uq_routing_slips_repo_sha is not yet applied, MORE THAN ONE row can exist for a
	// (repository, commit_sha). When several match, an implementation
	// MUST return a LIVE row (Status.IsLive()) if any exists, and the most recently updated one
	// otherwise. This is behaviour, not an implementation detail: CreateSlipForPush routes
	// entirely on the returned row's status — a live row dedups onto it, anything else is
	// repaved and CI fully re-dispatches — so returning an ended duplicate while a run is still
	// in flight repaves a running pipeline.
	//
	// Ordering by recency alone is NOT sufficient, and the failure is not exotic: a `completed`
	// duplicate touched after the live row's last write sorts first on updated_at DESC, and no
	// status filter screens it because `completed` is not terminal-superseded. State the rule
	// here rather than leaving it to each store, so dropping the live-first term from a query
	// reads as the contract break it is instead of a local optimisation.
	//
	// ClickHouseStore deliberately does not implement this ordering; the reason is specific to
	// VersionedCollapsingMergeTree and is documented at that implementation.
	LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error)

	// LoadLiveByCommit returns the LIVE (non-terminal) slip for the exact (repository, commitSHA).
	// Excludes status in {abandoned, promoted, compensated}. Returns ErrSlipNotFound when no live
	// slip exists. Use for in-flight dedup paths that require exact-SHA semantics. For
	// ancestry-aware lookups use FindByCommits/ResolveSlip.
	//
	// Error contract: identical to LoadByCommit above — a clean miss (no live row) MUST be
	// ErrSlipNotFound; any other error is a hard failure to callers on the push path. See
	// LoadByCommit's doc for the full rationale.
	LoadLiveByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error)

	// FindByCommits finds a slip matching any commit in the ordered list.
	// Returns the slip for the first (most recent) matching commit.
	// The third return value is the matched commit SHA.
	FindByCommits(ctx context.Context, repository string, commits []string) (*Slip, string, error)

	// FindAllByCommits finds all slips matching any commit in the ordered list.
	// Returns slips ordered by commit priority (first matching commit's slip first).
	// Each result includes the slip and its matched commit SHA.
	FindAllByCommits(ctx context.Context, repository string, commits []string) ([]SlipWithCommit, error)

	// Update persists changes to an existing slip.
	// With timestamp-based versioning, each update gets a unique nanosecond timestamp,
	// so there are no version conflicts.
	//
	// claimed_from is SELECT-only and is never written by Update, whatever status the slip
	// carries: the status in a full-row write comes from the caller's own snapshot, and a
	// claim may have been taken after that read, so keying a clear off it would end a claim
	// the caller never saw. UpdateSlipStatus is the one write path that ends a claim
	// (DEVOPS-367).
	Update(ctx context.Context, slip *Slip) error

	// UpdateStep updates a specific step's status
	UpdateStep(ctx context.Context, correlationID, stepName, componentName string, status StepStatus) error

	// UpdateStepWithHistory updates a step's status AND appends a history entry in a single atomic operation.
	// This prevents race conditions between separate UpdateStep and AppendHistory calls.
	UpdateStepWithHistory(
		ctx context.Context,
		correlationID, stepName, componentName string,
		status StepStatus,
		entry StateHistoryEntry,
	) error

	// UpdateComponentStatus updates a component's build or test status
	UpdateComponentStatus(ctx context.Context, correlationID, componentName, stepType string, status StepStatus) error

	// AppendHistory adds a state history entry to the slip
	AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error

	// UpdateSlipStatus atomically updates the slip's top-level status without a full Load+Update
	// round-trip. Uses INSERT SELECT to copy the current DB row and override only the status
	// column, preventing concurrent history appends from being lost under last-write-wins.
	//
	// This is THE ONE WRITE PATH THAT ENDS A CLAIM: a terminal status also clears
	// claimed_from, because terminal ends the run (DEVOPS-367). Non-terminal statuses —
	// failed included — leave the claim in place. Neither Create nor the full-row Update
	// touches the column, so every library path that must end a claim (AbandonSlip,
	// PromoteSlip, checkPipelineCompletion) comes through here.
	UpdateSlipStatus(ctx context.Context, correlationID string, status SlipStatus) error

	// ClaimSlip records that a run is in flight against a slip, as ONE transaction: lock
	// the row, compare-and-set on the current status, append the marker, set claimed_from to
	// the status the row had. It never writes status (DEVOPS-367).
	//
	// The claim is a flag. It lives until the run is over: a post-job's ReleaseClaim that finds
	// nothing in flight, or a terminal status write through UpdateSlipStatus — the one write
	// path that ends a claim. While it is held, Repave refuses the row (ErrSlipWentLive) and
	// the push path dedups onto it, whatever status the pipeline writes meanwhile. An abandon
	// or promote is the exception: both are terminal statuses written from outside the run, so
	// they end the claim even while steps are still running, and both are repaveable — an
	// ancestor abandon or a promotion deliberately overrides a live claim.
	//
	// expected is the set of statuses the caller agreed to claim out of. On a FRESH claim it
	// is a compare-and-set on the current status; nil admits any status EXCEPT an unclaimed
	// in_progress, which is a live run nobody has adopted — a caller that means to claim one
	// says so by listing in_progress (the Slippy CLI pre-job does; the rerunner, which names
	// only the ended set, is what the refusal protects). On a REPEAT claim expected is checked
	// against the RECORDED prior instead, so a retry after a lost response is idempotent while
	// a different claimant that did not agree to that prior is still refused. The decision is
	// DecideClaim, shared with the test doubles.
	//
	// The store builds the marker itself with ClaimMarker(prior, claimedBy, reason), because
	// only it knows the true status at write time. claimedBy is audit only: it names the
	// actor in the marker and is never a key the claim is checked against. There is no claim
	// owner; every pre-job of a run claims the same slip and every post-job releases it.
	//
	// Returns the prior status, and:
	//   - nil: claimed_from = prior was written and the marker appended.
	//   - nil with no write: the slip was already claimed; the returned prior is the recorded
	//     claimed_from. Repeat claims are idempotent — no second marker — so a retried
	//     request cannot inflate the audit trail, and all but the first pre-job of a run
	//     take this arm.
	//   - ErrClaimPreconditionFailed: the status was outside expected (the current status on a
	//     fresh claim, the recorded prior on a repeat), the slip has no status at all (an
	//     empty status cannot be recorded as a claim), or it is an unclaimed in_progress run
	//     and expected did not name in_progress. Nothing written.
	//   - ErrSlipNotFound: no row for correlationID.
	//   - ErrClaimUnsupported (wrapped): the store cannot claim at all (ClickHouse).
	ClaimSlip(
		ctx context.Context, correlationID string, expected []SlipStatus, claimedBy, reason string,
	) (SlipStatus, error)

	// ReleaseClaim ends a claim once nothing of the run is in flight, as ONE transaction
	// (DEVOPS-367): read the claim state FOR UPDATE — the claim, the status and every step
	// and aggregate column, which is DecideRelease's whole input, and nothing else — decide
	// (DecideRelease, shared with the test doubles), clear claimed_from and append a release
	// marker. It never writes status.
	//
	// Every post-job calls this on exit, whatever its own step's outcome, and MUST have
	// written its own step's terminal status FIRST: this call judges quiescence from the row,
	// so a post-job that releases before recording its step counts itself as in flight and no
	// post-job of the run ever clears the claim. While any step or component is running or
	// held the claim is KEPT — ReleaseOutcome{Released: false} with nothing written, which is
	// information rather than a failure — so the last post-job, the one that finds nothing in
	// flight, clears it. Held counts because a held step is work the run has committed to; a
	// step never reported at all reads pending and holds nothing. push_parsed, the library's
	// own bookkeeping step, never counts as in flight.
	//
	// releasedBy is audit only. ReleaseOutcome.Status is the slip's status at decision time on
	// BOTH arms — a release never writes status.
	//   - Released=false, nil error: the claim is held and work is in flight. Nothing written.
	//     If the run is DEAD — a step left running or held by a workflow that will never
	//     report — the stuck step is what holds the claim: resolve it (complete, fail or skip
	//     it), then release again, which now finds nothing in flight. On a NON-terminal slip
	//     POST /v1/slips/{id}/abandon also ends it, because AbandonSlip writes the terminal
	//     abandoned through UpdateSlipStatus; on an already-terminal slip AbandonSlip is a
	//     deliberate no-op (I4) and clears nothing, so use the step-then-release route there.
	//   - ErrNotClaimed: claimed_from empty — the normal outcome after a terminal status
	//     write already ended the claim. Nothing written.
	//   - ErrSlipNotFound: no row for correlationID.
	//   - ErrClaimUnsupported (wrapped): the store cannot release (ClickHouse).
	ReleaseClaim(ctx context.Context, correlationID, releasedBy, reason string) (ReleaseOutcome, error)

	// ProbeSchema is the readiness gate: it checks the columns this store's SELECTs name
	// against the live schema and returns ErrSchemaBehind when any are missing, so a process
	// running a library ahead of its database can refuse to serve instead of failing every
	// read at request time (DEVOPS-367). A store with no schema of its own to check —
	// ClickHouse, which is not the operational slip store — returns nil.
	ProbeSchema(ctx context.Context) error

	// Repave atomically replaces one commit's ended run with a fresh one: it removes the
	// routing_slips row for oldCorrelationID and its child rows (slip_component_states,
	// slip_ancestry), then creates newSlip — ALL AS ONE UNIT. Used by the same-commit repave
	// path (DEVOPS-231): a retrigger of an ended slip supersedes the prior run with a new one
	// under newSlip.CorrelationID.
	//
	// Atomicity of THAT replacement is the whole point of the method existing, and
	// implementations MUST provide it. The delete and the create were previously two separate
	// store calls, so a create failure after a committed delete left the commit with NO slip
	// at all and no way back: the next redelivery found no row to repave and failed the same
	// way. Any error from Repave therefore leaves the store exactly as it was.
	//
	// newSlip's own direct-parent link is written inside the same call but is deliberately NOT
	// part of that atomic unit: it is best-effort, and a failure to write it rolls back only
	// the link while the replacement still commits. Repave returns nil in that state. The
	// reasoning is that the link is the least important write here — a missing hop degrades a
	// later ancestry walk, whereas vetoing the replacement over it would fail the push and
	// leave the caller with no slip. Implementations MAY make the link atomic too, but MUST
	// NOT let its failure veto the replacement.
	//
	// Read "its failure" narrowly: it is the LINK WRITE that may not veto. A failure of the
	// savepoint machinery around it — opening, rolling back with anything other than an
	// already-closed error, or releasing — is a different class and DOES abort the whole
	// replacement, because at that point the transaction's state is no longer known to be
	// sound. Those land in the "any other error: nothing is written" case below.
	//
	// The delete half is status-guarded: it removes the row ONLY when its status is ended
	// (failed, completed, abandoned, promoted, compensated), so a slip that has gone live
	// again between the caller's repave decision and this call is never destroyed. The
	// superseded run's row, state_history, component states and ancestry rows are destroyed,
	// not archived — a recorded decision (DEVOPS-231 §4, confirmed DEVOPS-277).
	//
	// Descendant links: any OTHER slip whose ancestry points at oldCorrelationID as its
	// parent is repointed to newSlip — the WHOLE denormalized snapshot describing the parent is
	// rewritten: id, REPOSITORY, branch, status and commit SHA now name the successor,
	// created_at is re-stamped, and parent_failed_step is cleared — rather than left dangling,
	// which would silently truncate that descendant's ResolveAncestry walk. Every column
	// matters, not just the id: ResolveAncestry's next hop is an exact, case-sensitive match on
	// (repository, branch, correlation_id), so a stale repository or branch truncates the walk
	// exactly as a stale id would, and a stale parent_commit_sha leaves the descendant's
	// AncestryEntry.CommitSHA naming a run that no longer exists.
	//
	// The repoint happens AFTER newSlip's row exists, so it never names a correlation ID that
	// does not yet exist. That ordering is necessary but not sufficient for a foreign key on
	// slip_ancestry.parent_correlation_id: the guarded DELETE still runs first, while
	// descendants reference the row it removes, so a plain (NOT DEFERRABLE, NO ACTION) FK
	// would raise 23503 at the end of that statement for every repave that has a descendant.
	// Migration v5 (Phase B) deliberately adds no such FK — both of its FKs are on correlation_id.
	//
	// Descendants are repointed only when this call actually removed the old row: a repave
	// whose old row was already gone rewrites nothing, so a redelivery can never reassign an
	// unrelated descendant's parent.
	//
	// parent is newSlip's direct-parent link, or nil when the caller resolved none. When
	// it is nil and the superseded run had a parent link of its own, that link is carried
	// forward to newSlip instead of being destroyed with the old row — otherwise a
	// transient ancestry-resolution failure (e.g. a GitHub outage) would permanently
	// delete a lineage hop rather than merely fail to extend it.
	//
	// newSlip.CorrelationID must differ from oldCorrelationID. Passing the same value is
	// rejected rather than treated as a no-op: it would otherwise destroy an ended run's
	// history and children and re-insert it fresh under an unchanged ID, which no log line
	// or row can distinguish from nothing having happened.
	//
	// Note the successor insert is an UPSERT on correlation_id (unchanged from Create), so
	// a newSlip.CorrelationID that already belongs to some OTHER run overwrites that run's
	// row wholesale rather than failing. Callers mint correlation IDs per push and so do
	// not collide in practice, but nothing in this method enforces it.
	//
	// That collision is fail-OPEN, not fail-closed, which is what makes it worth stating.
	// Repave deletes the children of oldCorrelationID only — never of newSlip.CorrelationID —
	// and slip_component_states is keyed (correlation_id, step, component), so the victim's
	// component rows SURVIVE under the colliding ID and are inherited by the successor. On the
	// successor's own first component write, recomputeAggregate reads every row for that ID
	// and computeAggregateStatus can resolve the aggregate to completed over inherited rows for
	// components this run will never report, after which AllPrerequisitesMet reports satisfied.
	// Nothing recomputes at creation time (the empty-active-set early return), so the trigger
	// is that first component write rather than the collision itself. The fail-closed argument
	// for a colliding replacement holds for the routing_slips row, which starts pending; it
	// does NOT extend to child rows that were never deleted.
	//
	// The "exactly one slip_ancestry link row per correlation_id" invariant the carry-forward
	// read relies on rests on this same unenforced no-reuse premise.
	//
	// Returns:
	//   - nil: newSlip exists, and the superseded row is gone (removed here, or already
	//     absent — an absent old row is not an error, so redelivery converges).
	//   - ErrSlipWentLive: oldCorrelationID's row exists but is no longer ended, or a
	//     claimant holds it (claimed_from set, DEVOPS-367). Nothing
	//     is written and newSlip is NOT created; the caller must dedup onto the live run.
	//   - ErrDuplicateSlip: newSlip collided with the one-row-per-commit unique index
	//     (migration v5, Phase B). Nothing is written; the caller routes to its dedup backstop.
	//   - ErrInvalidConfiguration: a precondition on the arguments was violated — newSlip is
	//     nil, or newSlip.CorrelationID equals oldCorrelationID. Nothing is written, no
	//     transaction is opened, and REDELIVERY CANNOT CLEAR IT: the offending value is the
	//     caller's own input and is stable across attempts, so the push fails identically
	//     every time. Callers must treat it as a caller bug rather than a transient store
	//     failure; the push path gives it its own arm for exactly that reason.
	//   - any other error: nothing is written.
	//
	// The successor insert remaining an UPSERT rather than a conflict-free INSERT is a
	// deliberate choice, not an oversight: it is what makes an absent old row converge (the
	// row is simply written) and what keeps createTx byte-identical to Create. The cost is the
	// cross-run collision documented above, which is accepted and documented rather than
	// prevented.
	//
	// A store that cannot repave at all — e.g. ClickHouseStore, which is not the
	// operational slip store (DEVOPS-127) — MUST return an error wrapping
	// ErrRepaveUnsupported rather than a plain error or nil. The push path detects that
	// sentinel with errors.Is and falls back to abandon semantics (marking the superseded
	// slip abandoned, then creating the successor separately) instead of repaving.
	Repave(ctx context.Context, oldCorrelationID string, newSlip *Slip, parent *AncestryEntry) error

	// SetComponentImageTag records the built container image tag for a component in the event log.
	// stepName is the component step type (e.g. "build"); componentName is the service name.
	SetComponentImageTag(ctx context.Context, correlationID, stepName, componentName, imageTag string) error

	// InsertAncestryLink writes a single direct-parent link to the ancestry table.
	//
	// Implementations MAY require the slip's own row to exist first — the Postgres store does
	// once migration v5's fk_ancestry_slip (on correlation_id) is applied — so callers write
	// the slip before its link, as the push path already does. The PARENT side may dangle:
	// there is deliberately no FK on parent_correlation_id (see Repave).
	InsertAncestryLink(ctx context.Context, slip *Slip, parent AncestryEntry) error

	// ResolveAncestry walks parent links to reconstruct the full ancestry chain.
	// Returns entries ordered from direct parent to oldest ancestor, capped at maxDepth.
	ResolveAncestry(
		ctx context.Context,
		repository, branch, correlationID string,
		maxDepth int,
	) ([]AncestryEntry, error)

	// Close releases any resources held by the store
	Close() error

	// Ping verifies the underlying database connection is alive.
	// Returns nil if the connection is healthy, or an error if it is stale/dead.
	Ping(ctx context.Context) error
}

// GitHubAPI defines the interface for GitHub operations.
// This allows for mocking in tests and supports different GitHub implementations.
type GitHubAPI interface {
	// GetCommitAncestry retrieves the commit ancestry for a given ref.
	// Returns a slice of commit SHAs in order from newest to oldest.
	GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error)

	// GetPRHeadCommit retrieves the head commit SHA for a pull request.
	// This is used to link squash merge commits back to the original feature branch slip.
	// Returns the SHA of the PR's head commit before merging.
	GetPRHeadCommit(ctx context.Context, owner, repo string, prNumber int) (string, error)

	// ClearCache clears any cached data (useful for testing)
	ClearCache()
}

// PrereqResult represents the result of a prerequisite check.
// It provides details about which prerequisites are completed, running, or failed.
type PrereqResult struct {
	// Status is the aggregate prerequisite status
	Status PrereqStatus

	// FailedPrereqs lists prerequisites that have failed
	FailedPrereqs []string

	// RunningPrereqs lists prerequisites still in progress
	RunningPrereqs []string

	// CompletedPrereqs lists prerequisites that completed successfully
	CompletedPrereqs []string
}

// NopLogger returns a no-op logger that discards all messages.
// This is the default logger when none is provided.
func NopLogger() Logger {
	return &logger.NopLogger{}
}

// NewStdLogger creates a simple standard output logger.
// Set debug to true to enable debug-level logging.
func NewStdLogger(debug bool) Logger {
	return logger.NewStdLogger(debug)
}
