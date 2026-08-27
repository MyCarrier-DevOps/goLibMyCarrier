package slippy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// prNumberRegex matches PR references in commit messages.
// Handles: "Add feature (#42)", "Merge pull request #42 from ...", "(#42)"
var prNumberRegex = regexp.MustCompile(`(?:#|pull request #)(\d+)`)

// cherryPickRegex detects cherry-pick commits
var cherryPickRegex = regexp.MustCompile(`(?i)\b(cherry.pick|cherry-pick|picked from|backport)\b`)

// extractPRNumber extracts the first PR number from a commit message.
// Returns 0 if no PR number is found.
// This is a convenience wrapper around extractAllPRNumbers for callers that only need the first match.
//
//nolint:unused // Used in tests only
func extractPRNumber(commitMessage string) int {
	numbers := extractAllPRNumbers(commitMessage)
	if len(numbers) == 0 {
		return 0
	}
	return numbers[0]
}

// extractAllPRNumbers extracts all PR numbers from a commit message.
// Used for nested PR references (e.g., dev→main merge that mentions feature→dev PR).
func extractAllPRNumbers(commitMessage string) []int {
	matches := prNumberRegex.FindAllStringSubmatch(commitMessage, -1)
	if len(matches) == 0 {
		return nil
	}

	var prNumbers []int
	seen := make(map[int]bool)
	for _, match := range matches {
		if len(match) >= 2 {
			if prNum, err := strconv.Atoi(match[1]); err == nil {
				if !seen[prNum] {
					prNumbers = append(prNumbers, prNum)
					seen[prNum] = true
				}
			}
		}
	}
	return prNumbers
}

// isCherryPick detects if a commit message indicates a cherry-pick.
func isCherryPick(commitMessage string) bool {
	return cherryPickRegex.MatchString(commitMessage)
}

// isForceOrRewrite detects potential force push scenarios.
// This is heuristic-based: if no ancestry found despite having commits, might be force push.
func isForceOrRewrite(commitMessage string) bool {
	msg := strings.ToLower(commitMessage)
	return strings.Contains(msg, "force push") ||
		strings.Contains(msg, "rebase") ||
		strings.Contains(msg, "amend")
}

// normalizeRepository extracts the base repository path without fork prefixes.
// Handles: "user/repo" → "user/repo", "MyCarrier-DevOps/repo" → "MyCarrier-DevOps/repo"
// For fork detection, just returns as-is since we don't have enough context.
//
//nolint:unused // Reserved for future fork handling implementation
func normalizeRepository(repo string) string {
	// For now, return as-is. Future: could strip fork prefixes if we had org config
	return repo
}

// PushOptions contains the information needed to create a slip from a push event.
type PushOptions struct {
	// CorrelationID links this slip to Kafka events
	CorrelationID string

	// Repository is the full repository name (owner/repo)
	Repository string

	// Branch is the git branch name
	Branch string

	// CommitSHA is the full git commit SHA
	CommitSHA string

	// CommitMessage is the commit message text (optional).
	// When provided, enables PR-based ancestry resolution for squash merges.
	// Squash merge commits typically contain the PR number (e.g., "Add feature (#42)")
	// which allows linking to the original feature branch slip.
	CommitMessage string

	// Components defines the components to track
	Components []ComponentDefinition
}

// Validate checks that all required fields are present.
func (o PushOptions) Validate() error {
	if o.CorrelationID == "" {
		return fmt.Errorf("correlation_id is required")
	}
	if o.Repository == "" {
		return fmt.Errorf("repository is required")
	}
	if o.CommitSHA == "" {
		return fmt.Errorf("commit_sha is required")
	}
	return nil
}

// CreateSlipResult contains the result of slip creation including any warnings.
type CreateSlipResult struct {
	// Slip is the created routing slip
	Slip *Slip

	// Warnings contains non-fatal errors that occurred during creation.
	// These don't prevent slip creation but may indicate issues like:
	// - GitHub App not installed (ancestry resolution failed)
	// - Failed to abandon/promote ancestor slips
	// Callers can inspect these to decide if they should be treated as errors.
	Warnings []error

	// AncestryResolved indicates whether ancestry resolution completed without errors,
	// OR that no resolution was attempted or needed because the returned slip is
	// pre-existing. Concretely, true means either:
	//   - a fresh slip was created and its ancestry resolution attempt succeeded
	//     (whether or not ancestors were found — a first commit has no ancestors, but
	//     AncestryResolved=true because the resolution attempt itself succeeded); or
	//   - the result is a dedup onto an already-loaded slip where NO resolution was ever
	//     attempted before the dedup: the in-flight IsLive() reuse path, the empty-run
	//     guard, and the duplicate-create backstop's dedup paths all set this true
	//     unconditionally, since there is nothing to resolve for a slip that was not
	//     freshly created — "no resolution was needed" also counts as resolved.
	// False means a fresh slip WAS created but ancestry resolution failed for it (e.g.
	// GitHub API error, missing installation).
	//
	// The went-live repave-abort path (repaveExistingSlip's ErrSlipWentLive branch) is
	// deliberately NOT in the "nothing attempted" bucket above (DEVOPS-231 review D3.2):
	// resolveAndAbandonAncestors always runs before the repave delete is attempted, so by
	// the time a went-live abort is detected this field already holds the value that
	// attempt computed. That path preserves the existing value rather than forcing it
	// true — forcing true would clobber a legitimate false (resolution ran and failed,
	// with the failure recorded in Warnings) with a value that contradicts Warnings'
	// contents and could misfire alerting keyed on this field.
	//
	// This is deliberately NOT computed from a loaded slip's own Ancestry field
	// (e.g. `len(slip.Ancestry) > 0`): no store hydrates Slip.Ancestry on load in
	// production (it is only populated by initializeSlipForPush for a freshly created
	// slip), so that formula was unconditionally false for every dedup path and would
	// misfire any alerting keyed off this field.
	AncestryResolved bool
}

