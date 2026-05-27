package argocdclient

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetArgoApplicationResourceTree gets the resource tree from ArgoCD (for health status in v3.0+).
// It uses the retryable HTTP client with exponential backoff to handle transient failures,
// consistent with GetApplication and GetManifests.
//
// Backward-compatibility shim: delegates to GetArgoApplicationResourceTreeWithContext
// with context.Background(). Prefer GetArgoApplicationResourceTreeWithContext in new
// code to bound HTTP latency by your own deadline.
func (c *Client) GetArgoApplicationResourceTree(argoAppName string) (map[string]interface{}, error) {
	return c.GetArgoApplicationResourceTreeWithContext(context.Background(), argoAppName)
}

// GetArgoApplicationResourceTreeWithContext is the context-aware variant of
// GetArgoApplicationResourceTree. The request honors ctx cancellation/deadline.
// Prefer this over GetArgoApplicationResourceTree in new code; the no-ctx variant
// is retained for backward compatibility and passes context.Background() internally.
func (c *Client) GetArgoApplicationResourceTreeWithContext(
	ctx context.Context,
	argoAppName string,
) (map[string]interface{}, error) {
	apiUrl := fmt.Sprintf("%v/api/v1/applications/%v/resource-tree", c.baseUrl, argoAppName)

	body, err := c.doGET(ctx, apiUrl)
	if err != nil {
		return nil, err
	}

	var resourceTree map[string]interface{}
	err = json.Unmarshal(body, &resourceTree)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling ArgoCD resource tree data: %w", err)
	}

	return resourceTree, nil
}
