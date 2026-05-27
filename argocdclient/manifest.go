package argocdclient

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetManifests retrieves application manifests from ArgoCD for the specified application.
// If revision is provided, it fetches manifests for that specific revision; otherwise, it gets the current manifests.
// It uses the retryable HTTP client with exponential backoff and returns a slice of manifest strings or an error.
//
// Backward-compatibility shim: delegates to GetManifestsWithContext with
// context.Background(). Prefer GetManifestsWithContext in new code to bound
// HTTP latency by your own deadline.
func (c *Client) GetManifests(revision, argoAppName string) ([]string, error) {
	return c.GetManifestsWithContext(context.Background(), revision, argoAppName)
}

// GetManifestsWithContext is the context-aware variant of GetManifests.
// The request honors ctx cancellation/deadline. Prefer this over GetManifests
// in new code; the no-ctx variant is retained for backward compatibility and
// passes context.Background() internally.
func (c *Client) GetManifestsWithContext(ctx context.Context, revision, argoAppName string) ([]string, error) {
	var apiUrl string
	if revision == "" {
		apiUrl = fmt.Sprintf("%v/api/v1/applications/%v/manifests", c.baseUrl, argoAppName)
	} else {
		apiUrl = fmt.Sprintf("%v/api/v1/applications/%v/manifests?revision=%v", c.baseUrl, argoAppName, revision)
	}

	body, err := c.doGET(ctx, apiUrl)
	if err != nil {
		return nil, err
	}

	// ArgoCD API returns manifests wrapped in an object: {"manifests":[...]}
	var response struct {
		Manifests []string `json:"manifests"`
	}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling manifest data: %w", err)
	}

	return response.Manifests, nil
}