// CreateSlipForPush creates a new routing slip for a git push event.
// If a slip already exists for this commit (retry scenario), it resets
// the push_parsed step and returns the existing slip.
//
// This function also resolves the commit ancestry chain via GitHub,
// finds any existing slips for ancestor commits, and ensures they are
// in a terminal state (abandoning non-terminal slips that are being superseded).
//
// Retry vs repave vs new-slip behavior for the same commit SHA, decided by
// SlipStatus.IsLive() (the single live-vs-ended predicate shared with
// handleDuplicateSlipBackstop below — DEVOPS-231 review finding B5):
//   - Existing slip IsLive() (pending/in_progress/compensating): retried via
//     handlePushRetry (same correlation ID is reused) — the pipeline is still in flight,
//     so re-dispatching would double-run work.
//   - Existing slip is failed: the stuck slip is repaved — replaced, in one transaction,
//     by a fresh slip under the new correlation ID from opts. A failed slip never advances without
//     a step re-run, so a new push for the same commit (webhook re-delivery or a
//     same-commit re-push) is treated as a deliberate request to run CI again.
//   - Existing slip is terminal (abandoned, promoted, compensated, completed): treated as
//     stale and a fresh slip is created with the new correlation ID from opts. This
//     prevents resurrecting superseded slips on webhook re-delivery or bot-commit races.
//   - Existing slip is failed/terminal AND opts.Components is empty: the empty-run guard
//     short-circuits the repave above and returns the existing (ended) slip as a dedup,
//     since nothing would be dispatched and repaving would only destroy history for no
//     benefit. handleDuplicateSlipBackstop applies the identical guard so the two paths
//     converge on the same outcome for the same inputs.
//   - The repave itself can report that the decision is stale:
//     ErrSlipWentLive means the slip became live again before the repave landed, so the
//     repave is abandoned and treated like the IsLive() case above: dedup onto the
//     reloaded slip via handlePushRetry (same audit trail — push_parsed reset plus a
//     "retry detected" history entry — as the IsLive() case), no fresh slip created.
//     See repaveExistingSlip's doc for a caveat this path does NOT fully resolve: by the
//     time the went-live abort is detected, ancestor slips may already have been
//     abandoned/promoted on behalf of a successor correlation ID that will never be
//     created (DEVOPS-231 review D3.2).
//     ErrRepaveUnsupported means the store cannot repave at all (the ClickHouse
//     store, since Postgres is the operational store per DEVOPS-127); the fallback is
//     the pre-DEVOPS-231 semantics — AbandonSlip the superseded slip — followed by
//     fresh-slip creation as usual.
//
// Ancestry resolution (resolveAndAbandonAncestors, which makes multi-second GitHub API
// calls) runs BEFORE the successor is persisted, so no store mutation waits on GitHub. The
// replacement itself is then a single transactional SlipStore.Repave: the superseded row's
// removal, the successor's insert, the descendant repoint and the successor's ancestry link
// either all commit or none do. There is no longer a window in which the commit has no slip
// — the failure mode that made a create failure after a committed delete unrecoverable,
// since the next redelivery found no row to repave and failed identically forever.
//
// Even when no existing row is found for this commit, the insert can still fail with
// ErrDuplicateSlip: a concurrent push for the same commit can win the insert race between
// our LoadByCommit and our own write (the Redis dedup lock is fail-open). A backstop loads
// the conflicting row and applies the same live-vs-ended rule as above: a live conflicting
// slip is deduped onto (never destroyed — its pipeline may already be dispatched), while an
// ended one is repaved onto this push's successor.
//
// Phase A note (DEVOPS-231 review D3.6): ErrDuplicateSlip is unreachable via ANY path in
// Phase A, so handleDuplicateSlipBackstop is dormant until the Phase B migration lands.
// Without the uq_routing_slips_repo_sha unique index, the insert's ON CONFLICT target is
// correlation_id only, so two different pushes' correlation IDs for the SAME (repository,
// commit_sha) never conflict — both simply succeed, silently leaving two rows for one
// commit, and a lost Redis-lock race has no detection at all. What Phase A DOES now have,
// which it did not before, is convergence when a repave fails: nothing is written, the push
// fails, and Kafka redelivers against a store that still holds the superseded row.
//
// The returned CreateSlipResult contains both the slip and any non-fatal errors
// that occurred during processing (e.g., ancestry resolution failures).
// Callers should check Warnings for issues that didn't prevent slip creation
// but may indicate configuration problems (like missing GitHub App installation).
func (c *Client) CreateSlipForPush(ctx context.Context, opts PushOptions) (*CreateSlipResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid push options: %w", err)
	}

	c.logger.Info(ctx, "Creating routing slip", map[string]interface{}{
		"repository": opts.Repository,
		"commit":     shortSHA(opts.CommitSHA),
	})

	result := &CreateSlipResult{
		Warnings: make([]error, 0),
	}

	// Check for existing slip (retry detection / same-commit supersede decision).
	//
	// Exact-SHA intent: this lookup is keyed on the precise commit SHA being pushed,
	// not on git ancestry — we want to detect "is there an in-flight slip for THIS
	// commit?". The lookup is LoadByCommit (unfiltered) rather than LoadLiveByCommit
	// because under one-row-per-commit ANY existing row for this (repo, sha) —
	// including an abandoned/promoted/compensated row left over from a cross-commit
	// supersede — must be repaved before Create, or the unique (repository,
	// commit_sha) index rejects the insert. LoadLiveByCommit would filter those
	// statuses out at the DB layer, so the code would never see them and the stale
	// row would survive. LoadByCommit returns ErrSlipNotFound for a missing row
	// exactly like LoadLiveByCommit did — the err == nil guard shape is unchanged.
	//
	// Contract note (DEVOPS-231 review D3.5): the `case err != nil` branch below treats
	// ANY non-ErrSlipNotFound error as a hard failure of the push (it aborts, so Kafka
	// redelivers). This makes LoadByCommit's error taxonomy load-bearing — see its
	// contract on SlipStore in interfaces.go: a clean miss MUST be ErrSlipNotFound, and a
	// store that signals absence any other way (e.g. an untranslated sql.ErrNoRows, or a
	// generic error from a degraded/partial read) permanently hard-fails every push for
	// that commit instead of proceeding to create a slip.
	existingSlip, err := c.store.LoadByCommit(ctx, opts.Repository, opts.CommitSHA)
	switch {
	case err == nil && existingSlip != nil:
		// A slip already exists for this EXACT commit (existingSlip.Status is a
		// SlipStatus, not a step status). What we do next depends on whether the prior
		// pipeline can still make progress on its own:
		//
		//   - IsLive() (pending/in_progress/compensating — in_progress in practice;
		//     see STATE_MACHINE_V3.md): the prior pipeline is still in flight, or a
		//     concurrent create just won the repo:sha race. Reuse the existing slip
		//     and reset push_parsed via handlePushRetry — re-dispatching builds here
		//     would double-run work that is already running. The caller
		//     (slippy-api → pushhookparser) detects that the returned correlation_id
		//     differs from the one it sent and suppresses duplicate side-effects.
		if existingSlip.Status.IsLive() {
			slip, retryErr := c.handlePushRetry(ctx, existingSlip)
			if retryErr != nil {
				return nil, retryErr
			}
			result.Slip = slip
			// Dedup onto a pre-existing slip: nothing was (or needed to be) resolved.
			// See CreateSlipResult.AncestryResolved's doc.
			result.AncestryResolved = true
			return result, nil
		}

		//   - failed: the prior pipeline is stuck. A failed slip never advances on its
		//     own — it only recovers when its failed STEPS are re-run, which a fresh
		//     push event does not do. So a new push for the same commit (webhook
		//     re-delivery, or a same-commit re-push of a failed run) is a deliberate
		//     request to run CI again.
		//
		//   - terminal (abandoned, promoted, compensated, completed): stale or
		//     superseded. Resurrecting it on webhook re-delivery or a bot-commit race
		//     would be wrong, and under one-row-per-commit it must not be left behind
		//     when the new row is inserted.
		//
		if len(opts.Components) == 0 {
			// Empty-run guard: nothing will be dispatched for this push (branch
			// create/recreate at an existing SHA, or a components-less repo).
			// Repaving would destroy the prior run's history for zero benefit.
			// Return the existing slip; the caller sees returned != sent and
			// suppresses side effects. Trade-off for tests-only repos: see spec §6.2.
			c.logger.Info(ctx, "Empty-run guard: reusing ended slip for componentless push", map[string]interface{}{
				"existing_id": existingSlip.CorrelationID,
				"commit":      shortSHA(existingSlip.CommitSHA),
			})
			result.Slip = existingSlip
			result.AncestryResolved = true
			return result, nil
		}
		// Otherwise: existingSlip must be repaved. Deferred until immediately before
		// Create (see repaveExistingSlip below) so ancestry resolution's multi-second
		// GitHub API calls happen while the row still exists (see the doc comment on
		// this function).

	case errors.Is(err, ErrSlipNotFound):
		existingSlip = nil // clean miss: no existing row, proceed to create

	case err != nil:
		// A real lookup failure (DB timeout, connection refused, ...) — NOT a clean
		// miss. Proceeding as if no slip existed risks Create inserting a second row
		// while a LIVE slip for this commit already exists, and the caller would fully
		// re-dispatch a build that's already running. Failing the message (so Kafka
		// redelivers) is safer than guessing.
		return nil, fmt.Errorf("failed to load existing slip for %s@%s: %w",
			opts.Repository, shortSHA(opts.CommitSHA), err)
	}

	// Resolve ancestry chain and abandon superseded slips. Runs BEFORE the repave
	// delete below — see the doc comment on this function for why.
	ancestry, ancestryWarnings := c.resolveAndAbandonAncestors(ctx, opts)
	result.Warnings = append(result.Warnings, ancestryWarnings...)
	result.AncestryResolved = len(ancestry) > 0 || len(ancestryWarnings) == 0

	// D3.4 defensive guard: never let this push's own ancestry chain point at the slip
	// we are about to repave (delete) for this exact commit. The primary guard lives in
	// findAncestorViaSquashMerge below, which skips a squash-merge candidate whose
	// CommitSHA equals opts.CommitSHA — a fast-forward/no-op merge keeps the PR head SHA
	// identical to the pushed commit, so an ended slip for THIS SAME commit can otherwise
	// surface as its own "ancestor" via the PR-branch-history search, which deliberately
	// includes the head commit. This is a defensive backstop for any other path that
	// might still produce a self-referential entry: without it, InsertAncestryLink below
	// would write the newborn slip's parent pointing at existingSlip's correlation ID —
	// the very row repaveExistingSlip is about to delete — a dangling self-reference from
	// birth (DEVOPS-231 review D3.4).
	if existingSlip != nil {
		ancestry = dropSelfAncestorLink(ancestry, existingSlip.CorrelationID)
	}

	// Build the successor BEFORE persisting anything. Under one-row-per-commit the repave
	// below does not merely delete the superseded run — it replaces that run with this
	// exact slip inside a single transaction — so the successor has to exist as a value
	// before either half of the replacement can run.
	slip := c.initializeSlipForPush(opts, ancestry)

	// The direct parent link, handed to the store so it lands in the same transaction as
	// the successor's row. nil means "this push resolved no ancestry"; for a repave that
	// is not the same as "the successor has no parent", because the superseded run's own
	// link is carried forward in that case (see SlipStore.Repave).
	var parent *AncestryEntry
	if len(ancestry) > 0 {
		parent = &ancestry[0]
	}

	handled, persistErr := c.persistSlipForPush(ctx, existingSlip, opts, slip, parent, result)
	if persistErr != nil {
		return nil, persistErr
	}
	if handled {
		return result, nil
	}

	result.Slip = slip

	c.logger.Info(ctx, "Created routing slip", map[string]interface{}{
		"correlation_id": slip.CorrelationID,
		"components":     len(opts.Components),
		"ancestors":      len(ancestry),
		"warnings":       len(result.Warnings),
	})

	return result, nil
}

