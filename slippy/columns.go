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
	ColumnAncestry      = "ancestry"

	// VersionedCollapsingMergeTree columns
	ColumnSign    = "sign"
	ColumnVersion = "version"
)

// Table name constants
const (
	TableRoutingSlips        = "routing_slips"
	TableSlipComponentStates = "slip_component_states"
)
