package argocdclient

import (
	"context"
	"errors"
	"fmt"
)

// ErrInstanceNotConfigured is returned when an application routes to an ArgoCD
// instance that has no usable configuration (missing config, server URL or token).
var ErrInstanceNotConfigured = errors.New("argocd: instance not configured")

// Router holds one Client per configured ArgoCD Instance and sends every call
// for an application to the instance RouteInstance picks for it.
//
// Routing fails closed: an application whose instance is not configured is
// refused with ErrInstanceNotConfigured, never served by another instance.
type Router struct {
	clients map[Instance]*Client
}

// NewRouter builds a Router from per-instance configs. A nil config, or one
// with an empty ServerUrl or AuthToken, leaves that instance unconfigured.
func NewRouter(configs map[Instance]*Config) *Router {
	clients := make(map[Instance]*Client, len(configs))
	for instance, config := range configs {
		if config == nil || config.ServerUrl == "" || config.AuthToken == "" {
			continue
		}
		clients[instance] = NewClient(config)
	}
	return &Router{clients: clients}
}

// ClientFor returns the Client of the instance appName routes to. If that
// instance is not configured it returns an error wrapping ErrInstanceNotConfigured.
func (r *Router) ClientFor(appName string) (*Client, error) {
	instance := RouteInstance(appName)
	client, ok := r.clients[instance]
	if !ok {
		return nil, fmt.Errorf("%w: %s routes to %s", ErrInstanceNotConfigured, appName, instance)
	}
	return client, nil
}

// ListRolloutGroup lists the Rollouts labeled with correlationID in the Application appName,
// on the instance appName routes to. See Client.ListRolloutGroup. An unconfigured instance
// returns ErrInstanceNotConfigured before any HTTP call.
func (r *Router) ListRolloutGroup(ctx context.Context, appName, correlationID string) ([]RolloutStatus, error) {
	client, err := r.ClientFor(appName)
	if err != nil {
		return nil, err
	}
	return client.ListRolloutGroup(ctx, appName, correlationID)
}

// RunResourceAction runs action on the live resource ref inside the Application
// appName, on the instance appName routes to, using that instance's token.
//
// Actions are reachable only through Router (Client has no exported action
// method), so an action can never be sent with another instance's credentials.
// An unconfigured instance returns ErrInstanceNotConfigured before any HTTP call.
// The action is checked against ArgoCD's action discovery first: an action the
// resource does not offer returns an error wrapping ErrConflict. The action
// itself is sent once and never retried.
func (r *Router) RunResourceAction(
	ctx context.Context,
	appName string,
	ref ResourceRef,
	action ResourceAction,
) error {
	client, err := r.ClientFor(appName)
	if err != nil {
		return err
	}
	return client.runResourceAction(ctx, appName, ref, action)
}
