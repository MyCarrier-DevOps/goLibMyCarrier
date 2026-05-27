package argocdclient

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetApplication retrieves ArgoCD application data for the specified application.
// It uses the retryable HTTP client with exponential backoff to handle transient failures.
// Returns the application data as a map or an error if the operation fails after all retries.
//
// Backward-compatibility shim: delegates to GetApplicationWithContext with
// context.Background(). Prefer GetApplicationWithContext in new code to bound
// HTTP latency by your own deadline.
func (c *Client) GetApplication(argoAppName string) (map[string]interface{}, error) {
	return c.GetApplicationWithContext(context.Background(), argoAppName)
}

// GetApplicationWithContext is the context-aware variant of GetApplication.
// The request honors ctx cancellation/deadline. Prefer this over GetApplication
// in new code; the no-ctx variant is retained for backward compatibility and
// passes context.Background() internally.
func (c *Client) GetApplicationWithContext(ctx context.Context, argoAppName string) (map[string]interface{}, error) {
	apiUrl := fmt.Sprintf("%v/api/v1/applications/%v?refresh=soft", c.baseUrl, argoAppName)

	body, err := c.doGET(ctx, apiUrl)
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
