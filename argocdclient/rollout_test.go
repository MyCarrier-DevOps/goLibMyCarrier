package argocdclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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

func TestListRolloutGroup(t *testing.T) {
	tests := []struct {
		name        string
		state       string
		correlation string
		wantNames   []string
		wantHealth  HealthStatus
		wantMessage string
		wantPhase   RolloutPhase
		wantIndex   int32
		wantWeight  int32
	}{
		{"suspended group a", "suspended", fixtureGroupA, []string{"rt-api", "rt-worker"},
			HealthStatusSuspended, "CanaryPauseStep", RolloutPhasePaused, 1, 50},
		{"suspended group b", "suspended", fixtureGroupB, []string{"rt-other"},
			HealthStatusSuspended, "CanaryPauseStep", RolloutPhasePaused, 1, 50},
		{"progressing group a", "progressing", fixtureGroupA, []string{"rt-api", "rt-worker"},
			HealthStatusProgressing, "more replicas need to be updated", RolloutPhaseProgressing, 0, 0},
		{"unknown correlation id", "suspended", "devops-315-unknown", nil,
			HealthStatusSuspended, "", RolloutPhasePaused, 1, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, tt.state)

			got, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, tt.correlation)
			if err != nil {
				t.Fatalf("ListRolloutGroup: %v", err)
			}
			if got == nil || len(got) != len(tt.wantNames) {
				t.Fatalf("got %d statuses (nil=%v), want %d", len(got), got == nil, len(tt.wantNames))
			}
			for i, name := range tt.wantNames {
				want := RolloutStatus{
					Ref:                rolloutRef(name),
					Health:             tt.wantHealth,
					Message:            tt.wantMessage,
					Phase:              tt.wantPhase,
					CurrentStepIndex:   int32Ptr(tt.wantIndex),
					Aborted:            false,
					CanaryWeight:       tt.wantWeight,
					StepPluginStatuses: nil,
				}
				if !reflect.DeepEqual(got[i], want) {
					t.Errorf("status[%d] =\n%+v\nwant\n%+v", i, got[i], want)
				}
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
		{"live resource not found in application", func(t *testing.T, s *rolloutServer) {
			s.resourceStatus, s.resourceBody = http.StatusBadRequest, readFixture(t, "error-resource-not-found.json")
		}, ErrNotFound},
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
	srv.resourceStatus, srv.resourceBody = http.StatusBadRequest, readFixture(t, "error-resource-not-found.json")

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
