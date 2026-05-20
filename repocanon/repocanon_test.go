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
