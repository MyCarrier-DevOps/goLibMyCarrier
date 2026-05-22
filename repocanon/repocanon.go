// Package repocanon is the single source of truth for normalizing the many
// shapes a MyCarrier repository identifier can take into the canonical
// service-name (kebab) and repository-key (dotted) forms used across the
// platform.
//
// The canonical rule (per workflow-core render-deploy-core.yaml):
//
//	basename | lower | drop "mc." | dots->hyphens   // SERVICE
//	"mc." + dots-preserved-form-of-the-above        // REPOSITORY
//
// This package eliminates the override-registry pattern previously needed in
// TestEngine.Worker by applying the canonical transform uniformly to any
// accepted input shape. See README.md for the accepted input shapes and
// "deliberate divergence" notes (e.g. why autotriggertests' extractRepoName
// does NOT use this lib).
package repocanon

import "strings"

// Names holds the canonical name shapes derived from a raw repo identifier.
//
// Field ordering: Service is declared first because it is the more commonly
// used field across consumers (ArgoCD service names, Kubernetes Service DNS,
// pod labels) versus Repository which is consumed only by ClickHouse-joining
// callers. This ordering also matches the README and doc-comment narrative.
type Names struct {
	// Service is the canonical kebab form used by ArgoCD service names,
	// Kubernetes Service DNS, and pod labels (e.g. "mycarrier-frontend").
	// No "mc." prefix.
	Service string

	// Repository is the dotted lowercase form keyed by
	// ci.repoproperties.repository (e.g. "mc.mycarrier.frontend"). Preserves
	// the "mc." prefix and dots — this is the literal GitHub repo name
	// lowercased, which ClickHouse stores as the join key.
	Repository string
}

// FromRaw normalizes any of the input shapes MyCarrier producers emit into
// canonical Names. The input may be:
//
//   - Full GitHub repo name:   "MC.MyCarrier.Frontend"
//   - Lowercase dotted form:   "mc.mycarrier.frontend"
//   - Service kebab form:      "mycarrier-frontend"
//   - Dotted no-prefix form:   "mycarrier.frontend"
//   - Mixed case:              "MyCarrier.Frontend"
//   - owner/repo:              "MyCarrier-Engineering/MC.MyCarrier.Frontend"
//   - Full URL:                "https://github.com/MyCarrier-Engineering/MC.MyCarrier.Frontend.git"
//
// Rule applied:
//
//  1. Trim whitespace
//  2. Lowercase
//  3. Strip URL scheme/host (everything up to and including "://...host/")
//  4. Trim trailing "/" (so "owner/repo/" parses the same as "owner/repo")
//  5. Take last "/" segment
//  6. Strip ".git" suffix
//  7. TrimPrefix("mc.") and TrimPrefix("mc-")
//  8. Service    = ReplaceAll(s, ".", "-")
//     Repository = "mc." + ReplaceAll(s, "-", ".")
//
// Where s is the post-prefix-trim form. Empty/prefix-only input returns the
// zero-value Names; callers validate emptiness.
func FromRaw(raw string) Names {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return Names{}
	}

	// Strip URL scheme + host: "https://github.com/owner/repo" -> "owner/repo".
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			s = rest[slash+1:]
		} else {
			s = ""
		}
	}

	// Trim trailing "/" so URLs like ".../repo/" don't yield an empty last
	// segment in the LastIndex step below.
	s = strings.TrimRight(s, "/")

	// Take last path segment ("owner/repo" -> "repo").
	if slash := strings.LastIndex(s, "/"); slash >= 0 {
		s = s[slash+1:]
	}

	// Strip ".git" suffix from URLs. Re-trim trailing "/" in case input was
	// ".../repo.git/" (TrimRight already handled outer slashes, but a ".git/"
	// embedded before any earlier slash would survive — guard regardless).
	s = strings.TrimSuffix(s, ".git")

	// Strip a single "mc." or "mc-" prefix. Single non-greedy strip —
	// "mc.mc.frontend" keeps the second "mc." This is intentional; a greedy
	// strip would corrupt legitimate "mc.mc-prefixed.foo" names.
	if strings.HasPrefix(s, "mc.") {
		s = s[len("mc."):]
	} else if strings.HasPrefix(s, "mc-") {
		s = s[len("mc-"):]
	}

	if s == "" {
		return Names{}
	}

	return Names{
		Service:    strings.ReplaceAll(s, ".", "-"),
		Repository: "mc." + strings.ReplaceAll(s, "-", "."),
	}
}

// ArgoCDAppName composes the canonical ArgoCD application name from the
// canonical Service plus the deploy environment and chart-type. Rules
// match workflow-core/workflows/templates/render-deploy-core.yaml:
//
//	chartType == "mc-environment"     → "{svc}-{env}"
//	environment startswith "feature"  → "{svc}-offload-{env}"      (legacy)
//	environment == "prod"             → "production-csp-prod-{svc}" (legacy)
//	otherwise                          → "development-{env}-{svc}"  (legacy)
//
// Empty Service returns empty string — callers must validate emptiness
// before passing the result to ArgoCD/Kubernetes APIs (no nonsense names
// like "-dev"). environment and chartType are lowercased + whitespace-
// trimmed for comparison and for substitution; the canonical Service form
// (kebab) is preserved verbatim.
//
// Empty environment falls through to the legacy default branch and yields
// "development--{svc}" (locked in by TestArgoCDAppName); callers should
// validate environment non-empty before invoking. Unknown chartType
// (anything other than "mc-environment") triggers the legacy 3-branch
// ladder.
//
// Callers must ensure both Service and environment are non-empty; this
// method does not validate them, and empty inputs produce invalid DNS
// labels (e.g. "development--svc", "svc-") that ArgoCD will reject.
func (n Names) ArgoCDAppName(environment, chartType string) string {
	if n.Service == "" {
		return ""
	}
	env := strings.ToLower(strings.TrimSpace(environment))
	chart := strings.ToLower(strings.TrimSpace(chartType))

	if chart == "mc-environment" {
		return n.Service + "-" + env
	}
	if strings.HasPrefix(env, "feature") {
		return n.Service + "-offload-" + env
	}
	if env == "prod" {
		return "production-csp-prod-" + n.Service
	}
	return "development-" + env + "-" + n.Service
}
