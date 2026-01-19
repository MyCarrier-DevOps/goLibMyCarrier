// Package slippy provides routing slip functionality for CI/CD pipeline orchestration.
// It tracks the state of pipeline executions across multiple steps and components,
// enabling hold/proceed decisions based on prerequisite completion status.
//
// The library uses a JSON configuration to define the pipeline steps, their
// prerequisites, and aggregation relationships. This makes the library flexible
// and unopinionated about the specific CI/CD workflow being tracked.
package slippy

import (
	"time"
)

// Slip represents a routing slip tracking a pipeline execution.
//
// The CorrelationID is the unique identifier for this routing slip and will be
// persisted throughout the entire slip lifecycle. Within the MyCarrier organization,
// correlation_id is already used as a unique way to identify jobs across all systems
// (Kafka events, workflows, logging, etc.). This ID is synonymous with "slip ID" -
// there is ONE identifier for a slip, and it is the CorrelationID.
//
// The slip contains the overall state, components being built, pipeline steps,
// and a complete audit history of all state transitions.
type Slip struct {
	// CorrelationID is the unique identifier for this routing slip.
	// This ID persists through the entire slip lifecycle and links
	// the slip to Kafka events, workflows, and all related systems.
	CorrelationID string `json:"correlation_id" ch:"correlation_id"`

	// Repository is the full repository name (owner/repo)
	Repository string `json:"repository" ch:"repository"`

	// Branch is the git branch name
	Branch string `json:"branch" ch:"branch"`

	// CommitSHA is the full git commit SHA
	CommitSHA string `json:"commit_sha" ch:"commit_sha"`

	// CreatedAt is when the slip was created
	CreatedAt time.Time `json:"created_at" ch:"created_at"`

	// UpdatedAt is when the slip was last modified
	UpdatedAt time.Time `json:"updated_at" ch:"updated_at"`

	// Status is the overall slip status
	Status SlipStatus `json:"status" ch:"status"`

	// Steps maps step names to their current state
	// This is dynamically populated based on the pipeline configuration
	Steps map[string]Step `json:"steps" ch:"-"`

	// Aggregates maps aggregate step names to their component data
	// For steps with "aggregates" in config, this holds per-component details
	Aggregates map[string][]ComponentStepData `json:"aggregates" ch:"-"`

	// StateHistory is the complete audit trail of state transitions
	StateHistory []StateHistoryEntry `json:"state_history" ch:"-"`

	// Ancestry tracks the chain of prior slips that this slip supersedes.
	// Ordered most-recent-first, so Ancestry[0] is the immediate parent.
	// Nil or empty if this is the first slip for this commit lineage.
	Ancestry []AncestryEntry `json:"ancestry" ch:"-"`

	// PromotedTo holds the correlation ID of the slip this was promoted to.
	// Set when status is "promoted" (e.g., after a squash merge creates a new slip).
	// Empty if not promoted.
	PromotedTo string `json:"promoted_to,omitempty" ch:"promoted_to"`

	// Sign is used by VersionedCollapsingMergeTree for row management.
	// 1 = active row, -1 = cancelled/deleted row
	// This field is managed internally by the store and should not be set manually.
	Sign int8 `json:"-" ch:"sign"`

	// Version is used by VersionedCollapsingMergeTree to track row versions.
	// Higher versions take precedence during collapsing.
	// This field is managed internally by the store and should not be set manually.
	Version uint32 `json:"-" ch:"version"`
}

// AncestryEntry records metadata about a prior slip in the ancestry chain.
type AncestryEntry struct {
	// CorrelationID is the unique identifier of the ancestor slip
	CorrelationID string `json:"correlation_id"`

	// CommitSHA is the git commit SHA of the ancestor slip
	CommitSHA string `json:"commit_sha"`

	// Status is the final status of the ancestor slip (failed, completed, abandoned)
	Status SlipStatus `json:"status"`

	// FailedStep is the step that failed (if Status is failed)
	FailedStep string `json:"failed_step,omitempty"`

	// CreatedAt is when the ancestor slip was created
	CreatedAt time.Time `json:"created_at"`
}

