package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// envelopeFor wraps manifest in the {"manifest": "<json string>"} envelope ArgoCD's resource endpoint returns.
func envelopeFor(t *testing.T, manifest string) string {
	t.Helper()
	b, err := json.Marshal(struct {
		Manifest string `json:"manifest"`
	}{manifest})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return string(b)
}

func TestGetLiveResource_ReturnsManifest(t *testing.T) {
	tests := []struct {
		name     string
		ref      ResourceRef
		manifest string
	}{
		{
			"rollout",
			ResourceRef{Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout", Namespace: "ns-a", Name: "rt-api"},
			`{"apiVersion":"argoproj.io/v1alpha1","kind":"Rollout","metadata":{"name":"rt-api"}}`,
		},
		{
			"replicaset",
			ResourceRef{Group: "apps", Version: "v1", Kind: "ReplicaSet", Namespace: "ns-a", Name: "rt-api-775f8d9cb4"},
			`{"apiVersion":"apps/v1","kind":"ReplicaSet","metadata":{"name":"rt-api-775f8d9cb4"}}`,
		},
		{
			"virtualservice",
			ResourceRef{
				Group: "networking.istio.io", Version: "v1beta1", Kind: "VirtualService", Namespace: "ns-a", Name: "rt-api",
			},
			`{"apiVersion":"networking.istio.io/v1beta1","kind":"VirtualService","spec":{"http":[{"name":"primary"}]}}`,
		},
		{
			"configmap",
			ResourceRef{Group: "", Version: "v1", Kind: "ConfigMap", Namespace: "ns-a", Name: "settings"},
			`{"apiVersion":"v1","kind":"ConfigMap","data":{"k":"v"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")
			srv.resourceBody = envelopeFor(t, tt.manifest)

			got, err := srv.client().GetLiveResource(context.Background(), fixtureApp, tt.ref)

			if err != nil {
				t.Fatalf("GetLiveResource: %v", err)
			}
			if string(got) != tt.manifest {
				t.Errorf("manifest = %q, want %q", got, tt.manifest)
			}
			reqs := srv.recorded()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 HTTP call, got %d", len(reqs))
			}
			req := reqs[0]
			if req.Method != http.MethodGet || req.Path != "/api/v1/applications/"+fixtureApp+"/resource" {
				t.Errorf("request = %s %s", req.Method, req.Path)
			}
			if req.Auth != "Bearer "+testToken {
				t.Errorf("auth = %q", req.Auth)
			}
			want := url.Values{
				"group":        {tt.ref.Group},
				"version":      {tt.ref.Version},
				"kind":         {tt.ref.Kind},
				"namespace":    {tt.ref.Namespace},
				"resourceName": {tt.ref.Name},
			}
			if !reflect.DeepEqual(req.Query, want) {
				t.Errorf("query = %v, want %v", req.Query, want)
			}
		})
	}
}

func TestGetLiveResource_ErrorMapping(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantIs  error
		wantNot []error
	}{
		{"404", http.StatusNotFound, `{"code":5,"message":"not found"}`, ErrNotFound, []error{ErrPermissionDenied}},
		{
			"404 recorded ArgoCD body", http.StatusNotFound, readFixture(t, "error-resource-not-found.json"),
			ErrNotFound, []error{ErrPermissionDenied},
		},
		{
			"400 not found as part of application", http.StatusBadRequest,
			`{"code":3,"message":"Rollout rt-api not found as part of application x"}`,
			ErrNotFound, []error{ErrPermissionDenied},
		},
		{"403", http.StatusForbidden, `{"code":7,"message":"permission denied"}`, ErrPermissionDenied,
			[]error{ErrNotFound}},
		{
			"403 recorded ArgoCD body", http.StatusForbidden, readFixture(t, "error-app-not-found.json"),
			ErrPermissionDenied, []error{ErrNotFound},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")
			srv.resourceStatus, srv.resourceBody = tt.status, tt.body

			got, err := srv.client().GetLiveResource(context.Background(), fixtureApp, rolloutRef("rt-api"))

			if err == nil {
				t.Fatalf("expected error, got %q", got)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("expected errors.Is(%v), got %v", tt.wantIs, err)
			}
			for _, not := range tt.wantNot {
				if errors.Is(err, not) {
					t.Errorf("did not expect errors.Is(%v), got %v", not, err)
				}
			}
		})
	}
}

func TestGetLiveResource_EscapesAppAndQuery(t *testing.T) {
	const app = "my/app? x&y#z"
	ref := ResourceRef{
		Group: "g/r?p", Version: "v 1", Kind: "Kind&Co", Namespace: "ns#1", Name: "na/me? a&b=c d",
	}
	var gotURI, gotEscapedPath, gotRawQuery string
	var gotQuery url.Values
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotURI, gotEscapedPath, gotRawQuery, gotQuery = r.RequestURI, r.URL.EscapedPath(), r.URL.RawQuery, r.URL.Query()
		_, _ = w.Write([]byte(`{"manifest":"{}"}`))
	}))
	t.Cleanup(srv.Close)
	client := NewClient(&Config{ServerUrl: srv.URL, AuthToken: testToken})

	if _, err := client.GetLiveResource(context.Background(), app, ref); err != nil {
		t.Fatalf("GetLiveResource: %v", err)
	}

	if calls != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", calls)
	}
	wantPath := "/api/v1/applications/" + url.PathEscape(app) + "/resource"
	if gotEscapedPath != wantPath {
		t.Errorf("escaped path = %q, want %q", gotEscapedPath, wantPath)
	}
	for _, c := range []string{" ", "#"} {
		if strings.Contains(gotURI, c) {
			t.Errorf("request URI %q contains raw %q", gotURI, c)
		}
	}
	if pairs := strings.Split(gotRawQuery, "&"); len(pairs) != 5 {
		t.Errorf("raw query %q has %d pairs, want 5 (a value's & leaked unescaped)", gotRawQuery, len(pairs))
	}
	want := url.Values{
		"group":        {ref.Group},
		"version":      {ref.Version},
		"kind":         {ref.Kind},
		"namespace":    {ref.Namespace},
		"resourceName": {ref.Name},
	}
	if !reflect.DeepEqual(gotQuery, want) {
		t.Errorf("query round-trip = %v, want %v", gotQuery, want)
	}
}

func TestGetLiveResource_ContextCancel(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := srv.client().GetLiveResource(ctx, fixtureApp, rolloutRef("rt-api"))
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected errors.Is(context.Canceled), got %v", err)
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrPermissionDenied) {
		t.Errorf("a cancelled context must not map to an ArgoCD error, got %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("returned after %v, want well under 1s", elapsed)
	}
}

// Empty appName follows ListRolloutGroup: an "is required" error and no HTTP call.
// Empty ref.Name is treated the same way: ArgoCD's /resource endpoint cannot address a resource
// without a name, so the client rejects it locally rather than sending a request that is
// guaranteed to fail.
func TestGetLiveResource_EmptyArguments(t *testing.T) {
	tests := []struct {
		name string
		app  string
		ref  ResourceRef
	}{
		{"empty application name", "", rolloutRef("rt-api")},
		{"empty resource name", fixtureApp, rolloutRef("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")

			_, err := srv.client().GetLiveResource(context.Background(), tt.app, tt.ref)

			if err == nil || !strings.Contains(err.Error(), "is required") {
				t.Errorf("expected an \"is required\" error, got %v", err)
			}
			if n := len(srv.recorded()); n != 0 {
				t.Errorf("expected no HTTP calls, got %d", n)
			}
		})
	}
}

func TestGetLiveResource_ServerErrorIsNotNotFound(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	srv.resourceStatus, srv.resourceBody = http.StatusInternalServerError, `{"code":13,"message":"boom"}`
	client := srv.client()
	// A GET 5xx is retried; shrink the waits so the test stays fast.
	client.retryableClient.RetryWaitMin = time.Millisecond
	client.retryableClient.RetryWaitMax = 2 * time.Millisecond

	_, err := client.GetLiveResource(context.Background(), fixtureApp, rolloutRef("rt-api"))

	if err == nil {
		t.Fatal("expected error for a 500 answer")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrPermissionDenied) {
		t.Errorf("a 500 must not map to a typed ArgoCD error, got %v", err)
	}
	if !strings.Contains(err.Error(), "giving up") {
		t.Errorf("expected the retry client's \"giving up\" error, got %v", err)
	}
	if n := len(srv.recorded()); n != 4 {
		t.Errorf("expected 4 attempts (1 + 3 retries), got %d", n)
	}
}

func TestGetLiveResource_MalformedEnvelope(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	srv.resourceBody = "not json"

	_, err := srv.client().GetLiveResource(context.Background(), fixtureApp, rolloutRef("rt-api"))

	if err == nil || !strings.Contains(err.Error(), "error decoding resource response") {
		t.Errorf("expected \"error decoding resource response\", got %v", err)
	}
}

func TestGetLiveResource_EmptyObjectManifest(t *testing.T) {
	srv := newRolloutServer(t, "suspended")
	srv.resourceBody = `{"manifest":"{}"}`

	got, err := srv.client().GetLiveResource(context.Background(), fixtureApp, rolloutRef("rt-api"))

	if err != nil {
		t.Fatalf("GetLiveResource: %v", err)
	}
	if string(got) != "{}" {
		t.Errorf("manifest = %q, want {}", got)
	}
}

func TestGetLiveResource_AbsentOrNullManifest(t *testing.T) {
	tests := []struct{ name, body string }{
		{"no manifest key", `{}`},
		{"null manifest", `{"manifest": null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newRolloutServer(t, "suspended")
			srv.resourceBody = tt.body

			got, err := srv.client().GetLiveResource(context.Background(), fixtureApp, rolloutRef("rt-api"))

			if err != nil {
				t.Fatalf("GetLiveResource: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("manifest = %q, want empty", got)
			}
		})
	}
}

// fixtureManifest returns the live manifest inside the recorded resource envelope fixture name.
func fixtureManifest(t *testing.T, name string) string {
	t.Helper()
	var env struct {
		Manifest string `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(readFixture(t, name)), &env); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return env.Manifest
}

func TestNewRolloutStatus_Strategy(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{"canary recorded fixture", fixtureManifest(t, "resource-rt-api-healthy.json"), RolloutStrategyCanary},
		{"canary inline", `{"spec":{"strategy":{"canary":{}}}}`, RolloutStrategyCanary},
		{"blueGreen", `{"spec":{"strategy":{"blueGreen":{"activeService":"a"}}}}`, RolloutStrategyBlueGreen},
		{"neither", `{"spec":{"strategy":{}}}`, ""},
		{"null blocks", `{"spec":{"strategy":{"canary":null,"blueGreen":null}}}`, ""},
		{"both blocks", `{"spec":{"strategy":{"canary":{},"blueGreen":{}}}}`, RolloutStrategyCanary},
		{"no spec", `{}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newRolloutStatus(resourceNode{}, mustDecodeRollout(t, tt.manifest)).Strategy

			if got != tt.want {
				t.Errorf("Strategy = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRolloutStatus_Generation(t *testing.T) {
	tests := []struct {
		name         string
		manifest     string
		wantGen      int64
		wantObserved string
		wantCurrent  bool
	}{
		{"current recorded fixture", fixtureManifest(t, "resource-rt-api-healthy.json"), 2, "2", true},
		{"lagging", `{"metadata":{"generation":5},"status":{"observedGeneration":"4"}}`, 5, "4", false},
		{"no status yet", `{"metadata":{"generation":1}}`, 1, "", false},
		{"nothing set", `{}`, 0, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newRolloutStatus(resourceNode{}, mustDecodeRollout(t, tt.manifest))

			if got.Generation != tt.wantGen {
				t.Errorf("Generation = %d, want %d", got.Generation, tt.wantGen)
			}
			if got.ObservedGeneration != tt.wantObserved {
				t.Errorf("ObservedGeneration = %q, want %q", got.ObservedGeneration, tt.wantObserved)
			}
			if got.StatusCurrent() != tt.wantCurrent {
				t.Errorf("StatusCurrent() = %v, want %v", got.StatusCurrent(), tt.wantCurrent)
			}
		})
	}
}

func TestNewRolloutStatus_CanaryFields(t *testing.T) {
	const manifest = `{
	  "metadata": {
	    "uid": "afc0e4fd-69c8-48ff-8455-aff9e3909d68",
	    "labels": {"mycarrier.tech/correlationId": "grp", "app": "api"},
	    "annotations": {"rollout.argoproj.io/revision": "2"}
	  },
	  "spec": {"strategy": {"canary": {"trafficRouting": {
	    "istio": {"virtualService": {"name": "vs-main", "routes": ["primary"]}},
	    "managedRoutes": [{"name": "mr-one"}, {"name": "mr-two"}]
	  }}}},
	  "status": {"stableRS": "679bc4c8fc", "currentPodHash": "775f8d9cb4"}
	}`
	live := mustDecodeRollout(t, manifest)

	got := newRolloutStatus(resourceNode{Name: "rt-api"}, live)

	if got.UID != "afc0e4fd-69c8-48ff-8455-aff9e3909d68" {
		t.Errorf("UID = %q", got.UID)
	}
	if got.StableRS != "679bc4c8fc" {
		t.Errorf("StableRS = %q, want 679bc4c8fc", got.StableRS)
	}
	if got.CurrentPodHash != "775f8d9cb4" {
		t.Errorf("CurrentPodHash = %q, want 775f8d9cb4", got.CurrentPodHash)
	}
	wantLabels := map[string]string{"mycarrier.tech/correlationId": "grp", "app": "api"}
	if !reflect.DeepEqual(got.Labels, wantLabels) {
		t.Errorf("Labels = %v, want %v", got.Labels, wantLabels)
	}
	wantAnnotations := map[string]string{"rollout.argoproj.io/revision": "2"}
	if !reflect.DeepEqual(got.Annotations, wantAnnotations) {
		t.Errorf("Annotations = %v, want %v", got.Annotations, wantAnnotations)
	}
	if want := []string{"mr-one", "mr-two"}; !reflect.DeepEqual(got.ManagedRoutes, want) {
		t.Errorf("ManagedRoutes = %v, want %v", got.ManagedRoutes, want)
	}
	if want := []string{"vs-main"}; !reflect.DeepEqual(got.IstioVirtualServices, want) {
		t.Errorf("IstioVirtualServices = %v, want %v", got.IstioVirtualServices, want)
	}
}

func TestNewRolloutStatus_VirtualServiceSingularAndList(t *testing.T) {
	tests := []struct {
		name    string
		routing string
		want    []string // nil and empty are both accepted for "no virtual service"
	}{
		{
			"both forms",
			`{"istio":{"virtualService":{"name":"a"},"virtualServices":[{"name":"b"},{"name":"c"}]}}`,
			[]string{"a", "b", "c"},
		},
		{"singular only", `{"istio":{"virtualService":{"name":"a"}}}`, []string{"a"}},
		{"list only", `{"istio":{"virtualServices":[{"name":"b"},{"name":"c"}]}}`, []string{"b", "c"}},
		{"neither", `{"istio":{}}`, nil},
		{"no istio", `{}`, nil},
		{
			"duplicate across both is de-duplicated",
			`{"istio":{"virtualService":{"name":"a"},"virtualServices":[{"name":"b"},{"name":"a"},{"name":"b"}]}}`,
			[]string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live := mustDecodeRollout(t,
				`{"spec":{"strategy":{"canary":{"trafficRouting":`+tt.routing+`}}}}`)

			got := newRolloutStatus(resourceNode{}, live).IstioVirtualServices

			if len(tt.want) == 0 {
				if len(got) != 0 {
					t.Errorf("IstioVirtualServices = %v, want none", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IstioVirtualServices = %v, want %v (singular first, then list order)", got, tt.want)
			}
		})
	}
}

func TestListRolloutGroup_CarriesLabelsAndAnnotations(t *testing.T) {
	srv := newRolloutServer(t, "suspended")

	got, err := srv.client().ListRolloutGroup(context.Background(), fixtureApp, fixtureGroupA)

	if err != nil {
		t.Fatalf("ListRolloutGroup: %v", err)
	}
	var api *RolloutStatus
	for i := range got {
		if got[i].Ref.Name == "rt-api" {
			api = &got[i]
		}
	}
	if api == nil {
		t.Fatalf("rt-api missing from %v", got)
	}
	if api.UID != "afc0e4fd-69c8-48ff-8455-aff9e3909d68" {
		t.Errorf("UID = %q", api.UID)
	}
	if api.StableRS != "679bc4c8fc" || api.CurrentPodHash != "775f8d9cb4" {
		t.Errorf("StableRS/CurrentPodHash = %q/%q, want 679bc4c8fc/775f8d9cb4", api.StableRS, api.CurrentPodHash)
	}
	wantLabels := map[string]string{CorrelationIDLabel: fixtureGroupA}
	if !reflect.DeepEqual(api.Labels, wantLabels) {
		t.Errorf("Labels = %v, want %v", api.Labels, wantLabels)
	}
	wantAnnotations := map[string]string{
		"argocd.argoproj.io/tracking-id": "devops-315-rollout-test:argoproj.io/Rollout:devops-315-rollout-test/rt-api",
		"rollout.argoproj.io/revision":   "2",
	}
	if !reflect.DeepEqual(api.Annotations, wantAnnotations) {
		t.Errorf("Annotations = %v, want %v", api.Annotations, wantAnnotations)
	}
	if want := []string{"rt-api"}; !reflect.DeepEqual(api.IstioVirtualServices, want) {
		t.Errorf("IstioVirtualServices = %v, want %v", api.IstioVirtualServices, want)
	}
}
