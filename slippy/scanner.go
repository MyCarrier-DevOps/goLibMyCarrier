package slippy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	stepDetailsJSON  string
	stateHistoryJSON string
	stepStatuses     []string
	aggregateJSONs   map[string]*string
	scanDest         []interface{}
}

// BuildScanContext creates a scanContext with all scan destinations prepared.
// The extraDest parameter allows adding additional destinations (e.g., matched_commit).
func (s *SlipScanner) BuildScanContext(extraDest ...interface{}) *scanContext {
	ctx := &scanContext{
		slip:           &Slip{},
		stepStatuses:   make([]string, len(s.config.Steps)),
		aggregateJSONs: make(map[string]*string),
	}

	// Core column destinations
	ctx.scanDest = []interface{}{
		&ctx.slip.CorrelationID,
		&ctx.slip.Repository,
		&ctx.slip.Branch,
		&ctx.slip.CommitSHA,
		&ctx.slip.CreatedAt,
		&ctx.slip.UpdatedAt,
		&ctx.statusStr,
		&ctx.stepDetailsJSON,
		&ctx.stateHistoryJSON,
	}

	// Step status destinations
	for i := range s.config.Steps {
		ctx.scanDest = append(ctx.scanDest, &ctx.stepStatuses[i])
	}

	// Aggregate JSON destinations
	for _, step := range s.config.Steps {
		if step.Aggregates != "" {
			columnName := pluralize(step.Aggregates)
			var jsonStr string
			ctx.aggregateJSONs[columnName] = &jsonStr
			ctx.scanDest = append(ctx.scanDest, &jsonStr)
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

	// Parse step details and merge into steps
	s.mergeStepDetails(slip, ctx.stepDetailsJSON)

	// Parse aggregate JSON columns
	slip.Aggregates = make(map[string][]ComponentStepData)
	for columnName, jsonPtr := range ctx.aggregateJSONs {
		if jsonPtr != nil && *jsonPtr != "" {
			var componentData []ComponentStepData
			if err := json.Unmarshal([]byte(*jsonPtr), &componentData); err == nil {
				slip.Aggregates[columnName] = componentData
			}
		}
	}

	// Parse state history JSON
	if err := json.Unmarshal([]byte(ctx.stateHistoryJSON), &slip.StateHistory); err != nil {
		return fmt.Errorf("failed to unmarshal state history: %w", err)
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

// mergeStepDetails parses step details JSON and merges timing/actor info into steps.
func (s *SlipScanner) mergeStepDetails(slip *Slip, stepDetailsJSON string) {
	var stepDetails map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(stepDetailsJSON), &stepDetails); err != nil {
		return // Ignore parse errors, step details are optional
	}

	for name, details := range stepDetails {
		step, ok := slip.Steps[name]
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
