package argocdclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
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
	// CanaryWeight is the percentage of traffic on the canary. When the Rollout is aborted it is
	// the traffic-router weight when one is configured and reported (it steps down to 0 as the
	// controller reconciles), else 0. Otherwise it is 0 when the Rollout has no canary strategy
	// (blue-green); the max traffic weight when a canary strategy has no current step; else the
	// traffic-router weight when one is configured and reported, else the last setWeight at or
	// before the current step.
	CanaryWeight int32
	// StepPluginStatuses is status.canary.stepPluginStatuses of the live Rollout.
	StepPluginStatuses []StepPluginStatus
	// UID is metadata.uid of the live Rollout.
	UID string
	// StableRS is status.stableRS of the live Rollout.
	StableRS string
	// CurrentPodHash is status.currentPodHash of the live Rollout.
	CurrentPodHash string
	// Labels is metadata.labels of the live Rollout.
	Labels map[string]string
	// Annotations is metadata.annotations of the live Rollout. Under client-side apply it
	// may include kubectl.kubernetes.io/last-applied-configuration (the full applied spec);
	// do not log or forward the map wholesale.
	Annotations map[string]string
	// IstioVirtualServices lists the raw virtualService(s)[].name values of the canary traffic
	// routing as written in the Rollout, de-duplicated by raw string: a bare name (the Rollout's
	// namespace) or "name.namespace", optionally ending in ".cluster.local", interpreted as in
	// Argo Rollouts' GetVirtualServiceNamespaceName. Split it before using it as ResourceRef.Name.
	IstioVirtualServices []string
	// ManagedRoutes lists the names in spec.strategy.canary.trafficRouting.managedRoutes.
	ManagedRoutes []string
	// Strategy is the deployment strategy of the live Rollout: RolloutStrategyCanary when
	// spec.strategy.canary is present, else RolloutStrategyBlueGreen when spec.strategy.blueGreen
	// is present, else empty. When a Rollout carries both blocks (the API server accepts it; the
	// Argo Rollouts controller marks it InvalidSpec and its Phase is Degraded), Strategy reports "canary".
	Strategy string
	// Generation is metadata.generation of the live Rollout.
	Generation int64
	// ObservedGeneration is status.observedGeneration of the live Rollout, exactly as Argo
	// Rollouts writes it (a string, empty when unset). For a Rollout with an inline pod template,
	// the status reflects the current spec only when ObservedGeneration equals
	// strconv.FormatInt(Generation, 10); see StatusCurrent. This does not cover spec.workloadRef
	// Rollouts: a change to the referenced Deployment only patches the
	// rollout.argoproj.io/workload-generation annotation and does not move metadata.generation;
	// Argo tracks that in status.workloadObservedGeneration, which this type does not expose.
	// Current Argo Rollouts controllers write the plain generation number as a string. A missing
	// or empty value reports not reconciled, so callers must bound any retry on StatusCurrent.
	ObservedGeneration string
}

// Values of RolloutStatus.Strategy.
const (
	// RolloutStrategyCanary marks a Rollout whose spec.strategy has a canary block.
	RolloutStrategyCanary = "canary"
	// RolloutStrategyBlueGreen marks a Rollout whose spec.strategy has a blueGreen block.
	RolloutStrategyBlueGreen = "blueGreen"
)

