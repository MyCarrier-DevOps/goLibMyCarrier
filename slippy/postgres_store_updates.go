package slippy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Now that every SlipStore method is implemented, assert conformance at compile time.
var _ SlipStore = (*PostgresStore)(nil)

// terminalStepStatusesSQL / nonTerminalStepStatusesSQL are the step_status values used by
// the terminal-monotonicity guard. Kept in sync with StepStatus.IsTerminal.
const (
	terminalStepStatusesSQL    = "'completed','failed','error','aborted','timeout','skipped'"
	nonTerminalStepStatusesSQL = "'pending','held','running'"
)

// UpdateStep updates a step's status. Component-level updates (componentName != "") and
// aggregate steps roll up into the aggregate columns; pure pipeline steps write their
// status column directly. Every update is guarded by terminal-monotonicity.
func (s *PostgresStore) UpdateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
) error {
	return s.updateStepTx(ctx, correlationID, stepName, componentName, status, nil)
}

// UpdateComponentStatus updates a component's step status. Thin alias for UpdateStep.
func (s *PostgresStore) UpdateComponentStatus(
	ctx context.Context,
	correlationID, componentName, stepType string,
	status StepStatus,
) error {
	return s.UpdateStep(ctx, correlationID, stepType, componentName, status)
}

// UpdateStepWithHistory updates a step's status and appends a history entry in one
// transaction. Unlike ClickHouse, the status write and the audit entry are atomic — there
// is no best-effort write-back that can silently drop the history entry.
func (s *PostgresStore) UpdateStepWithHistory(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
	entry StateHistoryEntry,
) error {
	return s.updateStepTx(ctx, correlationID, stepName, componentName, status, &entry)
}

// AppendHistory appends a state-history entry to the slip.
func (s *PostgresStore) AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockSlip(ctx, tx, correlationID); err != nil {
			return err
		}
		return appendHistoryTx(ctx, tx, correlationID, entry)
	})
}

// UpdateSlipStatus atomically updates the slip's top-level status column.
func (s *PostgresStore) UpdateSlipStatus(ctx context.Context, correlationID string, newStatus SlipStatus) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE routing_slips SET status = $1, updated_at = now() WHERE correlation_id = $2",
		string(newStatus), correlationID)
	if err != nil {
		return fmt.Errorf("failed to update status for %s: %w", correlationID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSlipNotFound
	}
	return nil
}

// SetComponentImageTag records the built image tag for a component, preserving its current
// status, then refreshes the aggregate column so the tag surfaces on the slip.
func (s *PostgresStore) SetComponentImageTag(
	ctx context.Context,
	correlationID, stepName, componentName, imageTag string,
) error {
	// The event log stores rows under the component step type (e.g. "component_builds");
	// callers may pass the aggregate step name (e.g. "builds"). Normalize for the lookup.
	dbStep := stepName
	if s.config.IsAggregateStep(stepName) {
		if componentStep := s.config.GetComponentStep(stepName); componentStep != "" {
			dbStep = componentStep
		}
	}

	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockSlip(ctx, tx, correlationID); err != nil {
			return err
		}

		const q = "UPDATE slip_component_states SET image_tag = $1, updated_at = now() " +
			"WHERE correlation_id = $2 AND step = $3 AND component = $4"
		tag, err := tx.Exec(ctx, q, imageTag, correlationID, dbStep, componentName)
		if err != nil {
			return fmt.Errorf("failed to set image tag for %s/%s: %w", componentName, dbStep, err)
		}
		// Fall back to the caller's original step name if the normalized one matched nothing.
		if tag.RowsAffected() == 0 && dbStep != stepName {
			tag, err = tx.Exec(ctx, q, imageTag, correlationID, stepName, componentName)
			if err != nil {
				return fmt.Errorf("failed to set image tag for %s/%s: %w", componentName, stepName, err)
			}
			dbStep = stepName
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("component %s not found in event log for step %s", componentName, dbStep)
		}

		if aggStep := s.resolveAggregateStep(dbStep); aggStep != "" {
			return s.recomputeAggregate(ctx, tx, correlationID, aggStep)
		}
		return nil
	})
}

