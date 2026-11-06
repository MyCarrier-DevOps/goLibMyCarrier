// Package workflowmark reads an Argo Workflows workflow's supersede mark from the Kubernetes API.
//
// When a newer run of the same concurrency key supersedes an older one, it annotates the older run
// with its own name and, when it has them, the correlation id and commit of the work it carries. A
// process inside the older run reads the mark immediately before a write that only the current run
// may make, and holds back once it is set. The reader authenticates as the pod's service account,
// which needs get on workflows in its namespace.
package workflowmark

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InClusterAPIURL and InClusterServiceAccountDir are where a pod reaches the Kubernetes API and
// finds its service account's ca.crt and token.
const (
	InClusterAPIURL            = "https://kubernetes.default.svc"
	InClusterServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// The annotations a superseding run writes on the run it supersedes: its name, and the correlation
// id and commit of the work it carries.
const (
	supersededByAnnotation              = "ci.mycarrier.tech/superseded-by"
	supersededByCorrelationIDAnnotation = "ci.mycarrier.tech/superseded-by-correlation-id"
	supersededByCommitSHAAnnotation     = "ci.mycarrier.tech/superseded-by-commit-sha"
)

// requestTimeout bounds one read. A caller usually holds a write on it, so it stays short.
const requestTimeout = 10 * time.Second

// maxBody bounds the workflow read. A running workflow's status carries every node, so it is
// generous.
const maxBody = 32 << 20

// Mark is a workflow's supersede mark: the run that superseded it ("" while none has, or when the
// workflow's template is not preemptible), and that run's correlation id and commit ("" when the
// mark carries none).
type Mark struct {
	By              string
	ByCorrelationID string
	ByCommitSHA     string
}

// Reader reads the supersede mark of one workflow.
type Reader struct {
	client    *http.Client
	url       string
	tokenFile string
}

// NewInCluster returns a Reader of the workflow in namespace, authenticated as the pod's service
// account: serviceAccountDir holds its ca.crt and token. apiURL is the Kubernetes API's base URL.
// Pods pass InClusterAPIURL and InClusterServiceAccountDir.
func NewInCluster(apiURL, serviceAccountDir, namespace, workflow string) (*Reader, error) {
	ca, err := os.ReadFile(filepath.Join(serviceAccountDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("read the service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("the service account CA holds no certificate")
	}
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: requestTimeout,
		ForceAttemptHTTP2:   true,
	}
	return &Reader{
		client: &http.Client{Timeout: requestTimeout, Transport: transport},
		url: strings.TrimSuffix(apiURL, "/") + "/apis/argoproj.io/v1alpha1/namespaces/" +
			url.PathEscape(namespace) + "/workflows/" + url.PathEscape(workflow),
		tokenFile: filepath.Join(serviceAccountDir, "token"),
	}, nil
}

// SupersededBy returns the workflow's mark as it stands now.
func (r *Reader) SupersededBy(ctx context.Context) (Mark, error) {
	// Read on every call: the projected token rotates.
	token, err := os.ReadFile(r.tokenFile)
	if err != nil {
		return Mark{}, fmt.Errorf("read the service account token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, http.NoBody)
	if err != nil {
		return Mark{}, fmt.Errorf("build the workflow read: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return Mark{}, fmt.Errorf("read the workflow: %w", err)
	}
	annotations, err := decodeAnnotations(resp)
	if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("close the workflow read: %w", closeErr)
	}
	if err != nil {
		return Mark{}, err
	}
	return Mark{
		By:              annotations[supersededByAnnotation],
		ByCorrelationID: annotations[supersededByCorrelationIDAnnotation],
		ByCommitSHA:     annotations[supersededByCommitSHAAnnotation],
	}, nil
}

// decodeAnnotations returns the annotations of the workflow in a 200 answer.
func decodeAnnotations(resp *http.Response) (map[string]string, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read the workflow: HTTP %d", resp.StatusCode)
	}
	var wf struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&wf); err != nil {
		return nil, fmt.Errorf("decode the workflow: %w", err)
	}
	return wf.Metadata.Annotations, nil
}
