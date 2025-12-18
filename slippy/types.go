// Package slippy provides routing slip functionality for CI/CD pipeline orchestration.
// It tracks the state of pipeline executions across multiple steps and components,
// enabling hold/proceed decisions based on prerequisite completion status.
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

	// Components is the list of buildable components in this repository
	Components []Component `json:"components" ch:"components"`

	// Steps maps step names to their current state
	Steps map[string]Step `json:"steps" ch:"-"`

	// StateHistory is the complete audit trail of state transitions
	StateHistory []StateHistoryEntry `json:"state_history" ch:"-"`
}

// Component represents a buildable component within a repository.
// Each component typically corresponds to a Dockerfile and produces
// a container image.
type Component struct {
	// Name is the unique identifier for this component within the repo
	Name string `json:"name"`

	// DockerfilePath is the path to the Dockerfile relative to repo root
	DockerfilePath string `json:"dockerfile_path"`

	// BuildStatus is the current build status for this component
	BuildStatus StepStatus `json:"build_status"`

	// UnitTestStatus is the current unit test status for this component
	UnitTestStatus StepStatus `json:"unit_test_status"`

	// ImageTag is the container image tag after successful build
	ImageTag string `json:"image_tag,omitempty"`
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

	// Components defines the components to track
	Components []ComponentDefinition
}

// ComponentDefinition defines a component to track in the slip.
type ComponentDefinition struct {
	// Name is the unique identifier for this component
	Name string

	// DockerfilePath is the path to the Dockerfile
	DockerfilePath string
}

// PipelineSteps defines all the standard pipeline step names.
// These are the steps that slippy tracks for each pipeline execution.
var PipelineSteps = []string{
	"push_parsed",
	"builds_completed",
	"unit_tests_completed",
	"secret_scan_completed",
	"dev_deploy",
	"dev_tests",
	"preprod_deploy",
	"preprod_tests",
	"prod_release_created",
	"prod_deploy",
	"prod_tests",
	"alert_gate",
	"prod_steady_state",
}

// AggregateStepMap maps component-level steps to their aggregate steps.
// When all components complete a step, the aggregate is updated.
var AggregateStepMap = map[string]string{
	"build":     "builds_completed",
	"unit_test": "unit_tests_completed",
}
