package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// commitSHA is the revision ArgoCD's repo-server resolves for revision metadata (util/git/git.go
// truncatedCommitSHARegex): a full or abbreviated commit SHA of at least 7 hex digits, SHA-1 or SHA-256.
var commitSHA = regexp.MustCompile(`^[0-9A-Fa-f]{7,}$`)

// RevisionMetadata is the commit metadata ArgoCD reports for a Git revision of an Application's source.
type RevisionMetadata struct {
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Tags    []string  `json:"tags"`
	Message string    `json:"message"`
}

// GetRevisionMetadata returns the commit metadata of revision in the Git repository of the Application's source
// (GET /api/v1/applications/{name}/revisions/{revision}/metadata). revision must be a commit SHA of at least 7 hex
// digits, the only form ArgoCD resolves here; anything else is an error and sends no request.
//
// Date is the git author timestamp (%at) at one-second resolution, not the committer date: commits in the same
// second share a Date, and a rebased or cherry-picked commit keeps its original author date, so Date is not
// guaranteed to follow history order. ArgoCD sends the Unix epoch (1970-01-01T00:00:00Z) when it cannot read the
// timestamp; treat it and the zero Date as unknown.
//
// For a multi-source Application ArgoCD uses the first entry of spec.sources (this client sends no sourceIndex).
//
// Errors: a 500 is not retried and returns *APIError: ArgoCD answers 500 for a commit the repository does not have
// and for a Helm or OCI first source, and every attempt would make the repo-server fetch the repository again. 403
// matches ErrPermissionDenied (also a missing Application) and 404 ErrNotFound (the repository was not found); see
// APIError for the other statuses. A cancelled or expired ctx surfaces as the context error.
func (c *Client) GetRevisionMetadata(ctx context.Context, appName, revision string) (RevisionMetadata, error) {
	if appName == "" {
		return RevisionMetadata{}, errors.New("application name is required")
	}
	if revision == "" {
		return RevisionMetadata{}, errors.New("revision is required")
	}
	if !commitSHA.MatchString(revision) {
		return RevisionMetadata{}, fmt.Errorf("invalid revision %q: want a commit SHA of 7+ hex digits", revision)
	}
	apiURL := c.applicationURL(appName) + "/revisions/" + url.PathEscape(revision) + "/metadata"
	body, err := c.doGET(withoutServerErrorRetry(ctx), apiURL)
	if err != nil {
		return RevisionMetadata{}, fmt.Errorf("error getting metadata of revision %s: %w", revision, err)
	}
	var meta RevisionMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return RevisionMetadata{}, fmt.Errorf("error decoding metadata of revision %s: %w", revision, err)
	}
	return meta, nil
}