// persistSlipForPush writes slip — the successor this push is creating — choosing between
// the two persistence shapes the store offers:
//
//   - no existing row for this commit: a plain Create plus a best-effort ancestry link.
//   - an existing ended row: a Repave, which removes that row and writes slip and its link
//     as ONE transaction, so the commit is never left with no slip at all.
//
// Returns handled=true when result is already populated and the caller should return it
// as-is (the dedup outcomes); handled=false means slip itself is now persisted.
func (c *Client) persistSlipForPush(
	ctx context.Context,
	existingSlip *Slip,
	opts PushOptions,
	slip *Slip,
	parent *AncestryEntry,
	result *CreateSlipResult,
) (handled bool, err error) {
	if existingSlip == nil {
		return c.createFreshSlip(ctx, opts, slip, parent, result)
	}
	return c.repaveExistingSlip(ctx, existingSlip, opts, slip, parent, result)
}

// createFreshSlip inserts slip and writes its parent link, for the paths where there is no
// row to repave: a first push for this commit, or a store that cannot repave at all.
//
// A link failure here is a warning rather than a push failure — the slip exists and CI can
// run; only the lineage hop is missing. The repave path deliberately does NOT share that
// leniency: there the link is written inside the transaction, so a link failure rolls the
// whole replacement back rather than leaving a successor with a missing parent forever.
func (c *Client) createFreshSlip(
	ctx context.Context,
	opts PushOptions,
	slip *Slip,
	parent *AncestryEntry,
	result *CreateSlipResult,
) (handled bool, err error) {
	if createErr := c.store.Create(ctx, slip); createErr != nil {
		if !errors.Is(createErr, ErrDuplicateSlip) {
			return false, fmt.Errorf("failed to create slip: %w", createErr)
		}

		// Unique-index backstop (fail-open Redis race): another run holds the row.
		// This is the same no-existing-row race the reuse branch in CreateSlipForPush
		// handles for a row that was ALREADY there at lookup time - here, the winner's
		// insert landed between our own now-stale LoadByCommit and our Create.
		backstopHandled, backstopErr := c.handleDuplicateSlipBackstop(ctx, opts, slip, parent, result)
		if backstopErr != nil {
			return false, backstopErr
		}
		if backstopHandled {
			return true, nil
		}
		if retryErr := c.store.Create(ctx, slip); retryErr != nil {
			return false, fmt.Errorf("failed to create slip after duplicate backstop: %w", retryErr)
		}
	}

	c.writeAncestryLink(ctx, slip, parent, result)
	return false, nil
}

// writeAncestryLink writes slip's direct parent link outside any transaction, recording a
// failure as a warning. Used only by createFreshSlip; see its doc for why the repave path
// does not go through here.
func (c *Client) writeAncestryLink(
	ctx context.Context,
	slip *Slip,
	parent *AncestryEntry,
	result *CreateSlipResult,
) {
	if parent == nil {
		return
	}
	if err := c.store.InsertAncestryLink(ctx, slip, *parent); err != nil {
		c.logger.Warn(ctx, "Failed to write ancestry link", map[string]interface{}{
			"correlation_id": slip.CorrelationID,
			"parent_id":      parent.CorrelationID,
			"error":          err.Error(),
		})
		result.Warnings = append(result.Warnings, fmt.Errorf("failed to write ancestry link: %w", err))
	}
}

