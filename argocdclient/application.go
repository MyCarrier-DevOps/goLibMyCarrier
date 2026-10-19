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

// ApplicationRef identifies one Application by the namespace it lives in and its name. On an
// instance with "application.namespaces" set, a name can repeat across namespaces.
type ApplicationRef struct{ Namespace, Name string }

// applicationListPath asks ArgoCD for the name and namespace of every Application the token can
// see. The fields projection keeps the response small on an instance with thousands of
// Applications; a server that ignores it answers with the full objects, which ListApplications
// decodes the same way.
const applicationListPath = "/api/v1/applications?fields=items.metadata.name,items.metadata.namespace"

// ListApplications returns every Application the token can see, sorted by name, then namespace.
//
// ArgoCD answers HTTP 200, not HTTP 403, and leaves out each Application the token may not get
// (policy "applications, get"). So a token with no such policy gets an empty list, and a token
// scoped to some projects gets a short list. Neither is an error, and the result does not show
// which case applies; a caller that treats an empty or short result as an error must say so
// itself. When there are no Applications the result is an empty, non-nil slice and the error is
// nil.
//
// When ArgoCD has "application.namespaces" set, the list also holds Applications outside the
// control-plane namespace, and a name can repeat. ListRollouts, GetLiveResource and Router read
// only Applications in the control-plane namespace, so a caller skips any other Application on
// purpose: ListRollouts would return ErrPermissionDenied for it, or read the Application of the
// same name in the control-plane namespace. The caller knows its install namespace.
//
// An HTTP answer of 400 or above that is not retried wraps *APIError, so errors.Is reaches
// ErrPermissionDenied; see APIError for the statuses that are not retried. A cancelled or expired
// ctx surfaces as the context error.
func (c *Client) ListApplications(ctx context.Context) ([]ApplicationRef, error) {
	body, err := c.doGET(ctx, c.baseUrl+applicationListPath)
	if err != nil {
		return nil, fmt.Errorf("error listing applications: %w", err)
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("error decoding applications list: %w", err)
	}

	refs := make([]ApplicationRef, 0, len(list.Items))
	for _, item := range list.Items {
		refs = append(refs, ApplicationRef{Namespace: item.Metadata.Namespace, Name: item.Metadata.Name})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Namespace < refs[j].Namespace
	})
	return refs, nil
}
