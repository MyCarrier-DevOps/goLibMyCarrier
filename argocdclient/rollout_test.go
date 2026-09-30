package argocdclient

import (
	"reflect"
	"testing"
)

func mustDecodeRollout(t *testing.T, manifest string) liveRollout {
	t.Helper()
	live, err := decodeRollout(manifest)
	if err != nil {
		t.Fatalf("decodeRollout: %v", err)
	}
	return live
}

func int32Ptr(v int32) *int32 { return &v }

func TestCanaryWeight(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     int32
	}{
		{
			name: "aborted with weights present is zero",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"abort":true,"currentStepIndex":1,"canary":{"weights":{"canary":{"weight":50}}}}}`,
			want: 0,
		},
		{
			name: "fully promoted with routing ignores reset weights",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"currentStepIndex":2,"canary":{"weights":{"canary":{"weight":0},"stable":{"weight":100}}}}}`,
			want: 100,
		},
		{
			name: "fully promoted honors maxTrafficWeight",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{"maxTrafficWeight":1000},
				"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"currentStepIndex":2,"canary":{"weights":{"canary":{"weight":0}}}}}`,
			want: 1000,
		},
		{
			name:     "canary without steps is fully promoted",
			manifest: `{"spec":{"strategy":{"canary":{}}},"status":{}}`,
			want:     100,
		},
		{
			name:     "non canary strategy is fully promoted",
			manifest: `{"spec":{"strategy":{"blueGreen":{}}},"status":{}}`,
			want:     100,
		},
		{
			name: "routing weights win at a current step",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"currentStepIndex":1,"canary":{"weights":{"canary":{"weight":50}}}}}`,
			want: 50,
		},
		{
			name: "routing without weights falls back to steps",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"currentStepIndex":1}}`,
			want: 50,
		},
		{
			name: "no routing walks back to last setWeight",
			manifest: `{"spec":{"strategy":{"canary":{"steps":[{"setWeight":50},{"pause":{}},{"setWeight":75},
				{"pause":{}}]}}},"status":{"currentStepIndex":3}}`,
			want: 75,
		},
		{
			name:     "absent index starts at step zero with no setWeight yet",
			manifest: `{"spec":{"strategy":{"canary":{"steps":[{"pause":{}},{"setWeight":50}]}}},"status":{}}`,
			want:     0,
		},
		{
			name:     "absent index uses the first step setWeight",
			manifest: `{"spec":{"strategy":{"canary":{"steps":[{"setWeight":20},{"pause":{}}]}}},"status":{}}`,
			want:     20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canaryWeight(mustDecodeRollout(t, tt.manifest)); got != tt.want {
				t.Errorf("canaryWeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

const realisticRollout = `{
  "metadata": {"labels": {"mycarrier.tech/correlationId": "corr-1", "app": "api"}},
  "spec": {"strategy": {"canary": {
    "trafficRouting": {"maxTrafficWeight": 100},
    "steps": [{"setWeight": 50}, {"pause": {}}, {"setWeight": 100}]}}},
  "status": {
    "phase": "Paused",
    "currentStepIndex": 1,
    "abort": false,
    "canary": {
      "weights": {"canary": {"weight": 50}, "stable": {"weight": 50}},
      "stepPluginStatuses": [
        {"index": 0, "name": "mc/gate", "phase": "Successful", "message": "ok"},
        {"index": 1, "name": "mc/verify", "phase": "Running", "message": "waiting"}
      ]
    }
  }
}`

func TestDecodeRollout(t *testing.T) {
	live := mustDecodeRollout(t, realisticRollout)

	if got := live.Metadata.Labels[CorrelationIDLabel]; got != "corr-1" {
		t.Errorf("correlation label = %q, want corr-1", got)
	}
	if live.Status.Phase != RolloutPhasePaused {
		t.Errorf("phase = %q, want Paused", live.Status.Phase)
	}
	if live.Status.CurrentStepIndex == nil || *live.Status.CurrentStepIndex != 1 {
		t.Errorf("currentStepIndex = %v, want 1", live.Status.CurrentStepIndex)
	}
	if live.Status.Abort {
		t.Error("abort = true, want false")
	}
	wantPlugins := []StepPluginStatus{
		{Index: 0, Name: "mc/gate", Phase: StepPluginPhaseSuccessful, Message: "ok"},
		{Index: 1, Name: "mc/verify", Phase: StepPluginPhaseRunning, Message: "waiting"},
	}
	if !reflect.DeepEqual(live.Status.Canary.StepPluginStatuses, wantPlugins) {
		t.Errorf("stepPluginStatuses = %+v, want %+v", live.Status.Canary.StepPluginStatuses, wantPlugins)
	}
	if got := canaryWeight(live); got != 50 {
		t.Errorf("canaryWeight = %d, want 50", got)
	}
}

func TestDecodeRollout_InvalidJSON(t *testing.T) {
	if _, err := decodeRollout("not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestNewRolloutStatus(t *testing.T) {
	node := resourceNode{
		Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout", Namespace: "ns", Name: "api",
		Health: nodeHealth{Status: HealthStatusSuspended, Message: "paused by step"},
	}

	got := newRolloutStatus(node, mustDecodeRollout(t, realisticRollout))

	want := RolloutStatus{
		Ref: ResourceRef{
			Group:     "argoproj.io",
			Version:   "v1alpha1",
			Kind:      "Rollout",
			Namespace: "ns",
			Name:      "api",
		},
		Health:           HealthStatusSuspended,
		Message:          "paused by step",
		Phase:            RolloutPhasePaused,
		CurrentStepIndex: int32Ptr(1),
		Aborted:          false,
		CanaryWeight:     50,
		StepPluginStatuses: []StepPluginStatus{
			{Index: 0, Name: "mc/gate", Phase: StepPluginPhaseSuccessful, Message: "ok"},
			{Index: 1, Name: "mc/verify", Phase: StepPluginPhaseRunning, Message: "waiting"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("newRolloutStatus() =\n%+v\nwant\n%+v", got, want)
	}
}
