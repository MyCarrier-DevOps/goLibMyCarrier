package slippyapi

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// saveEnv captures the current value of key and restores it via t.Cleanup.
// Used for tests that need an env var explicitly *unset*; t.Setenv only
// covers the set case (and restores on cleanup automatically).
func saveEnv(t *testing.T, key string) {
	t.Helper()
	original, ok := os.LookupEnv(key)
	t.Cleanup(func() {
		if ok {
			os.Setenv(key, original)
		} else {
			os.Unsetenv(key)
		}
	})
}

func TestResolveAPIURL(t *testing.T) {
	cases := []struct {
		name              string
		explicit          string
		expSet            bool
		namespace         string
		nsSet             bool
		wantURL           string
		wantErr           bool
		wantNotConfigured bool
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
			name:     "explicit value trailing slash trimmed",
			explicit: "http://override.example.com/",
			expSet:   true,
			wantURL:  "http://override.example.com",
		},
		{
			name:     "explicit value with path and trailing slash trimmed",
			explicit: "http://override.example.com/v1/",
			expSet:   true,
			wantURL:  "http://override.example.com/v1",
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
			wantURL:   "https://slippy-api.api.mycarrier.tech/v1",
		},
		{
			name:      "namespace fallback: argo-events-test -> non-prod",
			namespace: "argo-events-test",
			nsSet:     true,
			wantURL:   "https://slippy-api-test.api.mycarrier.tech/v1",
		},
		{
			name:      "whitespace explicit falls through to namespace",
			explicit:  "   ",
			expSet:    true,
			namespace: "argo-events-test",
			nsSet:     true,
			wantURL:   "https://slippy-api-test.api.mycarrier.tech/v1",
		},
		{
			name:              "neither env set returns ErrNotConfigured",
			wantURL:           "",
			wantErr:           true,
			wantNotConfigured: true,
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
			name:              "empty namespace string treated as unset",
			namespace:         "",
			nsSet:             true,
			wantURL:           "",
			wantErr:           true,
			wantNotConfigured: true,
		},
		{
			name:              "whitespace-only namespace treated as unset",
			namespace:         "   ",
			nsSet:             true,
			wantURL:           "",
			wantErr:           true,
			wantNotConfigured: true,
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
			// Always save+restore so prior process env doesn't leak between
			// cases and a per-case unset truly means unset.
			saveEnv(t, envAPIURL)
			saveEnv(t, envK8sNamespace)
			if tc.expSet {
				t.Setenv(envAPIURL, tc.explicit)
			} else {
				os.Unsetenv(envAPIURL)
			}
			if tc.nsSet {
				t.Setenv(envK8sNamespace, tc.namespace)
			} else {
				os.Unsetenv(envK8sNamespace)
			}

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
			if tc.wantNotConfigured {
				if !errors.Is(err, ErrNotConfigured) {
					t.Errorf("expected errors.Is(err, ErrNotConfigured), got: %v", err)
				}
				return
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
