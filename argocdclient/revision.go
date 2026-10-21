package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// RevisionMetadata is the commit metadata ArgoCD reports for a Git revision of an Application's source.
type RevisionMetadata struct {
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Tags    []string  `json:"tags"`
	Message string    `json:"message"`
}

// GetRevisionMetadata returns the commit metadata of revision in the Git repository of the Application's source
// (GET /api/v1/applications/{name}/revisions/{revision}/metadata). Date is the commit time, so a caller can tell
// whether one revision of a shared GitOps repository is later than another. ArgoCD answers only for a Git source:
// a Helm chart version gets an error.
//
// An HTTP answer of 400 or above that is not retried wraps *APIError, so errors.Is reaches ErrPermissionDenied and
// ErrNotFound; see APIError for the statuses that are not retried. A cancelled or expired ctx surfaces as the
// context error.
func (c *Client) GetRevisionMetadata(ctx context.Context, appName, revision string) (RevisionMetadata, error) {
	if appName == "" {
		return RevisionMetadata{}, errors.New("application name is required")
	}
	if revision == "" {
		return RevisionMetadata{}, errors.New("revision is required")
	}
	body, err := c.doGET(ctx, c.applicationURL(appName)+"/revisions/"+url.PathEscape(revision)+"/metadata")
	if err != nil {
		return RevisionMetadata{}, fmt.Errorf("error getting metadata of revision %s: %w", revision, err)
	}
	var meta RevisionMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return RevisionMetadata{}, fmt.Errorf("error decoding metadata of revision %s: %w", revision, err)
	}
	return meta, nil
}
