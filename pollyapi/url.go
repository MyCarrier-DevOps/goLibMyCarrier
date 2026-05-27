// Package pollyapi provides a lightweight, stdlib-only client for the
// polly-api HTTP service (the centralized CI/CD notification service).
//
// It mirrors the goLibMyCarrier/slippyapi package: ResolveAPIURL discovers the
// polly-api base URL from the environment, and a small Client posts check-run
// and PR-comment payloads the same way the polly CLI does. The package pulls in
// no third-party dependencies so CLI tools, Argo workflow steps, and migrators
// can talk to polly-api without inheriting polly-api's transitive dependency
// graph (Gin, Huma, go-github, Redis, ...).
package pollyapi

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Environment variables consulted by ResolveAPIURL.
const (
	envAPIURL       = "POLLY_API_URL"
	envK8sNamespace = "K8S_NAMESPACE"
)

// Known cluster namespace -> polly-api URL. Unlike slippy, polly-api is only
// reachable in-cluster: there is no public ingress, so these are the
// svc.cluster.local service URLs. Callers outside the cluster (local dev,
// integration tests) must set POLLY_API_URL explicitly.
//
// The URLs are the bare service roots with no path suffix; the Client appends
// the versioned route (/v1/github/...), matching how the polly CLI builds URLs.
const (
	apiURLProd    = "http://polly-api.argo-events.svc.cluster.local:8080"
	apiURLNonProd = "http://polly-api-test.argo-events-test.svc.cluster.local:8080"
)

// ErrNotConfigured is returned by ResolveAPIURL when neither POLLY_API_URL nor
// K8S_NAMESPACE is set (or both are whitespace-only). This is the "polly not
// configured" path — callers decide whether that means "disabled" or "fail
// with not-configured error" by testing with
// errors.Is(err, pollyapi.ErrNotConfigured).
var ErrNotConfigured = errors.New("pollyapi: not configured (K8S_NAMESPACE unset)")

// ResolveAPIURL returns the polly-api base URL for the current environment.
//
// Resolution order:
//  1. POLLY_API_URL — explicit override. Honored verbatim (after TrimSpace and
//     trailing-slash trim), so integration tests, local dev, and incident-time
//     redirection all work without code changes.
//  2. K8S_NAMESPACE mapped to a known cluster URL:
//     argo-events       -> apiURLProd
//     argo-events-test  -> apiURLNonProd
//  3. ("", ErrNotConfigured) when K8S_NAMESPACE is unset, empty, or
//     whitespace-only. Callers distinguish this from other errors via
//     errors.Is(err, pollyapi.ErrNotConfigured).
//  4. ("", error) when K8S_NAMESPACE has a non-empty value that does not map to
//     a known cluster (operator typo, new cluster not yet added). Fails fast
//     rather than silently routing to the wrong place or collapsing into the
//     disabled path.
//
// The returned URL never ends with a trailing slash and never carries a path
// suffix; the Client appends the versioned route.
//
// The mapping is intentionally a code-level constant: cluster topology changes
// are rare and should travel through goLibMyCarrier review + dependency bump,
// not through a Helm values edit that bypasses code review. Callers needing
// per-cluster overrides set POLLY_API_URL.
func ResolveAPIURL() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv(envAPIURL)); explicit != "" {
		// Defense-in-depth: catch obviously malformed overrides (typo'd
		// scheme, missing host) before they surface as confusing HTTP client
		// errors. The scheme allow-list is http/https so the in-cluster form
		// (http://polly-api.<ns>.svc.cluster.local:8080) still works.
		u, parseErr := url.Parse(explicit)
		if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf(
				"pollyapi: %s=%q is not a valid http(s) URL",
				envAPIURL, explicit,
			)
		}
		return strings.TrimRight(explicit, "/"), nil
	}
	switch ns := strings.TrimSpace(os.Getenv(envK8sNamespace)); ns {
	case "":
		return "", ErrNotConfigured
	case "argo-events":
		return apiURLProd, nil
	case "argo-events-test":
		return apiURLNonProd, nil
	default:
		return "", fmt.Errorf(
			"pollyapi: K8S_NAMESPACE=%q is not a known polly-api cluster; set %s explicitly to override",
			ns, envAPIURL,
		)
	}
}
