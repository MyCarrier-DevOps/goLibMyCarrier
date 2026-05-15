// Package cievents constructs the CloudEvents that drive MyCarrier's CI
// pipeline. Two services produce these events:
//
//   - pushhookparser, when a GitHub push webhook arrives, fans them out across
//     Kafka so the workflow-core sensors instantiate buildkit / unit-test /
//     secretscan workflows automatically.
//   - pipeline-api, when a developer fires a job manually, mints the same
//     event shapes so it can either replay them through the existing Kafka
//     sensors or apply the sensor's parameter mapping locally before
//     submitting the WorkflowTemplate directly.
//
// This package owns the canonical event payloads — every field a downstream
// sensor expects to dataKey-map onto a workflow parameter — so the shape
// lives in exactly one place. It is intentionally a *compose-only* library:
// it does not touch Kafka, OpenTelemetry, or logging. Callers wrap the
// returned cloudevents.Event with whatever transport and tracing they need.
//
// Field conventions that the workflow-core sensors rely on, preserved here:
//
//   - `repo`         host/owner/name form (e.g. `github.com/Org/Repo`); the
//     buildkit and unit-test labelsFrom expressions do
//     `split(repo, "/")[2]`.
//   - `branch`       `refs/heads/<branch>` form; sensors index segment [2]
//     when computing the branch label.
//   - `source` URI   the GitHub commit URL; the buildkit sensor pipes this
//     onto the workflow's `commit` parameter, whose
//     labelsFrom does `split(commit, "/")[6]`.
//   - `version`      `YY.WW.SHORT_SHA` calendar-versioned image tag; the
//     build script does `cut -d'.' -f3` on it to extract the
//     commit SHA for check-run posting.
//   - `repository`   bare repo name (no owner prefix); secretscan uses this
//     shape rather than `repo`.
package cievents

import (
	"fmt"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

// BuildVersion returns the `YY.WW.SHORT_SHA` build version derived from a
// commit SHA and an event timestamp.
//
// Format components:
//   - YY  — two-digit ISO year (handles year-boundary correctly: Jan 1
//     can belong to ISO week 52 of the previous ISO year)
//   - WW  — zero-padded ISO week number
//   - SHA — first 7 characters of the commit SHA
//
// The timestamp is converted to UTC before extracting the year and week so
// the result aligns with slippy's server-recorded `created_at`, which is
// also UTC.
func BuildVersion(commitSHA string, t time.Time) (string, error) {
	if len(commitSHA) < 7 {
		return "", fmt.Errorf("commit SHA %q is too short, must be at least 7 characters", commitSHA)
	}
	t = t.UTC()
	isoYear, week := t.ISOWeek()
	return fmt.Sprintf("%02d.%02d.%s", isoYear%100, week, commitSHA[:7]), nil
}

// BuildVersionFromString parses an RFC3339 (or GitHub's
// `2006-01-02T15:04:05Z`) timestamp and returns the corresponding
// BuildVersion. Convenience for callers that hold the timestamp as a
// string (e.g. JSON payloads, slip API responses before unmarshalling
// into time.Time).
func BuildVersionFromString(commitSHA, timestamp string) (string, error) {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		// GitHub push events sometimes ship without a numeric offset; try
		// the more lenient form before giving up.
		t, err = time.Parse("2006-01-02T15:04:05Z", timestamp)
		if err != nil {
			return "", fmt.Errorf("parse timestamp %q: %w", timestamp, err)
		}
	}
	return BuildVersion(commitSHA, t)
}

// CommitURL returns the canonical commit URL used as the CloudEvents
// `source` for every CI-triggering event in this package. `repo` is the
// host/owner/name form (e.g. `github.com/MyCarrier-Engineering/MC.Example`).
// The buildkit sensor pipes this URL into the workflow's `commit`
// parameter; the template's labelsFrom expression
// `split(workflow.parameters.commit, "/")[6]` extracts the SHA.
func CommitURL(repo, commitSHA string) string {
	return "https://" + repo + "/commit/" + commitSHA
}

// ExtractComponentName extracts the component name from a build path.
//
//	"src/MC.Example.Api"                       -> "MC.Example.Api"
//	"testing/MC.Example.AutomatedTests/Tests"  -> "MC.Example.AutomatedTests"
//	"Dockerfile"                               -> "Dockerfile" (unchanged)
//
// Matches pushhookparser's historical extraction: split on "/", return the
// second segment when one exists, otherwise return the path unchanged.
func ExtractComponentName(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return path
}

