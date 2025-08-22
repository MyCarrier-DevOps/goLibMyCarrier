package argocdclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GetArgoApplication retrieves ArgoCD application data for the specified application.
// It implements retry logic with exponential backoff to handle transient failures.
// Returns the application data as a map or an error if the operation fails after all retries.
func GetArgoApplication(revision string, config *Config) (map[string]interface{}, error) {
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
		apiUrl := fmt.Sprintf("%v/api/v1/applications/%v?refresh=soft", config.ServerUrl, config.AppName)
		req, err := http.NewRequest("GET", apiUrl, nil)
		if err != nil {
			lastErr = fmt.Errorf("error creating request: %w", err)
			continue
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.AuthToken))
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("error making request (attempt %d/%d): %w", attempt+1, maxRetries, err)
			continue
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				lastErr = fmt.Errorf("error closing response body: %w", err)
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

		var appData map[string]interface{}
		err = json.Unmarshal(body, &appData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling ArgoCD application data: %w", err)
		}

		return appData, nil
	}

	return nil, fmt.Errorf("failed after %d attempts, last error: %w", maxRetries, lastErr)
}

// GetManifests retrieves application manifests from ArgoCD for the specified application.
// If revision is provided, it fetches manifests for that specific revision; otherwise, it gets the current manifests.
// It implements retry logic with exponential backoff and returns a slice of manifest strings or an error.
func GetManifests(revision string, config *Config) ([]string, error) {
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
		var apiUrl string
		if revision == "" {
			apiUrl = fmt.Sprintf("%v/api/v1/applications/%v/manifests", config.ServerUrl, config.AppName)
		} else {
			apiUrl = fmt.Sprintf("%v/api/v1/applications/%v/manifests?revision=%v", config.ServerUrl, config.AppName, revision)
		}
		req, err := http.NewRequest("GET", apiUrl, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %w", err)
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.AuthToken))
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("error making request (attempt %d/%d): %w", attempt+1, maxRetries, err)
			continue
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				lastErr = fmt.Errorf("error closing response body: %w", err)
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

		var manifestData []string
		err = json.Unmarshal(body, &manifestData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling ArgoCD application data: %w", err)
		}

		return manifestData, nil
	}
	return nil, fmt.Errorf("failed after %d attempts, last error: %w", maxRetries, lastErr)
}
