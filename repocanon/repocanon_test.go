package repocanon

import "testing"

func TestFromRaw(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSvc    string
		wantRepo   string
	}{
		{
			name:     "live prod input (full GitHub name)",
			input:    "MC.MyCarrier.Frontend",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "lowercased dotted form",
			input:    "mc.mycarrier.frontend",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "no mc. prefix dotted",
			input:    "mycarrier.frontend",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "already kebab",
			input:    "mycarrier-frontend",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "mixed case dotted",
			input:    "MyCarrier.Frontend",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "single-token mc-prefixed",
			input:    "MC.Mycarrier",
			wantSvc:  "mycarrier",
			wantRepo: "mc.mycarrier",
		},
		{
			name:     "single-token bare",
			input:    "mycarrier",
			wantSvc:  "mycarrier",
			wantRepo: "mc.mycarrier",
		},
		{
			name:     "single-token MC.Order",
			input:    "MC.Order",
			wantSvc:  "order",
			wantRepo: "mc.order",
		},
		{
			name:     "single-token no mc.",
			input:    "address",
			wantSvc:  "address",
			wantRepo: "mc.address",
		},
		{
			name:     "kebab mc- prefix",
			input:    "mc-environment",
			wantSvc:  "environment",
			wantRepo: "mc.environment",
		},
		{
			name:     "hypothetical multi-token MC.Order.Events",
			input:    "MC.Order.Events",
			wantSvc:  "order-events",
			wantRepo: "mc.order.events",
		},
		{
			name:     "owner/repo slash form",
			input:    "MyCarrier-Engineering/MC.MyCarrier.Frontend",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "slash form with different owner",
			input:    "otherorg/MC.Foo",
			wantSvc:  "foo",
			wantRepo: "mc.foo",
		},
		{
			name:     "full URL with .git suffix",
			input:    "https://github.com/MyCarrier-Engineering/MC.MyCarrier.Frontend.git",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "full URL with trailing slash",
			input:    "https://github.com/MyCarrier-Engineering/MC.MyCarrier.Frontend/",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "full URL with .git suffix and trailing slash",
			input:    "https://github.com/MyCarrier-Engineering/MC.MyCarrier.Frontend.git/",
			wantSvc:  "mycarrier-frontend",
			wantRepo: "mc.mycarrier.frontend",
		},
		{
			name:     "empty input zero-value",
			input:    "",
			wantSvc:  "",
			wantRepo: "",
		},
		{
			name:     "whitespace trim",
			input:    "   MC.Order   ",
			wantSvc:  "order",
			wantRepo: "mc.order",
		},
		{
			name:     "double mc. prefix keeps inner",
			input:    "mc.mc.frontend",
			wantSvc:  "mc-frontend",
			wantRepo: "mc.mc.frontend",
		},
		{
			name:     "kebab prefix only",
			input:    "mc-",
			wantSvc:  "",
			wantRepo: "",
		},
		{
			name:     "dotted prefix only",
			input:    "mc.",
			wantSvc:  "",
			wantRepo: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromRaw(tt.input)
			if got.Service != tt.wantSvc {
				t.Errorf("FromRaw(%q).Service = %q, want %q", tt.input, got.Service, tt.wantSvc)
			}
			if got.Repository != tt.wantRepo {
				t.Errorf("FromRaw(%q).Repository = %q, want %q", tt.input, got.Repository, tt.wantRepo)
			}
		})
	}
}

// TestFromRaw_CollapsedFormUnrecoverable pins the silent-wrong behavior for
// inputs that have already had their dot/hyphen boundary collapsed (e.g.
// "mycarrierfrontend" instead of "mycarrier.frontend"). The transform cannot
// recover the boundary, so the result diverges from the canonical form.
//
// Callers (TestEngine.Worker, autotriggertests, Slippy CLI) MUST pass the
// ORIGINAL un-stripped form (full GitHub repo name or URL). This test exists
// to make the divergence loudly visible in test reports if someone ever wires
// a collapsed-shape input into the wrong call site.
func TestFromRaw_CollapsedFormUnrecoverable(t *testing.T) {
	got := FromRaw("mycarrierfrontend")
	// Current (silent-wrong) behavior — pinned, not endorsed:
	const wantSvc = "mycarrierfrontend"
	const wantRepo = "mc.mycarrierfrontend"
	// Canonical (for comparison only) would have been:
	//   Service    = "mycarrier-frontend"
	//   Repository = "mc.mycarrier.frontend"
	if got.Service != wantSvc {
		t.Errorf("collapsed-form Service = %q, want %q (silent-wrong pin)", got.Service, wantSvc)
	}
	if got.Repository != wantRepo {
		t.Errorf("collapsed-form Repository = %q, want %q (silent-wrong pin)", got.Repository, wantRepo)
	}
}