// repaveExistingSlip replaces existingSlip with slip in a single store transaction, and
// handles the sentinels Repave can return.
//
// Returns handled=true when the caller should return result directly — the ErrSlipWentLive
// and backstop-dedup cases, where the repave is abandoned and result is populated with a
// dedup onto an existing slip. Returns handled=false, err=nil when slip itself is now
// persisted. Returns a non-nil err for a fatal condition: failing to reload the slip after
// aborting on ErrSlipWentLive, handlePushRetry itself failing (DEVOPS-231 review D3.2 routes
// the went-live dedup through handlePushRetry so it gets the same audit trail as the
// IsLive() case — see that branch's doc below), or the repave itself failing.
//
// A failed repave being fatal is a deliberate change from the pre-Repave code, which logged
// a failed delete as a warning and created the fresh slip anyway. That leniency only made
// sense while delete and create were separate calls: a create could still succeed on its
// own and CI could still run, at the cost of leaving a stale row behind. There is nothing
// left to fall through to now — a failed Repave wrote nothing, so there is no successor —
// so the honest outcome is to fail the push and let Kafka redeliver, which converges
// because the superseded row is still there to repave next time. The alternative (swallow
// the error and report a slip that was never written) is strictly worse.
//
// The phantom-successor window that the pre-Repave code documented here is closed rather
// than described: the store now inserts the successor BEFORE repointing any descendant onto
// it, inside one transaction, so no descendant can end up pointing at a correlation ID that
// never comes into existence — and Phase B is free to put a foreign key on
// slip_ancestry.parent_correlation_id, which the old repoint-before-insert ordering would
// have made impossible.
func (c *Client) repaveExistingSlip(
	ctx context.Context,
	existingSlip *Slip,
	opts PushOptions,
	slip *Slip,
	parent *AncestryEntry,
	result *CreateSlipResult,
) (handled bool, err error) {
	// Either way (failed or terminal): replace the existing slip so the caller sees a NEW
	// slip (not a dedup) and re-dispatches builds + unit tests. This keeps one row per
	// (repository, commit_sha) rather than leaving a superseded row behind
	// (STATE_MACHINE_V3.md §"Pipeline termination without completing").
	//
	// D3.3: log intent at Debug here, not as a claim of success — the "Repaved" log below
	// only fires once Repave has confirmed it happened. Every same-commit push against a
	// store that returns ErrRepaveUnsupported (i.e. every ClickHouse-backed client, since
	// Postgres is the only store DEVOPS-231 wired for real repaves) used to log this as if
	// delete + recreate had happened when it never did.
	c.logger.Debug(ctx, "Attempting repave for same-commit push", map[string]interface{}{
		"existing_id":     existingSlip.CorrelationID,
		"existing_commit": shortSHA(existingSlip.CommitSHA),
		"existing_status": string(existingSlip.Status),
		"superseding_id":  opts.CorrelationID,
	})

	repaveErr := c.store.Repave(ctx, existingSlip.CorrelationID, slip, parent)
	switch {
	case repaveErr == nil:
		c.logger.Info(ctx, "Repaved ended slip for same commit (replaced in one transaction)",
			map[string]interface{}{
				"repaved_id":     existingSlip.CorrelationID,
				"repaved_commit": shortSHA(existingSlip.CommitSHA),
				"repaved_status": string(existingSlip.Status),
				"superseding_id": opts.CorrelationID,
			})
		return false, nil

	case errors.Is(repaveErr, ErrSlipWentLive):
		// The repave decision is stale: the slip went live again between that decision
		// and this call (e.g. a failed slip recovering via executor.go's recovery
		// branch). Repave's status guard refused to destroy it, and — because the whole
		// replacement is one transaction — refused to create the successor either.
		// Creating a fresh slip now would produce two competing live runs for the same
		// commit; nothing at the DB level stops that pre-index (Phase B). Dedup onto the
		// live slip instead, reloaded so the returned copy reflects its current state.
		//
		// D3.2 (DEVOPS-231 review): this path is routed through handlePushRetry, exactly
		// like the IsLive() branch at the top of CreateSlipForPush, so the same audit
		// trail exists here too — a push_parsed reset plus a "retry detected" state
		// history entry — rather than silently deduping with no record that a second
		// push arrived. It still diverges from the IsLive() case in one way that is NOT
		// fixed here: by this point in CreateSlipForPush, resolveAndAbandonAncestors has
		// already run and may have abandoned or promoted ancestor slips on behalf of
		// opts.CorrelationID — a successor that, on this path, is never created. Those
		// ancestor status flips are persisted; only the phantom successor itself is
		// purely in logs/traces. Undoing that would require restructuring so ancestry
		// resolution runs after the went-live check is known, which this fix does not
		// attempt (see CreateSlipForPush's doc comment for the ordering rationale that
		// makes ancestry resolution run first).
		c.logger.Warn(ctx, "Repave aborted: slip went live between decision and repave",
			map[string]interface{}{
				"correlation_id": existingSlip.CorrelationID,
				"commit":         shortSHA(existingSlip.CommitSHA),
			})
		live, loadErr := c.store.Load(ctx, existingSlip.CorrelationID)
		if loadErr != nil {
			return false, fmt.Errorf(
				"failed to reload slip %s after went-live repave abort: %w", existingSlip.CorrelationID, loadErr,
			)
		}
		retried, retryErr := c.handlePushRetry(ctx, live)
		if retryErr != nil {
			return false, retryErr
		}
		result.Slip = retried
		// Do NOT force AncestryResolved = true here (D3.2): resolveAndAbandonAncestors
		// already ran for this push before this function was called and has already set
		// result.AncestryResolved to the accurate outcome of that attempt. Forcing true
		// would clobber a legitimate false (resolution ran and failed, with the failure
		// recorded in result.Warnings) — see CreateSlipResult.AncestryResolved's doc.
		return true, nil

	case errors.Is(repaveErr, ErrRepaveUnsupported):
		// The store cannot repave at all (the ClickHouse store: Postgres is the
		// operational slip store per DEVOPS-127, and NewClient still builds a
		// ClickHouseStore unconditionally, so this fires on every same-commit push for
		// a CH-backed client). Fall back to the pre-DEVOPS-231 semantics — abandon the
		// superseded slip rather than repaving it — then create the fresh slip the
		// non-transactional way, since that is all such a store can offer.
		// D3.3: abandonSupersededSlipForUnsupportedRepave only claims "abandoned" when
		// AbandonSlip actually changed the slip's status, and does not add a Warning for
		// this routine, expected-on-ClickHouse case (only a real AbandonSlip failure is
		// surfaced as a Warning) — see its doc for why.
		c.abandonSupersededSlipForUnsupportedRepave(ctx, existingSlip, opts, result, "Repave")
		return c.createFreshSlip(ctx, opts, slip, parent, result)

	case errors.Is(repaveErr, ErrDuplicateSlip):
		// Dormant until Phase B's unique index exists, but genuinely reachable after that,
		// via the concurrent same-commit push this whole feature is about. Two pushes for
		// one commit both try to repave the same row: A's guarded delete takes the row lock
		// and B blocks on it. When A commits (row deleted, A's successor inserted), B's
		// delete matches zero rows and B's existence check — which looks up B's OWN
		// oldCorrelationID — finds nothing, so B correctly reads it as "already gone" and
		// proceeds to insert its own successor. That insert is what conflicts with A's
		// successor on uq_routing_slips_repo_sha. Routed to the same backstop as the create
		// path, which then dedups B onto A's run.
		backstopHandled, backstopErr := c.handleDuplicateSlipBackstop(ctx, opts, slip, parent, result)
		if backstopErr != nil {
			return false, backstopErr
		}
		if backstopHandled {
			return true, nil
		}
		if retryErr := c.store.Repave(ctx, existingSlip.CorrelationID, slip, parent); retryErr != nil {
			return false, fmt.Errorf("failed to repave slip %s after duplicate backstop: %w",
				existingSlip.CorrelationID, retryErr)
		}
		return false, nil

	default:
		// Fatal — see this function's doc comment for why this is no longer a warning.
		// Nothing was written, so there is no successor to fall through to; failing the
		// push lets Kafka redeliver against a store that still holds the superseded row.
		return false, fmt.Errorf("failed to repave slip %s: %w", existingSlip.CorrelationID, repaveErr)
	}
}

// abandonSupersededSlipForUnsupportedRepave is the shared ErrRepaveUnsupported fallback
// for both repaveExistingSlip and handleDuplicateSlipBackstop (DEVOPS-231 review D3.1/D3.3):
// the store cannot repave (e.g. ClickHouseStore), so fall back to abandon semantics rather
// than claiming a repave that never happened.
//
// AbandonSlip's checkTerminalStatus (client.go) silently no-ops when the slip is already
// terminal, so a caller that unconditionally logged/warned "abandoned instead" was lying
// whenever the superseded slip was already terminal (exactly the rows LoadByCommit's
// unfiltered lookup surfaces). This function checks slip.Status.IsTerminal() BEFORE calling
// AbandonSlip: a same-commit dupe reaching this fallback is always either failed
// (non-terminal) or already-terminal by construction (CreateSlipForPush only repaves in
// those two cases), and terminal statuses never revert, so the caller's already-loaded
// snapshot is safe to trust here without an extra Load.
//
// Messaging (D3.3): this only ever logs at Info level and adds NOTHING to result.Warnings on
// the expected/successful outcomes (already-terminal: nothing to abandon; non-terminal:
// abandoned successfully) — this fallback fires on every same-commit push against a
// ClickHouse-backed client, so treating it as a Warning misfires any consumer that alerts on
// len(result.Warnings) > 0 for what is a routine webhook redelivery. A Warning is added only
// when AbandonSlip itself returns an error, since that means the superseded row's status was
// NOT updated and dashboards/consumers may show it as still active.
func (c *Client) abandonSupersededSlipForUnsupportedRepave(
	ctx context.Context,
	slip *Slip,
	opts PushOptions,
	result *CreateSlipResult,
	logPrefix string,
) {
	if slip.Status.IsTerminal() {
		c.logger.Info(ctx, logPrefix+" unsupported on this store; slip already terminal, old row left unchanged",
			map[string]interface{}{
				"correlation_id": slip.CorrelationID,
				"commit":         shortSHA(slip.CommitSHA),
				"status":         string(slip.Status),
			})
		return
	}

	if abandonErr := c.AbandonSlip(ctx, slip.CorrelationID, opts.CorrelationID); abandonErr != nil {
		result.Warnings = append(result.Warnings, fmt.Errorf(
			"failed to abandon slip %s after unsupported repave: %w", slip.CorrelationID, abandonErr,
		))
		return
	}

	c.logger.Info(ctx, logPrefix+" unsupported on this store; abandoned superseded slip instead",
		map[string]interface{}{
			"correlation_id": slip.CorrelationID,
			"commit":         shortSHA(slip.CommitSHA),
			"superseding_id": opts.CorrelationID,
		})
}

