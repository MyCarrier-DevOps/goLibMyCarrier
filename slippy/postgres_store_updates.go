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

// repaveableSlipStatusesSQL is the set of statuses Repave's guard treats as "ended" and
// therefore safe to supersede. Kept separate from the terminal-monotonicity guard's
// terminalStepStatusesSQL/nonTerminalStepStatusesSQL above (those are STEP statuses;
// this is the top-level SLIP status).
//
// This must always equal exactly the SlipStatus values for which IsLive() is false — the
// SQL guard below and the Go predicate are two independent encodings of the same "is this
// slip ended" decision. TestRepaveableSlipStatusesSQL_MatchesIsLive (in
// postgres_store_updates_test.go) parses this constant and asserts that equality across a
// hand-maintained list of SlipStatus values, not across the enum itself — what actually
// stops a new status slipping through unnoticed is the `exhaustive` linter, which fails
// IsTerminal's switch until the new constant is placed in one of its explicit case lists. If they ever drift (e.g. a ninth status added to IsTerminal/
// IsLive but not here), the guarded DELETE below stops matching a status CreateSlipForPush
// still treats as ended: RowsAffected() comes back 0, the existence check finds the row,
// and Repave returns ErrSlipWentLive for a slip that never actually went live — wedging
// every push for that commit onto the old, ended slip with no error surfaced anywhere
// (D2.3, DEVOPS-231 review). Update this constant AND the test's enumeration together with
// any change to SlipStatus.IsTerminal/IsLive.
const repaveableSlipStatusesSQL = "'failed','completed','abandoned','promoted','compensated'"

