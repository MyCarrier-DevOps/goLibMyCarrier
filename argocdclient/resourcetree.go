package argocdclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GetArgoApplicationResourceTree gets the resource tree from ArgoCD (for health status in v3.0+)
func (c *Client) GetArgoApplicationResourceTree(argoAppName string) (map[string]interface{}, error) {
	const maxRetries = 3
	const baseDelay = time.Second

	var lastErr error

	for attempt := range maxRetries {
		if attempt > 0 {
			// Exponential backoff: wait 1s, 2s, 4s
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			time.Sleep(delay)
		}

		client := &http.Client{}
		apiUrl := fmt.Sprintf("%v/api/v1/applications/%v/resource-tree", c.baseUrl, argoAppName)
		req, err := http.NewRequest("GET", apiUrl, nil)
		if err != nil {
			lastErr = fmt.Errorf("error creating request: %w", err)
			continue
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("error making request (attempt %d/%d): %w", attempt+1, maxRetries, err)
			continue
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				lastErr = fmt.Errorf("error closing response body: %w", closeErr)
			}
		}()

		// Check for HTTP error status codes
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error %d (attempt %d/%d)", resp.StatusCode, attempt+1, maxRetries)
			continue
		} else if resp.StatusCode >= 400 {
			// Client errors (4xx) shouldn't be retried
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("client error %d: %s", resp.StatusCode, string(body))
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("error reading response body (attempt %d/%d): %w", attempt+1, maxRetries, err)
			continue
		}

		var resourceTree map[string]interface{}
		err = json.Unmarshal(body, &resourceTree)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling ArgoCD resource tree data: %w", err)
		}

		return resourceTree, nil
	}

	return nil, fmt.Errorf("failed after %d attempts, last error: %w", maxRetries, lastErr)
}
