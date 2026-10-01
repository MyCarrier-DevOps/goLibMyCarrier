package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
)

const (
	testAppName = "devops-315-rollout-test"
	testToken   = "action-token"
)

var testRolloutRef = ResourceRef{
	Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout", Namespace: "default", Name: "devops-315-rollout-test",
}

type recordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Auth   string
	Type   string
	Body   []byte
}

// fakeArgoCD answers the action discovery GET and the action POST, recording every request.
type fakeArgoCD struct {
	*httptest.Server

	mu       sync.Mutex
	requests []recordedRequest

	actionsStatus int
	actionsBody   string
	postStatus    int
	postBody      string
}

func newFakeArgoCD(t *testing.T, actionsBody string) *fakeArgoCD {
	t.Helper()
	f := &fakeArgoCD{actionsStatus: http.StatusOK, actionsBody: actionsBody, postStatus: http.StatusOK, postBody: "{}"}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.EscapedPath(),
			Query:  r.URL.Query(),
			Auth:   r.Header.Get("Authorization"),
			Type:   r.Header.Get("Content-Type"),
			Body:   body,
		})
		f.mu.Unlock()

		status, resp := f.actionsStatus, f.actionsBody
		if r.Method == http.MethodPost {
			status, resp = f.postStatus, f.postBody
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeArgoCD) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func (f *fakeArgoCD) posts() []recordedRequest {
	var posts []recordedRequest
	for _, r := range f.recorded() {
		if r.Method == http.MethodPost {
			posts = append(posts, r)
		}
	}
	return posts
}

func (f *fakeArgoCD) client() *Client {
	return NewClient(&Config{ServerUrl: f.URL, AuthToken: testToken})
}

func TestRunResourceAction_HappyPath(t *testing.T) {
	for _, action := range []ResourceAction{ActionResume, ActionAbort} {
		t.Run(string(action), func(t *testing.T) {
			f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))

			if err := f.client().
				runResourceAction(context.Background(), testAppName, testRolloutRef, action); err != nil {
				t.Fatalf("runResourceAction: %v", err)
			}

			reqs := f.recorded()
			if len(reqs) != 2 {
				t.Fatalf("expected 2 requests, got %d", len(reqs))
			}

			get := reqs[0]
			if get.Method != http.MethodGet || get.Path != "/api/v1/applications/"+testAppName+"/resource/actions" {
				t.Errorf("unexpected pre-check %s %s", get.Method, get.Path)
			}
			wantQuery := url.Values{
				"namespace":    {"default"},
				"resourceName": {"devops-315-rollout-test"},
				"version":      {"v1alpha1"},
				"group":        {"argoproj.io"},
				"kind":         {"Rollout"},
			}
			if !reflect.DeepEqual(get.Query, wantQuery) {
				t.Errorf("pre-check query = %v, want %v", get.Query, wantQuery)
			}

			posts := f.posts()
			if len(posts) != 1 {
				t.Fatalf("expected exactly 1 POST, got %d", len(posts))
			}
			post := posts[0]
			if post.Path != "/api/v1/applications/"+testAppName+"/resource/actions/v2" {
				t.Errorf("POST path = %s", post.Path)
			}
			if post.Auth != "Bearer "+testToken {
				t.Errorf("POST Authorization = %q", post.Auth)
			}
			if post.Type != "application/json" {
				t.Errorf("POST Content-Type = %q", post.Type)
			}
			var got map[string]string
			if err := json.Unmarshal(post.Body, &got); err != nil {
				t.Fatalf("POST body is not JSON: %v", err)
			}
			want := map[string]string{
				"name": testAppName, "namespace": "default", "resourceName": "devops-315-rollout-test",
				"version": "v1alpha1", "group": "argoproj.io", "kind": "Rollout", "action": string(action),
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("POST body = %v, want %v", got, want)
			}
		})
	}
}

func TestRunResourceAction_EscapesApplicationName(t *testing.T) {
	f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))

	if err := f.client().
		runResourceAction(context.Background(), "app/with space", testRolloutRef, ActionAbort); err != nil {
		t.Fatalf("runResourceAction: %v", err)
	}

	for _, r := range f.recorded() {
		if r.Path != "/api/v1/applications/app%2Fwith%20space/resource/actions" &&
			r.Path != "/api/v1/applications/app%2Fwith%20space/resource/actions/v2" {
			t.Errorf("application name not path-escaped: %s", r.Path)
		}
	}
}

func TestRunResourceAction_NotOffered(t *testing.T) {
	tests := []struct {
		name    string
		actions string
		action  ResourceAction
	}{
		{"disabled action", readFixture(t, "actions-rt-api-suspended.json"), ActionRetry},
		{"disabled in healthy state", readFixture(t, "actions-rt-api-healthy.json"), ActionAbort},
		{"absent action", `{"actions":[{"name":"abort"}]}`, ActionResume},
		{"no actions", `{"actions":[]}`, ActionAbort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeArgoCD(t, tt.actions)

			err := f.client().runResourceAction(context.Background(), testAppName, testRolloutRef, tt.action)

			if !errors.Is(err, ErrConflict) {
				t.Fatalf("expected ErrConflict, got %v", err)
			}
			if len(f.posts()) != 0 {
				t.Errorf("expected no POST, got %d", len(f.posts()))
			}
		})
	}
}

