package slippy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// PipelineConfig represents the complete pipeline configuration loaded from JSON.
// It defines all steps, their prerequisites, and aggregation relationships.
type PipelineConfig struct {
	// Version is the configuration schema version
	Version string `json:"version"`

	// Name is a human-readable identifier for this pipeline
	Name string `json:"name"`

	// Description provides context about the pipeline
	Description string `json:"description"`

	// Steps defines all pipeline steps in order
	Steps []StepConfig `json:"steps"`

	// Computed fields (not from JSON)
	stepsByName  map[string]*StepConfig
	aggregateMap map[string]string // component step -> aggregate step name
	gateSteps    []string          // steps with is_gate: true
	configHash   string            // SHA256 hash of the config for migration tracking
}

// StepConfig represents a single pipeline step configuration.
type StepConfig struct {
	// Name is the unique identifier for this step (used in column names)
	Name string `json:"name"`

	// Description provides context about what this step does
	Description string `json:"description"`

	// Prerequisites lists step names that must complete before this step can proceed
	Prerequisites []string `json:"prerequisites"`

	// Aggregates is the component-level step name this step aggregates (optional)
	// When set, creates an additional JSON column for per-component tracking
	Aggregates string `json:"aggregates,omitempty"`

	// IsGate marks this step as a gate - its success/failure is an implicit
	// prerequisite for all subsequent steps
	IsGate bool `json:"is_gate,omitempty"`

	// Computed fields
	order int // position in the pipeline (0-indexed)
}

// PipelineConfigEnvVar is the environment variable name for pipeline configuration.
// The value can be either a file path or raw JSON content.
const PipelineConfigEnvVar = "SLIPPY_PIPELINE_CONFIG"

// LoadPipelineConfig loads pipeline configuration from the environment variable.
// If the value looks like a file path, it reads from the file.
// Otherwise, it parses the value as JSON directly.
func LoadPipelineConfig() (*PipelineConfig, error) {
	configValue := os.Getenv(PipelineConfigEnvVar)
	if configValue == "" {
		return nil, fmt.Errorf("environment variable %s is not set", PipelineConfigEnvVar)
	}

	return LoadPipelineConfigFromString(configValue)
}

// LoadPipelineConfigFromString loads pipeline configuration from a string.
// If the string looks like a file path, it reads from the file.
// Otherwise, it parses the string as JSON directly.
func LoadPipelineConfigFromString(configValue string) (*PipelineConfig, error) {
	var jsonData []byte
	var err error

	// Determine if it's a file path or raw JSON
	if isFilePath(configValue) {
		jsonData, err = os.ReadFile(configValue)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %s: %w", configValue, err)
		}
	} else {
		jsonData = []byte(configValue)
	}

	return ParsePipelineConfig(jsonData)
}

// LoadPipelineConfigFromFile loads pipeline configuration from a specific file path.
func LoadPipelineConfigFromFile(filePath string) (*PipelineConfig, error) {
	jsonData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filePath, err)
	}

	return ParsePipelineConfig(jsonData)
}