// SlipWithCommit pairs a slip with the commit SHA that matched it.
// Used by FindAllByCommits to return multiple matching slips with their matched commits.
type SlipWithCommit struct {
	// Slip is the routing slip
	Slip *Slip

	// MatchedCommit is the commit SHA that matched this slip
	MatchedCommit string
}

// ComponentStepData represents per-component data for aggregate steps.
// This replaces the old Component struct for tracking component-level step progress.
type ComponentStepData struct {
	// Component is the unique identifier for this component within the repo
	Component string `json:"component"`

	// DockerfilePath is the path to the Dockerfile relative to repo root (for builds)
	DockerfilePath string `json:"dockerfile_path,omitempty"`

	// Status is the current status for this component's step
	Status StepStatus `json:"status"`

	// StartedAt is when this component's step began executing
	StartedAt *time.Time `json:"started_at,omitempty"`

	// CompletedAt is when this component's step finished
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Actor is the system or user that performed this step
	Actor string `json:"actor,omitempty"`

	// Error contains error details if the step failed
	Error string `json:"error,omitempty"`

	// ImageTag is the container image tag (for build steps)
	ImageTag string `json:"image_tag,omitempty"`
}

// ApplyStatusTransition updates the component's status and sets appropriate timestamps.
// This centralizes the common pattern of setting StartedAt when running
// and CompletedAt when terminal.
func (c *ComponentStepData) ApplyStatusTransition(status StepStatus, now time.Time) {
	c.Status = status
	if status == StepStatusRunning && c.StartedAt == nil {
		c.StartedAt = &now
	}
	if status.IsTerminal() && c.CompletedAt == nil {
		c.CompletedAt = &now
	}
}

// Step represents a pipeline step's current state.
// Steps track when they started, completed, who performed them,
// and any error information if they failed.
type Step struct {
	// Status is the current step status
	Status StepStatus `json:"status"`

	// StartedAt is when the step began executing
	StartedAt *time.Time `json:"started_at,omitempty"`

	// CompletedAt is when the step finished (success or failure)
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Actor is the system or user that performed the step
	Actor string `json:"actor,omitempty"`

	// Error contains error details if the step failed
	Error string `json:"error,omitempty"`

	// HeldReason explains why the step is held (if Status is held)
	HeldReason string `json:"held_reason,omitempty"`
}

// ApplyStatusTransition updates the step's status and sets appropriate timestamps.
// This centralizes the common pattern of setting StartedAt when running
// and CompletedAt when terminal.
func (s *Step) ApplyStatusTransition(status StepStatus, now time.Time) {
	s.Status = status
	if status == StepStatusRunning && s.StartedAt == nil {
		s.StartedAt = &now
	}
	if status.IsTerminal() && s.CompletedAt == nil {
		s.CompletedAt = &now
	}
}

// StateHistoryEntry records a single state transition for audit purposes.
// The history provides a complete timeline of all changes to the slip.
type StateHistoryEntry struct {
	// Step is the name of the step that changed
	Step string `json:"step"`

	// Component is the component name (if this is a component-specific step)
	Component string `json:"component,omitempty"`

	// Status is the new status after this transition
	Status StepStatus `json:"status"`

	// Timestamp is when this transition occurred
	Timestamp time.Time `json:"timestamp"`

	// Actor is the system or user that caused this transition
	Actor string `json:"actor"`

	// Message provides additional context about the transition
	Message string `json:"message,omitempty"`
}

// SlipOptions configures a new slip during creation.
type SlipOptions struct {
	// CorrelationID links this slip to Kafka events
	CorrelationID string

	// Repository is the full repository name (owner/repo)
	Repository string

	// Branch is the git branch name
	Branch string

	// CommitSHA is the full git commit SHA
	CommitSHA string

	// Components defines the components to track (for aggregate steps)
	Components []ComponentDefinition
}

// ComponentDefinition defines a component to track in the slip.
type ComponentDefinition struct {
	// Name is the unique identifier for this component
	Name string

	// DockerfilePath is the path to the Dockerfile
	DockerfilePath string
}