// StatusCurrent reports whether ObservedGeneration equals strconv.FormatInt(Generation, 10).
// A missing or empty ObservedGeneration reports false (not reconciled), so callers must bound
// any retry on it. It does not cover spec.workloadRef Rollouts: a change to the referenced
// Deployment only patches the rollout.argoproj.io/workload-generation annotation and does not
// move metadata.generation; Argo tracks that in status.workloadObservedGeneration, which this
// type does not expose.
func (r RolloutStatus) StatusCurrent() bool {
	return r.ObservedGeneration == strconv.FormatInt(r.Generation, 10)
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
		UID         string            `json:"uid"`
		Generation  int64             `json:"generation"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Strategy struct {
			Canary    *canaryStrategy `json:"canary"`
			BlueGreen *struct{}       `json:"blueGreen"`
		} `json:"strategy"`
	} `json:"spec"`
	Status struct {
		Phase            RolloutPhase `json:"phase"`
		CurrentStepIndex *int32       `json:"currentStepIndex"`
		Abort            bool         `json:"abort"`
		StableRS         string       `json:"stableRS"`
		ObservedGen      string       `json:"observedGeneration"`
		CurrentPodHash   string       `json:"currentPodHash"`
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
		Istio            *struct {
			VirtualService *struct {
				Name string `json:"name"`
			} `json:"virtualService"`
			VirtualServices []struct {
				Name string `json:"name"`
			} `json:"virtualServices"`
		} `json:"istio"`
		ManagedRoutes []struct {
			Name string `json:"name"`
		} `json:"managedRoutes"`
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

// canaryWeight returns the percentage of traffic on the canary. For an aborted Rollout it is the
// router-confirmed weight when a traffic router reports one, because the weight steps down to 0
// as the controller reconciles, and 0 otherwise. For any other Rollout see RolloutStatus.CanaryWeight.
func canaryWeight(live liveRollout) int32 {
	canary := live.Spec.Strategy.Canary
	if canary == nil {
		return 0
	}

	if live.Status.Abort {
		if canary.TrafficRouting != nil && live.Status.Canary.Weights != nil {
			return live.Status.Canary.Weights.Canary.Weight
		}
		return 0
	}

	index := int32(0)
	if live.Status.CurrentStepIndex != nil {
		index = *live.Status.CurrentStepIndex
	}
	if len(canary.Steps) == 0 || int(index) >= len(canary.Steps) {
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
		Health:               node.Health.Status,
		Message:              node.Health.Message,
		Phase:                live.Status.Phase,
		CurrentStepIndex:     live.Status.CurrentStepIndex,
		Aborted:              live.Status.Abort,
		CanaryWeight:         canaryWeight(live),
		StepPluginStatuses:   live.Status.Canary.StepPluginStatuses,
		UID:                  live.Metadata.UID,
		StableRS:             live.Status.StableRS,
		CurrentPodHash:       live.Status.CurrentPodHash,
		Labels:               live.Metadata.Labels,
		Annotations:          live.Metadata.Annotations,
		IstioVirtualServices: istioVirtualServices(live.Spec.Strategy.Canary),
		ManagedRoutes:        managedRoutes(live.Spec.Strategy.Canary),
		Strategy:             strategyOf(live),
		Generation:           live.Metadata.Generation,
		ObservedGeneration:   live.Status.ObservedGen,
	}
}

// strategyOf returns the RolloutStrategy* value for the strategy block live declares, or "".
func strategyOf(live liveRollout) string {
	switch {
	case live.Spec.Strategy.Canary != nil:
		return RolloutStrategyCanary
	case live.Spec.Strategy.BlueGreen != nil:
		return RolloutStrategyBlueGreen
	}
	return ""
}

// istioVirtualServices returns the de-duplicated VirtualService names of the canary traffic
// routing: trafficRouting.istio.virtualService first, then trafficRouting.istio.virtualServices
// in list order. It is nil when neither is set.
func istioVirtualServices(canary *canaryStrategy) []string {
	if canary == nil || canary.TrafficRouting == nil || canary.TrafficRouting.Istio == nil {
		return nil
	}
	istio := canary.TrafficRouting.Istio

	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if istio.VirtualService != nil {
		add(istio.VirtualService.Name)
	}
	for _, vs := range istio.VirtualServices {
		add(vs.Name)
	}
	return names
}

// managedRoutes returns the names in trafficRouting.managedRoutes, nil when there are none.
func managedRoutes(canary *canaryStrategy) []string {
	if canary == nil || canary.TrafficRouting == nil {
		return nil
	}
	var names []string
	for _, route := range canary.TrafficRouting.ManagedRoutes {
		names = append(names, route.Name)
	}
	return names
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

// GetLiveResource returns the live manifest of the resource ref in the Application appName, as
// the JSON document ArgoCD holds for it. It works for any kind ResourceRef can name; for a
// core-group resource such as a ConfigMap, leave ref.Group empty. A 2xx answer whose manifest
// is absent or null yields an empty slice and a nil error; callers decoding it must handle that.
//
// An empty appName or ref.Name is an error and sends no request. appName and every ref field are
// escaped, so names with "/", "?", "&", "#" or spaces are sent intact. A resource ArgoCD does not
// have (HTTP 404, or HTTP 400 "not found as part of application") matches ErrNotFound; HTTP 403
// matches ErrPermissionDenied. A cancelled or expired ctx surfaces as the context error.
func (c *Client) GetLiveResource(ctx context.Context, appName string, ref ResourceRef) ([]byte, error) {
	if appName == "" {
		return nil, errors.New("application name is required")
	}
	if ref.Name == "" {
		return nil, errors.New("resource name is required")
	}

	body, err := c.doGET(ctx, c.applicationURL(appName)+"/resource?"+ref.query().Encode())
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Manifest string `json:"manifest"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("error decoding resource response: %w", err)
	}
	return []byte(envelope.Manifest), nil
}

