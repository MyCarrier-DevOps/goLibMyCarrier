package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
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
			name: "aborted with routing reports the router weight while it steps down",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"abort":true,"currentStepIndex":1,"canary":{"weights":{"canary":{"weight":10}}}}}`,
			want: 10,
		},
		{
			name: "aborted past the last step still reports the router weight",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50}]}}},
				"status":{"abort":true,"currentStepIndex":1,"canary":{"weights":{"canary":{"weight":10}}}}}`,
			want: 10,
		},
		{
			name: "aborted with routing reports a settled router weight of zero",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"abort":true,"currentStepIndex":1,"canary":{"weights":{"canary":{"weight":0},"stable":{"weight":100}}}}}`,
			want: 0,
		},
		{
			name: "aborted with routing but no reported weights is zero",
			manifest: `{"spec":{"strategy":{"canary":{"trafficRouting":{},"steps":[{"setWeight":50},{"pause":{}}]}}},
				"status":{"abort":true,"currentStepIndex":1}}`,
			want: 0,
		},
		{
			name: "aborted without routing ignores setWeight",
			manifest: `{"spec":{"strategy":{"canary":{"steps":[{"setWeight":20},{"pause":{}}]}}},
				"status":{"abort":true,"currentStepIndex":0}}`,
			want: 0,
		},
		{
			name:     "aborted without a canary strategy is zero",
			manifest: `{"spec":{"strategy":{"blueGreen":{}}},"status":{"abort":true}}`,
			want:     0,
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
			name:     "non canary strategy has no canary weight",
			manifest: `{"spec":{"strategy":{"blueGreen":{}}},"status":{}}`,
			want:     0,
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
        {"index": 0, "name": "mc/gate", "operation": "Run", "phase": "Successful", "message": "ok"},
        {"index": 1, "name": "mc/verify", "operation": "Run", "phase": "Running", "message": "waiting"},
        {"index": 1, "name": "mc/verify", "operation": "Abort", "phase": "Successful"}
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
		{Index: 0, Name: "mc/gate", Operation: StepPluginOperationRun, Phase: StepPluginPhaseSuccessful, Message: "ok"},
		{
			Index:     1,
			Name:      "mc/verify",
			Operation: StepPluginOperationRun,
			Phase:     StepPluginPhaseRunning,
			Message:   "waiting",
		},
		{Index: 1, Name: "mc/verify", Operation: StepPluginOperationAbort, Phase: StepPluginPhaseSuccessful},
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
		Strategy:         RolloutStrategyCanary,
		// Labels come from realisticRollout metadata.labels. The fixture has no uid, stableRS,
		// currentPodHash, annotations, Istio virtual services or managedRoutes, so those stay empty.
		Labels: map[string]string{CorrelationIDLabel: "corr-1", "app": "api"},
		StepPluginStatuses: []StepPluginStatus{
			{
				Index:     0,
				Name:      "mc/gate",
				Operation: StepPluginOperationRun,
				Phase:     StepPluginPhaseSuccessful,
				Message:   "ok",
			},
			{
				Index:     1,
				Name:      "mc/verify",
				Operation: StepPluginOperationRun,
				Phase:     StepPluginPhaseRunning,
				Message:   "waiting",
			},
			{Index: 1, Name: "mc/verify", Operation: StepPluginOperationAbort, Phase: StepPluginPhaseSuccessful},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("newRolloutStatus() =\n%+v\nwant\n%+v", got, want)
	}
}

// readFixture returns a response recorded from the dev ArgoCD (v3.1.5).
func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(data)
}

const (
	fixtureApp       = "devops-315-rollout-test"
	fixtureNamespace = "devops-315-rollout-test"
	fixtureGroupA    = "devops-315-group-a"
	fixtureGroupB    = "devops-315-group-b"
)

type nodeWant struct {
	health  HealthStatus
	message string
}

func TestDecodeRolloutNodes(t *testing.T) {
	tests := []struct {
		state string
		want  map[string]nodeWant
	}{
		{"healthy", map[string]nodeWant{
			"rt-api":    {HealthStatusHealthy, ""},
			"rt-worker": {HealthStatusHealthy, ""},
			"rt-other":  {HealthStatusHealthy, ""},
		}},
		{"progressing", map[string]nodeWant{
			"rt-api":    {HealthStatusProgressing, "more replicas need to be updated"},
			"rt-worker": {HealthStatusProgressing, "more replicas need to be updated"},
			"rt-other":  {HealthStatusProgressing, "more replicas need to be updated"},
		}},
		{"degraded", map[string]nodeWant{
			"rt-api":    {HealthStatusDegraded, "RolloutAborted: Rollout aborted update to revision 2"},
			"rt-worker": {HealthStatusSuspended, "CanaryPauseStep"},
			"rt-other":  {HealthStatusSuspended, "CanaryPauseStep"},
		}},
		{"suspended", map[string]nodeWant{
			"rt-api":    {HealthStatusSuspended, "CanaryPauseStep"},
			"rt-worker": {HealthStatusSuspended, "CanaryPauseStep"},
			"rt-other":  {HealthStatusSuspended, "CanaryPauseStep"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			nodes, err := decodeRolloutNodes([]byte(readFixture(t, "resource-tree-"+tt.state+".json")))
			if err != nil {
				t.Fatalf("decodeRolloutNodes: %v", err)
			}

			got := map[string]nodeWant{}
			for _, n := range nodes {
				if n.Group != "argoproj.io" || n.Kind != "Rollout" || n.Version != "v1alpha1" ||
					n.Namespace != fixtureNamespace {
					t.Errorf("unexpected node %+v", n)
				}
				got[n.Name] = nodeWant{n.Health.Status, n.Health.Message}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rollout nodes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeRolloutNodes_InvalidJSON(t *testing.T) {
	if _, err := decodeRolloutNodes([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// rolloutServer answers the resource-tree and live-resource calls from recorded fixtures.
type rolloutServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []recordedRequest

	treeStatus     int
	treeBody       string
	resourceStatus int
	resourceBody   string // when empty the recorded resource-<name>-<state> fixture is served
	// missing lists Rollouts whose live read answers 404, as for a stale resource tree.
	missing map[string]bool
}

func newRolloutServer(t *testing.T, state string) *rolloutServer {
	t.Helper()
	s := &rolloutServer{
		treeStatus:     http.StatusOK,
		treeBody:       readFixture(t, "resource-tree-"+state+".json"),
		resourceStatus: http.StatusOK,
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{
			Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.Query(), Auth: r.Header.Get("Authorization"),
		})
		s.mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/resource-tree"):
			w.WriteHeader(s.treeStatus)
			_, _ = w.Write([]byte(s.treeBody))
		case strings.HasSuffix(r.URL.Path, "/resource"):
			if s.missing[r.URL.Query().Get("resourceName")] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":5,"message":"not found"}`))
				return
			}
			body := s.resourceBody
			if body == "" {
				body = readFixture(t, "resource-"+r.URL.Query().Get("resourceName")+"-"+state+".json")
			}
			w.WriteHeader(s.resourceStatus)
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *rolloutServer) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

