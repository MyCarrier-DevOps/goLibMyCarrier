package argocdclient

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/hashicorp/go-retryablehttp"
)

// GetApplication retrieves ArgoCD application data for the specified application.
// It uses the retryable HTTP client with exponential backoff to handle transient failures.
// Returns the application data as a map or an error if the operation fails after all retries.
func (c *Client) GetApplication(argoAppName string) (map[string]interface{}, error) {
	apiUrl := fmt.Sprintf("%v/api/v1/applications/%v?refresh=soft", c.baseUrl, argoAppName)

	req, err := retryablehttp.NewRequest("GET", apiUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.retryableClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Log the error but don't override the main error
			_ = closeErr
		}
	}()

	// Handle 4xx client errors (these weren't retried)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading body %w", err)
		}
		return nil, fmt.Errorf("client error %d: %s", resp.StatusCode, string(body))
	}

	// Handle any remaining 5xx errors that exhausted retries
	if resp.StatusCode >= 500 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading body %w", err)
		}
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	var appData map[string]interface{}
	err = json.Unmarshal(body, &appData)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling ArgoCD application data: %w", err)
	}

	return appData, nil
}
