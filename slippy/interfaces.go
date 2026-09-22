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
	// Implementations upsert rather than reject an existing correlation ID, and claimed_from
	// is NOT among the columns the conflict arm writes: the Postgres SET list is
	// slipColumns(), which excludes it. So a Create carrying a TERMINAL status over a claimed
	// row resets the row's own columns and leaves the claim standing — a terminal status in a
	// caller's snapshot never ends a claim, only UpdateSlipStatus does (DEVOPS-367). That is
	// the safe direction: the snapshot cannot clear a claim taken after it was read. If a
	// claim is left behind by a run that will never release it, the way out is the recovery
	// route documented on ErrNotClaimed in errors.go — resolve the step that holds it, then
	// release.
	//
	// WHICH CALLER ACTUALLY REACHES THE CONFLICT ARM. Not a Kafka redelivery, whatever an
	// earlier version of this comment said (finding p3): pushhookparser mints a NEW
	// correlation ID per delivery (pkg/cmd/consumer.go), so a redelivery cannot collide on
	// ON CONFLICT (correlation_id) at all — it collides, if anything, on the commit, which is
	// LoadByCommit's and Repave's business. The one caller that arrives with an id already in
	// the table is the IN-DELIVERY bounded retry, which reuses its own id; push.go names it
	// correctly at persistSlipForPush's self-referential arm.
	//
	// THE CLAIM INVARIANT THIS PATH MUST PRESERVE: claimed_from and the slip_claimed marker
	// in state_history are both present or both absent. The conflict arm does not write
	// claimed_from but DOES write state_history (it is in slipColumns()), so an upsert over a
	// claimed row would otherwise keep the column and destroy the marker — and pushhookparser
	// derives "who claimed this" from the markers and gates its stranded-cleanup exemption on
	// it, so the row would read claimed to one reader and unclaimed to the other (finding p2).
	//
	// A CALLER CANNOT KEEP THAT INVARIANT THROUGH THIS METHOD, and that is why
	// ResetSlipInPlace exists rather than a convention about what to write here. Whether the
	// row is claimed is knowable only under a row lock this method never takes, and the push
	// path's own evidence is an unlocked read taken seconds of GitHub calls earlier — so a
	// caller carrying the claim forward from its snapshot restores the marker only for a claim
	// it happened to see. Any caller upserting over a row that may be claimed uses
	// ResetSlipInPlace, which re-states the marker from the row it has locked; push.go's
	// in-place reset arms both do.
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
	// the push path dedups onto it, whatever status the pipeline writes meanwhile — with ONE
	// exception, added by finding p1: a push bearing the claimed row's OWN correlation ID is
	// that row's in-delivery retry rather than another run, and it resets the row in place
	// when the run is quiescent. It does not when a step or component is in flight; then the
	// dedup applies as it does to any other push. An abandon or promote is the other
	// exception: both are terminal statuses written from outside the run, so they end the
	// claim even while steps are still running, and both are repaveable — an ancestor abandon
	// or a promotion deliberately overrides a live claim.
	//
	// "QUIESCENT" THERE MEANS UNDER THE ROW LOCK THE RESET ITSELF TAKES, not as of the push's
	// own read, and the difference used to be reachable (DEVOPS-367, closing PR #87 pkuzmenko
	// finding 2). The push reads its claim evidence from an UNLOCKED LoadByCommit and ancestor
	// resolution's GitHub round trips run for seconds before it writes, so a row it read
	// unclaimed and quiescent can be claimed, and its first step started, before the write
	// lands. That write is now ResetSlipInPlace, which re-reads the claim state FOR UPDATE,
	// evaluates RunInFlight on that read and either upserts or refuses with
	// ErrSlipClaimedInFlight, all in one transaction — so a claim taken in the window is seen,
	// a step started in the window is seen, and the push deduplicates onto the live row
	// instead of resetting it. The marker is re-stated from the locked row too, so
	// claimed_from and slip_claimed stay both present or both absent (the invariant stated
	// under Create above) even for a claim the push never read. The claim itself still does
	// not protect against this — ClaimSlip's own lock is released at its commit — the reset's
	// lock does. The full account is on CreateSlipForPush's claimed arm in push.go.
	//
	// expected is the set of statuses the caller agreed to claim out of, and it is ALWAYS a
	// compare-and-set on the CURRENT status — whether or not a claim is already held. On an
	// UNCLAIMED row, nil admits any status EXCEPT one whose run has a step or component IN
	// FLIGHT (running or held): a run that is executing is never ADOPTED by a caller that did
	// not name its status. A caller that means to adopt a running run says so by listing the
	// status (the Slippy CLI pre-job lists every non-terminal status; the rerunner, which
	// names only the ended set, is what the refusal protects). The decision is DecideClaim,
	// shared with the test doubles.
	//
	// THAT REFUSAL DOES NOT APPLY TO A ROW THAT IS ALREADY CLAIMED, and the qualifier is the
	// fix for finding j-claim (PR #87, jhicks round). Adoption is the WRITE; on a claimed row
	// there is nothing to adopt, the claim stays where it is, and nothing is written on either
	// ordering — so refusing there bought no protection and made the idempotency promised
	// below false for every caller sending a nil expected: pre-job 1's StartStep puts the run
	// in flight, and a nil expected names nothing, so pre-job 2 of the SAME run got
	// ErrClaimPreconditionFailed on a slip its own run held. The repeat arm now sits ahead of
	// the refusal; the arm that RECORDS a claim still sits behind it.
	//
	// That refusal reads the step and aggregate columns, NOT the status name, and the change
	// is visible in both directions (finding j3): a `pending` slip with a step running is
	// refused, where the old status-name rule carved `pending` out as "nothing dispatched onto
	// it"; and an `in_progress` slip between one step's post-job and the next step's pre-job is
	// claimable, where the old rule refused it as "a live run". A slip keeps `pending` for its
	// whole run — checkPipelineCompletion only reconciles away from `failed` — so the name
	// never carried the fact.
	//
	// A retry after a lost response therefore dispatches in exactly the window where it
	// should. If the response was lost BEFORE the dispatch, nothing ran and the status has
	// not moved, so the same expected still matches and the retry claims. If it was lost
	// AFTER the dispatch and a POST-JOB has reported — a terminal step status, the only write
	// that runs checkPipelineCompletion — the status has moved off the ended set and the retry
	// is REFUSED, because the dispatch it is retrying already happened (PR #87, round 6).
	//
	// BETWEEN THOSE TWO IS THE WINDOW ClaimOutcome.InFlight EXISTS FOR, and it is not a retry
	// at all: a pre-job's StartStep writes `running`, which is not terminal, so nothing
	// reconciles the status and the slip still reads `failed` from dispatch until the run's
	// first post-job — minutes, for a build. A SECOND rerun message arriving in that window
	// passes the same compare-and-set and takes the same idempotent repeat arm as a lost-
	// response retry. Claimed=false cannot tell them apart; InFlight can, and a caller that
	// must not duplicate work reads it (finding A0, PR #87 seventh review).
	//
	// KNOWN RESIDUAL, not closed here: between a claimant's claim and its pre-job's StartStep
	// nothing is running, so two rerun messages arriving in THAT window both read
	// InFlight=false and both dispatch. Closing it needs a per-message claim identity carried
	// end to end — the rerunner sends a constant claimedBy today, and claimedBy is audit only
	// (see below) — which is a library, API and parser change rather than a store one.
	//
	// A CLAIM WITH NOTHING IN FLIGHT IS REAPABLE, and there are two routes to reaping one.
	// The state: a claim taken by a pre-job whose workflow was then never dispatched sets
	// claimed_from with no step ever reported. RunInFlight is false, so a release WOULD clear
	// it — but no post-job will ever run to call one, and every later same-commit push
	// deduplicates onto the row.
	//
	//   - THE OPERATOR ROUTE, which works in every case: POST /v1/slips/{id}/release. With
	//     nothing in flight it clears the claim on the first call; there is no stuck step to
	//     resolve first, because no step was ever reported.
	//   - THE AUTOMATIC ROUTE, which is NARROW: pushhookparser's stranded-slip cleanup. It
	//     used to skip a claimed slip outright and now exempts one only while a step or
	//     component is running or held — the same evidence DecideRelease uses. But its claim
	//     gate is reached only after its earlier gates, so what it actually reaps is a
	//     claimed, quiescent slip whose status is pending, in_progress or compensating, on a
	//     commit a force-push or branch delete made unreachable, on the slip's own branch,
	//     with SLIPPY_STRANDED_CLEANUP armed. A claimed quiescent FAILED slip (the rerunner's
	//     usual adoption) returns at its `failed` carve-out and a terminal one at its
	//     live-status gate, both BEFORE the claim gate — neither is reaped by it. Those are
	//     the operator route's cases.
	//
	// This library adds no time-based sweeper for any of it, deliberately: elapsed time cannot
	// tell a long build from a wedge (DEVOPS-367).
	//
	// The store builds the marker itself with ClaimMarker(prior, claimedBy, reason), because
	// only it knows the true status at write time. claimedBy is audit only: it names the
	// actor in the marker and is never a key the claim is checked against. There is no claim
	// owner; every pre-job of a run claims the same slip and every post-job releases it.
	//
	// Returns a ClaimOutcome, and:
	//   - Claimed=true, nil error: claimed_from = Prior was written and the marker appended.
	//     Prior is the status the row read under the lock.
	//   - Claimed=false, nil error: the slip was already claimed and NOTHING was written;
	//     Prior is the recorded claimed_from. Repeat claims are idempotent — no second marker
	//     — so a retried request cannot inflate the audit trail, and all but the first pre-job
	//     of a run take this arm. That holds for EVERY expected, nil included, and while the
	//     claim's own run is executing; it is the one statement finding j-claim showed the
	//     code did not keep. Both arms mean the slip is claimed on return.
	//   - InFlight, on both of those arms: whether a step or component was running or held at
	//     decision time, read from the SAME locked row as the status and the claim, so it
	//     cannot disagree with what the decision was made on. A caller that must not dispatch
	//     onto work already running reads this, not Claimed.
	//   - ErrClaimPreconditionFailed: the CURRENT status was outside expected (claimed or
	//     not), the slip has no status at all (an empty status cannot be recorded as a claim),
	//     or the row was UNCLAIMED with work in flight and expected did not name its status.
	//     Nothing written.
	//   - ErrSlipNotFound: no row for correlationID.
	//   - ErrClaimUnsupported (wrapped): the store cannot claim at all (ClickHouse).
	ClaimSlip(
		ctx context.Context, correlationID string, expected []SlipStatus, claimedBy, reason string,
	) (ClaimOutcome, error)

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

	// ResetSlipInPlace rewrites one commit's run under its OWN correlation ID — the
	// in-delivery retry's reset — as ONE transaction that DECIDES and WRITES under the same
	// row lock: read the claim state of slip.CorrelationID FOR UPDATE (the claim, the status
	// and every step and aggregate column — RunInFlight's whole input, and nothing else),
	// judge it with DecideReset, then either upsert slip or refuse. Nothing else resets a
	// claimed row; a bare Create cannot, because it takes no lock at all (DEVOPS-367).
	//
	// WHY IT IS A STORE OPERATION RATHER THAN A PUSH-SIDE BRANCH. The push decides whether to
	// reset from an UNLOCKED LoadByCommit, and resolveAndAbandonAncestors' progressive-depth
	// ancestor search makes real GitHub round trips — seconds of them — between that read and
	// the write it gates. A row read unclaimed and quiescent can therefore be claimed, and its
	// pre-job's StartStep can land, before the write does. Deciding again here, under the lock
	// the upsert itself lands beneath, is the only place the two can be made to agree: the
	// evidence the decision is made on cannot change before the write it authorises.
	//
	// Implementations MUST:
	//   - take a row lock on slip.CorrelationID and make the decision from what it reads
	//     under that lock, not from anything the caller passed;
	//   - return an error wrapping ErrSlipClaimedInFlight, having written NOTHING, when the
	//     locked row is claimed and a step or component of that run is running or held;
	//   - preserve claimed_from across the upsert (it is Create's invariant: a reset never
	//     ends a claim) and re-state the slip_claimed marker in the state_history it writes
	//     whenever the LOCKED row was claimed, naming the recorded claimant. The caller cannot
	//     supply that marker, which is the point: its snapshot may predate the claim;
	//   - upsert slip unchanged otherwise, identically to Create, including the case where the
	//     row has gone (a concurrent repave) — a reset whose target is absent is simply the
	//     insert a first push would have made, so redelivery converges.
	//
	// A store that cannot do any of that — ClickHouseStore, which has no claimed_from column
	// and no transaction to hold the decision and the write together — MUST return an error
	// wrapping ErrResetUnsupported. The push path detects that sentinel with errors.Is and
	// falls back to a plain Create, which loses nothing on such a store: with no claim column
	// there is no claim for the refused decision to protect.
	//
	// The caller's OWN ancestry link is deliberately NOT written here, unlike Repave's. It
	// stays outside this transaction, so the reset touches exactly one table and one row and
	// can never hold a routing_slips lock while waiting for a slip_ancestry one — see the lock
	// ordering on PostgresStore.ResetSlipInPlace.
	//
	// Returns:
	//   - nil: the row now holds slip, with the claim (and its marker) intact if one was held.
	//   - ErrSlipClaimedInFlight (wrapped): refused, nothing written. The caller deduplicates
	//     onto the live row rather than failing the push — the claimant's run owns that slip
	//     and the desired end state, one run for this commit, already holds.
	//   - ErrDuplicateSlip (wrapped): the row was gone and another correlation ID now holds
	//     this (repository, commit_sha). Nothing written; the caller's duplicate backstop
	//     handles it exactly as it handles the same sentinel from Create.
	//   - ErrResetUnsupported (wrapped): the store cannot decide under a lock.
	//   - any other error: nothing is written.
	ResetSlipInPlace(ctx context.Context, slip *Slip) error

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