func (s *rolloutServer) client() *Client {
	return NewClient(&Config{ServerUrl: s.URL, AuthToken: testToken})
}

func rolloutRef(name string) ResourceRef {
	return ResourceRef{
		Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout", Namespace: fixtureNamespace, Name: name,
	}
}

// fixtureLive holds the identity-bound live fields of each recorded Rollout manifest in
// testdata/resource-<name>-<state>.json: metadata.uid, the correlation label value and the
// single trafficRouting.istio.virtualService name (equal to the rollout name in every fixture).
// The fixtures set no trafficRouting.managedRoutes, so ManagedRoutes is nil.
var fixtureLive = map[string]struct{ uid, group string }{
	"rt-api":    {"afc0e4fd-69c8-48ff-8455-aff9e3909d68", fixtureGroupA},
	"rt-worker": {"759734b8-af00-4e55-97f3-27fafb89bd0d", fixtureGroupA},
	"rt-other":  {"358be5b9-f474-4f3c-a9c5-3b4258e0d3d3", fixtureGroupB},
}

// fixtureStatus builds the expected status of a recorded Rollout. stableRS and podHash are the
// manifest's status.stableRS and status.currentPodHash, which differ per state.
func fixtureStatus(
	name string, health HealthStatus, message string, phase RolloutPhase, index int32, aborted bool, weight int32,
	stableRS, podHash string,
) RolloutStatus {
	live := fixtureLive[name]
	return RolloutStatus{
		Ref:                rolloutRef(name),
		Health:             health,
		Message:            message,
		Phase:              phase,
		CurrentStepIndex:   int32Ptr(index),
		Aborted:            aborted,
		CanaryWeight:       weight,
		StepPluginStatuses: nil,
		UID:                live.uid,
		StableRS:           stableRS,
		CurrentPodHash:     podHash,
		Labels:             map[string]string{CorrelationIDLabel: live.group},
		Annotations: map[string]string{
			"argocd.argoproj.io/tracking-id": fixtureApp + ":argoproj.io/Rollout:" + fixtureNamespace + "/" + name,
			"rollout.argoproj.io/revision":   "2",
		},
		IstioVirtualServices: []string{name},
		Strategy:             RolloutStrategyCanary,
		Generation:           2,
		ObservedGeneration:   "2",
	}
}

