package slippy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/chcol"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
)

// SlipScanner handles the deserialization of database rows into Slip structs.
// It encapsulates all row scanning logic, ensuring consistent handling of
// dynamic columns based on pipeline configuration.
type SlipScanner struct {
	config *PipelineConfig
}

// NewSlipScanner creates a new scanner for the given configuration.
func NewSlipScanner(config *PipelineConfig) *SlipScanner {
	return &SlipScanner{config: config}
}

// scanContext holds the scan destinations and intermediate values for row scanning.
type scanContext struct {
	slip             *Slip
	statusStr        string
	stepDetailsJSON  *chcol.JSON // Native ClickHouse JSON type
	stateHistoryJSON *chcol.JSON // Native ClickHouse JSON type
	ancestryJSON     *chcol.JSON // Native ClickHouse JSON type for ancestry chain
	stepStatuses     []string
	aggregateJSONs   map[string]*chcol.JSON // Native ClickHouse JSON type
	scanDest         []interface{}
}

// BuildScanContext creates a scanContext with all scan destinations prepared.
// The extraDest parameter allows adding additional destinations (e.g., matched_commit).
func (s *SlipScanner) BuildScanContext(extraDest ...interface{}) *scanContext {
	ctx := &scanContext{
		slip:             &Slip{},
		stepDetailsJSON:  chcol.NewJSON(),
		stateHistoryJSON: chcol.NewJSON(),
		ancestryJSON:     chcol.NewJSON(),
		stepStatuses:     make([]string, len(s.config.Steps)),
		aggregateJSONs:   make(map[string]*chcol.JSON),
	}

	// Core column destinations (order must match BuildSelectColumns)
	ctx.scanDest = []interface{}{
		&ctx.slip.CorrelationID,
		&ctx.slip.Repository,
		&ctx.slip.Branch,
		&ctx.slip.CommitSHA,
		&ctx.slip.CreatedAt,
		&ctx.slip.UpdatedAt,
		&ctx.statusStr,
		ctx.stepDetailsJSON,
		ctx.stateHistoryJSON,
		ctx.ancestryJSON,
		&ctx.slip.Sign,
		&ctx.slip.Version,
	}

	// Step status destinations
	for i := range s.config.Steps {
		ctx.scanDest = append(ctx.scanDest, &ctx.stepStatuses[i])
	}

	// Aggregate JSON destinations
	for _, step := range s.config.Steps {
		if step.Aggregates != "" {
			// Column name is the step name (e.g., "builds")
			columnName := step.Name
			jsonCol := chcol.NewJSON()
			ctx.aggregateJSONs[columnName] = jsonCol
			ctx.scanDest = append(ctx.scanDest, jsonCol)
		}
	}

	// Add any extra destinations (e.g., matched_commit)
	ctx.scanDest = append(ctx.scanDest, extraDest...)

	return ctx
}

// PopulateSlipFromScan populates the Slip from scanned values.
// This is called after row.Scan() completes successfully.
func (s *SlipScanner) PopulateSlipFromScan(ctx *scanContext) error {
	slip := ctx.slip

	// Parse status
	slip.Status = SlipStatus(ctx.statusStr)

	// Build steps map from status columns
	slip.Steps = make(map[string]Step)
	for i, step := range s.config.Steps {
		slip.Steps[step.Name] = Step{Status: StepStatus(ctx.stepStatuses[i])}
	}

	// Parse step details from chcol.JSON and merge into steps
	if ctx.stepDetailsJSON != nil {
		s.mergeStepDetailsFromJSON(slip, ctx.stepDetailsJSON)
	}

	// Parse aggregate JSON columns from chcol.JSON (unwrap from object)
	// The ClickHouse Go driver returns chcol.JSON which needs to be marshaled to JSON bytes
	// and then unmarshaled to our Go structs for proper type conversion.
	slip.Aggregates = make(map[string][]ComponentStepData)
	for columnName, jsonCol := range ctx.aggregateJSONs {
		if jsonCol == nil {
			continue
		}

		// Marshal to JSON bytes and unmarshal to Go structs
		jsonBytes, err := jsonCol.MarshalJSON()
		if err != nil {
			continue
		}

		var wrapper struct {
			Items []ComponentStepData `json:"items"`
		}
		if err := json.Unmarshal(jsonBytes, &wrapper); err != nil {
			continue
		}
		slip.Aggregates[columnName] = wrapper.Items
	}

	// Parse state history from chcol.JSON (unwrap from object)
	if ctx.stateHistoryJSON != nil {
		// Marshal to JSON bytes and unmarshal to Go structs
		jsonBytes, err := ctx.stateHistoryJSON.MarshalJSON()
		if err == nil {
			var wrapper struct {
				Entries []StateHistoryEntry `json:"entries"`
			}
			if err := json.Unmarshal(jsonBytes, &wrapper); err == nil {
				slip.StateHistory = wrapper.Entries
			}
		}
	}

	// Reconstruct step timing from state_history for any steps missing timing in step_details.
	// This handles the case where timing was lost due to version conflicts during concurrent updates.
	// The state_history is append-only and contains authoritative timestamps for state transitions.
	s.reconstructStepTimingFromHistory(slip)

	// Parse ancestry from chcol.JSON (unwrap from object)
	if ctx.ancestryJSON != nil {
		// Marshal to JSON bytes and unmarshal to Go structs
		jsonBytes, err := ctx.ancestryJSON.MarshalJSON()
		if err == nil {
			var wrapper struct {
				Chain []AncestryEntry `json:"chain"`
			}
			if err := json.Unmarshal(jsonBytes, &wrapper); err == nil {
				slip.Ancestry = wrapper.Chain
			}
		}
	}

	return nil
}