// TestFromRaw_LeadingDotFootGun pins the silent-wrong behavior for inputs
// with leading dots or doubled dots. The transform produces a leading hyphen
// in Service, which violates RFC 1123 DNS-label rules (^[a-z0-9]([-a-z0-9]*[a-z0-9])?$).
// Callers MUST validate output against RFC 1123 before passing Service to
// Kubernetes / ArgoCD APIs. See README "Unsupported Shapes" for guidance.
func TestFromRaw_LeadingDotFootGun(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantSvc  string
		wantRepo string
	}{
		{
			name:     "leading dot",
			input:    ".foo",
			wantSvc:  "-foo",
			wantRepo: "mc..foo",
		},
		{
			name:     "double dot after mc prefix",
			input:    "mc..foo",
			wantSvc:  "-foo",
			wantRepo: "mc..foo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromRaw(tt.input)
			if got.Service != tt.wantSvc {
				t.Errorf("FromRaw(%q).Service = %q, want %q (foot-gun pin)", tt.input, got.Service, tt.wantSvc)
			}
			if got.Repository != tt.wantRepo {
				t.Errorf("FromRaw(%q).Repository = %q, want %q (foot-gun pin)", tt.input, got.Repository, tt.wantRepo)
			}
		})
	}
}

// TestArgoCDAppName covers the 4-branch ArgoCD app-name derivation rules
// against representative service inputs plus edge cases. Each test row
// pins observable behavior for one (service, environment, chartType)
// tuple.
func TestArgoCDAppName(t *testing.T) {
	tests := []struct {
		name      string
		service   string
		env       string
		chartType string
		want      string
	}{
		// chartType == "mc-environment" → new scheme: "{svc}-{env}"
		{
			name:      "mc-environment + dev",
			service:   "mycarrier-frontend",
			env:       "dev",
			chartType: "mc-environment",
			want:      "mycarrier-frontend-dev",
		},
		{
			name:      "mc-environment + preprod",
			service:   "mycarrier-frontend",
			env:       "preprod",
			chartType: "mc-environment",
			want:      "mycarrier-frontend-preprod",
		},
		{
			name:      "mc-environment + prod",
			service:   "mycarrier-frontend",
			env:       "prod",
			chartType: "mc-environment",
			want:      "mycarrier-frontend-prod",
		},
		{
			name:      "mc-environment + featureN",
			service:   "mycarrier-frontend",
			env:       "feature20",
			chartType: "mc-environment",
			want:      "mycarrier-frontend-feature20",
		},

		// Legacy ladder: feature* env → "{svc}-offload-{env}"
		{
			name:      "legacy mycarrier-helm + feature20",
			service:   "mycarrier-frontend",
			env:       "feature20",
			chartType: "mycarrier-helm",
			want:      "mycarrier-frontend-offload-feature20",
		},
		{
			name:      "legacy empty chartType + feature20",
			service:   "mycarrier-frontend",
			env:       "feature20",
			chartType: "",
			want:      "mycarrier-frontend-offload-feature20",
		},

		// Legacy ladder: env == "prod" → "production-csp-prod-{svc}"
		{
			name:      "legacy mycarrier-helm + prod",
			service:   "mycarrier-frontend",
			env:       "prod",
			chartType: "mycarrier-helm",
			want:      "production-csp-prod-mycarrier-frontend",
		},
		{
			name:      "legacy empty chartType + prod",
			service:   "mycarrier-frontend",
			env:       "prod",
			chartType: "",
			want:      "production-csp-prod-mycarrier-frontend",
		},

		// Legacy ladder: default → "development-{env}-{svc}"
		{
			name:      "legacy mycarrier-helm + dev",
			service:   "mycarrier-frontend",
			env:       "dev",
			chartType: "mycarrier-helm",
			want:      "development-dev-mycarrier-frontend",
		},
		{
			name:      "legacy mycarrier-helm + preprod",
			service:   "mycarrier-frontend",
			env:       "preprod",
			chartType: "mycarrier-helm",
			want:      "development-preprod-mycarrier-frontend",
		},
		{
			name:      "legacy empty chartType + dev",
			service:   "mycarrier-frontend",
			env:       "dev",
			chartType: "",
			want:      "development-dev-mycarrier-frontend",
		},

		// Single-token service across all four branches
		{
			name:      "mc-environment + dev (single-token svc)",
			service:   "order",
			env:       "dev",
			chartType: "mc-environment",
			want:      "order-dev",
		},
		{
			name:      "legacy + feature20 (single-token svc)",
			service:   "order",
			env:       "feature20",
			chartType: "mycarrier-helm",
			want:      "order-offload-feature20",
		},
		{
			name:      "legacy + prod (single-token svc)",
			service:   "order",
			env:       "prod",
			chartType: "mycarrier-helm",
			want:      "production-csp-prod-order",
		},
		{
			name:      "legacy + dev (single-token svc)",
			service:   "order",
			env:       "dev",
			chartType: "mycarrier-helm",
			want:      "development-dev-order",
		},

		// Edge cases
		{
			name:      "empty service returns empty (mc-environment)",
			service:   "",
			env:       "dev",
			chartType: "mc-environment",
			want:      "",
		},
		{
			name:      "empty service returns empty (legacy)",
			service:   "",
			env:       "prod",
			chartType: "mycarrier-helm",
			want:      "",
		},
		{
			name:      "mixed-case env normalized (FeatureX → featurex)",
			service:   "mycarrier-frontend",
			env:       "FeatureX",
			chartType: "mycarrier-helm",
			want:      "mycarrier-frontend-offload-featurex",
		},
		{
			name:      "mixed-case chartType normalized (MC-Environment)",
			service:   "mycarrier-frontend",
			env:       "dev",
			chartType: "MC-Environment",
			want:      "mycarrier-frontend-dev",
		},
		{
			name:      "unusual env feature-test-1 still feature-prefix",
			service:   "mycarrier-frontend",
			env:       "feature-test-1",
			chartType: "mycarrier-helm",
			want:      "mycarrier-frontend-offload-feature-test-1",
		},
		{
			name:      "whitespace env trimmed",
			service:   "mycarrier-frontend",
			env:       "  dev  ",
			chartType: "mycarrier-helm",
			want:      "development-dev-mycarrier-frontend",
		},
		{
			name:      "unknown chartType falls through to legacy",
			service:   "mycarrier-frontend",
			env:       "dev",
			chartType: "some-other-chart",
			want:      "development-dev-mycarrier-frontend",
		},
		// Empty environment — locked-in behavior. Falls through the legacy
		// ladder (not feature-prefix, not "prod") and yields the documented
		// "development--{svc}" form. Callers should validate env non-empty
		// before invoking; this assertion guards against silent change.
		{
			name:      "empty environment legacy yields development--svc",
			service:   "mycarrier-frontend",
			env:       "",
			chartType: "mycarrier-helm",
			want:      "development--mycarrier-frontend",
		},
		{
			name:      "empty environment mc-environment yields svc-",
			service:   "mycarrier-frontend",
			env:       "",
			chartType: "mc-environment",
			want:      "mycarrier-frontend-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := Names{Service: tt.service}
			got := n.ArgoCDAppName(tt.env, tt.chartType)
			if got != tt.want {
				t.Errorf("Names{Service:%q}.ArgoCDAppName(%q, %q) = %q, want %q",
					tt.service, tt.env, tt.chartType, got, tt.want)
			}
		})
	}
}