// updateStepTx performs a step update (optionally with a history entry) inside one
// transaction: lock the slip, upsert the component-state row under the terminal guard,
// then either recompute the affected aggregate or write the pipeline step's status column.
func (s *PostgresStore) updateStepTx(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
	entry *StateHistoryEntry,
) error {
	message := ""
	if entry != nil {
		message = entry.Message
	}

	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockSlip(ctx, tx, correlationID); err != nil {
			return err
		}

		applied, err := s.upsertComponentState(ctx, tx, correlationID, stepName, componentName, status, message, "")
		if err != nil {
			return err
		}
		if !applied {
			// The freshness guard rejected a non-terminal status overwriting a still-fresh
			// terminal one (a stale duplicate/redelivery within the freshness window).
			return ErrTerminalAlreadyExists
		}

		if componentName != "" || s.config.IsAggregateStep(stepName) {
			if aggStep := s.resolveAggregateStep(stepName); aggStep != "" {
				if err := s.recomputeAggregate(ctx, tx, correlationID, aggStep); err != nil {
					return err
				}
			}
		} else if s.config.GetStep(stepName) != nil && safeStepNameForDerivePattern.MatchString(stepName) {
			// Pure pipeline step: the status column on routing_slips is authoritative.
			// stepName arrives from unvalidated HTTP/CI input through the SlipStore
			// interface, and is interpolated into the column identifier — so it is spliced
			// in ONLY after confirming it is a configured step AND a bare identifier
			// (^[A-Za-z0-9_]+$), which blocks SQL identifier injection. An unknown or
			// unsafe step name skips this column write; its slip_component_states event was
			// already recorded above, matching ClickHouse (which materializes only
			// config-known columns rather than erroring on unknown steps).
			col := stepName + "_status"
			upd := fmt.Sprintf("UPDATE routing_slips SET %s = $1, updated_at = now() WHERE correlation_id = $2", col)
			if _, err := tx.Exec(ctx, upd, string(status), correlationID); err != nil {
				return fmt.Errorf("failed to update step %s: %w", stepName, err)
			}
		}

		if entry != nil {
			return appendHistoryTx(ctx, tx, correlationID, *entry)
		}
		return nil
	})
}

// upsertComponentState writes the current state for one (step, component), returning
// whether the write was applied. It reports false (applied=false, err=nil) only when the
// I5 terminal-freshness guard rejected the write.
//
// The guard ports the ClickHouse enforceTerminalFreshnessGate semantics (same SLIPPY_I5_*
// knobs) into the upsert's WHERE clause: a non-terminal status may not overwrite a terminal
// one for the same step/component, but ONLY while the terminal is younger than the freshness
// window (default 750ms) — the window that discriminates a stale duplicate/redelivery
// (reject) from a genuine re-run written much later (allow). Additionally allowed: the gate
// being disabled (SLIPPY_I5_GATE_ENABLED=false), a bypass step's pipeline-level write
// (push_parsed, so push-webhook retries can reset it), an incoming terminal status, and the
// aborted->pending cascade-reset. The age is computed entirely on the Postgres server clock
// (now() vs the stored updated_at, both this one server), so it is immune to clock skew.
// A non-empty image_tag is preserved when the incoming one is empty.
func (s *PostgresStore) upsertComponentState(
	ctx context.Context,
	tx pgx.Tx,
	correlationID, step, component string,
	status StepStatus,
	message, imageTag string,
) (bool, error) {
	q := fmt.Sprintf(`
		INSERT INTO slip_component_states (correlation_id, step, component, status, message, image_tag, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (correlation_id, step, component) DO UPDATE SET
			status     = EXCLUDED.status,
			message    = EXCLUDED.message,
			image_tag  = CASE WHEN EXCLUDED.image_tag <> '' THEN EXCLUDED.image_tag
			                  ELSE slip_component_states.image_tag END,
			updated_at = now()
		WHERE NOT $8::boolean
		   OR NOT (slip_component_states.status IN (%s)
		           AND EXCLUDED.status IN (%s)
		           AND now() - slip_component_states.updated_at <= $7 * interval '1 millisecond')
		   OR (slip_component_states.status = 'aborted' AND EXCLUDED.status = 'pending')`,
		terminalStepStatusesSQL, nonTerminalStepStatusesSQL)

	// guarded is false when the gate is disabled or this is a bypass step's pipeline-level
	// write; then NOT $8 makes the WHERE always true (a plain upsert). Otherwise the terminal
	// guard applies within the freshness window.
	window := freshnessWindow()
	if window > maxFreshnessWindowMS*time.Millisecond {
		window = maxFreshnessWindowMS * time.Millisecond
	}
	guarded := gateEnabled() && (!gateBypassSteps[step] || component != "")

	tag, err := tx.Exec(ctx, q, correlationID, step, component, string(status), message, imageTag,
		window.Milliseconds(), guarded)
	if err != nil {
		return false, fmt.Errorf("failed to record component state %s/%s: %w", step, component, err)
	}
	// A pure INSERT or a guard-passing DO UPDATE affects one row; a guard-blocked conflict
	// affects zero.
	return tag.RowsAffected() > 0, nil
}

