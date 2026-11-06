package argocdclient

import (
	"testing"
	"time"
)

const testBarrier = "example/barrier"

// Abort messages Argo Rollouts v1.10.0 writes (rollout/analysis.go:438, utils/conditions/conditions.go:156).
const (
	stepAnalysisAbortMessage = "RolloutAborted: Step-based analysis phase error/failed: Metric \"health\" assessed Error"
	backgroundAbortMessage   = "RolloutAborted: Background analysis phase error/failed: Metric \"compare\" assessed Failed"
	handAbortMessage         = "RolloutAborted: Rollout aborted update to revision 5"
	deadlineAbortMessage     = handAbortMessage + ": ReplicaSet \"api-6d4f\" has timed out progressing."
)

func weightStep(w int32) CanaryStep { return CanaryStep{SetWeight: w} }

// waitStep is a pause, analysis, experiment or plugin (barrier) step.
func waitStep() CanaryStep { return CanaryStep{Waits: true} }

// instantStep is a setCanaryScale or setHeaderRoute step.
func instantStep() CanaryStep { return CanaryStep{} }

// barrierRun is a Run entry of the barrier plugin at index, in phase, finished at finishedAt.
func barrierRun(index int32, phase StepPluginPhase, finishedAt *time.Time) StepPluginStatus {
	return StepPluginStatus{
		Index: index, Name: testBarrier, Operation: StepPluginOperationRun, Phase: phase, FinishedAt: finishedAt,
	}
}

// barrierFinished is when the first barrier finished in the pause cases.
var barrierFinished = time.Date(2026, 11, 6, 3, 9, 51, 0, time.UTC)

// afterFinish returns barrierFinished plus d.
func afterFinish(d time.Duration) *time.Time {
	t := barrierFinished.Add(d)
	return &t
}

// passed is a Successful barrier Run entry at index that finished at barrierFinished.
func passed(index int32) StepPluginStatus {
	return barrierRun(index, StepPluginPhaseSuccessful, afterFinish(0))
}

// Steps of a canary with barriers: start, setWeight 10, pause, w10, setWeight 25, pause, w25, setWeight 100.
func barrieredSteps() []CanaryStep {
	return []CanaryStep{
		waitStep(),
		weightStep(10), waitStep(), waitStep(),
		weightStep(25), waitStep(), waitStep(),
		weightStep(100),
	}
}

// Steps of a canary with a dark stage: start, setCanaryScale, setHeaderRoute, dark-ready, dark-passed,
// setHeaderRoute, setCanaryScale, setWeight 10, pause, w10, setWeight 100.
func darkStageSteps() []CanaryStep {
	return []CanaryStep{
		waitStep(), instantStep(), instantStep(),
		waitStep(), waitStep(),
		instantStep(), instantStep(),
		weightStep(10), waitStep(), waitStep(),
		weightStep(100),
	}
}

// start, setWeight 10, then a waiting step (a step analysis or a pause).
func waitAfter10() []CanaryStep { return []CanaryStep{waitStep(), weightStep(10), waitStep()} }

type evidenceCase struct {
	name     string
	evidence CanaryEvidence
	want     bool
}

