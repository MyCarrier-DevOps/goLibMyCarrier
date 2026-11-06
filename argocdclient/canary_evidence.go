package argocdclient

import (
	"strings"
	"time"
)

// stepAnalysisAbort opens the abort message of a failed or errored step analysis (Argo Rollouts v1.10.0
// rollout/analysis.go:438): only a step analysis that was the current step aborts with it.
const stepAnalysisAbort = "Step-based analysis phase error/failed"

// CanaryEvidence is what an aborted Rollout's status still shows of its canary steps. A RolloutStatus fills it
// from CanarySteps, StepPluginStatuses, CurrentStepIndex, Message and PausedAt.
type CanaryEvidence struct {
	// Steps is spec.strategy.canary.steps, by step index.
	Steps []CanaryStep
	// BarrierPlugin names the step plugin whose steps are barriers: its Run entry turns Successful only when the
	// Rollout may go on. Other plugins' entries are no evidence.
	BarrierPlugin string
	// StepPluginStatuses is status.canary.stepPluginStatuses.
	StepPluginStatuses []StepPluginStatus
	// CurrentStepIndex is status.currentStepIndex; nil when unset.
	CurrentStepIndex *int32
	// Message is the abort message: the Rollout's status message, or the ArgoCD health message, which carries it.
	Message string
	// PausedAt is the lastTransitionTime of the Paused condition while it is True; nil otherwise (see
	// RolloutStatus.PausedAt).
	PausedAt *time.Time
}

// MovedPastTrafficStep reports whether e shows that the Rollout moved past a setWeight step above 0, which is
// evidence that its canary took traffic before the abort.
//
// The controller completes at most one step per reconcile, and a setWeight step completes only once the canary pods
// are available and the router weight is verified, so a setWeight at index i counts when:
//   - i is below the index of one of BarrierPlugin's Run entries, in any phase: the controller writes a plugin
//     step's Run entry only once that step is current;
//   - i is below CurrentStepIndex. The controller resets the index to 0 in the reconcile that acts on an abort,
//     and at a pause step it persists that reset before anything else runs (rollout/canary.go:94-105, 135-174,
//     394-402), so this term covers only a read between the abort and that reconcile, or an abort on a step that is
//     neither a pause nor a plugin step (a step analysis, an experiment, or a setCanaryScale after the setWeight);
//   - i lies after the last passed barrier (the highest index of a Successful Run entry; from the first step when
//     none passed), at or before the first waiting step after it, and either the abort is a step analysis abort, or
//     the Paused condition turned True strictly after that barrier finished (never without a passed barrier). A
//     failed step analysis and a pause both happen at or after that waiting step, so the Rollout moved past every
//     step before it. The Paused condition keeps its time through the abort (rollout/sync.go:510), so this covers
//     a hand, cascaded or background-analysis abort at a pause. When step 0 is a barrier, which waits, a Rollout
//     that passed none gets no credit.
//
// The trade-offs:
//   - Underclaim (false after traffic): an abort on the setWeight step, or after it without a pause, step analysis
//     or later Run entry that the status still shows. Crediting the current setWeight would count a canary whose
//     pods never became available. A pause that began in the second the barrier finished gets no credit either:
//     the times have one-second resolution and the comparison is strict.
//   - Overclaim (true without traffic): a pause that is not a pause step also sets the Paused condition. A person's
//     pause (spec.paused) or an inconclusive background analysis while the Rollout still waits on the setWeight
//     step, followed by an abort, reads as traffic even if the canary pods never became available.
//   - Overclaim: a person's skip of the setWeight step (kubectl argo rollouts promote or the Rollouts dashboard's
//     Promote while no pause condition is set, the deprecated --skip-current-step, Argo CD's skip-current-step
//     action). The index moves past the setWeight without completing it, and any of the evidence above can then
//     credit it. The status keeps no trace of the skip.
func MovedPastTrafficStep(e CanaryEvidence) bool {
	last, reached := int32(-1), int32(-1)
	finished := map[int32]*time.Time{} // a passed barrier's finishedAt, by step index
	if e.CurrentStepIndex != nil {
		reached = *e.CurrentStepIndex
	}
	for _, p := range e.StepPluginStatuses {
		if p.Name != e.BarrierPlugin || p.Operation != StepPluginOperationRun {
			continue
		}
		reached = max(reached, p.Index)
		if p.Phase == StepPluginPhaseSuccessful {
			last, finished[p.Index] = max(last, p.Index), p.FinishedAt
		}
	}
	for i, step := range e.Steps {
		if step.SetWeight > 0 && int32(i) < reached {
			return true
		}
	}
	if !strings.Contains(e.Message, stepAnalysisAbort) && !pausedAfter(e.PausedAt, finished[last]) {
		return false
	}
	for i := int(last) + 1; i < len(e.Steps); i++ {
		if e.Steps[i].SetWeight > 0 {
			return true
		}
		if e.Steps[i].Waits {
			return false
		}
	}
	return false
}

// pausedAfter reports whether a Rollout paused at pausedAt, strictly after a step finished at finishedAt.
func pausedAfter(pausedAt, finishedAt *time.Time) bool {
	return pausedAt != nil && finishedAt != nil && pausedAt.After(*finishedAt)
}
