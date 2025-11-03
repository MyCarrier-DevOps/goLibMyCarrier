package argocdclient

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/hashicorp/go-retryablehttp"
)

// GetManifests retrieves application manifests from ArgoCD for the specified application.
// If revision is provided, it fetches manifests for that specific revision; otherwise, it gets the current manifests.
// It uses the retryable HTTP client with exponential backoff and returns a slice of manifest strings or an error.
func (c *Client) GetManifests(revision, argoAppName string) ([]string, error) {
	var apiUrl string
	if revision == "" {
		apiUrl = fmt.Sprintf("%v/api/v1/applications/%v/manifests", c.baseUrl, argoAppName)
	} else {
		apiUrl = fmt.Sprintf("%v/api/v1/applications/%v/manifests?revision=%v", c.baseUrl, argoAppName, revision)
	}

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
			return nil, fmt.Errorf("error reading body: %w", err)
		}
		return nil, fmt.Errorf("client error %d: %s", resp.StatusCode, string(body))
	}

	// Handle any remaining 5xx errors that exhausted retries
	if resp.StatusCode >= 500 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error reading body: %w", err)
		}
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
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