// ScanSlipFromRow scans a single row into a Slip.
func (s *SlipScanner) ScanSlipFromRow(row ch.Row) (*Slip, error) {
	ctx := s.BuildScanContext()

	if err := row.Scan(ctx.scanDest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSlipNotFound
		}
		return nil, fmt.Errorf("failed to scan slip: %w", err)
	}

	if err := s.PopulateSlipFromScan(ctx); err != nil {
		return nil, err
	}

	return ctx.slip, nil
}

// ScanSlipWithMatchFromRows scans a slip with matched_commit from driver.Rows.
// This is used for multi-row queries where we need to iterate.
func (s *SlipScanner) ScanSlipWithMatchFromRows(rows ch.Rows) (*Slip, string, error) {
	var matchedCommit string
	ctx := s.BuildScanContext(&matchedCommit)

	if err := rows.Scan(ctx.scanDest...); err != nil {
		return nil, "", fmt.Errorf("failed to scan slip from rows: %w", err)
	}

	if err := s.PopulateSlipFromScan(ctx); err != nil {
		return nil, "", err
	}

	return ctx.slip, matchedCommit, nil
}

// ScanSlipWithMatch scans a slip with an additional matched_commit column.
func (s *SlipScanner) ScanSlipWithMatch(row ch.Row) (*Slip, string, error) {
	var matchedCommit string
	ctx := s.BuildScanContext(&matchedCommit)

	if err := row.Scan(ctx.scanDest...); err != nil {
		return nil, "", err
	}

	if err := s.PopulateSlipFromScan(ctx); err != nil {
		return nil, "", err
	}

	return ctx.slip, matchedCommit, nil
}

// mergeStepDetailsFromJSON extracts step details from chcol.JSON and merges timing/actor info into steps.
// It uses the NestedMap() method to get the data structure, which works with both
// real ClickHouse data and test mocks using Scan().
func (s *SlipScanner) mergeStepDetailsFromJSON(slip *Slip, jsonCol *chcol.JSON) {
	// Get the nested map representation of the JSON
	nestedMap := jsonCol.NestedMap()
	if nestedMap == nil {
		return
	}

	for name, detailsRaw := range nestedMap {
		step, ok := slip.Steps[name]
		if !ok {
			continue
		}

		details, ok := detailsRaw.(map[string]interface{})
		if !ok {
			continue
		}

		if startedStr, ok := details["started_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, startedStr); err == nil {
				step.StartedAt = &t
			}
		}
		if completedStr, ok := details["completed_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, completedStr); err == nil {
				step.CompletedAt = &t
			}
		}
		if actor, ok := details["actor"].(string); ok {
			step.Actor = actor
		}
		if errStr, ok := details["error"].(string); ok {
			step.Error = errStr
		}
		if heldReason, ok := details["held_reason"].(string); ok {
			step.HeldReason = heldReason
		}

		slip.Steps[name] = step
	}
}

// reconstructStepTimingFromHistory fills in missing step timing from state_history entries.
// This is critical for handling version conflicts where step_details timing was lost
// because a concurrent update overwrote the slip before timing could be persisted.
//
// The state_history is append-only and contains authoritative timestamps for when
// steps transitioned to "running" (StartedAt) and terminal states (CompletedAt).
// By scanning history, we can recover timing that would otherwise be lost.
//
// This function only fills in MISSING timing - it does not overwrite existing values
// from step_details, preserving any timing that was successfully persisted.
func (s *SlipScanner) reconstructStepTimingFromHistory(slip *Slip) {
	if len(slip.StateHistory) == 0 {
		return
	}

	// Build a map of step timing from history entries.
	// For each step (non-component entries only), find:
	// - First "running" transition -> StartedAt
	// - First terminal transition -> CompletedAt
	type stepTiming struct {
		startedAt   *time.Time
		completedAt *time.Time
	}
	historyTiming := make(map[string]*stepTiming)

	for i := range slip.StateHistory {
		entry := &slip.StateHistory[i]

		// Skip component-level entries - they're tracked in Aggregates
		if entry.Component != "" {
			continue
		}

		// Initialize timing struct if needed
		if historyTiming[entry.Step] == nil {
			historyTiming[entry.Step] = &stepTiming{}
		}
		timing := historyTiming[entry.Step]

		// Capture the timestamp (make a copy to avoid pointer issues)
		ts := entry.Timestamp

		// Record first "running" transition as StartedAt
		if entry.Status == StepStatusRunning && timing.startedAt == nil {
			timing.startedAt = &ts
		}

		// Record first terminal transition as CompletedAt
		if entry.Status.IsTerminal() && timing.completedAt == nil {
			timing.completedAt = &ts
		}
	}

	// Fill in missing timing for steps
	for stepName, timing := range historyTiming {
		step, ok := slip.Steps[stepName]
		if !ok {
			continue
		}

		// Only fill in if the step is missing timing
		if step.StartedAt == nil && timing.startedAt != nil {
			step.StartedAt = timing.startedAt
		}
		if step.CompletedAt == nil && timing.completedAt != nil {
			step.CompletedAt = timing.completedAt
		}

		slip.Steps[stepName] = step
	}
}