// handleDuplicateSlipBackstop handles an ErrDuplicateSlip from Create: another concurrent
// push won the insert race for this (repository, commit_sha) between our caller's
// LoadByCommit and its Create call. It loads the conflicting row and applies the same
// live-vs-ended decision as the main retry/repave logic in CreateSlipForPush, via the
// shared SlipStatus.IsLive() predicate (DEVOPS-231 review finding B5): a live conflicting
// slip is deduped onto (never destroyed - its pipeline may already be dispatched, and
// destroying it here would pull the rug out from under an in-flight run while we dispatch a
// duplicate); an ended one is either deduped onto (componentless push: the empty-run guard,
// mirrored from the main path so identical inputs converge on identical outcomes through
// either path) or repaved onto slip, this push's successor.
//
// Returns handled=true when the caller should return result directly, which now covers two
// outcomes: the dedup cases (result.Slip is the conflicting slip) AND a successful repave of
// the conflicting row (result.Slip is slip, already persisted by that repave — there is
// nothing left for the caller to insert). Returns handled=false only when nothing was
// written and the caller should retry its own insert: no conflicting row was found or
// loadable, or the store cannot repave. Returns a non-nil err on a fatal repave failure.
//
// D3.1 (DEVOPS-231 review): the conflicting row's repave below applies the SAME
// live-vs-ended decision as repaveExistingSlip's sentinel handling, not just a bare fatal
// error — the doc above already claims this backstop "applies the same live-vs-ended
// decision" as the main path, so treating ErrSlipWentLive or ErrRepaveUnsupported as
// unconditionally fatal here contradicted that claim. ErrSlipWentLive reloads the
// conflicting slip and dedups onto it (handled=true), mirroring the IsLive() dedup branch
// above in this same function. ErrRepaveUnsupported falls back to abandon semantics via
// abandonSupersededSlipForUnsupportedRepave (shared with repaveExistingSlip's D3.3 fix) and
// then falls through to the caller's insert retry, for symmetry with repaveExistingSlip's
// own ErrRepaveUnsupported branch. Both sentinels are dormant in Phase A (ErrDuplicateSlip
// itself is unreachable without the uq_routing_slips_repo_sha index — see CreateSlipForPush's
// doc comment), so this fix has zero behavioral effect until Phase B, but is still correct to
// make now. Every other repave error remains fatal here — this backstop is already the
// last-resort convergence path, so there is nothing further to fall back on.
func (c *Client) handleDuplicateSlipBackstop(
	ctx context.Context,
	opts PushOptions,
	slip *Slip,
	parent *AncestryEntry,
	result *CreateSlipResult,
) (handled bool, err error) {
	conflicting, loadErr := c.store.LoadByCommit(ctx, opts.Repository, opts.CommitSHA)
	if loadErr != nil || conflicting == nil {
		// No conflicting row found, or the lookup itself failed: nothing to repave
		// here, so fall through to the caller's single Create retry exactly like
		// today's pre-fix behavior - the retry will surface the real Create error if
		// the conflict is still present.
		return false, nil //nolint:nilerr // intentional fall-through, see comment above
	}

	if conflicting.Status.IsLive() {
		c.logger.Info(ctx, "Duplicate-create backstop: live conflicting slip, deduping", map[string]interface{}{
			"conflicting_id": conflicting.CorrelationID,
			"commit":         shortSHA(conflicting.CommitSHA),
			"superseding_id": opts.CorrelationID,
		})
		result.Slip = conflicting
		result.AncestryResolved = true
		return true, nil
	}

	if len(opts.Components) == 0 {
		// Empty-run guard (mirrored from CreateSlipForPush's main path): nothing would
		// be dispatched for this push, so repaving the conflicting row would only
		// destroy its history for no benefit. Dedup onto it instead of deleting it.
		c.logger.Info(ctx, "Duplicate-create backstop: empty-run guard, deduping onto ended conflicting slip",
			map[string]interface{}{
				"conflicting_id": conflicting.CorrelationID,
				"commit":         shortSHA(conflicting.CommitSHA),
				"superseding_id": opts.CorrelationID,
			})
		result.Slip = conflicting
		result.AncestryResolved = true
		return true, nil
	}

	c.logger.Debug(ctx, "Duplicate-create backstop: attempting repave of ended conflicting slip",
		map[string]interface{}{
			"conflicting_id":     conflicting.CorrelationID,
			"conflicting_commit": shortSHA(conflicting.CommitSHA),
			"conflicting_status": string(conflicting.Status),
			"superseding_id":     opts.CorrelationID,
		})
	repaveErr := c.store.Repave(ctx, conflicting.CorrelationID, slip, parent)
	switch {
	case repaveErr == nil:
		// The repave replaced the conflicting row WITH our successor in one transaction,
		// so unlike the pre-Repave code there is nothing left for the caller to retry:
		// the slip it wanted to create already exists. Report it as handled and populate
		// result here, rather than returning handled=false and letting the caller re-run
		// an insert that would only re-write the same row.
		c.logger.Info(ctx, "Duplicate-create backstop: repaved ended conflicting slip", map[string]interface{}{
			"repaved_id":     conflicting.CorrelationID,
			"repaved_commit": shortSHA(conflicting.CommitSHA),
			"repaved_status": string(conflicting.Status),
			"superseding_id": opts.CorrelationID,
		})
		result.Slip = slip
		// AncestryResolved is deliberately left as resolveAndAbandonAncestors set it (D3.2):
		// this is a fresh successor, not a dedup onto someone else's slip, so the accurate
		// outcome of this push's own resolution attempt is the right value to keep.
		return true, nil

	case errors.Is(repaveErr, ErrSlipWentLive):
		// D3.1: mirror the IsLive() dedup branch above in this same function - the
		// conflicting row went live between our decision (conflicting.Status was ended)
		// and this call, so Repave's status guard refused to destroy it. Dedup onto it,
		// reloaded so the returned copy reflects its current (live) state.
		c.logger.Warn(ctx, "Duplicate-create backstop: conflicting slip went live between decision and repave",
			map[string]interface{}{
				"conflicting_id": conflicting.CorrelationID,
				"commit":         shortSHA(conflicting.CommitSHA),
			})
		live, loadErr := c.store.Load(ctx, conflicting.CorrelationID)
		if loadErr != nil {
			return false, fmt.Errorf(
				"failed to reload conflicting slip %s after went-live backstop abort: %w",
				conflicting.CorrelationID, loadErr,
			)
		}
		result.Slip = live
		result.AncestryResolved = true
		return true, nil

	case errors.Is(repaveErr, ErrRepaveUnsupported):
		// D3.1: symmetric with repaveExistingSlip's own ErrRepaveUnsupported fallback -
		// the store cannot repave, so abandon the conflicting row instead and let the
		// caller retry its insert once (handled=false).
		c.abandonSupersededSlipForUnsupportedRepave(ctx, conflicting, opts, result, "Duplicate-create backstop repave")
		return false, nil

	default:
		// Fatal: this backstop is already the last-resort convergence path, so there is
		// nothing further to fall back on if the conflicting row survives and the retry
		// hits the same conflict again. ErrDuplicateSlip landing here would mean a third
		// row for this (repository, commit_sha), which the unique index makes impossible.
		return false, fmt.Errorf(
			"failed to repave conflicting slip %s: %w", conflicting.CorrelationID, repaveErr,
		)
	}
}

