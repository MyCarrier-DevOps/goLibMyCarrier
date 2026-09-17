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
