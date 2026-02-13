package argocdclient

import (
	"encoding/json"
	"fmt"
)

// GetArgoApplicationResourceTree gets the resource tree from ArgoCD (for health status in v3.0+).
// It uses the retryable HTTP client with exponential backoff to handle transient failures,
// consistent with GetApplication and GetManifests.
func (c *Client) GetArgoApplicationResourceTree(argoAppName string) (map[string]interface{}, error) {
	apiUrl := fmt.Sprintf("%v/api/v1/applications/%v/resource-tree", c.baseUrl, argoAppName)

	body, err := c.doGET(apiUrl)
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