// BuildEventInput is the data needed to compose a single git.push CloudEvent
// (one component, one event). The buildkit kafka-sensor maps every field on
// this struct onto a buildkit WorkflowTemplate parameter.
type BuildEventInput struct {
	Author        string   // commit author email
	CISystem      string   // "argo" — lowercased before emitting
	RepoURL       string   // host/owner/name (e.g. github.com/Org/Repo)
	Organization  string   // GitHub organization
	Branch        string   // bare branch name; `refs/heads/` is prepended on emit
	CommitSHA     string   // full commit SHA; the helper builds the commit URL
	BuildVersion  string   // YY.WW.SHORT_SHA (use BuildVersion to compute)
	CorrelationID string   // slippy correlation_id
	AppPath       string   // build path, e.g. src/MC.Example.Api
	BuildArgs     []string // optional; empty slice is fine
}

// BuildEvent composes the git.push CloudEvent that triggers a buildkit
// workflow for one component. Callers fan out by calling this once per
// AppPath. The returned event's source is the commit URL so the buildkit
// sensor's body.source → commit mapping resolves correctly.
func BuildEvent(in BuildEventInput) (cloudevents.Event, error) {
	if in.BuildArgs == nil {
		in.BuildArgs = []string{}
	}
	data := map[string]any{
		"author":         in.Author,
		"repo":           in.RepoURL,
		"branch":         "refs/heads/" + in.Branch,
		"organization":   in.Organization,
		"path":           in.AppPath,
		"extra_paths":    "",
		"version":        in.BuildVersion,
		"correlation_id": in.CorrelationID,
		"build_args":     in.BuildArgs,
		"build_image":    "",
		"build_tag":      "",
		"ci_system":      strings.ToLower(in.CISystem),
	}
	return composeEvent(data, "git.push", CommitURL(in.RepoURL, in.CommitSHA))
}

// UnitTesterEventInput is the data needed to compose a unit.tester CloudEvent
// triggering a unit-test workflow. The shape mirrors a BuildEvent payload
// minus per-component fields, plus an explicit `commit_sha` field that the
// unit-test template reads.
type UnitTesterEventInput struct {
	Author        string
	CISystem      string
	RepoURL       string // host/owner/name
	Organization  string
	Branch        string // bare branch name; `refs/heads/` is prepended on emit
	CommitSHA     string
	BuildVersion  string
	CorrelationID string
}

// UnitTesterEvent composes the unit.tester CloudEvent. Unit-test is not a
// per-component workflow in production — exactly one event per push.
func UnitTesterEvent(in UnitTesterEventInput) (cloudevents.Event, error) {
	data := map[string]any{
		"author":         in.Author,
		"repo":           in.RepoURL,
		"branch":         "refs/heads/" + in.Branch,
		"organization":   in.Organization,
		"path":           "",
		"extra_paths":    "",
		"version":        in.BuildVersion,
		"correlation_id": in.CorrelationID,
		"build_args":     "",
		"build_image":    "",
		"build_tag":      "",
		"ci_system":      strings.ToLower(in.CISystem),
		"commit_sha":     in.CommitSHA,
	}
	return composeEvent(data, "unit.tester", CommitURL(in.RepoURL, in.CommitSHA))
}

// SecretScanEventInput is the data needed to compose a scan.secrets
// CloudEvent triggering a secretscan workflow. Note `RepoName` is the bare
// repo (not host/owner/name) — secretscan's sensor maps it onto
// `workflow.parameters.repository`, which the template uses directly.
type SecretScanEventInput struct {
	Author        string
	RepoName      string // bare repo, e.g. "MC.Example"
	Organization  string // GitHub organization
	Branch        string // bare branch name; `refs/heads/` is prepended on emit
	CommitSHA     string
	CorrelationID string

	// CommitSourceRepo is the host/owner/name string used to construct the
	// CloudEvents source URI. Separate from RepoName so the helper preserves
	// the canonical event source even when the workflow consumes only the
	// bare repo. e.g. "github.com/MyCarrier-Engineering/MC.Example".
	CommitSourceRepo string
}

// SecretScanEvent composes the scan.secrets CloudEvent for one push.
func SecretScanEvent(in SecretScanEventInput) (cloudevents.Event, error) {
	data := map[string]any{
		"author":         in.Author,
		"repository":     in.RepoName,
		"branch":         "refs/heads/" + in.Branch,
		"organization":   in.Organization,
		"commit_sha":     in.CommitSHA,
		"correlation_id": in.CorrelationID,
	}
	return composeEvent(data, "scan.secrets", CommitURL(in.CommitSourceRepo, in.CommitSHA))
}

// composeEvent wraps a payload map in a CloudEvent with the given type and
// source. Kept internal so callers can't accidentally emit a partial event
// without going through one of the typed constructors above.
func composeEvent(data map[string]any, eventType, eventSource string) (cloudevents.Event, error) {
	event := cloudevents.NewEvent()
	event.SetSource(eventSource)
	event.SetType(eventType)
	if err := event.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return event, fmt.Errorf("set CloudEvent data: %w", err)
	}
	return event, nil
}
