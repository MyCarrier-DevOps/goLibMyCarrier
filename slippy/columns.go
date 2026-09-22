package slippy

// Column name constants for the routing_slips table.
// Using constants prevents typos and enables IDE auto-completion.
const (
	// Core columns
	ColumnCorrelationID = "correlation_id"
	ColumnRepository    = "repository"
	ColumnBranch        = "branch"
	ColumnCommitSHA     = "commit_sha"
	ColumnCreatedAt     = "created_at"
	ColumnUpdatedAt     = "updated_at"
	ColumnStatus        = "status"
	ColumnStepDetails   = "step_details"
	ColumnStateHistory  = "state_history"
	// ColumnClaimedFrom is SELECT-only: written by ClaimSlip and ReleaseClaim, and cleared by
	// UpdateSlipStatus on a terminal status — the one write path that ends a claim. Neither
	// Create nor the full-row Update ever writes it, whatever status they carry.
	ColumnClaimedFrom = "claimed_from"
	ColumnAncestry    = "ancestry"

	// VersionedCollapsingMergeTree columns
	ColumnSign    = "sign"
	ColumnVersion = "version"
)

// Table name constants
const (
	TableRoutingSlips        = "routing_slips"
	TableSlipComponentStates = "slip_component_states"
	TableSlipAncestry        = "slip_ancestry"
)

// Column name constants for the slip_ancestry table.
const (
	ColumnParentCorrelationID = "parent_correlation_id"
	ColumnParentCommitSHA     = "parent_commit_sha"
	ColumnParentStatus        = "parent_status"
	ColumnParentFailedStep    = "parent_failed_step"
	ColumnParentRepository    = "parent_repository"
	ColumnParentBranch        = "parent_branch"
)

// fixedSlipColumns returns the routing_slips columns every configuration carries regardless
// of its steps, in the order PostgresStore.slipColumns emits them: that method builds its
// INSERT/SELECT/SET list by appending the per-step columns to exactly this slice, and
// slipSelectColumns appends the SELECT-only ColumnClaimedFrom after those. It returns a fresh
// slice on every call because both callers append to it.
//
// It is also where validateStepIdentifier (pipeline_config.go) gets the names no step may
// take: an aggregate step's column is its BARE name, so a step named after one of these
// columns names a column that already exists. Deriving the reserved set from here rather than
// restating it means a column added to this list is reserved against step names in the same
// edit, and cannot silently stop being reserved (PR #87, pkuzmenko finding 1 arm B).
func fixedSlipColumns() []string {
	return []string{
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus, ColumnStepDetails, ColumnStateHistory,
	}
}

// stepStatusColumn returns the column that carries one step's status: the step's name with a
// `_status` suffix. Together with aggregateColumn below it is the WHOLE of what a configured
// step puts into the schema (generatedColumnsFor), and both stores follow the same convention.
//
// It exists because that convention was open-coded at every site that needed it, each asking
// the reader to keep it in step with the others (PR #87, jhicks review). Rather than enumerate
// them — a list that has now been wrong twice, first by miscounting and then by omitting five
// sites, two of them on the operational backend (PR #87 review, pkuzmenko) — the invariant is
// stated as something checkable instead:
//
//	NO CALLER BUILDS A STEP'S COLUMN NAME BY HAND. Every `<name>_status` and every bare
//	aggregate column comes from these two helpers, on BOTH backends.
//
// Two greps are the check, because the invariant has two clauses and the obvious grep only
// sees one of them (PR #87 review, pkuzmenko) — the bare aggregate form contains no `_status`
// literal at all:
//
//	grep -rn '%s_status' --include='*.go' slippy/
//	grep -rnE 'Sprintf\(.*(SELECT|UPDATE|ALTER|SET) .*%s' --include='*.go' slippy/
//
// Both should return only error-message text. Those are greps a reviewer can run; a site list
// is only ever as good as the last edit that remembered to update it.
//
// This matters beyond tidiness because generatedColumnsFor's collision validator pins itself
// to stepColumnEnsurer's emission: an identifier produced anywhere else is one the validator
// never sees, so every hand-rolled splice was a second definition able to outvote it. A
// validator that can be outvoted by a copy is weaker than one convention with one definition.
//
// It does NOT validate or quote. A step name reaches a SQL identifier either from a config
// that passed validateStepIdentifier or through an explicit bare-identifier check at the one
// site that splices caller-supplied input (updateStepTx); both remain each caller's business.
func stepStatusColumn(stepName string) string {
	return stepName + "_status"
}

// aggregateColumn returns the column that carries an aggregate step's component rollup, which
// is the step's BARE name (e.g. "builds"). Stated as a function beside stepStatusColumn rather
// than left implicit at each call site because the bare name is the reason fixedSlipColumns is
// the reserved-name set: a step named after a fixed column names a column that already exists.
func aggregateColumn(stepName string) string {
	return stepName
}