// ParsePipelineConfig parses JSON data into a PipelineConfig and validates it.
func ParsePipelineConfig(jsonData []byte) (*PipelineConfig, error) {
	var config PipelineConfig
	if err := json.Unmarshal(jsonData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline config JSON: %w", err)
	}

	// Compute the config hash for migration tracking
	config.configHash = computeConfigHash(jsonData)

	// Initialize computed fields
	config.initialize()

	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

// Validate checks the configuration for errors.
func (c *PipelineConfig) Validate() error {
	if len(c.Steps) == 0 {
		return fmt.Errorf("pipeline config must have at least one step")
	}

	// Check first step has no prerequisites
	if len(c.Steps[0].Prerequisites) > 0 {
		return fmt.Errorf("first step '%s' must have no prerequisites", c.Steps[0].Name)
	}

	// Check for duplicate step names
	seen := make(map[string]bool)
	for _, step := range c.Steps {
		if step.Name == "" {
			return fmt.Errorf("step name cannot be empty")
		}
		if seen[step.Name] {
			return fmt.Errorf("duplicate step name: %s", step.Name)
		}
		seen[step.Name] = true
	}

	// Check all prerequisites reference valid steps
	for _, step := range c.Steps {
		for _, prereq := range step.Prerequisites {
			if !seen[prereq] {
				return fmt.Errorf("step '%s' has unknown prerequisite '%s'", step.Name, prereq)
			}
		}
	}

	// Check for circular dependencies
	if err := c.detectCircularDependencies(); err != nil {
		return err
	}

	// Check aggregate references are unique
	aggregates := make(map[string]string)
	for _, step := range c.Steps {
		if step.Aggregates != "" {
			if existing, ok := aggregates[step.Aggregates]; ok {
				return fmt.Errorf("aggregate '%s' is used by both '%s' and '%s'",
					step.Aggregates, existing, step.Name)
			}
			aggregates[step.Aggregates] = step.Name
		}
	}

	return nil
}

// GetStep returns a step by name, or nil if not found.
func (c *PipelineConfig) GetStep(name string) *StepConfig {
	return c.stepsByName[name]
}

// GetStepNames returns all step names in order.
func (c *PipelineConfig) GetStepNames() []string {
	names := make([]string, len(c.Steps))
	for i, step := range c.Steps {
		names[i] = step.Name
	}
	return names
}

// GetAggregateStep returns the aggregate step name for a component step.
// Returns empty string if the component step doesn't have an aggregate.
func (c *PipelineConfig) GetAggregateStep(componentStep string) string {
	return c.aggregateMap[componentStep]
}

// GetComponentStep returns the component step name for an aggregate step.
// Returns empty string if the step doesn't aggregate anything.
func (c *PipelineConfig) GetComponentStep(aggregateStep string) string {
	step := c.stepsByName[aggregateStep]
	if step == nil {
		return ""
	}
	return step.Aggregates
}

// IsAggregateStep returns true if the step aggregates component-level data.
func (c *PipelineConfig) IsAggregateStep(stepName string) bool {
	step := c.stepsByName[stepName]
	return step != nil && step.Aggregates != ""
}

// IsGateStep returns true if the step is marked as a gate.
func (c *PipelineConfig) IsGateStep(stepName string) bool {
	step := c.stepsByName[stepName]
	return step != nil && step.IsGate
}

// GetGateSteps returns all gate step names.
func (c *PipelineConfig) GetGateSteps() []string {
	return c.gateSteps
}

// GetEffectivePrerequisites returns all prerequisites for a step,
// including implicit prerequisites from gate steps.
func (c *PipelineConfig) GetEffectivePrerequisites(stepName string) []string {
	step := c.stepsByName[stepName]
	if step == nil {
		return nil
	}

	// Start with explicit prerequisites
	prereqSet := make(map[string]bool)
	for _, prereq := range step.Prerequisites {
		prereqSet[prereq] = true
	}

	// Add implicit gate prerequisites
	// A step must wait for any gate step that comes before it in the pipeline
	for _, gateStep := range c.gateSteps {
		gateConfig := c.stepsByName[gateStep]
		if gateConfig != nil && gateConfig.order < step.order {
			// This gate comes before our step, add it as implicit prereq
			prereqSet[gateStep] = true
		}
	}

	// Convert to sorted slice for deterministic ordering
	prereqs := make([]string, 0, len(prereqSet))
	for prereq := range prereqSet {
		prereqs = append(prereqs, prereq)
	}
	sort.Strings(prereqs)

	return prereqs
}

// GetAggregateSteps returns all steps that have aggregates.
func (c *PipelineConfig) GetAggregateSteps() []StepConfig {
	result := make([]StepConfig, 0)
	for _, step := range c.Steps {
		if step.Aggregates != "" {
			result = append(result, step)
		}
	}
	return result
}

// StepVisitor is a function that processes a step configuration.
// Return an error to stop iteration early; return nil to continue.
type StepVisitor func(step *StepConfig) error

// ForEachStep iterates over all steps in order and calls the visitor function.
// Iteration stops early if the visitor returns an error.
func (c *PipelineConfig) ForEachStep(visitor StepVisitor) error {
	for i := range c.Steps {
		if err := visitor(&c.Steps[i]); err != nil {
			return err
		}
	}
	return nil
}

// ForEachAggregateStep iterates over steps that aggregate component data.
// Iteration stops early if the visitor returns an error.
func (c *PipelineConfig) ForEachAggregateStep(visitor StepVisitor) error {
	for i := range c.Steps {
		if c.Steps[i].Aggregates != "" {
			if err := visitor(&c.Steps[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ConfigHash returns the SHA256 hash of the configuration.
// This is used to track which config version generated migrations.
func (c *PipelineConfig) ConfigHash() string {
	return c.configHash
}

// isFilePath determines if a string looks like a file path.
// Returns true if it starts with /, ./, ../, or contains path separators
// and doesn't start with { (JSON object).
func isFilePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	// If it starts with {, it's JSON
	if strings.HasPrefix(s, "{") {
		return false
	}

	// Common file path indicators
	if strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "~") {
		return true
	}

	// Check for file extension common to config files
	if strings.HasSuffix(s, ".json") {
		return true
	}

	return false
}

// computeConfigHash computes SHA256 hash of the config JSON.
func computeConfigHash(jsonData []byte) string {
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:])
}

// GetAggregateColumnName returns the JSON column name for an aggregate step.
// For example, a step with aggregates="build" returns "builds".
func (c *PipelineConfig) GetAggregateColumnName(stepName string) string {
	step := c.stepsByName[stepName]
	if step == nil || step.Aggregates == "" {
		return ""
	}
	return pluralize(step.Aggregates)
}

// initialize builds the internal lookup structures.
func (c *PipelineConfig) initialize() {
	c.stepsByName = make(map[string]*StepConfig)
	c.aggregateMap = make(map[string]string)
	c.gateSteps = make([]string, 0)

	for i := range c.Steps {
		step := &c.Steps[i]
		step.order = i
		c.stepsByName[step.Name] = step

		if step.Aggregates != "" {
			c.aggregateMap[step.Aggregates] = step.Name
		}

		if step.IsGate {
			c.gateSteps = append(c.gateSteps, step.Name)
		}
	}
}

// detectCircularDependencies checks for circular dependencies in the prerequisite graph.
func (c *PipelineConfig) detectCircularDependencies() error {
	// Build adjacency list
	graph := make(map[string][]string)
	for _, step := range c.Steps {
		graph[step.Name] = step.Prerequisites
	}

	// DFS-based cycle detection
	white := make(map[string]bool) // not visited
	gray := make(map[string]bool)  // in current path
	black := make(map[string]bool) // fully processed

	for _, step := range c.Steps {
		white[step.Name] = true
	}

	var dfs func(node string, path []string) error
	dfs = func(node string, path []string) error {
		white[node] = false
		gray[node] = true
		path = append(path, node)

		for _, prereq := range graph[node] {
			if gray[prereq] {
				// Found cycle - build cycle path for error message
				cycleStart := -1
				for i, n := range path {
					if n == prereq {
						cycleStart = i
						break
					}
				}
				// Build the cycle path by copying the relevant portion and adding the prereq
				cyclePath := make([]string, len(path[cycleStart:])+1)
				copy(cyclePath, path[cycleStart:])
				cyclePath[len(cyclePath)-1] = prereq
				return fmt.Errorf("circular dependency detected: %s", strings.Join(cyclePath, " -> "))
			}
			if white[prereq] {
				if err := dfs(prereq, path); err != nil {
					return err
				}
			}
		}

		gray[node] = false
		black[node] = true
		return nil
	}

	for _, step := range c.Steps {
		if white[step.Name] {
			if err := dfs(step.Name, nil); err != nil {
				return err
			}
		}
	}

	return nil
}