func runEvidenceCases(t *testing.T, tests []evidenceCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.evidence.BarrierPlugin = testBarrier
			if got := MovedPastTrafficStep(tt.evidence); got != tt.want {
				t.Errorf("MovedPastTrafficStep() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMovedPastTrafficStep_RunEntriesAndIndex(t *testing.T) {
	runEvidenceCases(t, []evidenceCase{
		{name: "no steps", evidence: CanaryEvidence{
			StepPluginStatuses: []StepPluginStatus{passed(0)}, Message: stepAnalysisAbortMessage,
		}},
		{name: "aborted before the first barrier ran", evidence: CanaryEvidence{Steps: barrieredSteps()}},
		{name: "waiting at the first barrier", evidence: CanaryEvidence{
			Steps: barrieredSteps(), StepPluginStatuses: []StepPluginStatus{barrierRun(0, StepPluginPhaseRunning, nil)},
		}},
		{name: "a Failed barrier is not passed", evidence: CanaryEvidence{
			Steps: barrieredSteps(), StepPluginStatuses: []StepPluginStatus{barrierRun(0, StepPluginPhaseFailed, nil)},
		}},
		{name: "waiting inside the dark stage", evidence: CanaryEvidence{
			Steps: darkStageSteps(),
			StepPluginStatuses: []StepPluginStatus{
				passed(0), passed(3), barrierRun(4, StepPluginPhaseRunning, nil),
			},
		}},
		{name: "a Run entry past a setWeight, Running", want: true, evidence: CanaryEvidence{
			Steps:              barrieredSteps(),
			StepPluginStatuses: []StepPluginStatus{passed(0), barrierRun(3, StepPluginPhaseRunning, nil)},
		}},
		{name: "a Run entry past a setWeight, Failed", want: true, evidence: CanaryEvidence{
			Steps:              barrieredSteps(),
			StepPluginStatuses: []StepPluginStatus{passed(0), barrierRun(3, StepPluginPhaseFailed, nil)},
			Message:            handAbortMessage,
			CurrentStepIndex:   int32Ptr(0),
		}},
		{name: "a Run entry past a setWeight, after the dark stage", want: true, evidence: CanaryEvidence{
			Steps: darkStageSteps(),
			StepPluginStatuses: []StepPluginStatus{
				passed(0), passed(3), passed(4), barrierRun(9, StepPluginPhaseRunning, nil),
			},
		}},
		{name: "a setWeight before the last passed barrier", want: true, evidence: CanaryEvidence{
			Steps:              barrieredSteps(),
			StepPluginStatuses: []StepPluginStatus{passed(0), passed(3), barrierRun(6, StepPluginPhaseRunning, nil)},
		}},
		{name: "another plugin's Run entry is no evidence", evidence: CanaryEvidence{
			Steps: waitAfter10(), Message: handAbortMessage, CurrentStepIndex: int32Ptr(0),
			StepPluginStatuses: []StepPluginStatus{
				passed(
					0,
				),
				{Index: 2, Name: "other/plugin", Operation: StepPluginOperationRun, Phase: StepPluginPhaseRunning},
			},
		}},
		{name: "the barrier plugin's Abort entry is no Run entry", evidence: CanaryEvidence{
			Steps: waitAfter10(), Message: deadlineAbortMessage, CurrentStepIndex: int32Ptr(0),
			StepPluginStatuses: []StepPluginStatus{
				passed(
					0,
				),
				{Index: 2, Name: testBarrier, Operation: StepPluginOperationAbort, Phase: StepPluginPhaseSuccessful},
			},
		}},
		{name: "an index past the setWeight (read before the reset)", want: true, evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: handAbortMessage, CurrentStepIndex: int32Ptr(2),
		}},
		{name: "an index on the setWeight step itself", evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: handAbortMessage, CurrentStepIndex: int32Ptr(1),
		}},
	})
}

func TestMovedPastTrafficStep_StepAnalysisAbort(t *testing.T) {
	runEvidenceCases(t, []evidenceCase{
		{name: "after a setWeight", want: true, evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: stepAnalysisAbortMessage, CurrentStepIndex: int32Ptr(0),
		}},
		{name: "the highest passed barrier counts, not slice order", want: true, evidence: CanaryEvidence{
			Steps: darkStageSteps(), StepPluginStatuses: []StepPluginStatus{passed(4), passed(0)},
			Message: stepAnalysisAbortMessage,
		}},
		{name: "a waiting step before any setWeight ends the window", evidence: CanaryEvidence{
			Steps: []CanaryStep{
				waitStep(),
				waitStep(),
				weightStep(10),
			},
			StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message:            stepAnalysisAbortMessage,
			CurrentStepIndex:   int32Ptr(0),
		}},
		{name: "no setWeight after the barrier", evidence: CanaryEvidence{
			Steps: []CanaryStep{waitStep(), instantStep()}, StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: stepAnalysisAbortMessage, CurrentStepIndex: int32Ptr(0),
		}},
		{name: "before the first barrier passed", evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{barrierRun(0, StepPluginPhaseRunning, nil)},
			Message: stepAnalysisAbortMessage, CurrentStepIndex: int32Ptr(0),
		}},
		{name: "with no barriers, the window starts at the first step", want: true, evidence: CanaryEvidence{
			Steps: []CanaryStep{
				weightStep(10),
				waitStep(),
			},
			Message:          stepAnalysisAbortMessage,
			CurrentStepIndex: int32Ptr(0),
		}},
		{name: "a deadline abort is no step analysis abort", evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: deadlineAbortMessage, CurrentStepIndex: int32Ptr(0),
		}},
	})
}

