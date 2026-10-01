package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
)

// CorrelationIDLabel is the Rollout label that carries the pipeline correlation ID.
const CorrelationIDLabel = "mycarrier.tech/correlationId"

// Coordinates of the Argo Rollouts custom resource inside an ArgoCD Application.
const (
	rolloutGroup   = "argoproj.io"
	rolloutVersion = "v1alpha1"
	rolloutKind    = "Rollout"
)

// defaultMaxTrafficWeight is the canary weight of a fully promoted Rollout
// when spec.strategy.canary.trafficRouting.maxTrafficWeight is not set.
const defaultMaxTrafficWeight int32 = 100

// ResourceRef identifies a live resource inside an ArgoCD Application.
type ResourceRef struct {
	Group     string
	Version   string
	Kind      string
	Namespace string
	Name      string
}

// query returns the query parameters ArgoCD's resource and action endpoints use to address ref.
func (r ResourceRef) query() url.Values {
	return url.Values{
		"namespace":    {r.Namespace},
		"resourceName": {r.Name},
		"version":      {r.Version},
		"group":        {r.Group},
		"kind":         {r.Kind},
	}
}

// HealthStatus is an ArgoCD health code, as reported on a resource-tree node.
type HealthStatus string

// ArgoCD health codes.
const (
	HealthStatusHealthy     HealthStatus = "Healthy"
	HealthStatusProgressing HealthStatus = "Progressing"
	HealthStatusSuspended   HealthStatus = "Suspended"
	HealthStatusDegraded    HealthStatus = "Degraded"
	HealthStatusMissing     HealthStatus = "Missing"
	HealthStatusUnknown     HealthStatus = "Unknown"
)

// RolloutPhase is the Argo Rollouts status.phase of a Rollout.
type RolloutPhase string

// Argo Rollouts phases.
const (
	RolloutPhaseHealthy     RolloutPhase = "Healthy"
	RolloutPhaseProgressing RolloutPhase = "Progressing"
	RolloutPhasePaused      RolloutPhase = "Paused"
	RolloutPhaseDegraded    RolloutPhase = "Degraded"
)

// StepPluginPhase is the phase of a canary step plugin run.
type StepPluginPhase string

// Step plugin phases.
const (
	StepPluginPhaseRunning    StepPluginPhase = "Running"
	StepPluginPhaseSuccessful StepPluginPhase = "Successful"
	StepPluginPhaseFailed     StepPluginPhase = "Failed"
	StepPluginPhaseError      StepPluginPhase = "Error"
)

// StepPluginOperation is the operation a step plugin entry records.
type StepPluginOperation string

// Step plugin operations.
const (
	StepPluginOperationRun       StepPluginOperation = "Run"
	StepPluginOperationTerminate StepPluginOperation = "Terminate"
	StepPluginOperationAbort     StepPluginOperation = "Abort"
)

// StepPluginStatus is one entry of a Rollout's status.canary.stepPluginStatuses.
// Argo Rollouts keeps one entry per step and operation: an abort or a full promotion adds an
// Abort or Terminate entry next to the step's Run entry. An empty Phase on a Run entry means
// the plugin is globally disabled.
type StepPluginStatus struct {
	Index     int32               `json:"index"`
	Name      string              `json:"name"`
	Operation StepPluginOperation `json:"operation"`
	Phase     StepPluginPhase     `json:"phase"`
	Message   string              `json:"message"`
}

// RolloutStatus is the combined state of one Rollout in an Application.
//
// Ref, Health and Message come from the Application's resource-tree node.
// Every other field comes from the live Rollout manifest.
type RolloutStatus struct {
	// Ref identifies the Rollout (resource-tree node).
	Ref ResourceRef
	// Health is the ArgoCD health code (resource-tree node).
	Health HealthStatus
	// Message is the ArgoCD health message (resource-tree node).
	Message string
	// Phase is status.phase of the live Rollout.
	Phase RolloutPhase
	// CurrentStepIndex is status.currentStepIndex of the live Rollout; nil when unset.
	CurrentStepIndex *int32
	// Aborted is status.abort of the live Rollout.
	Aborted bool
	// CanaryWeight is the percentage of traffic on the canary: 0 when aborted; the max traffic
	// weight when no canary step is current; otherwise the traffic-router weight when one is
	// configured and reported, else the last setWeight at or before the current step.
	CanaryWeight int32
	// StepPluginStatuses is status.canary.stepPluginStatuses of the live Rollout.
	StepPluginStatuses []StepPluginStatus
}

// resourceNode is a node of the ArgoCD resource-tree response.
type resourceNode struct {
	Group     string     `json:"group"`
	Version   string     `json:"version"`
	Kind      string     `json:"kind"`
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	UID       string     `json:"uid"`
	Health    nodeHealth `json:"health"`
}

type nodeHealth struct {
	Status  HealthStatus `json:"status"`
	Message string       `json:"message"`
}

// liveRollout holds the parts of a live Rollout manifest this package reads.
type liveRollout struct {
	Metadata struct {
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Strategy struct {
			Canary *canaryStrategy `json:"canary"`
		} `json:"strategy"`
	} `json:"spec"`
	Status struct {
		Phase            RolloutPhase `json:"phase"`
		CurrentStepIndex *int32       `json:"currentStepIndex"`
		Abort            bool         `json:"abort"`
		Canary           struct {
			Weights *struct {
				Canary struct {
					Weight int32 `json:"weight"`
				} `json:"canary"`
			} `json:"weights"`
			StepPluginStatuses []StepPluginStatus `json:"stepPluginStatuses"`
		} `json:"canary"`
	} `json:"status"`
}