// Repave atomically replaces one commit's ended run with a fresh one. See SlipStore.Repave
// in interfaces.go for the contract; this comment covers the Postgres mechanics.
//
// Everything runs in a single transaction, which is the reason the method exists: the
// delete and the create used to be two store calls, so a create failure after a committed
// delete left the commit with no slip at all and every Kafka redelivery reproduced it (the
// producible case being a pipeline config deployed ahead of the migration that adds its
// step's column — slipColumns() then names a column that does not exist and the insert
// fails 42703 deterministically). Now that insert failure rolls the delete back.
//
// Statement order inside the transaction, and why each position is load-bearing:
//
//  1. Guarded DELETE of the superseded row (status IN the ended set). Runs first so the
//     went-live rejection happens before anything is written.
//  2. Read the superseded run's OWN parent link — before step 3 deletes it — so it can be
//     carried forward when the caller resolved no ancestry of its own.
//  3. Delete the superseded run's children explicitly (not via cascade FKs), so this is
//     correct both before and after migration v5's cascade FKs exist.
//  4. Insert the successor. This must precede step 5: the repoint names the successor's
//     correlation ID, so the row has to exist first — that is what keeps a descendant from
//     ever pointing at a phantom, and what lets Phase B add a foreign key on
//     slip_ancestry.parent_correlation_id at all.
//  5. Repoint descendants of the superseded run onto the successor, rewriting the whole
//     denormalized snapshot that describes the parent: id, repository, branch and status
//     now name the successor, and parent_failed_step is cleared. All three of id,
//     repository and branch are ResolveAncestry join keys — its next hop selects on
//     (repository, branch, correlation_id) using the values recorded beside the parent id,
//     and none of them is case-folded — so leaving any one describing the deleted run
//     truncates the walk at exactly the hop this repoint exists to preserve. That bites
//     for repository via casing (LoadByCommit matches lower(repository), so webhook
//     casing variance is real and expected) as well as via a genuine repo change.
//     parent_status is now
//     knowable (unlike under the old two-call sequence) precisely because step 4 already
//     inserted the successor in this same transaction.
//  6. Insert the successor's own parent link: the caller's, or the one carried forward at
//     step 2. This one statement runs inside a SAVEPOINT and is best-effort — a failure
//     rolls back only the link and lets the replacement commit, because failing the whole
//     repave over a lineage hop would permanently block CI for the commit. See
//     insertAncestryLinkBestEffort.
//
// Steps 2, 3 and 5 are reached only when step 1 reported RowsAffected() > 0 — i.e. only
// when this call itself removed the row. A repave whose superseded row was already gone
// still creates the successor (so redelivery converges) but rewrites nothing else, so it
// can never reassign an unrelated descendant's parent (D2.1, DEVOPS-231 review).
func (s *PostgresStore) Repave(
	ctx context.Context,
	oldCorrelationID string,
	newSlip *Slip,
	parent *AncestryEntry,
) error {
	if newSlip == nil {
		return fmt.Errorf("%w: Repave requires a successor slip", ErrInvalidConfiguration)
	}
	if oldCorrelationID == newSlip.CorrelationID {
		// Self-repave: nothing distinguishes "replaced" from "untouched" afterwards, so
		// this can only destroy data silently. Without the guard the transaction runs to
		// completion with no error — delete the row, delete its children, re-insert it
		// fresh — leaving an ended run's state history and component rows replaced by a
		// pending run under the SAME correlation ID, with a success log whose repaved_id
		// and superseding_id are identical and therefore indistinguishable from a no-op.
		//
		// It is reachable: a caller that retries within one delivery reuses its
		// correlation ID, so a retry after a partially-observed failure can present the
		// same id on both sides. Rejecting is strictly better than the alternative of
		// treating it as a no-op, because a caller in this state has a bug worth seeing.
		return fmt.Errorf("%w: Repave successor %s is the slip being repaved",
			ErrInvalidConfiguration, newSlip.CorrelationID)
	}

	return s.inTx(ctx, func(tx pgx.Tx) error {
		// Read the superseded run's own ancestry link BEFORE the guarded DELETE below, not
		// after. This ordering is required by Phase B and is invisible without it.
		//
		// Phase B adds fk_ancestry_slip (correlation_id) REFERENCES routing_slips
		// ON DELETE CASCADE. A cascade is an AFTER ROW trigger, so for a non-deferrable
		// constraint it fires at end of statement: the moment the guarded DELETE of the
		// routing_slips row completes, this run's slip_ancestry rows are gone — and gone to
		// every later statement in this same transaction. Read after the delete and the
		// lookup returns nothing, with no error and no warning.
		//
		// The consequence lands where nobody would see it. The carry-forward only runs when
		// the caller resolved no ancestry of its own (a GitHub outage), so the lineage hop
		// would be destroyed in exactly the degraded case the mechanism exists for, and
		// never in the healthy case. Nor would the suite catch it: CI migrates to v4 and the
		// FK arrives in v5, so everything stays green until the migration ships.
		// TestPostgresStore_Repave_CarriesForwardParentLinkUnderCascadeFK_Integration
		// installs that FK itself so the ordering is pinned now rather than on trust.
		//
		// Reading before the DELETE means reading without the row lock the DELETE takes,
		// and that introduces no new race: two concurrent repaves of the same old ID read
		// the same link and carry the same value forward (idempotent), and the guarded
		// DELETE still decides which one wins. The only cost is one wasted round-trip on
		// the paths where the delete then finds nothing.
		var carried AncestryEntry
		var carriedFound bool
		if parent == nil {
			var carryErr error
			carried, carriedFound, carryErr = loadOwnAncestryLinkTx(ctx, tx, oldCorrelationID)
			if carryErr != nil {
				return fmt.Errorf("repave %s: reading superseded ancestry link: %w",
					oldCorrelationID, carryErr)
			}
		}

		removedOld, err := removeSupersededSlipTx(ctx, tx, oldCorrelationID)
		if err != nil {
			return err
		}

		link := parent
		if removedOld {
			if link == nil && carriedFound {
				// The caller resolved no ancestry (e.g. a GitHub outage). Carry the
				// superseded run's own parent link forward rather than destroying the
				// lineage hop along with its row. Gated on removedOld so a repave that
				// found no old row to replace does not invent a parent for the successor.
				link = &carried
			}

			for _, stmt := range []string{
				"DELETE FROM slip_component_states WHERE correlation_id = $1",
				"DELETE FROM slip_ancestry WHERE correlation_id = $1",
			} {
				if _, err := tx.Exec(ctx, stmt, oldCorrelationID); err != nil {
					return fmt.Errorf("repave %s: deleting superseded children: %w", oldCorrelationID, err)
				}
			}
		}

		// The successor's row must exist before anything is pointed at it.
		if err := s.createTx(ctx, tx, newSlip); err != nil {
			return err
		}

		if removedOld {
			if _, err := tx.Exec(ctx,
				"UPDATE slip_ancestry SET parent_correlation_id = $1, parent_repository = $2, "+
					"parent_branch = $3, parent_status = $4, parent_failed_step = '' "+
					"WHERE parent_correlation_id = $5",
				newSlip.CorrelationID, newSlip.Repository, newSlip.Branch,
				string(newSlip.Status), oldCorrelationID,
			); err != nil {
				return fmt.Errorf("repave %s: repointing descendants to %s: %w",
					oldCorrelationID, newSlip.CorrelationID, err)
			}
		}

		if removedOld {
			// Record on the successor that it replaced a predecessor. Without this the
			// successor row carries no evidence a prior run ever existed for this commit:
			// the old row and its children are gone, and the only other link
			// (supersededBy) lives in spans and log lines, not on any row. One history
			// entry inside this same transaction makes the replacement visible to anyone
			// reading the slip later, and costs one UPDATE on a row already locked here.
			//
			// This is NOT the history-preservation decision (DEVOPS-277) — the prior run's
			// own state history is still destroyed. It only records that it happened.
			if err := appendHistoryTx(ctx, tx, newSlip.CorrelationID, StateHistoryEntry{
				Step:      "push_parsed",
				Status:    StepStatusRunning,
				Timestamp: time.Now(),
				// "slippy-library" matches every other library-emitted history entry
				// (history.go, push.go, steps.go). It was "slippy" here, which made this
				// entry distinguishable by Actor purely by accident — worth avoiding,
				// because a consumer would then be relying on a typo. A repave is
				// identified by the Message, as the integration test does.
				Actor: "slippy-library",
				Message: fmt.Sprintf("repaved %s for commit %s", oldCorrelationID,
					shortSHA(newSlip.CommitSHA)),
			}); err != nil {
				return fmt.Errorf("repave %s: recording predecessor on successor: %w",
					oldCorrelationID, err)
			}
		}

		if link != nil {
			if err := s.insertAncestryLinkBestEffort(ctx, tx, newSlip, *link); err != nil {
				return err
			}
		}
		return nil
	})
}

