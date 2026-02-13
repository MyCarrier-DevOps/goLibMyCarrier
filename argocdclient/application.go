package argocdclient

import (
	"encoding/json"
	"fmt"
)

// GetApplication retrieves ArgoCD application data for the specified application.
// It uses the retryable HTTP client with exponential backoff to handle transient failures.
// Returns the application data as a map or an error if the operation fails after all retries.
func (c *Client) GetApplication(argoAppName string) (map[string]interface{}, error) {
	apiUrl := fmt.Sprintf("%v/api/v1/applications/%v?refresh=soft", c.baseUrl, argoAppName)

	body, err := c.doGET(apiUrl)
	if err != nil {
		return nil, err
	}

	var appData map[string]interface{}
	err = json.Unmarshal(body, &appData)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling ArgoCD application data: %w", err)
	}

	return appData, nil
}