// TestMetaEnvFor pins the prod/dev bucket routing used by gitops layout.
func TestMetaEnvFor(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "prod", env: "prod", want: "prod"},
		{name: "dev", env: "dev", want: "dev"},
		{name: "preprod", env: "preprod", want: "dev"},
		{name: "feature17", env: "feature17", want: "dev"},
		{name: "empty", env: "", want: "dev"},
		{name: "mixed-case PROD", env: "PROD", want: "prod"},
		{name: "whitespace prod", env: "  prod  ", want: "prod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MetaEnvFor(tt.env); got != tt.want {
				t.Errorf("MetaEnvFor(%q) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}

// TestClusterFor pins the metaEnv -> ArgoCD cluster name mapping (matches
// Vault clusterSecrets/<env>.name).
func TestClusterFor(t *testing.T) {
	tests := []struct {
		name    string
		metaEnv string
		want    string
	}{
		{name: "prod", metaEnv: "prod", want: "production-csp"},
		{name: "dev", metaEnv: "dev", want: "development"},
		{name: "preprod (non-prod default)", metaEnv: "preprod", want: "development"},
		{name: "unknown falls back to development", metaEnv: "wat", want: "development"},
		{name: "empty falls back to development", metaEnv: "", want: "development"},
		{name: "mixed-case PROD", metaEnv: "PROD", want: "production-csp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClusterFor(tt.metaEnv); got != tt.want {
				t.Errorf("ClusterFor(%q) = %q, want %q", tt.metaEnv, got, tt.want)
			}
		})
	}
}