// insertAncestryLinkBestEffort writes the successor's parent link inside a SAVEPOINT, so a
// failure to write it rolls back only that statement and lets the replacement itself
// commit. It returns an error only when the savepoint machinery itself fails.
//
// Why the link is not allowed to veto the replacement: it is the least important write in
// the transaction, and failing it here would permanently block CI for the commit. The
// failure is not transient — every redelivery meets the same superseded row and the same
// failing insert — while slip_ancestry's only reader (Client.ResolveAncestry) has no
// non-test caller in this repo today. Blocking a pipeline on a lineage hop nothing reads is
// the wrong trade, and it would also make the repave path stricter than the fresh-create
// path, where writeAncestryLink already treats the identical failure as a warning.
//
// The delete/create atomicity that motivates Repave is untouched: the savepoint scopes the
// relaxation to this one statement.
func (s *PostgresStore) insertAncestryLinkBestEffort(
	ctx context.Context,
	tx pgx.Tx,
	slip *Slip,
	parent AncestryEntry,
) error {
	sp, spErr := tx.Begin(ctx)
	if spErr != nil {
		return fmt.Errorf("repave %s: open ancestry-link savepoint: %w", slip.CorrelationID, spErr)
	}

	if linkErr := insertAncestryLinkTx(ctx, sp, slip, parent); linkErr != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return fmt.Errorf("repave %s: roll back ancestry-link savepoint: %w",
				slip.CorrelationID, rbErr)
		}
		s.logger.Warn(ctx, "Repave committed without the successor's ancestry link",
			map[string]interface{}{
				"correlation_id": slip.CorrelationID,
				"parent_id":      parent.CorrelationID,
				"error":          linkErr.Error(),
			})
		return nil
	}

	if commitErr := sp.Commit(ctx); commitErr != nil {
		return fmt.Errorf("repave %s: release ancestry-link savepoint: %w",
			slip.CorrelationID, commitErr)
	}
	return nil
}