func TestListRolloutGroup(t *testing.T) {
	const abortedMessage = "RolloutAborted: Rollout aborted update to revision 2"
	tests := []struct {
		name        string
		state       string
		correlation string
		want        []RolloutStatus
	}{
		{
			"healthy group a after promotion",
			"healthy",
			fixtureGroupA,
			[]RolloutStatus{
				fixtureStatus(
					"rt-api",
					HealthStatusHealthy,
					"",
					RolloutPhaseHealthy,
					5,
					false,
					100,
					"775f8d9cb4",
					"775f8d9cb4",
				),
				fixtureStatus(
					"rt-worker",
					HealthStatusHealthy,
					"",
					RolloutPhaseHealthy,
					5,
					false,
					100,
					"6d866788c6",
					"6d866788c6",
				),
			},
		},
		{
			"degraded group a with an aborted rollout",
			"degraded",
			fixtureGroupA,
			[]RolloutStatus{
				fixtureStatus(
					"rt-api",
					HealthStatusDegraded,
					abortedMessage,
					RolloutPhaseDegraded,
					0,
					true,
					0,
					"679bc4c8fc",
					"775f8d9cb4",
				),
				fixtureStatus(
					"rt-worker",
					HealthStatusSuspended,
					"CanaryPauseStep",
					RolloutPhasePaused,
					1,
					false,
					50,
					"7b89495949",
					"6d866788c6",
				),
			},
		},
		{
			"suspended group a",
			"suspended",
			fixtureGroupA,
			[]RolloutStatus{
				fixtureStatus(
					"rt-api",
					HealthStatusSuspended,
					"CanaryPauseStep",
					RolloutPhasePaused,
					1,
					false,
					50,
					"679bc4c8fc",
					"775f8d9cb4",
				),
				fixtureStatus(
					"rt-worker",
					HealthStatusSuspended,
					"CanaryPauseStep",
					RolloutPhasePaused,
					1,
					false,
					50,
					"7b89495949",
					"6d866788c6",
				),
			},
		},
		{
			"suspended group b",
			"suspended",
			fixtureGroupB,
			[]RolloutStatus{
				fixtureStatus(
					"rt-other",
					HealthStatusSuspended,
					"CanaryPauseStep",
					RolloutPhasePaused,
					1,
					false,
					50,
					"796b5bb5b6",
					"f99554ff5",
				),
			},
		},
		{"progressing group a", "progressing", fixtureGroupA, []RolloutStatus{
			fixtureStatus("rt-api", HealthStatusProgressing, "more replicas need to be updated",
				RolloutPhaseProgressing, 0, false, 0, "679bc4c8fc", "775f8d9cb4"),
			fixtureStatus("rt-worker", HealthStatusProgressing, "more replicas need to be updated",
				RolloutPhaseProgressing, 0, false, 0, "7b89495949", "6d866788c6"),
		}},
		{"unknown correlation id", "suspended", "devops-315-unknown", []RolloutStatus{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, tt.state)

			got, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, tt.correlation)
			if err != nil {
				t.Fatalf("ListRolloutGroup: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("statuses =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestListRolloutGroup_Requests(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	app := "app/with space"

	if _, err := srv.client().ListRolloutGroup(context.Background(), app, fixtureGroupB); err != nil {
		t.Fatalf("ListRolloutGroup: %v", err)
	}

	reqs := srv.recorded()
	if len(reqs) != 4 {
		t.Fatalf("expected 1 tree call and 3 live calls, got %d requests", len(reqs))
	}
	if reqs[0].Path != "/api/v1/applications/app%2Fwith%20space/resource-tree" {
		t.Errorf("tree path = %s", reqs[0].Path)
	}
	seen := map[string]bool{}
	for _, r := range reqs[1:] {
		if r.Method != http.MethodGet || r.Path != "/api/v1/applications/app%2Fwith%20space/resource" {
			t.Errorf("unexpected live call %s %s", r.Method, r.Path)
		}
		if r.Auth != "Bearer "+testToken {
			t.Errorf("Authorization = %q", r.Auth)
		}
		name := r.Query.Get("resourceName")
		seen[name] = true
		want := url.Values{
			"namespace":    {fixtureNamespace},
			"resourceName": {name},
			"version":      {"v1alpha1"},
			"group":        {"argoproj.io"},
			"kind":         {"Rollout"},
		}
		if !reflect.DeepEqual(r.Query, want) {
			t.Errorf("live query = %v, want %v", r.Query, want)
		}
	}
	if !reflect.DeepEqual(seen, map[string]bool{"rt-api": true, "rt-worker": true, "rt-other": true}) {
		t.Errorf("live calls for %v", seen)
	}
}

func TestListRolloutGroup_Errors(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, s *rolloutServer)
		wantIs  error
	}{
		{"tree permission denied", func(t *testing.T, s *rolloutServer) {
			s.treeStatus, s.treeBody = http.StatusForbidden, readFixture(t, "error-app-not-found.json")
		}, ErrPermissionDenied},
		{"live resource permission denied", func(t *testing.T, s *rolloutServer) {
			s.resourceStatus, s.resourceBody = http.StatusForbidden, readFixture(t, "error-app-not-found.json")
		}, ErrPermissionDenied},
		{"malformed tree", func(_ *testing.T, s *rolloutServer) { s.treeBody = "not json" }, nil},
		{"malformed envelope", func(_ *testing.T, s *rolloutServer) { s.resourceBody = "not json" }, nil},
		{
			"malformed manifest",
			func(_ *testing.T, s *rolloutServer) { s.resourceBody = `{"manifest":"not json"}` },
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")
			tt.prepare(t, srv)

			got, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, fixtureGroupA)

			if err == nil {
				t.Fatalf("expected error, got %v", got)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("expected errors.Is(%v), got %v", tt.wantIs, err)
			}
		})
	}
}