func TestMovedPastTrafficStep_PauseAfterTheLastBarrier(t *testing.T) {
	runEvidenceCases(t, []evidenceCase{
		{name: "a hand abort paused after the barrier", want: true, evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: handAbortMessage, CurrentStepIndex: int32Ptr(0), PausedAt: afterFinish(114 * time.Second),
		}},
		{name: "a background analysis abort paused after the barrier", want: true, evidence: CanaryEvidence{
			Steps: barrieredSteps(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: backgroundAbortMessage, CurrentStepIndex: int32Ptr(0), PausedAt: afterFinish(time.Minute),
		}},
		{name: "a pause after a later barrier", want: true, evidence: CanaryEvidence{
			Steps:              darkStageSteps(),
			StepPluginStatuses: []StepPluginStatus{passed(4), barrierRun(0, StepPluginPhaseSuccessful, nil)},
			Message:            handAbortMessage,
			CurrentStepIndex:   int32Ptr(0),
			PausedAt:           afterFinish(time.Minute),
		}},
		{name: "a pause between two passed barriers counts only the last one's finish", evidence: CanaryEvidence{
			Steps: darkStageSteps(),
			StepPluginStatuses: []StepPluginStatus{
				passed(4), barrierRun(0, StepPluginPhaseSuccessful, afterFinish(-time.Hour)),
			},
			Message: handAbortMessage, CurrentStepIndex: int32Ptr(0), PausedAt: afterFinish(-30 * time.Minute),
		}},
		{name: "the pause began before the barrier finished", evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: handAbortMessage, CurrentStepIndex: int32Ptr(0), PausedAt: afterFinish(-time.Minute),
		}},
		{name: "the pause began in the second the barrier finished", evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: handAbortMessage, CurrentStepIndex: int32Ptr(0), PausedAt: afterFinish(0),
		}},
		{name: "the barrier has no finish time", evidence: CanaryEvidence{
			Steps:              waitAfter10(),
			StepPluginStatuses: []StepPluginStatus{barrierRun(0, StepPluginPhaseSuccessful, nil)},
			Message:            handAbortMessage, CurrentStepIndex: int32Ptr(0), PausedAt: afterFinish(time.Minute),
		}},
		{name: "a pause with no barrier", evidence: CanaryEvidence{
			Steps:   []CanaryStep{weightStep(10), waitStep()},
			Message: handAbortMessage, CurrentStepIndex: int32Ptr(0), PausedAt: afterFinish(time.Minute),
		}},
		{name: "a waiting step before any setWeight ends the window", evidence: CanaryEvidence{
			Steps: []CanaryStep{
				waitStep(),
				waitStep(),
				weightStep(10),
				waitStep(),
			},
			StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message:            handAbortMessage,
			CurrentStepIndex:   int32Ptr(0),
			PausedAt:           afterFinish(time.Minute),
		}},
		{name: "not paused", evidence: CanaryEvidence{
			Steps: waitAfter10(), StepPluginStatuses: []StepPluginStatus{passed(0)},
			Message: deadlineAbortMessage, CurrentStepIndex: int32Ptr(0),
		}},
	})
}