// removeSupersededSlipTx runs Repave's guarded delete of the superseded row and reports
// whether it removed it. A false return means the row was already gone, which is not an
// error: the caller still creates the successor, but must skip every statement that is
// only licensed by having removed the row itself.
//
// Returns ErrSlipWentLive when the row is still present but no longer ended — the repave
// decision is stale, and rolling back leaves both the live run and the absent successor
// exactly as the caller found them.
func removeSupersededSlipTx(ctx context.Context, tx pgx.Tx, correlationID string) (bool, error) {
	tag, err := tx.Exec(ctx,
		"DELETE FROM routing_slips WHERE correlation_id = $1 AND status IN ("+repaveableSlipStatusesSQL+")",
		correlationID,
	)
	if err != nil {
		return false, fmt.Errorf("repave %s: deleting superseded row: %w", correlationID, err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}

	// Nothing matched: either the guard rejected the delete (row present, status no longer
	// ended) or the row was already gone. Distinguish them, because one is a rejection and
	// the other is a legitimate idempotent path.
	var id string
	checkErr := tx.QueryRow(ctx,
		"SELECT correlation_id FROM routing_slips WHERE correlation_id = $1", correlationID,
	).Scan(&id)
	switch {
	case checkErr == nil:
		return false, fmt.Errorf("repave %s: %w", correlationID, ErrSlipWentLive)
	case isNoRows(checkErr):
		return false, nil
	default:
		return false, fmt.Errorf("repave %s: checking superseded row: %w", correlationID, checkErr)
	}
}

// loadOwnAncestryLinkTx reads the direct-parent link belonging to correlationID. Used by
// Repave to carry a superseded run's lineage hop forward to its successor.
//
// found distinguishes "this run has no parent link" (found=false, a normal state for a
// root commit) from a read failure, so the caller can treat only the latter as fatal.
//
// LIMIT 1 with no ORDER BY is deliberate. slip_ancestry's primary key is (repository,
// branch, correlation_id), so a single correlation ID could in principle carry more than one
// row — but a run's (repository, branch) never changes after creation, so in practice there
// is exactly one link row per correlation_id and there is no tiebreak to make.
//
// Specifically NOT ordered by created_at: that column holds the ANCESTOR's creation time
// (ancestryLinkArgs binds parent.CreatedAt, and the upsert re-stamps it), not the link's
// write time, so it cannot express link recency. Ordering by it would install an invariant
// the schema does not support. Ordering properly would need a real linked_at column and a
// new versioned migration, which this change deliberately does not ship.
func loadOwnAncestryLinkTx(
	ctx context.Context,
	tx pgx.Tx,
	correlationID string,
) (entry AncestryEntry, found bool, err error) {
	var status string
	err = tx.QueryRow(ctx,
		"SELECT parent_correlation_id, parent_commit_sha, parent_status, parent_failed_step, "+
			"parent_repository, parent_branch, created_at FROM slip_ancestry "+
			"WHERE correlation_id = $1 LIMIT 1",
		correlationID,
	).Scan(
		&entry.CorrelationID, &entry.CommitSHA, &status, &entry.FailedStep,
		&entry.Repository, &entry.Branch, &entry.CreatedAt,
	)
	switch {
	case err == nil:
		entry.Status = SlipStatus(status)
		return entry, true, nil
	case isNoRows(err):
		return AncestryEntry{}, false, nil
	default:
		return AncestryEntry{}, false, err
	}
}
