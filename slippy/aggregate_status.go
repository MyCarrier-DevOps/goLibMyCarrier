package slippy

// computeAggregateStatus determines an aggregate step's status from its component statuses,
// and is the single source of truth shared by both the ClickHouse and Postgres stores (so the
// two backends cannot drift). The aggregate is:
//   - "failed"    if any component has failed
//   - "completed" if all components are completed (or skipped/other success)
//   - "running"   if any component is running OR already completed (work is in progress)
//   - "pending"   if all components are pending (no work has started)
//
// Empty input yields "completed" (a vacuous "all completed"); callers that must not resolve a
// component-less aggregate to completed guard len==0 first (see recomputeAggregate).
func computeAggregateStatus(components []ComponentStepData) StepStatus {
	allCompleted := true
	anyRunning := false
	anyCompleted := false
	anyFailed := false

	for _, comp := range components {
		if comp.Status.IsFailure() {
			anyFailed = true
		}
		if comp.Status.IsSuccess() {
			anyCompleted = true
		} else {
			allCompleted = false
		}
		if comp.Status.IsRunning() {
			anyRunning = true
		}
	}

	switch {
	case anyFailed:
		return StepStatusFailed
	case allCompleted:
		return StepStatusCompleted
	case anyRunning || anyCompleted:
		return StepStatusRunning
	default:
		return StepStatusPending
	}
}
