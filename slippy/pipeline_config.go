package slippy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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
	claimed := make(map[string]string, len(c.Steps))
	for _, step := range c.Steps {
		if step.Name == "" {
			return fmt.Errorf("step name cannot be empty")
		}
		if seen[step.Name] {
			return fmt.Errorf("duplicate step name: %s", step.Name)
		}
		if err := validateStepIdentifier(step, claimed); err != nil {
			return err
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

// MaxStepNameLen is the longest step name a pipeline config may carry.
//
// Every step name becomes a Postgres column, `{name}_status` (postgres_store.go's
// slipColumns/claimStateColumns, postgres_migrations.go's stepColumnEnsurer). Postgres
// truncates an unquoted identifier at NAMEDATALEN-1 = 63 BYTES silently — no error, no warning
// — so a longer name produces a column whose real name is not the one the SELECT list and the
// UPDATE SET list are built from, and every read of that step fails with 42703 against a schema
// the migrator reported as applied. 63 minus len("_status") is the bound.
const MaxStepNameLen = 63 - len("_status")

// stepNameIdentifierPattern matches the step names that survive being spliced UNQUOTED into
// identifier position. It is the INTERSECTION of the two backends' unquoted-identifier rules:
// Postgres requires a leading letter or underscore and then admits letters, digits,
// underscores and dollar signs; ClickHouse's non-quoted identifiers must match
// ^[a-zA-Z_][0-9a-zA-Z_]*$, which excludes the dollar sign. A config is written once and
// generates schema for both, so the stricter of the two is what a step name has to satisfy.
var stepNameIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedStepNames is every fixed routing_slips column on EITHER backend, folded to lower
// case the way Postgres folds the DDL that would collide with it. DERIVED from
// fixedSlipColumns() plus ColumnClaimedFrom — exactly the non-step part of
// PostgresStore.slipSelectColumns() — rather than restated, so a column added to that list is
// reserved against step names by the same edit and cannot silently stop being reserved.
//
// The three ClickHouse-only columns are appended explicitly because no Postgres-derived list
// contains them (PR #87 review, pkuzmenko): ColumnSign and ColumnVersion are the unconditional
// base-schema columns of the VersionedCollapsingMergeTree(sign, version) engine, and
// ColumnAncestry is the v3 inline column. An aggregate step's jsonb/JSON column is its BARE
// name, so an aggregate step named `version` collides with one of them on ClickHouse exactly
// as one named `status` collides on Postgres — and it passes every other arm, since none of
// the three is a SQL keyword.
//
// Reserving them on BOTH backends rather than only where they exist is the same choice
// stepNameIdentifierPattern makes one paragraph below: a config is written once and generates
// schema for both, so it must satisfy the stricter of the two. The cost is three step names
// nobody wants; the alternative is a validator whose answer depends on which store the reader
// happens to be looking at.
var reservedStepNames = func() map[string]struct{} {
	cols := append(fixedSlipColumns(), ColumnClaimedFrom, ColumnSign, ColumnVersion, ColumnAncestry)
	reserved := make(map[string]struct{}, len(cols))
	for _, col := range cols {
		reserved[strings.ToLower(col)] = struct{}{}
	}
	return reserved
}()

// validateStepIdentifier rejects the five step-name shapes that pass exact-case uniqueness and
// then break either the SCHEMA the config generates or the claim's audit record. All five are caught here, in the config, rather than in
// PostgresStore.ProbeSchema: the probe folds case when it diffs the live catalogue against
// slipSelectColumns(), which is correct for the probe — Postgres folded the DDL, so a configured
// `Deploy_Dev` legitimately lands as `deploy_dev` — but that folding also hides these faults
// from it, and the probe is the wrong place to find them anyway. A config is rejected at parse
// time, once, before any DDL runs (PR #87 finding j6). They are listed in the order they are
// checked, and this list is the whole of what this function rejects — see the closing note for
// the one schema-breaking shape it still admits, which is recorded rather than left implied.
// The list, the count and the arms are meant to agree; keep all three in step when adding one.
//
//   - A NAME THAT IS NOT A BARE IDENTIFIER. Step names reach identifier position UNQUOTED:
//     postgres_migrations.go's stepColumnEnsurer emits
//     `ADD COLUMN IF NOT EXISTS {name}_status step_status ...` (plus a bare `{name}` column for
//     an aggregate step), and slipColumns/slipSelectColumns rebuild the same identifiers into
//     every SELECT and every SET list. So a step named `prod-deploy` emits `prod-deploy_status`
//     and a step named `1deploy` emits `1deploy_status`, each a 42601 syntax error on the
//     migration every consumer runs at startup — and nothing recovers, because the identifier
//     is rebuilt from the same name on every read and write. See stepNameIdentifierPattern for
//     the shape and for why it is stricter than either backend alone. It is also stricter than
//     clickhouse_store.go's safeStepNameForDerivePattern, which admits a leading digit; the
//     note there says why that one is left as it is (PR #87, pkuzmenko finding 1 arm A).
//   - A NAME THAT TRUNCATES. See MaxStepNameLen.
//   - A NAME THAT IS ALREADY A FIXED routing_slips COLUMN. An AGGREGATE step's jsonb column is
//     its BARE name — slipColumns() appends step.Name and the ensurer emits
//     `ADD COLUMN IF NOT EXISTS {name} jsonb` — so an aggregate step named `status` or
//     `state_history` names a column that already exists. IF NOT EXISTS then silently does
//     nothing, ProbeSchema reports the column present because it IS present, and every later
//     write puts that column twice in one SET list: 42701, the same fault the fold arm below
//     prevents, reached by another route. Checked for EVERY step, not only aggregate ones, even
//     though a non-aggregate step's `{name}_status` column cannot collide with any fixed column
//     (no fixed column ends in `_status`, and an empty name is rejected before this): adding
//     `"aggregates"` to an existing step is a one-word config edit, and it is the only thing
//     standing between a merely confusing name and that silent collision. The reserved set is
//     derived from the store's own column list — see reservedStepNames (PR #87, pkuzmenko
//     finding 1 arm B).
//   - TWO STEPS THAT GENERATE THE SAME COLUMN. Postgres folds unquoted identifiers to lower
//     case, so `Deploy` and `deploy` are ONE column: exact-case uniqueness above admits both,
//     and then every write builds `SET Deploy_status = $n, deploy_status = $m` — 42701 on every
//     update the slip ever takes. The check keys on the identifiers a step GENERATES rather than
//     on its name, because two unrelated names can produce one column: a step `deploy` emits
//     `deploy_status`, and an aggregate step literally named `deploy_status` emits that same bare
//     column, so a name-keyed check admits the pair. See generatedColumnsFor, which must stay in
//     step with stepColumnEnsurer — an identifier it does not return is one nothing checks.
//   - A NAME THAT IS ONE OF THE LIBRARY'S OWN state_history MARKERS. Unlike the four above this
//     one breaks no SQL: it is caught here because it forges or suppresses the claim's audit
//     record, which pushhookparser's ClaimedBy derives by scanning state_history backwards
//     for those names. See reservedMarkerSteps for the set and for why PushParsedStep is
//     deliberately NOT in it, and ErrReservedStepName for what a marker-named entry costs a
//     reader. The same names are refused on the caller-supplied WRITE paths by
//     GuardReservedStepWrite, because a step name need not be in the config to reach
//     state_history.
//
// STILL ADMITTED, and deliberately: a name that is a bare identifier but a SQL RESERVED KEYWORD.
// An aggregate step named `order`, `group`, `table` or `select` emits
// `ADD COLUMN IF NOT EXISTS order jsonb` — 42601, the same class as the first arm above, and
// ClickHouse's rules agree that a non-quoted identifier may not equal a keyword. It is left open
// because the only check that fits here is a hand-typed keyword list, which is exactly the
// drifting duplicate the reserved-column arm was written to avoid: Postgres's reserved set is
// version-dependent and long. The fix that actually closes the class is to QUOTE the generated
// identifiers at every splice site, using the lower-cased name so existing folded columns still
// match — a change worth making deliberately rather than bolting onto this one.
//
// claimed is the caller's accumulator, mapping each generated identifier to the step name that
// claimed it, so the error can name BOTH colliding steps rather than only the second.
func validateStepIdentifier(step StepConfig, claimed map[string]string) error {
	name := step.Name
	if !stepNameIdentifierPattern.MatchString(name) {
		return fmt.Errorf(
			"step name %q is not a bare SQL identifier (it must match %s): the generated DDL and "+
				"every SELECT and SET list splice it unquoted as %s_status",
			name, stepNameIdentifierPattern, name)
	}
	if len(name) > MaxStepNameLen {
		return fmt.Errorf(
			"step name %q is %d bytes; Postgres truncates the %s_status column at 63 bytes, so it must be at most %d",
			name, len(name), name, MaxStepNameLen)
	}
	key := strings.ToLower(name)
	if _, ok := reservedStepNames[key]; ok {
		return fmt.Errorf(
			"step name %q is already the routing_slips column %s: an aggregate step's column is its "+
				"BARE name, so ADD COLUMN IF NOT EXISTS %s silently does nothing and every later "+
				"update names %s twice in one SET list",
			name, key, key, key)
	}
	// Checked separately from the column set above, and with its own message, because this is a
	// different fault with a different fix: the name does not collide with a column, it enters
	// the state_history namespace the claim's audit record is derived from. See
	// reservedMarkerSteps and ErrReservedStepName.
	if reservedMarkerStep(name) {
		return fmt.Errorf(
			"step name %q is a state_history marker the library owns (%s and %s): "+
				"pushhookparser's ClaimedBy derives who holds a claim "+
				"by scanning for these names, so a step reporting under one would forge or "+
				"suppress that signal: %w",
			name, ClaimMarkerStep, ReleaseMarkerStep, ErrReservedStepName)
	}
	// Collision is checked over the identifiers a step GENERATES, not over its name, because two
	// different names can generate one identifier: a step `deploy` emits `deploy_status`, and an
	// aggregate step literally named `deploy_status` emits that same bare column. Keying on the
	// name would admit both. This also subsumes the fold case it replaces, since `Deploy` and
	// `deploy` generate one lower-cased `deploy_status`.
	for _, ident := range generatedColumnsFor(step) {
		ident = strings.ToLower(ident)
		if first, ok := claimed[ident]; ok {
			return fmt.Errorf(
				"step names '%s' and '%s' both generate the routing_slips column %s: unquoted "+
					"identifiers fold to lower case, a step emits {name}_status, and an aggregate "+
					"step also emits a bare {name} column. ADD COLUMN IF NOT EXISTS then runs once "+
					"and every later update names %s twice in one SET list",
				first, name, ident, ident)
		}
		claimed[ident] = name
	}
	return nil
}

// generatedColumnsFor returns every routing_slips identifier a step puts into the schema, in the
// order postgres_migrations.go's stepColumnEnsurer emits them. Keep the two in step: an identifier
// this function does not return is one nothing validates against collision.
func generatedColumnsFor(step StepConfig) []string {
	cols := []string{stepStatusColumn(step.Name)}
	if step.Aggregates != "" {
		cols = append(cols, aggregateColumn(step.Name))
	}
	return cols
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
// The column name is the step name itself (e.g., "builds_completed").
// This must match the column created by dynamic_migrations.go.
func (c *PipelineConfig) GetAggregateColumnName(stepName string) string {
	step := c.stepsByName[stepName]
	if step == nil || step.Aggregates == "" {
		return ""
	}
	return step.Name
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
