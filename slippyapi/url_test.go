package slippyapi

import (
	"os"
	"strings"
	"testing"
)

func setEnv(t *testing.T, key, value string, present bool) {
	t.Helper()
	original, ok := os.LookupEnv(key)
	t.Cleanup(func() {
		if ok {
			os.Setenv(key, original)
		} else {
			os.Unsetenv(key)
		}
	})
	if present {
		os.Setenv(key, value)
	} else {
		os.Unsetenv(key)
	}
}

func TestResolveAPIURL(t *testing.T) {
	cases := []struct {
		name      string
		explicit  string
		expSet    bool
		namespace string
		nsSet     bool
		wantURL   string
		wantErr   bool
	}{
		{
			name:      "explicit SLIPPY_API_URL wins over namespace",
			explicit:  "http://override.example.com",
			expSet:    true,
			namespace: "argo-events",
			nsSet:     true,
			wantURL:   "http://override.example.com",
		},
		{
			name:     "explicit value trimmed",
			explicit: "  http://override.example.com  ",
			expSet:   true,
			wantURL:  "http://override.example.com",
		},
		{
			name:     "in-cluster override is honored verbatim",
			explicit: "http://slippy-api.argo-events.svc.cluster.local:8080/v1",
			expSet:   true,
			wantURL:  "http://slippy-api.argo-events.svc.cluster.local:8080/v1",
		},
		{
			name:      "namespace fallback: argo-events -> prod",
			namespace: "argo-events",
			nsSet:     true,
			wantURL:   apiURLProd,
		},
		{
			name:      "namespace fallback: argo-events-test -> non-prod",
			namespace: "argo-events-test",
			nsSet:     true,
			wantURL:   apiURLNonProd,
		},
		{
			name:      "whitespace explicit falls through to namespace",
			explicit:  "   ",
			expSet:    true,
			namespace: "argo-events-test",
			nsSet:     true,
			wantURL:   apiURLNonProd,
		},
		{
			name:    "neither env set returns empty without error",
			wantURL: "",
		},
		{
			name:     "explicit override with no scheme returns error",
			explicit: "slippy-api.example.com",
			expSet:   true,
			wantErr:  true,
		},
		{
			name:     "explicit override with non-http scheme returns error",
			explicit: "ftp://slippy-api.example.com",
			expSet:   true,
			wantErr:  true,
		},
		{
			name:     "explicit override with only scheme returns error",
			explicit: "http://",
			expSet:   true,
			wantErr:  true,
		},
		{
			name:      "empty namespace string treated as unset",
			namespace: "",
			nsSet:     true,
			wantURL:   "",
		},
		{
			name:      "whitespace-only namespace treated as unset",
			namespace: "   ",
			nsSet:     true,
			wantURL:   "",
		},
		{
			name:      "unknown namespace returns error (fail-fast)",
			namespace: "some-other-ns",
			nsSet:     true,
			wantErr:   true,
		},
		{
			name:      "namespace match is case-sensitive",
			namespace: "ARGO-EVENTS",
			nsSet:     true,
			wantErr:   true,
		},
		{
			name:      "typo namespace returns error rather than silent disable",
			namespace: "argo-evenst",
			nsSet:     true,
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, envAPIURL, tc.explicit, tc.expSet)
			setEnv(t, envK8sNamespace, tc.namespace, tc.nsSet)

			got, err := ResolveAPIURL()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("ResolveAPIURL() error = nil, want error; got url=%q", got)
			case !tc.wantErr && err != nil:
				t.Fatalf("ResolveAPIURL() unexpected error: %v", err)
			}
			if got != tc.wantURL {
				t.Errorf("ResolveAPIURL() url = %q, want %q", got, tc.wantURL)
			}
			if tc.wantErr {
				if !strings.Contains(err.Error(), envAPIURL) {
					t.Errorf("error message should mention %s for operator clarity, got: %v", envAPIURL, err)
				}
				// Namespace echo is asserted only for unknown-namespace errors.
				// Explicit-URL validation errors echo the offending URL instead.
				if tc.nsSet && tc.namespace != "" && !tc.expSet {
					if !strings.Contains(err.Error(), tc.namespace) {
						t.Errorf("error message should echo offending namespace %q, got: %v", tc.namespace, err)
					}
				}
			}
		})
	}
}