func TestRunResourceAction_PostErrorMapping(t *testing.T) {
	denied := readFixture(t, "error-action-permission-denied.json")
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"403", http.StatusForbidden, denied, ErrPermissionDenied},
		{"404", http.StatusNotFound, `{"code":5,"message":"not found"}`, ErrNotFound},
		{"409", http.StatusConflict, `{"code":10,"message":"conflict"}`, ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
			f.postStatus, f.postBody = tt.status, tt.body

			err := f.client().runResourceAction(context.Background(), testAppName, testRolloutRef, ActionAbort)

			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status || apiErr.Body != tt.body {
				t.Errorf("expected *APIError{%d, body}, got %v", tt.status, err)
			}
		})
	}
}

func TestRunResourceAction_PreCheckNotFoundInApplication(t *testing.T) {
	f := newFakeArgoCD(t, "")
	f.actionsStatus = http.StatusBadRequest
	f.actionsBody = readFixture(t, "error-resource-not-found.json")

	err := f.client().runResourceAction(context.Background(), testAppName, testRolloutRef, ActionAbort)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if len(f.posts()) != 0 {
		t.Errorf("expected no POST, got %d", len(f.posts()))
	}
}

func TestRunResourceAction_PostIsNeverRetried(t *testing.T) {
	f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
	f.postStatus, f.postBody = http.StatusServiceUnavailable, "unavailable"

	err := f.client().runResourceAction(context.Background(), testAppName, testRolloutRef, ActionResume)

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected *APIError 503, got %v", err)
	}
	if n := len(f.posts()); n != 1 {
		t.Errorf("expected exactly 1 POST attempt, got %d", n)
	}
}

// withRef returns a copy of testRolloutRef modified by change.
func withRef(change func(*ResourceRef)) ResourceRef {
	ref := testRolloutRef
	change(&ref)
	return ref
}

func TestRunResourceAction_Validation(t *testing.T) {
	tests := []struct {
		name   string
		ref    ResourceRef
		action ResourceAction
	}{
		{"empty name", ResourceRef{Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout"}, ActionAbort},
		{"empty kind", ResourceRef{Version: "v1alpha1", Name: "x"}, ActionAbort},
		{"empty version", ResourceRef{Kind: "Rollout", Name: "x"}, ActionAbort},
		{"empty action", testRolloutRef, ""},
		{
			"kind other than Rollout",
			withRef(func(r *ResourceRef) { r.Group, r.Kind = "apps", "Deployment" }),
			ActionAbort,
		},
		{"group other than argoproj.io", withRef(func(r *ResourceRef) { r.Group = "apps" }), ActionAbort},
		{"empty group", withRef(func(r *ResourceRef) { r.Group = "" }), ActionAbort},
		{"restart is not a rollout action", testRolloutRef, ResourceAction("restart")},
		{"skip-current-step is not offered", testRolloutRef, ResourceAction("skip-current-step")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))

			if err := f.client().runResourceAction(context.Background(), testAppName, tt.ref, tt.action); err == nil {
				t.Fatal("expected validation error")
			}
			if n := len(f.recorded()); n != 0 {
				t.Errorf("expected no HTTP calls, got %d", n)
			}
		})
	}
}

func TestRunResourceAction_EmptyApplicationName(t *testing.T) {
	f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))

	if err := f.client().runResourceAction(context.Background(), "", testRolloutRef, ActionAbort); err == nil {
		t.Fatal("expected error for empty application name")
	}
	if n := len(f.recorded()); n != 0 {
		t.Errorf("expected no HTTP calls, got %d", n)
	}
}

func TestRunResourceAction_CanceledContext(t *testing.T) {
	f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-suspended.json"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.client().runResourceAction(ctx, testAppName, testRolloutRef, ActionAbort); err == nil {
		t.Fatal("expected error for canceled context")
	}
	if len(f.posts()) != 0 {
		t.Error("expected no POST for canceled context")
	}
}

func TestRunResourceAction_DegradedRollout(t *testing.T) {
	tests := []struct {
		action   ResourceAction
		wantPost bool
	}{
		{ActionAbort, false},
		{ActionResume, false},
		{ActionRetry, true},
		{ActionPromoteFull, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			f := newFakeArgoCD(t, readFixture(t, "actions-rt-api-degraded.json"))

			err := f.client().runResourceAction(context.Background(), testAppName, testRolloutRef, tt.action)

			if tt.wantPost {
				if err != nil {
					t.Fatalf("runResourceAction: %v", err)
				}
				if n := len(f.posts()); n != 1 {
					t.Errorf("expected 1 POST, got %d", n)
				}
				return
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("expected ErrConflict, got %v", err)
			}
			if n := len(f.posts()); n != 0 {
				t.Errorf("expected no POST, got %d", n)
			}
		})
	}
}
