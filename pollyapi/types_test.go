package pollyapi

import "testing"

func TestCheckRunName(t *testing.T) {
	t.Parallel()
	base := BaseCheckRun{Owner: "o", Repo: "repo", SHA: "s", RunID: "w"}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"build", BuildRequest{BaseCheckRun: base, Tag: "v1.2.3"}.CheckRunName(), "BuildV2.v1.2.3"},
		{"unittest", UnittestRequest{BaseCheckRun: base, Service: "svc"}.CheckRunName(), "UnitTestV2.svc"},
		{"deploy", DeployRequest{BaseCheckRun: base, Environment: "prod"}.CheckRunName(), "RenderV2.repo.prod"},
		{
			"offload",
			OffloadRequest{BaseCheckRun: base, Environment: "prod", Operation: "drain"}.CheckRunName(),
			"OffloadV2.prod.drain",
		},
		{"npmartifact", NpmArtifactRequest{BaseCheckRun: base}.CheckRunName(), "NpmArtifact.repo"},
		{"nugetartifact", NugetArtifactRequest{BaseCheckRun: base}.CheckRunName(), "NugetArtifactV2.repo"},
		{
			"artifactvalidation",
			ArtifactValidationRequest{BaseCheckRun: base}.CheckRunName(),
			"Check: Artifact Validation",
		},
		{"secretsvalidation", SecretsValidationRequest{BaseCheckRun: base}.CheckRunName(), "Check: Secrets Validation"},
		{"secretscan", SecretScanRequest{BaseCheckRun: base}.CheckRunName(), "Secret Scan"},
		{
			"ghassecretsscan",
			GHASSecretsScanRequest{BaseCheckRun: base}.CheckRunName(),
			"GHAS Secret Scanning Alerts Check",
		},
		{"alertgate", AlertGateRequest{BaseCheckRun: base}.CheckRunName(), "Alert gate"},
		{"gitopsrollback", GitopsRollbackRequest{BaseCheckRun: base}.CheckRunName(), "GitOps rollback"},
		{"custom", CustomRequest{BaseCheckRun: base, Name: "Custom Check"}.CheckRunName(), "Custom Check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("CheckRunName() = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestIsCompleted(t *testing.T) {
	t.Parallel()
	if (BaseCheckRun{}).IsCompleted() {
		t.Error("empty conclusion should not be completed")
	}
	if !(BaseCheckRun{Conclusion: "success"}).IsCompleted() {
		t.Error("non-empty conclusion should be completed")
	}
}