type canaryStrategy struct {
	Steps []struct {
		SetWeight *int32 `json:"setWeight"`
	} `json:"steps"`
	TrafficRouting *struct {
		MaxTrafficWeight *int32 `json:"maxTrafficWeight"`
	} `json:"trafficRouting"`
}

// decodeRollout parses the JSON manifest string ArgoCD returns for a live resource.
func decodeRollout(manifest string) (liveRollout, error) {
	var live liveRollout
	if err := json.Unmarshal([]byte(manifest), &live); err != nil {
		return liveRollout{}, fmt.Errorf("error decoding rollout manifest: %w", err)
	}
	return live, nil
}

// canaryWeight mirrors the actual weight reported by `kubectl argo rollouts`.
func canaryWeight(live liveRollout) int32 {
	if live.Status.Abort {
		return 0
	}

	canary := live.Spec.Strategy.Canary
	index := int32(0)
	if live.Status.CurrentStepIndex != nil {
		index = *live.Status.CurrentStepIndex
	}
	if canary == nil || len(canary.Steps) == 0 || int(index) >= len(canary.Steps) {
		return maxTrafficWeight(canary)
	}

	if canary.TrafficRouting != nil && live.Status.Canary.Weights != nil {
		return live.Status.Canary.Weights.Canary.Weight
	}

	for i := int(index); i >= 0; i-- {
		if w := canary.Steps[i].SetWeight; w != nil {
			return *w
		}
	}
	return 0
}

func maxTrafficWeight(canary *canaryStrategy) int32 {
	if canary != nil && canary.TrafficRouting != nil && canary.TrafficRouting.MaxTrafficWeight != nil {
		return *canary.TrafficRouting.MaxTrafficWeight
	}
	return defaultMaxTrafficWeight
}

// newRolloutStatus combines a resource-tree node with its live Rollout.
func newRolloutStatus(node resourceNode, live liveRollout) RolloutStatus {
	return RolloutStatus{
		Ref: ResourceRef{
			Group:     node.Group,
			Version:   node.Version,
			Kind:      node.Kind,
			Namespace: node.Namespace,
			Name:      node.Name,
		},
		Health:             node.Health.Status,
		Message:            node.Health.Message,
		Phase:              live.Status.Phase,
		CurrentStepIndex:   live.Status.CurrentStepIndex,
		Aborted:            live.Status.Abort,
		CanaryWeight:       canaryWeight(live),
		StepPluginStatuses: live.Status.Canary.StepPluginStatuses,
	}
}

// decodeRolloutNodes returns the Argo Rollouts nodes of an ArgoCD resource-tree response that
// have a live object. ArgoCD adds a node without a uid for a managed Rollout that does not exist
// in the cluster (not yet created, rejected by admission, or deleted out of band).
func decodeRolloutNodes(tree []byte) ([]resourceNode, error) {
	var parsed struct {
		Nodes []resourceNode `json:"nodes"`
	}
	if err := json.Unmarshal(tree, &parsed); err != nil {
		return nil, fmt.Errorf("error decoding resource tree: %w", err)
	}

	var rollouts []resourceNode
	for _, node := range parsed.Nodes {
		if node.Group == rolloutGroup && node.Kind == rolloutKind && node.UID != "" {
			rollouts = append(rollouts, node)
		}
	}
	return rollouts, nil
}

// getLiveRollout reads the live manifest of the Rollout behind node.
func (c *Client) getLiveRollout(ctx context.Context, appName string, node resourceNode) (liveRollout, error) {
	ref := ResourceRef{
		Group: node.Group, Version: node.Version, Kind: node.Kind, Namespace: node.Namespace, Name: node.Name,
	}
	body, err := c.doGET(ctx, c.applicationURL(appName)+"/resource?"+ref.query().Encode())
	if err != nil {
		return liveRollout{}, err
	}

	var envelope struct {
		Manifest string `json:"manifest"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return liveRollout{}, fmt.Errorf("error decoding resource response: %w", err)
	}
	return decodeRollout(envelope.Manifest)
}

// ListRolloutGroup returns the status of every Argo Rollout in the Application appName whose
// CorrelationIDLabel label equals correlationID, sorted by namespace and then name.
//
// The Rollouts come from the Application's resource tree; the label, phase, step index and
// canary weight come from each Rollout's live manifest. An empty correlationID is an error, so
// unlabeled Rollouts are never matched. A Rollout with no live object (not yet created, or deleted
// since ArgoCD last refreshed the tree) is skipped; any other failure to read a live Rollout fails
// the whole call.
// When no Rollout matches, the result is an empty, non-nil slice and the error is nil.
func (c *Client) ListRolloutGroup(ctx context.Context, appName, correlationID string) ([]RolloutStatus, error) {
	if correlationID == "" {
		return nil, errors.New("correlation id is required")
	}

	tree, err := c.doGET(ctx, c.applicationURL(appName)+"/resource-tree")
	if err != nil {
		return nil, fmt.Errorf("error reading resource tree of %s: %w", appName, err)
	}
	nodes, err := decodeRolloutNodes(tree)
	if err != nil {
		return nil, err
	}

	statuses := make([]RolloutStatus, 0, len(nodes))
	for _, node := range nodes {
		live, err := c.getLiveRollout(ctx, appName, node)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("error reading rollout %s/%s: %w", node.Namespace, node.Name, err)
		}
		if live.Metadata.Labels[CorrelationIDLabel] == correlationID {
			statuses = append(statuses, newRolloutStatus(node, live))
		}
	}

	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].Ref.Namespace != statuses[j].Ref.Namespace {
			return statuses[i].Ref.Namespace < statuses[j].Ref.Namespace
		}
		return statuses[i].Ref.Name < statuses[j].Ref.Name
	})
	return statuses, nil
}
