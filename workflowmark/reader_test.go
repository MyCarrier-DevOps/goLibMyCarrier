package workflowmark

import (
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wantPath = "/apis/argoproj.io/v1alpha1/namespaces/ci/workflows/build-old"

// fakeAPI is a Kubernetes API that serves one workflow, and records the bearer token it was sent.
func fakeAPI(t *testing.T, status int, body string, gotToken *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotToken = r.Header.Get("Authorization")
		if r.Method != http.MethodGet || r.URL.Path != wantPath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serviceAccountDir writes a service account directory that trusts srv's certificate.
func serviceAccountDir(t *testing.T, srv *httptest.Server, token string) string {
	t.Helper()
	dir := t.TempDir()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newReader(t *testing.T, srv *httptest.Server, dir string) *Reader {
	t.Helper()
	r, err := NewInCluster(srv.URL, dir, "ci", "build-old")
	if err != nil {
		t.Fatalf("NewInCluster: %v", err)
	}
	return r
}

func TestReader_SupersededBy(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    Mark
		wantErr bool
	}{
		{
			name: "a marked run", status: http.StatusOK,
			body: `{"metadata":{"annotations":{"ci.mycarrier.tech/superseded-by":"build-new",` +
				`"ci.mycarrier.tech/superseded-by-correlation-id":"cid-y",` +
				`"ci.mycarrier.tech/superseded-by-commit-sha":"b7c1e9a"}}}`,
			want: Mark{By: "build-new", ByCorrelationID: "cid-y", ByCommitSHA: "b7c1e9a"},
		},
		{
			name: "a run marked without a correlation id", status: http.StatusOK,
			body: `{"metadata":{"annotations":{"ci.mycarrier.tech/superseded-by":"build-new"}}}`,
			want: Mark{By: "build-new"},
		},
		{
			name: "an unmarked run", status: http.StatusOK,
			body: `{"metadata":{"annotations":{"ci.mycarrier.tech/superseded-by":""}}}`,
		},
		{name: "a run of a template that is not preemptible", status: http.StatusOK, body: `{"metadata":{}}`},
		{name: "the API refuses the read", status: http.StatusForbidden, body: `{"kind":"Status"}`, wantErr: true},
		{name: "the API fails", status: http.StatusServiceUnavailable, wantErr: true},
		{name: "an answer that is not a workflow", status: http.StatusOK, body: `<html>`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotToken string
			srv := fakeAPI(t, tt.status, tt.body, &gotToken)
			r := newReader(t, srv, serviceAccountDir(t, srv, "sa-token"))

			mark, err := r.SupersededBy(t.Context())

			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if mark != tt.want {
				t.Errorf("mark = %+v, want %+v", mark, tt.want)
			}
			if gotToken != "Bearer sa-token" {
				t.Errorf("Authorization = %q, want the trimmed service account token", gotToken)
			}
		})
	}
}

// The service account token is projected and rotates: every read sends the current one.
func TestReader_SupersededBy_ReadsTheTokenEachTime(t *testing.T) {
	var gotToken string
	srv := fakeAPI(t, http.StatusOK, `{"metadata":{}}`, &gotToken)
	dir := serviceAccountDir(t, srv, "first")
	r := newReader(t, srv, dir)

	if _, err := r.SupersededBy(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("rotated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SupersededBy(t.Context()); err != nil {
		t.Fatal(err)
	}

	if gotToken != "Bearer rotated" {
		t.Errorf("Authorization = %q, want the rotated token", gotToken)
	}
}

// A read without a token fails rather than going out unauthenticated.
func TestReader_SupersededBy_NoToken(t *testing.T) {
	var gotToken string
	srv := fakeAPI(t, http.StatusOK, `{"metadata":{}}`, &gotToken)
	dir := serviceAccountDir(t, srv, "sa-token")
	r := newReader(t, srv, dir)
	if err := os.Remove(filepath.Join(dir, "token")); err != nil {
		t.Fatal(err)
	}

	if _, err := r.SupersededBy(t.Context()); err == nil {
		t.Fatal("want an error without a token")
	}
	if gotToken != "" {
		t.Errorf("a request went out: Authorization = %q", gotToken)
	}
}

func TestNewInCluster_RefusesAMissingCA(t *testing.T) {
	if _, err := NewInCluster(InClusterAPIURL, t.TempDir(), "ci", "build-old"); err == nil {
		t.Fatal("want an error without a CA")
	}
}

func TestNewInCluster_RefusesACAWithoutACertificate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewInCluster(InClusterAPIURL, dir, "ci", "build-old"); err == nil {
		t.Fatal("want an error for a CA file with no certificate")
	}
}

// The namespace and workflow are path segments: they are escaped, never spliced into the path.
func TestNewInCluster_EscapesThePath(t *testing.T) {
	var gotPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"metadata":{}}`))
	}))
	t.Cleanup(srv.Close)
	r, err := NewInCluster(srv.URL+"/", serviceAccountDir(t, srv, "sa-token"), "ci", "a/b")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.SupersededBy(t.Context()); err != nil {
		t.Fatal(err)
	}

	if want := "/apis/argoproj.io/v1alpha1/namespaces/ci/workflows/a%2Fb"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// failingClose is a 200 answer carrying an unmarked workflow whose body fails to close.
type failingClose struct{}

func (failingClose) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: closeErrBody{strings.NewReader(`{"metadata":{}}`)}}, nil
}

type closeErrBody struct{ io.Reader }

func (closeErrBody) Close() error { return errors.New("connection reset") }

// A read whose body fails to close is not reported as unmarked: the caller sees the error.
func TestReader_SupersededBy_CloseError(t *testing.T) {
	var gotToken string
	srv := fakeAPI(t, http.StatusOK, `{"metadata":{}}`, &gotToken)
	r := newReader(t, srv, serviceAccountDir(t, srv, "sa-token"))
	r.client.Transport = failingClose{}

	if _, err := r.SupersededBy(t.Context()); err == nil {
		t.Fatal("want the close error")
	}
}

// The read asks the API server for the workflow's metadata only (PartialObjectMetadata, which it serves for custom
// resources too), so a workflow with a large node status costs no more than one without. Plain JSON stays acceptable:
// a server that does not convert answers with the whole workflow, whose metadata decodes the same way.
func TestReader_SupersededBy_AsksForMetadataOnly(t *testing.T) {
	const wantAccept = "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1,application/json"
	var gotAccept string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1")
		_, _ = w.Write([]byte(`{"kind":"PartialObjectMetadata","apiVersion":"meta.k8s.io/v1",` +
			`"metadata":{"name":"build-old","annotations":{"ci.mycarrier.tech/superseded-by":"build-new"}}}`))
	}))
	t.Cleanup(srv.Close)
	r := newReader(t, srv, serviceAccountDir(t, srv, "sa-token"))

	mark, err := r.SupersededBy(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if gotAccept != wantAccept {
		t.Errorf("Accept = %q, want %q", gotAccept, wantAccept)
	}
	if mark != (Mark{By: "build-new"}) {
		t.Errorf("mark = %+v, want the PartialObjectMetadata answer's mark", mark)
	}
}