func TestListRolloutGroup_LiveFailureNamesTheRollout(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	srv.resourceStatus, srv.resourceBody = http.StatusForbidden, readFixture(t, "error-app-not-found.json")

	_, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, fixtureGroupA)

	if err == nil || !strings.Contains(err.Error(), fixtureNamespace+"/rt-") {
		t.Errorf("expected error naming the rollout, got %v", err)
	}
}

func TestListRolloutGroup_EmptyCorrelationID(t *testing.T) {
	srv := newRolloutServer(t, "suspended")

	if _, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, ""); err == nil {
		t.Fatal("expected error for empty correlation id")
	}
	if n := len(srv.recorded()); n != 0 {
		t.Errorf("expected no HTTP calls, got %d", n)
	}
}

func TestListRolloutGroup_EmptyApplicationName(t *testing.T) {
	srv := newRolloutServer(t, "suspended")

	if _, err := srv.client().ListRolloutGroup(context.Background(), "", fixtureGroupA); err == nil {
		t.Fatal("expected error for empty application name")
	}
	if n := len(srv.recorded()); n != 0 {
		t.Errorf("expected no HTTP calls, got %d", n)
	}
}

// withUnmanagedRollout appends to the recorded tree a Rollout node without a uid, as ArgoCD
// adds for a managed Rollout that has no live object.
func withUnmanagedRollout(t *testing.T, tree, name string) string {
	t.Helper()
	return withRolloutNode(t, tree, fixtureNamespace, name, "")
}