// recomputeAggregate rebuilds the aggregate column for aggStep from the component-state
// rows, merging into the existing items (preserving StartedAt across transitions) and
// recomputing the status column over the active components. The caller must already hold
// the slip's row lock.
func (s *PostgresStore) recomputeAggregate(ctx context.Context, tx pgx.Tx, correlationID, aggStep string) error {
	var itemsBytes []byte
	sel := fmt.Sprintf("SELECT %s FROM routing_slips WHERE correlation_id = $1", aggStep)
	if err := tx.QueryRow(ctx, sel, correlationID).Scan(&itemsBytes); err != nil {
		if isNoRows(err) {
			return ErrSlipNotFound
		}
		return fmt.Errorf("failed to read aggregate %s: %w", aggStep, err)
	}

	var wrapper struct {
		Items []ComponentStepData `json:"items"`
	}
	if len(itemsBytes) > 0 {
		if err := json.Unmarshal(itemsBytes, &wrapper); err != nil {
			wrapper.Items = nil
		}
	}
	items := wrapper.Items
	byName := make(map[string]int, len(items))
	for i := range items {
		byName[items[i].Component] = i
	}

	rows, err := tx.Query(ctx,
		"SELECT component, status, message, image_tag, updated_at FROM slip_component_states "+
			"WHERE correlation_id = $1 AND step = ANY($2) AND component <> ''",
		correlationID, s.aggregateStepAliases(aggStep))
	if err != nil {
		return fmt.Errorf("failed to read component states for %s: %w", aggStep, err)
	}
	defer rows.Close()

	var active []ComponentStepData
	for rows.Next() {
		var row componentStateRow
		if err := rows.Scan(&row.Component, &row.Status, &row.Message, &row.ImageTag, &row.Timestamp); err != nil {
			return fmt.Errorf("failed to scan component state: %w", err)
		}
		cd := buildComponentData(row.Component, row)
		active = append(active, cd)
		if idx, ok := byName[row.Component]; ok {
			updateExistingComponent(&items[idx], cd)
		} else {
			items = append(items, cd)
			byName[row.Component] = len(items) - 1
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate component states for %s: %w", aggStep, err)
	}

	// No component has reported yet (e.g. a pipeline-level StartStep on the aggregate step
	// before its components exist). Aggregating over an empty set would vacuously resolve to
	// "completed" and could mis-gate a downstream prerequisite, so leave the aggregate at its
	// current value. Matches the ClickHouse store, which only aggregates a non-empty set.
	if len(active) == 0 {
		return nil
	}

	// Status is computed over the active components only (excludes any config placeholders
	// left in the items list), matching filterActiveComponents in the ClickHouse store.
	status := computeAggregateStatus(active)

	itemsJSON, err := json.Marshal(struct {
		Items []ComponentStepData `json:"items"`
	}{Items: items})
	if err != nil {
		return fmt.Errorf("failed to marshal aggregate %s: %w", aggStep, err)
	}

	upd := fmt.Sprintf(
		"UPDATE routing_slips SET %s_status = $1, %s = $2, updated_at = now() WHERE correlation_id = $3",
		aggStep, aggStep)
	if _, err := tx.Exec(ctx, upd, string(status), string(itemsJSON), correlationID); err != nil {
		return fmt.Errorf("failed to write aggregate %s: %w", aggStep, err)
	}
	return nil
}

// resolveAggregateStep maps a step name to its aggregate step: the aggregate a component
// step rolls into, or the step itself if it is the aggregate. Returns "" if neither.
func (s *PostgresStore) resolveAggregateStep(stepName string) string {
	if agg := s.config.GetAggregateStep(stepName); agg != "" {
		return agg
	}
	if s.config.IsAggregateStep(stepName) {
		return stepName
	}
	return ""
}

// aggregateStepAliases returns the DB step names whose component rows roll into aggStep:
// the aggregate step name itself and its configured component step type.
func (s *PostgresStore) aggregateStepAliases(aggStep string) []string {
	aliases := []string{aggStep}
	if componentStep := s.config.GetComponentStep(aggStep); componentStep != "" && componentStep != aggStep {
		aliases = append(aliases, componentStep)
	}
	return aliases
}

// inTx runs fn inside a transaction, committing on success and rolling back on error.
func (s *PostgresStore) inTx(ctx context.Context, fn func(pgx.Tx) error) (err error) {
	tx, beginErr := s.pool.Begin(ctx)
	if beginErr != nil {
		return fmt.Errorf("begin transaction: %w", beginErr)
	}
	// Roll back on any early exit — including a panic in fn — so the transaction's pooled
	// connection is always released. Skipped after a successful commit; a genuine rollback
	// failure is surfaced (via the named return) only when no earlier error already is.
	committed := false
	defer func() {
		if committed {
			return
		}
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) && err == nil {
			err = fmt.Errorf("rollback: %w", rbErr)
		}
	}()

	if fnErr := fn(tx); fnErr != nil {
		return fnErr
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return fmt.Errorf("commit transaction: %w", commitErr)
	}
	committed = true
	return nil
}

