package argocdclient

import "testing"

// TestRouteInstance covers the 5 routing rules + the "-preprod" carve-out
// and the empty-input default. Each row pins observable behavior for one
// app-name shape; new app-name patterns should add a row here before the
// routing logic is extended.
func TestRouteInstance(t *testing.T) {
	tests := []struct {
		name    string
		appName string
		want    Instance
	}{
		// Offload pattern (priority 1) — feature offloads always land on dev.
		{
			name:    "offload feature lands on dev",
			appName: "mycarrier-frontend-offload-feature20",
			want:    InstanceDev,
		},
		{
			name:    "offload single-token svc",
			appName: "order-offload-feature7",
			want:    InstanceDev,
		},

		// Legacy dev/preprod prefix → mgmt.
		{
			name:    "legacy development-dev prefix → mgmt",
			appName: "development-dev-mycarrier-frontend",
			want:    InstanceMgmt,
		},
		{
			name:    "legacy development-preprod prefix → mgmt",
			appName: "development-preprod-mycarrier-frontend",
			want:    InstanceMgmt,
		},

		// Legacy production-csp prefix → mgmt.
		{
			name:    "legacy production-csp-prod prefix → mgmt",
			appName: "production-csp-prod-mycarrier-frontend",
			want:    InstanceMgmt,
		},

		// New mc-environment scheme prod (suffix-based) → prod.
		{
			name:    "new-scheme -prod suffix → prod",
			appName: "mycarrier-frontend-prod",
			want:    InstanceProd,
		},
		{
			name:    "new-scheme -prod suffix single-token → prod",
			appName: "order-prod",
			want:    InstanceProd,
		},

		// Carve-out: -preprod must NOT match the -prod rule.
		{
			name:    "new-scheme -preprod carve-out → dev",
			appName: "mycarrier-frontend-preprod",
			want:    InstanceDev,
		},

		// Default fallback for the new mc-environment scheme.
		{
			name:    "new-scheme -dev → dev (default)",
			appName: "mycarrier-frontend-dev",
			want:    InstanceDev,
		},
		{
			name:    "new-scheme feature (no offload substring) → dev",
			appName: "mycarrier-frontend-feature20",
			want:    InstanceDev,
		},

		// Empty input → dev (documented default).
		{
			name:    "empty appName returns InstanceDev",
			appName: "",
			want:    InstanceDev,
		},

		// Case-normalization parity with deploy-reporter / DeployVerifier.
		{
			name:    "mixed-case input normalizes",
			appName: "Development-Dev-Mycarrier-Frontend",
			want:    InstanceMgmt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RouteInstance(tt.appName)
			if got != tt.want {
				t.Errorf("RouteInstance(%q) = %v (%s), want %v (%s)",
					tt.appName, got, got, tt.want, tt.want)
			}
		})
	}
}

// TestInstance_String pins the stringer output. The exact strings are
// consumed as env-var suffix lookups (e.g. ARGOCD_SERVER_DEV) and metric
// labels, so they're part of the public surface.
func TestInstance_String(t *testing.T) {
	tests := []struct {
		name string
		in   Instance
		want string
	}{
		{name: "dev", in: InstanceDev, want: "DEV"},
		{name: "mgmt", in: InstanceMgmt, want: "MGMT"},
		{name: "prod", in: InstanceProd, want: "PROD"},
		{name: "unknown returns UNKNOWN", in: Instance(99), want: "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.String(); got != tt.want {
				t.Errorf("Instance(%d).String() = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