func TestListRolloutGroup_SkipsRolloutWithoutLiveObject(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, s *rolloutServer)
		wantGet map[string]bool // live reads expected, by Rollout name
	}{
		{"node without uid", func(t *testing.T, s *rolloutServer) {
			s.treeBody = withUnmanagedRollout(t, s.treeBody, "rt-ghost")
		}, map[string]bool{"rt-api": true, "rt-worker": true, "rt-other": true}},
		{"live read answers 400 not found as part of application", func(t *testing.T, s *rolloutServer) {
			s.resourceStatus, s.resourceBody = http.StatusBadRequest, readFixture(t, "error-resource-not-found.json")
		}, map[string]bool{"rt-api": true, "rt-worker": true, "rt-other": true}},
		{"live read answers 404", func(_ *testing.T, s *rolloutServer) {
			s.missing = map[string]bool{"rt-worker": true}
		}, map[string]bool{"rt-api": true, "rt-worker": true, "rt-other": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")
			tt.prepare(t, srv)

			got, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, fixtureGroupA)
			if err != nil {
				t.Fatalf("ListRolloutGroup: %v", err)
			}

			var names []string
			for _, s := range got {
				names = append(names, s.Ref.Name)
			}
			want := []string{"rt-api", "rt-worker"}
			switch tt.name {
			case "live read answers 404":
				want = []string{"rt-api"}
			case "live read answers 400 not found as part of application":
				want = nil
			}
			if !reflect.DeepEqual(names, want) {
				t.Errorf("rollouts = %v, want %v", names, want)
			}
			read := map[string]bool{}
			for _, r := range srv.recorded()[1:] {
				read[r.Query.Get("resourceName")] = true
			}
			if !reflect.DeepEqual(read, tt.wantGet) {
				t.Errorf("live reads = %v, want %v", read, tt.wantGet)
			}
		})
	}
}

func TestListRolloutGroup_LivePermissionDeniedFailsTheCall(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	srv.resourceStatus, srv.resourceBody = http.StatusForbidden, readFixture(t, "error-app-not-found.json")

	_, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, fixtureGroupA)

	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
}