// getLiveRollout reads the live manifest of the Rollout behind node.
func (c *Client) getLiveRollout(ctx context.Context, appName string, node resourceNode) (liveRollout, error) {
	ref := ResourceRef{
		Group: node.Group, Version: node.Version, Kind: node.Kind, Namespace: node.Namespace, Name: node.Name,
	}
	manifest, err := c.GetLiveResource(ctx, appName, ref)
	if err != nil {
		return liveRollout{}, err
	}
	return decodeRollout(string(manifest))
}

// ListRollouts returns the status of every Argo Rollout in the Application appName, labelled or
// not, sorted by namespace and then name.
//
// The Rollouts come from the Application's resource tree; the labels, phase, step index and canary
// weight come from each Rollout's live manifest. An empty appName is an error and sends no
// request. A Rollout with no live object (a tree node without a uid, not yet created, or deleted
// since ArgoCD last refreshed the tree) is skipped; any other failure to read a live Rollout fails
// the whole call, and the error names the Rollout (<namespace>/<name>). When the Application has
// no Rollout the result is an empty, non-nil slice and the error is nil.
//
// An HTTP answer of 400 or above that is not retried wraps *APIError, so errors.Is reaches
// ErrPermissionDenied. A missing Application surfaces as ErrPermissionDenied (HTTP 403), as for
// GetLiveResource. A cancelled or expired ctx surfaces as the context error.
func (c *Client) ListRollouts(ctx context.Context, appName string) ([]RolloutStatus, error) {
	if appName == "" {
		return nil, errors.New("application name is required")
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
		statuses = append(statuses, newRolloutStatus(node, live))
	}
	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].Ref.Namespace != statuses[j].Ref.Namespace {
			return statuses[i].Ref.Namespace < statuses[j].Ref.Namespace
		}
		return statuses[i].Ref.Name < statuses[j].Ref.Name
	})
	return statuses, nil
}

// ListRolloutGroup returns the status of every Argo Rollout in the Application appName whose
// CorrelationIDLabel label equals correlationID, sorted by namespace and then name.
//
// The Rollouts come from the Application's resource tree; the label, phase, step index and
// canary weight come from each Rollout's live manifest. An empty appName or correlationID is an
// error, so unlabeled Rollouts are never matched. A Rollout with no live object (not yet created, or deleted
// since ArgoCD last refreshed the tree) is skipped; any other failure to read a live Rollout fails
// the whole call.
// When no Rollout matches, the result is an empty, non-nil slice and the error is nil.
func (c *Client) ListRolloutGroup(ctx context.Context, appName, correlationID string) ([]RolloutStatus, error) {
	if appName == "" {
		return nil, errors.New("application name is required")
	}
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