// TestArgoCDAppNames pins the dual-layer L1 (gitops-watcher) + L2
// (chart-sourced workload) ArgoCD app-name derivation. L1 is always
// "<cluster>-<metaEnv>-<appStack>"; L2 only exists for mc-environment
// charts and for mycarrier-helm offload feature envs.
func TestArgoCDAppNames(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		chartType string
		appStack  string
		wantL1    string
		wantL2    string
		wantHasL2 bool
	}{
		{
			name:      "mc-environment + prod + mycarrier",
			env:       "prod",
			chartType: "mc-environment",
			appStack:  "mycarrier",
			wantL1:    "production-csp-prod-mycarrier",
			wantL2:    "mycarrier-prod",
			wantHasL2: true,
		},
		{
			name:      "mc-environment + feature17 + mycarrier",
			env:       "feature17",
			chartType: "mc-environment",
			appStack:  "mycarrier",
			wantL1:    "development-dev-mycarrier",
			wantL2:    "mycarrier-feature17",
			wantHasL2: true,
		},
		{
			name:      "mc-environment + dev + mycarrier",
			env:       "dev",
			chartType: "mc-environment",
			appStack:  "mycarrier",
			wantL1:    "development-dev-mycarrier",
			wantL2:    "mycarrier-dev",
			wantHasL2: true,
		},
		{
			name:      "mycarrier-helm + prod + quote (no L2)",
			env:       "prod",
			chartType: "mycarrier-helm",
			appStack:  "quote",
			wantL1:    "production-csp-prod-quote",
			wantL2:    "",
			wantHasL2: false,
		},
		{
			name:      "mycarrier-helm + dev + quote (no L2)",
			env:       "dev",
			chartType: "mycarrier-helm",
			appStack:  "quote",
			wantL1:    "development-dev-quote",
			wantL2:    "",
			wantHasL2: false,
		},
		{
			name:      "mycarrier-helm + feature13 + quote (offload)",
			env:       "feature13",
			chartType: "mycarrier-helm",
			appStack:  "quote",
			wantL1:    "development-dev-quote",
			wantL2:    "quote-offload-feature13",
			wantHasL2: true,
		},
		{
			name:      "appStack divergence: frontend.domains used literally in L1",
			env:       "dev",
			chartType: "mc-environment",
			appStack:  "frontend.domains",
			wantL1:    "development-dev-frontend.domains",
			wantL2:    "frontend.domains-dev",
			wantHasL2: true,
		},
		{
			// Mixed-case env is lowercased + trimmed before routing, so PROD
			// normalizes through metaEnv=prod -> cluster=production-csp and
			// the L2 suffix is also lowercased. Parity with TestArgoCDAppName.
			name:      "mixed-case PROD normalizes to prod",
			env:       "PROD",
			chartType: "mc-environment",
			appStack:  "mycarrier",
			wantL1:    "production-csp-prod-mycarrier",
			wantL2:    "mycarrier-prod",
			wantHasL2: true,
		},
		{
			// appStack is preserved verbatim (NOT trimmed), unlike env/chartType.
			// Pinning the current contract: leading/trailing whitespace in
			// appStack leaks into the rendered names. Callers must pre-trim.
			name:      "whitespace in appStack preserved verbatim",
			env:       "dev",
			chartType: "mc-environment",
			appStack:  "  mycarrier  ",
			wantL1:    "development-dev-  mycarrier  ",
			wantL2:    "  mycarrier  -dev",
			wantHasL2: true,
		},
		{
			// G3 guard: empty appStack short-circuits to ("", "", false)
			// instead of emitting invalid trailing-dash names.
			name:      "empty appStack returns empty + hasL2=false",
			env:       "dev",
			chartType: "mc-environment",
			appStack:  "",
			wantL1:    "",
			wantL2:    "",
			wantHasL2: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Names.Service is intentionally divergent from appStack here to
			// prove ArgoCDAppNames uses the explicit appStack param, not
			// n.Service.
			n := Names{Service: "frontend-domains"}
			l1, l2, hasL2 := n.ArgoCDAppNames(tt.env, tt.chartType, tt.appStack)
			if l1 != tt.wantL1 {
				t.Errorf("L1 = %q, want %q", l1, tt.wantL1)
			}
			if l2 != tt.wantL2 {
				t.Errorf("L2 = %q, want %q", l2, tt.wantL2)
			}
			if hasL2 != tt.wantHasL2 {
				t.Errorf("hasL2 = %v, want %v", hasL2, tt.wantHasL2)
			}
		})
	}
}

func BenchmarkFromRaw(b *testing.B) {
	// Hot path: the most common input shape (full GitHub repo name from
	// webhook events).
	const input = "MC.MyCarrier.Frontend"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FromRaw(input)
	}
}

func BenchmarkFromRawURL(b *testing.B) {
	const input = "https://github.com/MyCarrier-Engineering/MC.MyCarrier.Frontend.git"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FromRaw(input)
	}
}

// BenchmarkFromRaw_AlreadyCanonical measures the floor cost when input is
// already in canonical kebab form — no URL stripping, no segment splitting,
// no .git trimming, no mc.-prefix work. The remaining cost is the
// lowercase/trim and the two ReplaceAll allocations.
func BenchmarkFromRaw_AlreadyCanonical(b *testing.B) {
	const input = "mycarrier-frontend"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FromRaw(input)
	}
}
