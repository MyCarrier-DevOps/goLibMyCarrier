// Package slippyapi provides lightweight helpers for talking to the
// slippy-api HTTP service. It is intentionally separate from the
// goLibMyCarrier/slippy package — that one is the slippy state-machine
// library and pulls in ClickHouse, GitHub, and pipeline-config deps.
// Consumers that only need to discover the slippy-api URL (CLI tools,
// migrators, deploy-race checks, push-event parsers) import this
// stdlib-only package instead.
package slippyapi

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Environment variables consulted by ResolveAPIURL.
const (
	envAPIURL       = "SLIPPY_API_URL"
	envK8sNamespace = "K8S_NAMESPACE"
)

// Known cluster namespace -> slippy-api public-ingress URL. In-cluster URLs
// (svc.cluster.local form) are intentionally not handled here; callers wanting
// in-cluster routing should set SLIPPY_API_URL explicitly.
const (
	apiURLProd    = "https://slippy-api.api.mycarrier.tech/v1"
	apiURLNonProd = "https://slippy-api-test.api.mycarrier.tech/v1"
)

// ResolveAPIURL returns the slippy-api base URL for the current environment.
//
// Resolution order:
//  1. SLIPPY_API_URL — explicit override. Honored verbatim (after TrimSpace),
//     so integration tests, local dev, in-cluster URLs, and incident-time
//     redirection all work without code changes.
//  2. K8S_NAMESPACE mapped to a known cluster URL:
//     argo-events       -> apiURLProd
//     argo-events-test  -> apiURLNonProd
//  3. ("", nil) when K8S_NAMESPACE is unset, empty, or whitespace-only.
//     This is the "slippy not configured" path — callers decide whether
//     that means "disabled" or "fail with not-configured error."
//  4. ("", error) when K8S_NAMESPACE has a non-empty value that does not
//     map to a known cluster (operator typo, new cluster not yet added).
//     Fails fast rather than silently routing to the wrong place or
//     collapsing into the disabled path.
//
// The mapping is intentionally a code-level constant: cluster topology
// changes are rare and should travel through goLibMyCarrier review +
// dependency bump, not through a Helm values edit that bypasses code
// review. Callers needing per-cluster overrides (e.g. brownouts, in-cluster
// vs public-ingress switches) set SLIPPY_API_URL.
func ResolveAPIURL() (string, error) {
	if explicit := strings.TrimSpace(os.Getenv(envAPIURL)); explicit != "" {
		// Defense-in-depth: catch obviously malformed overrides (typo'd
		// scheme, missing host) before they surface as confusing HTTP
		// client errors. Scheme allow-list is http/https so the in-cluster
		// override (http://slippy-api.<ns>.svc.cluster.local:8080/v1)
		// still works.
		u, parseErr := url.Parse(explicit)
		if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf(
				"slippy: %s=%q is not a valid http(s) URL",
				envAPIURL, explicit,
			)
		}
		return explicit, nil
	}
	switch ns := strings.TrimSpace(os.Getenv(envK8sNamespace)); ns {
	case "":
		return "", nil
	case "argo-events":
		return apiURLProd, nil
	case "argo-events-test":
		return apiURLNonProd, nil
	default:
		return "", fmt.Errorf(
			"slippy: K8S_NAMESPACE=%q is not a known slippy-api cluster; set %s explicitly to override",
			ns, envAPIURL,
		)
	}
}