func TestListRolloutGroup_AbortedRolloutReportsRouterWeight(t *testing.T) {
	var resource struct {
		Manifest string `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(readFixture(t, "resource-rt-api-degraded.json")), &resource); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(resource.Manifest), &manifest); err != nil {
		t.Fatalf("decoding fixture manifest: %v", err)
	}
	status, _ := manifest["status"].(map[string]any)
	canary, _ := status["canary"].(map[string]any)
	weights, _ := canary["weights"].(map[string]any)
	canaryWeights, _ := weights["canary"].(map[string]any)
	if canaryWeights == nil {
		t.Fatal("fixture has no status.canary.weights.canary")
	}
	canaryWeights["weight"] = 10
	edited, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encoding manifest: %v", err)
	}
	body, err := json.Marshal(map[string]string{"manifest": string(edited)})
	if err != nil {
		t.Fatalf("encoding resource: %v", err)
	}

	srv := newRolloutServer(t, "degraded")
	srv.resourceBody = string(body)

	got, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, fixtureGroupA)
	if err != nil {
		t.Fatalf("ListRolloutGroup: %v", err)
	}
	for _, status := range got {
		if status.Ref.Name != "rt-api" {
			continue
		}
		if !status.Aborted || status.CanaryWeight != 10 {
			t.Errorf("rt-api Aborted = %t, CanaryWeight = %d, want true and 10", status.Aborted, status.CanaryWeight)
		}
		return
	}
	t.Fatal("rt-api missing from the statuses")
}

func TestListRollouts_ReturnsEveryRollout(t *testing.T) {
	srv := newRolloutServer(t, "suspended")

	got, err := srv.client().ListRollouts(context.Background(), fixtureApp)
	if err != nil {
		t.Fatalf("ListRollouts: %v", err)
	}

	want := []RolloutStatus{
		fixtureStatus(
			"rt-api",
			HealthStatusSuspended,
			"CanaryPauseStep",
			RolloutPhasePaused,
			1,
			false,
			50,
			"679bc4c8fc",
			"775f8d9cb4",
		),
		fixtureStatus(
			"rt-other",
			HealthStatusSuspended,
			"CanaryPauseStep",
			RolloutPhasePaused,
			1,
			false,
			50,
			"796b5bb5b6",
			"f99554ff5",
		),
		fixtureStatus(
			"rt-worker",
			HealthStatusSuspended,
			"CanaryPauseStep",
			RolloutPhasePaused,
			1,
			false,
			50,
			"7b89495949",
			"6d866788c6",
		),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("statuses =\n%+v\nwant\n%+v", got, want)
	}
}

// resourceEnvelope wraps a manifest in the {"manifest": "<json string>"} document ArgoCD returns for a
// live resource.
func resourceEnvelope(t *testing.T, manifest string) string {
	t.Helper()
	out, err := json.Marshal(map[string]string{"manifest": manifest})
	if err != nil {
		t.Fatalf("encode resource: %v", err)
	}
	return string(out)
}

func TestListRollouts_IncludesUnlabelledRollouts(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	srv.resourceBody = resourceEnvelope(t,
		`{"metadata":{"uid":"uid-1"},"spec":{"strategy":{"canary":{}}},"status":{"phase":"Healthy"}}`)

	got, err := srv.client().ListRollouts(context.Background(), fixtureApp)
	if err != nil {
		t.Fatalf("ListRollouts: %v", err)
	}

	var names []string
	for _, s := range got {
		names = append(names, s.Ref.Name)
		if len(s.Labels) != 0 {
			t.Errorf("%s Labels = %v, want none", s.Ref.Name, s.Labels)
		}
	}
	if want := []string{"rt-api", "rt-other", "rt-worker"}; !reflect.DeepEqual(names, want) {
		t.Errorf("rollouts = %v, want %v", names, want)
	}
}

// withRolloutNode appends a Rollout node to the recorded tree. An empty uid models the node ArgoCD adds
// for a managed Rollout that has no live object.
func withRolloutNode(t *testing.T, tree, namespace, name, uid string) string {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(tree), &parsed); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	node := map[string]any{
		"group": "argoproj.io", "version": "v1alpha1", "kind": "Rollout", "namespace": namespace, "name": name,
	}
	if uid != "" {
		node["uid"] = uid
	}
	nodes, _ := parsed["nodes"].([]any)
	parsed["nodes"] = append(nodes, node)
	out, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("encode tree: %v", err)
	}
	return string(out)
}

func TestListRollouts_SortsByNamespaceThenName(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	// Appended last in the tree, so only the sort puts it first; its live read is served from the
	// rt-api fixture (the test server keys on the resource name).
	srv.treeBody = withRolloutNode(t, srv.treeBody, "aaa-namespace", "rt-api", "uid-aaa")

	got, err := srv.client().ListRollouts(context.Background(), fixtureApp)
	if err != nil {
		t.Fatalf("ListRollouts: %v", err)
	}

	var order []string
	for _, s := range got {
		order = append(order, s.Ref.Namespace+"/"+s.Ref.Name)
	}
	want := []string{
		"aaa-namespace/rt-api",
		fixtureNamespace + "/rt-api",
		fixtureNamespace + "/rt-other",
		fixtureNamespace + "/rt-worker",
	}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestListRollouts_SkipsRolloutWithoutLiveObject(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(t *testing.T, s *rolloutServer)
		wantNames []string
		wantReads []string // live reads expected, by Rollout name, sorted
	}{
		{"node without uid", func(t *testing.T, s *rolloutServer) {
			s.treeBody = withRolloutNode(t, s.treeBody, fixtureNamespace, "rt-ghost", "")
		}, []string{"rt-api", "rt-other", "rt-worker"}, []string{"rt-api", "rt-other", "rt-worker"}},
		{"live read answers 404", func(_ *testing.T, s *rolloutServer) {
			s.missing = map[string]bool{"rt-worker": true}
		}, []string{"rt-api", "rt-other"}, []string{"rt-api", "rt-other", "rt-worker"}},
		{"live read answers 400 not found as part of application", func(t *testing.T, s *rolloutServer) {
			s.resourceStatus, s.resourceBody = http.StatusBadRequest, readFixture(t, "error-resource-not-found.json")
		}, nil, []string{"rt-api", "rt-other", "rt-worker"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")
			tt.prepare(t, srv)

			got, err := srv.client().ListRollouts(context.Background(), fixtureApp)
			if err != nil {
				t.Fatalf("ListRollouts: %v", err)
			}

			var names []string
			for _, s := range got {
				names = append(names, s.Ref.Name)
			}
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("rollouts = %v, want %v", names, tt.wantNames)
			}
			var reads []string
			for _, r := range srv.recorded()[1:] {
				reads = append(reads, r.Query.Get("resourceName"))
			}
			sort.Strings(reads)
			if !reflect.DeepEqual(reads, tt.wantReads) {
				t.Errorf("live reads = %v, want %v", reads, tt.wantReads)
			}
		})
	}
}

func TestListRollouts_EmptyApplicationName(t *testing.T) {
	srv := newRolloutServer(t, "suspended")

	if _, err := srv.client().ListRollouts(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty application name")
	}
	if n := len(srv.recorded()); n != 0 {
		t.Errorf("expected no HTTP calls, got %d", n)
	}
}

func TestListRollouts_Errors(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, s *rolloutServer)
		wantIs  error
		wantMsg string
	}{
		{"tree permission denied", func(t *testing.T, s *rolloutServer) {
			s.treeStatus, s.treeBody = http.StatusForbidden, readFixture(t, "error-app-not-found.json")
		}, ErrPermissionDenied, "resource tree of " + fixtureApp},
		{"live resource permission denied", func(t *testing.T, s *rolloutServer) {
			s.resourceStatus, s.resourceBody = http.StatusForbidden, readFixture(t, "error-app-not-found.json")
		}, ErrPermissionDenied, fixtureNamespace + "/rt-"},
		{"malformed tree", func(_ *testing.T, s *rolloutServer) { s.treeBody = "not json" }, nil, "resource tree"},
		{"malformed manifest", func(_ *testing.T, s *rolloutServer) {
			s.resourceBody = `{"manifest":"not json"}`
		}, nil, fixtureNamespace + "/rt-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")
			tt.prepare(t, srv)

			got, err := srv.client().ListRollouts(context.Background(), fixtureApp)

			if err == nil {
				t.Fatalf("expected error, got %v", got)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("expected errors.Is(%v), got %v", tt.wantIs, err)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestListRollouts_Requests(t *testing.T) {
	srv := newRolloutServer(t, "suspended")

	if _, err := srv.client().ListRollouts(context.Background(), "app/with space"); err != nil {
		t.Fatalf("ListRollouts: %v", err)
	}

	reqs := srv.recorded()
	if len(reqs) != 4 {
		t.Fatalf("expected 1 tree call and 3 live calls, got %d requests", len(reqs))
	}
	if reqs[0].Path != "/api/v1/applications/app%2Fwith%20space/resource-tree" {
		t.Errorf("tree path = %s", reqs[0].Path)
	}
	for _, r := range reqs[1:] {
		if r.Method != http.MethodGet || r.Path != "/api/v1/applications/app%2Fwith%20space/resource" {
			t.Errorf("unexpected live call %s %s", r.Method, r.Path)
		}
		if r.Auth != "Bearer "+testToken {
			t.Errorf("Authorization = %q", r.Auth)
		}
	}
}

func TestListRollouts_NoRolloutsReturnsEmptySlice(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	srv.treeBody = `{"nodes":[]}`

	got, err := srv.client().ListRollouts(context.Background(), fixtureApp)
	if err != nil {
		t.Fatalf("ListRollouts: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("statuses = %#v, want empty non-nil slice", got)
	}
}
