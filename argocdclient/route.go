package argocdclient

import "strings"

// Instance identifies which ArgoCD control plane an app name routes to.
//
// MyCarrier runs three logical ArgoCD instances:
//
//   - InstanceDev  — feature/offload + new-scheme dev/preprod cluster
//   - InstanceMgmt — legacy dev/preprod + legacy production cluster selector
//   - InstanceProd — new-scheme production cluster
//
// Use RouteInstance(appName) to pick the correct instance from an ArgoCD
// application name; the caller then resolves the matching server URL +
// auth token pair from environment-scoped configuration.
type Instance int

const (
	// InstanceDev is the default ArgoCD instance and serves feature
	// offloads plus new-scheme dev/preprod environments.
	InstanceDev Instance = iota

	// InstanceMgmt is the management ArgoCD instance and serves the
	// legacy dev/preprod ("development-{env}-{svc}") and legacy prod
	// ("production-csp-prod-{svc}") application name shapes.
	InstanceMgmt

	// InstanceProd is the production ArgoCD instance and serves the
	// new-scheme prod ("{svc}-prod") application name shape.
	InstanceProd
)

// String returns the canonical short name for the instance ("DEV",
// "MGMT", "PROD"). Useful for log labels, metrics tags, and env-var
// suffix lookups (e.g. ARGOCD_SERVER_DEV).
func (i Instance) String() string {
	switch i {
	case InstanceMgmt:
		return "MGMT"
	case InstanceProd:
		return "PROD"
	case InstanceDev:
		return "DEV"
	default:
		return "UNKNOWN"
	}
}

// RouteInstance picks the ArgoCD instance from the app name. Patterns
// matched in priority order:
//
//   - "-offload-" substring                        → InstanceDev   (feature offloads)
//   - "development-dev-" or "development-preprod-" → InstanceMgmt  (legacy dev/preprod)
//   - "production-csp-" prefix                     → InstanceMgmt  (legacy prod cluster selector)
//   - "-prod" suffix (NOT "-preprod")              → InstanceProd  (new mc-environment scheme)
//   - otherwise                                     → InstanceDev   (default — new-scheme dev/preprod)
//
// Empty appName returns InstanceDev. Routing is purely a string-pattern
// classifier; it does NOT validate that the input is a well-formed app
// name. Callers wanting validation should call repocanon and/or check
// the result of GetApplication.
func RouteInstance(appName string) Instance {
	// Normalize to lower-case so case-variant inputs route consistently
	// with deploy-reporter and MC.TestEngine.DeployVerifier (both lower
	// before matching). Without this, "Development-Dev-Mycarrier-Frontend"
	// would fall through to InstanceDev instead of InstanceMgmt.
	n := strings.ToLower(appName)
	if n == "" {
		return InstanceDev
	}

	// Feature offload always lands on the dev instance, regardless of
	// surrounding shape. Match first so legacy-style "development-*"
	// offload names (none today, but cheap to be defensive) can't
	// mis-route.
	if strings.Contains(n, "-offload-") {
		return InstanceDev
	}

	// Legacy non-prod scheme: "development-dev-{svc}" / "development-preprod-{svc}".
	if strings.HasPrefix(n, "development-dev-") ||
		strings.HasPrefix(n, "development-preprod-") {
		return InstanceMgmt
	}

	// Legacy prod cluster selector: "production-csp-{...}-{svc}".
	if strings.HasPrefix(n, "production-csp-") {
		return InstanceMgmt
	}

	// New mc-environment scheme prod: "{svc}-prod". Must exclude the
	// "-preprod" suffix which also matches "-prod" as a suffix substring
	// but represents a different environment.
	if strings.HasSuffix(n, "-prod") && !strings.HasSuffix(n, "-preprod") {
		return InstanceProd
	}

	return InstanceDev
}