// resolveAndAbandonAncestors fetches commit ancestry from GitHub,
// finds any existing slips for those commits, abandons non-terminal ones,
// and returns the ancestry chain along with any warnings encountered.
//
// This uses progressive depth searching: starts with AncestryDepth (default 25),
// and if no ancestor slip is found, expands to AncestryMaxDepth (default 100).
// This handles cases where pushes contain many commits or there are gaps between slips.
//
// For squash merges (when CommitMessage contains a PR reference like "#42"),
// if no ancestor is found via git history, falls back to PR-based lookup.
// This finds the original feature branch slip and marks it as "promoted" (not abandoned).
//
// This function collects ALL errors as warnings rather than failing on the first error.
// This allows slip creation to proceed while giving callers visibility into what went wrong.
//
// Warnings may include:
// - GitHub App not installed on organization
// - Failed to fetch commit ancestry from GitHub API
// - Failed to promote/abandon ancestor slips
// - Invalid repository format
func (c *Client) resolveAndAbandonAncestors(ctx context.Context, opts PushOptions) ([]AncestryEntry, []error) {
	warnings := make([]error, 0)

	// Parse owner/repo for GitHub API
	parts := strings.SplitN(opts.Repository, "/", 2)
	if len(parts) != 2 {
		warnings = append(warnings, NewAncestryError(
			opts.Repository,
			opts.CommitSHA,
			"setup",
			fmt.Errorf("invalid repository format: %s (expected owner/repo)", opts.Repository),
		))
		return nil, warnings
	}
	owner, repo := parts[0], parts[1]

	// Progressive depth search: start with initial depth, expand if no ancestor found
	ancestorSlips, err := c.findAncestorSlipsWithProgressiveDepth(ctx, owner, repo, opts)
	if err != nil {
		warnings = append(warnings, NewAncestryError(
			opts.Repository,
			opts.CommitSHA,
			"github_api",
			err,
		))
		// Return early with the warning - can't continue without ancestry info
		return nil, warnings
	}

	// Detect potential edge cases that might break ancestry
	if opts.CommitMessage != "" {
		if isCherryPick(opts.CommitMessage) {
			c.logger.Warn(
				ctx,
				"Cherry-pick detected - ancestry may not link to original commit",
				map[string]interface{}{
					"commit":  shortSHA(opts.CommitSHA),
					"message": opts.CommitMessage,
				},
			)
		}
		if isForceOrRewrite(opts.CommitMessage) {
			c.logger.Warn(
				ctx,
				"Possible force push or rebase detected - ancestry chain may be broken",
				map[string]interface{}{
					"commit":  shortSHA(opts.CommitSHA),
					"message": opts.CommitMessage,
				},
			)
		}
	}

	// If no ancestors found via git history, try PR-based lookup for squash merges
	isSquashMerge := false
	if len(ancestorSlips) == 0 && opts.CommitMessage != "" {
		prSlip, found := c.findAncestorViaSquashMerge(ctx, owner, repo, opts)
		if found {
			ancestorSlips = []SlipWithCommit{prSlip}
			isSquashMerge = true
		}
	}

	if len(ancestorSlips) == 0 {
		// No ancestors found is not an error - this might be the first commit
		return nil, nil
	}

	// Build ancestry chain and handle the ancestor slip
	// For squash merges: promote the feature branch slip
	// For regular pushes: abandon non-terminal ancestor slips
	var ancestry []AncestryEntry
	// handledAncestor tracks whether we have already promoted or abandoned an
	// ancestor slip.  We use this instead of checking i == 0 so that a
	// cross-branch slip that happens to sit at index 0 (because it shares a
	// more-recent commit SHA with another branch) does not block abandonment of
	// the first eligible same-branch non-terminal slip found later in the list.
	handledAncestor := false
	for _, ancestorSlip := range ancestorSlips {
		slip := ancestorSlip.Slip

		// Capture failure context BEFORE any status modification (abandon/promote).
		// This preserves which step failed so it can be recorded in ancestry even
		// if the slip is subsequently abandoned by a newer push.
		// Check all primary failure statuses — a slip can be marked Failed due to
		// a step with Error or Timeout status, not just Failed.
		var failedStep string
		if slip.Status == SlipStatusFailed {
			for stepName, step := range slip.Steps {
				switch step.Status {
				case StepStatusFailed, StepStatusError, StepStatusTimeout:
					failedStep = stepName
				case StepStatusPending, StepStatusHeld, StepStatusRunning,
					StepStatusCompleted, StepStatusAborted, StepStatusSkipped:
					// Non-primary-failure statuses — not relevant for failedStep capture
				}
				if failedStep != "" {
					break
				}
			}
		}

		// Only the most recent non-terminal ancestor slip needs a status update.
		// Cross-branch skipping applies only when both branch values are known and
		// different; if either branch is empty, we intentionally fall through to
		// abandonment for backward compatibility with older slips that lack branch
		// metadata. Failed slips are non-terminal and eligible for abandonment here
		// because a new push indicates the developer has moved on. If they wanted
		// to retry the same commit, they would re-run without pushing and the
		// non-terminal slip would be found by ancestry resolution.
		if !handledAncestor && !slip.Status.IsTerminal() {
			switch {
			case isSquashMerge:
				// Squash merge: promote the feature branch slip (successful outcome).
				// Cross-branch promotion is expected here — the ancestor is on the
				// feature branch being merged into the current branch.
				c.logger.Info(ctx, "Promoting feature branch slip via squash merge", map[string]interface{}{
					"promoted_id":     slip.CorrelationID,
					"promoted_commit": shortSHA(slip.CommitSHA),
					"promoted_status": string(slip.Status),
					"promoted_to":     opts.CorrelationID,
					"merge_commit":    shortSHA(opts.CommitSHA),
				})

				if err := c.PromoteSlip(ctx, slip.CorrelationID, opts.CorrelationID); err != nil {
					warnings = append(warnings, NewAncestorUpdateError(
						opts.Repository,
						opts.CommitSHA,
						"promote",
						slip.CorrelationID,
						fmt.Errorf("failed to promote feature branch slip: %w", err),
					))
					// Continue - still build ancestry chain
				} else {
					// Update the local copy to reflect the promotion
					slip.Status = SlipStatusPromoted
				}
				handledAncestor = true

			case opts.Branch != "" && slip.Branch != "" && slip.Branch != opts.Branch:
				// Cross-branch ancestor found via shared git history (e.g. a push to
				// "main" whose ancestry walks through commits that also exist on
				// "integration"). Only skip abandonment when both branch values are
				// known and different; otherwise preserve existing abandonment
				// behavior for slips with missing branch metadata.
				// Do NOT set handledAncestor — keep looking for the first
				// same-branch non-terminal slip further in the list.
				c.logger.Info(ctx, "Skipping cross-branch ancestor slip (different branch)", map[string]interface{}{
					"ancestor_id":        slip.CorrelationID,
					"ancestor_branch":    slip.Branch,
					"ancestor_commit":    shortSHA(slip.CommitSHA),
					"ancestor_status":    string(slip.Status),
					"current_branch":     opts.Branch,
					"superseding_commit": shortSHA(opts.CommitSHA),
				})

			default:
				// Regular push on the same branch: abandon the superseded slip.
				c.logger.Info(ctx, "Abandoning superseded slip", map[string]interface{}{
					"superseded_id":      slip.CorrelationID,
					"superseded_commit":  shortSHA(slip.CommitSHA),
					"superseded_status":  string(slip.Status),
					"superseding_commit": shortSHA(opts.CommitSHA),
				})

				if err := c.AbandonSlip(ctx, slip.CorrelationID, opts.CorrelationID); err != nil {
					warnings = append(warnings, NewAncestorUpdateError(
						opts.Repository,
						opts.CommitSHA,
						"abandon",
						slip.CorrelationID,
						fmt.Errorf("failed to abandon superseded slip: %w", err),
					))
					// Continue - still build ancestry chain
				} else {
					// Update the local copy to reflect the abandonment
					slip.Status = SlipStatusAbandoned
				}
				handledAncestor = true
			}
		}

		ancestry = append(ancestry, AncestryEntry{
			CorrelationID: slip.CorrelationID,
			CommitSHA:     slip.CommitSHA,
			Status:        slip.Status,
			FailedStep:    failedStep,
			CreatedAt:     slip.CreatedAt,
			Repository:    slip.Repository,
			Branch:        slip.Branch,
		})
	}

	c.logger.Info(ctx, "Resolved ancestry chain", map[string]interface{}{
		"commit":       shortSHA(opts.CommitSHA),
		"ancestors":    len(ancestry),
		"squash_merge": isSquashMerge,
		"warnings":     len(warnings),
	})

	return ancestry, warnings
}

