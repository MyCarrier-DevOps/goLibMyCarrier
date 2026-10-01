package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ResourceAction names an Argo Rollouts action that ArgoCD can run on a live resource.
// Only the four actions below are accepted, and only on argoproj.io Rollouts; any
// other action or resource is refused before any HTTP call.
type ResourceAction string

const (
	// ActionAbort sets status.abort on the Rollout. A repeat never changes the
	// Rollout, but ArgoCD stops offering abort once the Rollout is aborted or fully
	// promoted, so a repeat then returns ErrConflict; RolloutStatus.Aborted tells
	// the two cases apart.
	ActionAbort ResourceAction = "abort"
	// ActionPromoteFull skips the Rollout's remaining canary steps. Once the
	// Rollout is fully promoted, a repeat returns ErrConflict.
	ActionPromoteFull ResourceAction = "promote-full"
	// ActionRetry clears an abort so the Rollout tries again. Once the abort is
	// cleared, a repeat returns ErrConflict.
	ActionRetry ResourceAction = "retry"
	// ActionResume clears the Rollout's pause conditions. It is NOT idempotent:
	// a repeated resume can release the next `pause: {}` step.
	ActionResume ResourceAction = "resume"
)

// supported reports whether a is one of the actions this package runs.
func (a ResourceAction) supported() bool {
	switch a {
	case ActionAbort, ActionPromoteFull, ActionRetry, ActionResume:
		return true
	}
	return false
}

// availableActions is the ArgoCD action discovery response.
type availableActions struct {
	Actions []struct {
		Name     string `json:"name"`
		Disabled bool   `json:"disabled"`
	} `json:"actions"`
}

// offers reports whether action is listed and enabled.
func (a availableActions) offers(action ResourceAction) bool {
	for _, candidate := range a.Actions {
		if candidate.Name == string(action) {
			return !candidate.Disabled
		}
	}
	return false
}

// runActionRequest is the body of the ArgoCD resource action (v2) call.
type runActionRequest struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	ResourceName string `json:"resourceName"`
	Version      string `json:"version"`
	Group        string `json:"group"`
	Kind         string `json:"kind"`
	Action       string `json:"action"`
}

// runResourceAction runs action on ref inside the Application appName.
//
// ArgoCD does not enforce `disabled` server-side, so the action is first
// checked against action discovery; an action that is absent or disabled
// returns an error wrapping ErrConflict and nothing is posted.
func (c *Client) runResourceAction(ctx context.Context, appName string, ref ResourceRef, action ResourceAction) error {
	if appName == "" {
		return errors.New("application name is required")
	}
	if ref.Name == "" || ref.Version == "" {
		return errors.New("resource name and version are required")
	}
	if ref.Group != rolloutGroup || ref.Kind != rolloutKind {
		return fmt.Errorf(
			"actions are limited to %s %s resources, got %s %q",
			rolloutGroup,
			rolloutKind,
			ref.Group,
			ref.Kind,
		)
	}
	if !action.supported() {
		return fmt.Errorf("unsupported action %q: must be one of %s, %s, %s, %s",
			action, ActionAbort, ActionPromoteFull, ActionRetry, ActionResume)
	}

	appPath := c.applicationURL(appName)

	body, err := c.doGET(ctx, appPath+"/resource/actions?"+ref.query().Encode())
	if err != nil {
		return fmt.Errorf("error listing actions for %s %s/%s: %w", ref.Kind, ref.Namespace, ref.Name, err)
	}

	var available availableActions
	if err := json.Unmarshal(body, &available); err != nil {
		return fmt.Errorf("error decoding available actions: %w", err)
	}
	if !available.offers(action) {
		return fmt.Errorf("%w: action %q is not available on %s %s/%s",
			ErrConflict, action, ref.Kind, ref.Namespace, ref.Name)
	}

	payload, err := json.Marshal(runActionRequest{
		Name:         appName,
		Namespace:    ref.Namespace,
		ResourceName: ref.Name,
		Version:      ref.Version,
		Group:        ref.Group,
		Kind:         ref.Kind,
		Action:       string(action),
	})
	if err != nil {
		return fmt.Errorf("error encoding action request: %w", err)
	}

	if _, err := c.doPOST(ctx, appPath+"/resource/actions/v2", payload); err != nil {
		return fmt.Errorf("error running action %q on %s %s/%s: %w", action, ref.Kind, ref.Namespace, ref.Name, err)
	}
	return nil
}
