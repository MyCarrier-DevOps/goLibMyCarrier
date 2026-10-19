package argocdclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

// applicationListPath asks ArgoCD for the name of every Application the token can see. The fields
// projection keeps the response small on an instance with thousands of Applications; a server that
// ignores it answers with the full objects, which ListApplications decodes the same way.
const applicationListPath = "/api/v1/applications?fields=items.metadata.name"

// ListApplications returns the names of every Application the token can see, sorted.
//
// ArgoCD answers HTTP 200 with an empty list, not HTTP 403, for a token that is not allowed to get
// any Application (policy "applications, get"), so an empty result can mean "nothing to see" or
// "no permission"; callers that treat an empty result as an error must say so themselves. When
// there are no Applications the result is an empty, non-nil slice and the error is nil.
//
// An HTTP answer of 400 or above that is not retried wraps *APIError, so errors.Is reaches
// ErrPermissionDenied; see APIError for the statuses that are not retried. A cancelled or expired ctx surfaces as
// the context error.
func (c *Client) ListApplications(ctx context.Context) ([]string, error) {
	body, err := c.doGET(ctx, c.baseUrl+applicationListPath)
	if err != nil {
		return nil, fmt.Errorf("error listing applications: %w", err)
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("error decoding applications list: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Name)
	}
	sort.Strings(names)
	return names, nil
}