// dropSelfAncestorLink removes any entry from ancestry whose CorrelationID matches
// repavedCorrelationID (DEVOPS-231 review D3.4). It is a defensive backstop invoked from
// CreateSlipForPush right after ancestry is resolved, in case a self-referential entry
// (the pushed commit's own prior slip, about to be repaved/deleted) makes it into the
// chain via some path other than the primary guard in findAncestorViaSquashMerge. Returns
// ancestry unchanged (including nil) when repavedCorrelationID is empty or nothing matches.
func dropSelfAncestorLink(ancestry []AncestryEntry, repavedCorrelationID string) []AncestryEntry {
	if repavedCorrelationID == "" || len(ancestry) == 0 {
		return ancestry
	}

	hasMatch := false
	for _, entry := range ancestry {
		if entry.CorrelationID == repavedCorrelationID {
			hasMatch = true
			break
		}
	}
	if !hasMatch {
		return ancestry
	}

	filtered := make([]AncestryEntry, 0, len(ancestry)-1)
	for _, entry := range ancestry {
		if entry.CorrelationID == repavedCorrelationID {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// findAncestorViaSquashMerge attempts to find an ancestor slip by parsing
// a PR number from the commit message and looking up the PR's head commit.
// This handles squash merge scenarios where git ancestry is broken.
// Supports nested PR references for multi-stage merges (feature→dev→main).
// Uses the same progressive depth ancestry search starting from the PR head.
// Returns the slip and true if found, nil and false otherwise.
func (c *Client) findAncestorViaSquashMerge(
	ctx context.Context,
	owner, repo string,
	opts PushOptions,
) (SlipWithCommit, bool) {
	// Try all PR numbers found in commit message (supports nested merges)
	prNumbers := extractAllPRNumbers(opts.CommitMessage)
	if len(prNumbers) == 0 {
		return SlipWithCommit{}, false
	}

	c.logger.Debug(ctx, "Detected potential squash merge, looking up PRs", map[string]interface{}{
		"pr_numbers": prNumbers,
		"commit":     shortSHA(opts.CommitSHA),
	})

	// Try each PR number until we find a slip
	for _, prNumber := range prNumbers {
		// Get the PR's original head commit
		prHeadCommit, err := c.github.GetPRHeadCommit(ctx, owner, repo, prNumber)
		if err != nil {
			c.logger.Debug(ctx, "Failed to get PR head commit, trying next", map[string]interface{}{
				"error":     err.Error(),
				"pr_number": prNumber,
			})
			continue
		}

		c.logger.Debug(ctx, "Starting ancestry search from PR head commit", map[string]interface{}{
			"pr_number":   prNumber,
			"head_commit": shortSHA(prHeadCommit),
		})

		// Search for slips starting from the PR head commit and walking back its ancestry
		// Note: we want to INCLUDE the PR head commit itself in the search, since that's
		// where a slip might exist (unlike normal ancestry where we skip the merge commit)
		ancestorSlips, err := c.findSlipsInPRBranchHistory(ctx, owner, repo, opts.Repository, prHeadCommit)
		if err != nil {
			c.logger.Debug(ctx, "Failed to search PR head ancestry, trying next", map[string]interface{}{
				"error":       err.Error(),
				"pr_number":   prNumber,
				"head_commit": shortSHA(prHeadCommit),
			})
			continue
		}

		if len(ancestorSlips) > 0 {
			// Found a slip via this PR
			prSlip := ancestorSlips[0]

			// D3.4 guard: a fast-forward / no-op merge keeps the PR head SHA identical to
			// the commit being pushed (opts.CommitSHA). findSlipsInPRBranchHistory
			// deliberately INCLUDES the head commit in its search (unlike the normal
			// git-history ancestor search, which explicitly skips opts.CommitSHA), so an
			// ended slip for THIS SAME commit can surface here as its own "ancestor".
			// Using it would make the newborn slip its own ancestor: PromoteSlip would
			// promote it, repaveExistingSlip would then delete it (same commit, ended),
			// and InsertAncestryLink would write the newborn slip's parent pointing at the
			// row just deleted — a dangling self-reference from birth (DEVOPS-231 review
			// D3.4). Skip this candidate and keep trying other PR numbers.
			if prSlip.Slip.CommitSHA == opts.CommitSHA {
				c.logger.Debug(ctx, "Skipping squash-merge ancestor candidate matching the pushed commit itself",
					map[string]interface{}{
						"pr_number": prNumber,
						"pr_head":   shortSHA(prHeadCommit),
						"slip_id":   prSlip.Slip.CorrelationID,
						"commit":    shortSHA(opts.CommitSHA),
					})
				continue
			}

			c.logger.Info(ctx, "Found feature branch slip via squash merge PR ancestry", map[string]interface{}{
				"pr_number":   prNumber,
				"pr_head":     shortSHA(prHeadCommit),
				"slip_commit": shortSHA(prSlip.MatchedCommit),
				"slip_id":     prSlip.Slip.CorrelationID,
				"slip_status": string(prSlip.Slip.Status),
			})
			return prSlip, true
		}
	}

	c.logger.Debug(ctx, "No slips found for any PR references", map[string]interface{}{
		"pr_numbers": prNumbers,
		"commit":     shortSHA(opts.CommitSHA),
	})
	return SlipWithCommit{}, false
}

// findSlipsInPRBranchHistory searches for slips in a PR branch's commit history,
// starting from (and including) the PR head commit. Unlike findAncestorSlipsWithProgressiveDepth,
// this includes the starting commit in the search since the PR head itself may have a slip.
func (c *Client) findSlipsInPRBranchHistory(
	ctx context.Context,
	owner, repo, repository, headCommit string,
) ([]SlipWithCommit, error) {
	// Define search depths: initial, then max if needed
	depths := []int{c.config.AncestryDepth}
	if c.config.AncestryMaxDepth > c.config.AncestryDepth {
		depths = append(depths, c.config.AncestryMaxDepth)
	}

	for i, depth := range depths {
		isRetry := i > 0

		if isRetry {
			c.logger.Debug(ctx, "Expanding PR branch history search depth", map[string]interface{}{
				"head_commit":    shortSHA(headCommit),
				"previous_depth": depths[i-1],
				"new_depth":      depth,
			})
		}

		// Get commit ancestry from GitHub
		commits, err := c.github.GetCommitAncestry(ctx, owner, repo, headCommit, depth)
		if err != nil {
			return nil, fmt.Errorf("failed to get commit ancestry: %w", err)
		}

		// Unlike ancestor search, we INCLUDE the head commit in PR branch search
		// since that's where a slip is most likely to exist

		if len(commits) == 0 {
			c.logger.Debug(ctx, "No commits found in PR branch history", map[string]interface{}{
				"head_commit": shortSHA(headCommit),
				"depth":       depth,
			})
			return nil, nil
		}

		// Find all slips matching commits in PR branch history
		slips, err := c.store.FindAllByCommits(ctx, repository, commits)
		if err != nil {
			return nil, fmt.Errorf("failed to find slips by commits: %w", err)
		}

		if len(slips) > 0 {
			c.logger.Debug(ctx, "Found slips in PR branch history", map[string]interface{}{
				"head_commit": shortSHA(headCommit),
				"slip_count":  len(slips),
				"depth":       depth,
			})
			return slips, nil
		}

		// No slips found at this depth; if max depth not reached, try again
		if !isRetry && c.config.AncestryMaxDepth > c.config.AncestryDepth {
			continue
		}

		// Either first attempt with no retry configured, or final retry
		break
	}

	return nil, nil
}

// findAncestorSlipsWithProgressiveDepth searches for ancestor slips using progressive depth.
// It starts with AncestryDepth and expands to AncestryMaxDepth if no ancestors are found.
// This handles cases where many commits occur between slip creations (e.g., large pushes).
func (c *Client) findAncestorSlipsWithProgressiveDepth(
	ctx context.Context,
	owner, repo string,
	opts PushOptions,
) ([]SlipWithCommit, error) {
	// Define search depths: initial, then max if needed
	depths := []int{c.config.AncestryDepth}
	if c.config.AncestryMaxDepth > c.config.AncestryDepth {
		depths = append(depths, c.config.AncestryMaxDepth)
	}

	for i, depth := range depths {
		isRetry := i > 0

		if isRetry {
			c.logger.Debug(ctx, "Expanding ancestry search depth", map[string]interface{}{
				"commit":         shortSHA(opts.CommitSHA),
				"previous_depth": depths[i-1],
				"new_depth":      depth,
			})
		}

		// Get commit ancestry from GitHub
		commits, err := c.github.GetCommitAncestry(ctx, owner, repo, opts.CommitSHA, depth)
		if err != nil {
			return nil, fmt.Errorf("failed to get commit ancestry: %w", err)
		}

		// Skip the first commit if it's the current one (we're looking for ancestors)
		if len(commits) > 0 && commits[0] == opts.CommitSHA {
			commits = commits[1:]
		}

		if len(commits) == 0 {
			c.logger.Debug(ctx, "No ancestor commits found", map[string]interface{}{
				"commit": shortSHA(opts.CommitSHA),
				"depth":  depth,
			})
			return nil, nil // No point retrying if there are no commits at all
		}

		// Find all slips matching ancestor commits
		ancestorSlips, err := c.store.FindAllByCommits(ctx, opts.Repository, commits)
		if err != nil {
			return nil, fmt.Errorf("failed to find ancestor slips: %w", err)
		}

		if len(ancestorSlips) > 0 {
			c.logger.Debug(ctx, "Found ancestor slips", map[string]interface{}{
				"commit":            shortSHA(opts.CommitSHA),
				"ancestors_checked": len(commits),
				"ancestors_found":   len(ancestorSlips),
				"depth_used":        depth,
			})
			return ancestorSlips, nil
		}

		// No ancestors found at this depth
		c.logger.Debug(ctx, "No ancestor slips found at depth", map[string]interface{}{
			"commit":            shortSHA(opts.CommitSHA),
			"ancestors_checked": len(commits),
			"depth":             depth,
		})
	}

	return nil, nil
}

// handlePushRetry resets a slip for retry processing.
func (c *Client) handlePushRetry(ctx context.Context, slip *Slip) (*Slip, error) {
	c.logger.Info(ctx, "Found existing slip for commit, handling retry", map[string]interface{}{
		"correlation_id": slip.CorrelationID,
		"commit":         shortSHA(slip.CommitSHA),
	})

	now := time.Now()
	entry := StateHistoryEntry{
		Step:      "push_parsed",
		Status:    StepStatusRunning,
		Timestamp: now,
		Actor:     "slippy-library",
		Message:   "retry detected, resetting push_parsed",
	}

	// Use UpdateStepWithHistory (not separate UpdateStep + AppendHistory calls) so the
	// push_parsed status write and the history append happen atomically with the same
	// caller-supplied stepStatusOverride that UpdateStepWithHistory's pure-step branch
	// (see appendHistoryWithOverrides call in clickhouse_store.go) already uses to pin
	// push_parsed_status = running. Two separate calls would let AppendHistory's derive
	// CTE race the insertComponentState write UpdateStep just performed under ClickHouse
	// async-insert visibility lag, falling back to a stale clone of push_parsed_status.
	//
	// Routing through UpdateStepWithHistory also means this call adopts its best-effort
	// history write-back semantics: a failed history write-back is Warn-logged and
	// non-fatal, superseding a45e63c's hard-fail contract for standalone AppendHistory on
	// this retry path — the audit entry for this transition is lost in that narrow window,
	// status self-heals on next Load, and event insert / gate-check failures still fail
	// the retry.
	//
	// Because history write-back failures are already swallowed inside UpdateStepWithHistory
	// (Warn-logged, not returned), any error surfaced here is NOT a history-append failure —
	// it is either the terminal-freshness gate rejecting the write (ErrTerminalAlreadyExists)
	// or a genuine event-insert failure. Wrap with %w (not ErrHistoryAppendFailed) so the
	// underlying error chain — including errors.Is(err, ErrTerminalAlreadyExists) — survives.
	if err := c.store.UpdateStepWithHistory(
		ctx,
		slip.CorrelationID,
		"push_parsed",
		"",
		StepStatusRunning,
		entry,
	); err != nil {
		return nil, fmt.Errorf("retry push_parsed reset failed: %w", err)
	}

	// Reload to get updated slip
	return c.store.Load(ctx, slip.CorrelationID)
}

// initializeSlipForPush creates a fully initialized slip for a push event.
// Steps are initialized from the pipeline configuration rather than hardcoded.
// The ancestry parameter records any ancestor slips in the commit lineage.
func (c *Client) initializeSlipForPush(opts PushOptions, ancestry []AncestryEntry) *Slip {
	now := time.Now()

	// Initialize all pipeline steps from config as pending
	// The first step (typically push_parsed) starts as running
	steps := make(map[string]Step)
	aggregates := make(map[string][]ComponentStepData)
	var firstStep string

	if c.pipelineConfig != nil {
		for i, stepConfig := range c.pipelineConfig.Steps {
			step := Step{Status: StepStatusPending}

			// First step starts as running (unless it's an aggregate with no components)
			if i == 0 {
				firstStep = stepConfig.Name
				// Only auto-run first step if it's NOT an aggregate step,
				// OR if it's an aggregate but has components to process.
				// This prevents mobile apps (zero-component slips) from getting stuck.
				isAggregateStep := stepConfig.Aggregates != ""
				hasComponents := len(opts.Components) > 0
				if !isAggregateStep || hasComponents {
					step.Status = StepStatusRunning
					step.StartedAt = &now
				}
			}

			steps[stepConfig.Name] = step

			// Initialize aggregate columns with component data
			// Column name is the step name (e.g., "builds_completed"), not the pluralized aggregate
			if stepConfig.Aggregates != "" {
				columnName := stepConfig.Name
				componentData := make([]ComponentStepData, len(opts.Components))
				for j, def := range opts.Components {
					componentData[j] = ComponentStepData{
						Component: def.Name,
						Status:    StepStatusPending,
					}
				}
				aggregates[columnName] = componentData
			}
		}
	} else {
		// Fallback to default first step if no config (for backward compatibility)
		firstStep = "push_parsed"
		steps["push_parsed"] = Step{Status: StepStatusRunning, StartedAt: &now}
	}

	history := []StateHistoryEntry{
		{
			Step:      firstStep,
			Status:    StepStatusRunning,
			Timestamp: now,
			Actor:     "slippy-library",
			Message:   "processing push event",
		},
	}

	return &Slip{
		CorrelationID: opts.CorrelationID,
		Repository:    opts.Repository,
		Branch:        opts.Branch,
		CommitSHA:     opts.CommitSHA,
		CreatedAt:     now,
		UpdatedAt:     now,
		Status:        SlipStatusInProgress,
		Steps:         steps,
		Aggregates:    aggregates,
		StateHistory:  history,
		Ancestry:      ancestry,
	}
}