// lockSlip takes a row lock on the slip (serializing concurrent updates) and confirms it
// exists, returning ErrSlipNotFound otherwise.
//
// Ordering contract: a step/component update requires the slip's routing_slips row to be
// already committed by Create. This is a deliberate change from the ClickHouse store, which
// recorded the component event durably even before the slip existed and retried slip-creation
// with backoff for ~30 minutes to absorb a producer/consumer race (a CI StartStep arriving
// before pushhookparser's Create). Postgres Create is synchronous and committed, so that race
// is rare; when it does occur the update returns ErrSlipNotFound and the caller relies on
// Kafka at-least-once redelivery to retry once Create has landed, rather than the store
// buffering the event. Producers must therefore create the slip before emitting step updates.
func lockSlip(ctx context.Context, tx pgx.Tx, correlationID string) error {
	var id string
	err := tx.QueryRow(ctx,
		"SELECT correlation_id FROM routing_slips WHERE correlation_id = $1 FOR UPDATE",
		correlationID).Scan(&id)
	if err != nil {
		if isNoRows(err) {
			return ErrSlipNotFound
		}
		return fmt.Errorf("failed to lock slip %s: %w", correlationID, err)
	}
	return nil
}

// appendHistoryTx appends one entry to routing_slips.state_history within a transaction.
func appendHistoryTx(ctx context.Context, tx pgx.Tx, correlationID string, entry StateHistoryEntry) error {
	entryJSON, err := json.Marshal([]StateHistoryEntry{entry})
	if err != nil {
		return fmt.Errorf("failed to marshal history entry: %w", err)
	}
	// nullif guards against a JSON-null entries value (e.g. a row copied from ClickHouse as
	// {"entries":null}); coalesce then also covers a missing key. Either way we append onto
	// a real JSON array.
	const q = `
		UPDATE routing_slips SET
			state_history = jsonb_set(
				coalesce(state_history, '{"entries":[]}'::jsonb), '{entries}',
				coalesce(nullif(state_history -> 'entries', 'null'::jsonb), '[]'::jsonb) || $1::jsonb
			),
			updated_at = now()
		WHERE correlation_id = $2`
	tag, err := tx.Exec(ctx, q, string(entryJSON), correlationID)
	if err != nil {
		return fmt.Errorf("failed to append history for %s: %w", correlationID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSlipNotFound
	}
	return nil
}
