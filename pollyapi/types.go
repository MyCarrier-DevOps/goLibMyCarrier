package pollyapi

// These request/response types mirror polly-api's internal api package
// (github.com/mycarrier/polly-api/internal/api). That package lives under
// internal/ and cannot be imported, so the wire contract is duplicated here.
// Keep this file in sync with polly-api when the API changes.

// ErrorResponse is the JSON envelope polly-api returns on the auth (401) error
// path. Handler errors instead use Huma's application/problem+json shape; the
// client's APIError decoding tolerates both. See apierror.go.
type ErrorResponse struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

// BaseCheckRun holds fields common to every check run request.
type BaseCheckRun struct {
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	SHA        string `json:"sha"`
	RunID      string `json:"run_id"`
	Conclusion string `json:"conclusion,omitempty"`
}

// IsCompleted reports whether a conclusion is present, signalling the update path.
func (b BaseCheckRun) IsCompleted() bool { return b.Conclusion != "" }

// BuildRequest is the payload for the "build" workflow check run.
type BuildRequest struct {
	BaseCheckRun
	Tag                   string `json:"tag"`
	Version               string `json:"version,omitempty"`
	JobID                 string `json:"job_id,omitempty"`
	GrafanaURL            string `json:"grafana_url"`
	BuildStatusGrafanaURL string `json:"build_status_grafana_url,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r BuildRequest) CheckRunName() string { return "BuildV2." + r.Tag }

// UnittestFinding is a single diff-cover finding row.
type UnittestFinding struct {
	File   string `json:"file"`
	Lines  string `json:"lines"`
	Status string `json:"status"`
}

// UnittestRequest is the payload for the "unittest" workflow check run.
type UnittestRequest struct {
	BaseCheckRun
	Service         string `json:"service"`
	GrafanaURL      string `json:"grafana_url"`
	PassCount       int    `json:"pass_count,omitempty"`
	FailCount       int    `json:"fail_count,omitempty"`
	CoveragePct     string `json:"coverage_pct,omitempty"`
	DiffCoveragePct string `json:"diff_coverage_pct,omitempty"`
	DiffReport      string `json:"diff_report,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r UnittestRequest) CheckRunName() string { return "UnitTestV2." + r.Service }

// DeployRequest is the payload for the "deploy" workflow check run.
type DeployRequest struct {
	BaseCheckRun
	Environment  string `json:"environment"`
	JobID        string `json:"job_id"`
	GrafanaURL   string `json:"grafana_url"`
	GitopsCommit string `json:"gitops_commit,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r DeployRequest) CheckRunName() string { return "RenderV2." + r.Repo + "." + r.Environment }

// OffloadRequest is the payload for the "offload" workflow check run.
type OffloadRequest struct {
	BaseCheckRun
	Environment string `json:"environment"`
	Operation   string `json:"operation"`
	JobID       string `json:"job_id"`
	GrafanaURL  string `json:"grafana_url"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r OffloadRequest) CheckRunName() string {
	return "OffloadV2." + r.Environment + "." + r.Operation
}

// NpmArtifactRequest is the payload for the "npmartifact" workflow check run.
type NpmArtifactRequest struct {
	BaseCheckRun
	GrafanaURL        string `json:"grafana_url"`
	PackagesPublished string `json:"packages_published,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r NpmArtifactRequest) CheckRunName() string { return "NpmArtifact." + r.Repo }

// NugetArtifactRequest is the payload for the "nugetartifact" workflow check run.
type NugetArtifactRequest struct {
	BaseCheckRun
	GrafanaURL  string `json:"grafana_url"`
	PackageList string `json:"package_list,omitempty"`
	Version     string `json:"version,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r NugetArtifactRequest) CheckRunName() string { return "NugetArtifactV2." + r.Repo }

// ArtifactValidationRequest is the payload for the "artifactvalidation" workflow check run.
type ArtifactValidationRequest struct {
	BaseCheckRun
	GrafanaURL  string `json:"grafana_url"`
	JobStatus   string `json:"job_status,omitempty"`
	PackageList string `json:"package_list,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r ArtifactValidationRequest) CheckRunName() string { return "Check: Artifact Validation" }

// SecretsValidationFinding is a single gitleaks finding.
type SecretsValidationFinding struct {
	File   string `json:"file"`
	RuleID string `json:"rule_id"`
	Line   int    `json:"line"`
	URL    string `json:"url,omitempty"`
}

// SecretsValidationRequest is the payload for the "secretsvalidation" workflow check run.
type SecretsValidationRequest struct {
	BaseCheckRun
	Branch     string                     `json:"branch"`
	BaseBranch string                     `json:"base_branch"`
	GrafanaURL string                     `json:"grafana_url,omitempty"`
	LeakCount  int                        `json:"leak_count,omitempty"`
	Findings   []SecretsValidationFinding `json:"findings,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r SecretsValidationRequest) CheckRunName() string { return "Check: Secrets Validation" }

// SecretScanFinding is a single gitleaks finding for the full repo scan.
type SecretScanFinding struct {
	File   string `json:"file"`
	RuleID string `json:"rule_id"`
	Line   int    `json:"line"`
	URL    string `json:"url,omitempty"`
}

// SecretScanRequest is the payload for the "secretscan" workflow check run.
type SecretScanRequest struct {
	BaseCheckRun
	Findings []SecretScanFinding `json:"findings,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r SecretScanRequest) CheckRunName() string { return "Secret Scan" }

// GHASSecretsScanRequest is the payload for the "ghassecretsscan" workflow check run.
type GHASSecretsScanRequest struct {
	BaseCheckRun
	AlertURLs []string `json:"alert_urls,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r GHASSecretsScanRequest) CheckRunName() string { return "GHAS Secret Scanning Alerts Check" }

// AlertGateAlert is a single firing alert.
type AlertGateAlert struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// AlertGateRequest is the payload for the "alertgate" workflow check run.
type AlertGateRequest struct {
	BaseCheckRun
	Environment string           `json:"environment"`
	Service     string           `json:"service"`
	SourceType  string           `json:"source_type"`
	Alerts      []AlertGateAlert `json:"alerts,omitempty"`
	GrafanaURL  string           `json:"grafana_url,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r AlertGateRequest) CheckRunName() string { return "Alert gate" }

// GitopsRollbackRequest is the payload for the "gitopsrollback" workflow check run.
type GitopsRollbackRequest struct {
	BaseCheckRun
	Environment       string `json:"environment"`
	GitopsCommitShort string `json:"gitops_commit_short"`
	GitopsCommitURL   string `json:"gitops_commit_url"`
	SourceCommitURL   string `json:"source_commit_url"`
	GrafanaURL        string `json:"grafana_url,omitempty"`
	FailureReason     string `json:"failure_reason,omitempty"`
}

// CheckRunName returns the GitHub check run name polly-api will derive for this request.
func (r GitopsRollbackRequest) CheckRunName() string { return "GitOps rollback" }

// CustomRequest is the payload for the "custom" workflow check run. The caller
// supplies pre-rendered markdown — polly-api passes it through as-is.
type CustomRequest struct {
	BaseCheckRun
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Text    string `json:"text,omitempty"`
}

// CheckRunName returns the caller-supplied check run name.
func (r CustomRequest) CheckRunName() string { return r.Name }

// CommentRequest is the payload for the PR comment endpoint.
type CommentRequest struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	PR     int    `json:"pr"`
	Marker string `json:"marker"`
	Body   string `json:"body"`
}
