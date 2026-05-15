package cievents_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/cievents"
)

// 2026-05-15T15:33:24Z lands in ISO week 20 of ISO year 2026, so YY.WW.SHA
// must come out as 26.20.<short>. Holding this fixed across tests means a
// wall-clock change won't flip any assertion.
var sampleTime = time.Date(2026, 5, 15, 15, 33, 24, 0, time.UTC)

func TestBuildVersion_FormatsAsYYWWShortSha(t *testing.T) {
	v, err := cievents.BuildVersion("1f07594513e95942a38cb6db95691b1a9a0d53b3", sampleTime)
	require.NoError(t, err)
	assert.Equal(t, "26.20.1f07594", v)
}

func TestBuildVersion_HandlesISOYearBoundary(t *testing.T) {
	// 2023-01-01 (Sunday) belongs to ISO week 52 of ISO year 2022. The
	// helper must report year 22, not 23.
	t1 := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	v, err := cievents.BuildVersion("abcdef1234567", t1)
	require.NoError(t, err)
	assert.Equal(t, "22.52.abcdef1", v)
}

func TestBuildVersion_NormalizesToUTC(t *testing.T) {
	// Same instant expressed in a non-UTC zone should produce the same
	// version as its UTC equivalent.
	loc, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)
	tLocal := time.Date(2026, 5, 15, 10, 33, 24, 0, loc)
	v, err := cievents.BuildVersion("1f07594513e95942a38cb6db95691b1a9a0d53b3", tLocal)
	require.NoError(t, err)
	assert.Equal(t, "26.20.1f07594", v)
}

func TestBuildVersion_ShortShaRejected(t *testing.T) {
	_, err := cievents.BuildVersion("abc", sampleTime)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestBuildVersionFromString_RFC3339(t *testing.T) {
	v, err := cievents.BuildVersionFromString("1f07594513e95942a38cb6db95691b1a9a0d53b3", "2026-05-15T15:33:24Z")
	require.NoError(t, err)
	assert.Equal(t, "26.20.1f07594", v)
}

func TestBuildVersionFromString_GitHubFallbackFormat(t *testing.T) {
	// Same Z-suffixed format, no fractional seconds — RFC3339 accepts this
	// too, but exercising the call path keeps the fallback regression-safe.
	v, err := cievents.BuildVersionFromString("1f07594513e95942a38cb6db95691b1a9a0d53b3", "2026-05-15T15:33:24Z")
	require.NoError(t, err)
	assert.Equal(t, "26.20.1f07594", v)
}

func TestBuildVersionFromString_InvalidTimestamp(t *testing.T) {
	_, err := cievents.BuildVersionFromString("1f07594513e95942a38cb6db95691b1a9a0d53b3", "not-a-date")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse timestamp")
}

func TestCommitURL_BuildsCanonicalGitHubURL(t *testing.T) {
	url := cievents.CommitURL("github.com/MyCarrier-Engineering/MC.Example", "1f07594513e95942a38cb6db95691b1a9a0d53b3")
	assert.Equal(
		t,
		"https://github.com/MyCarrier-Engineering/MC.Example/commit/1f07594513e95942a38cb6db95691b1a9a0d53b3",
		url,
	)
}

func TestExtractComponentName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"src/MC.Example.Api", "MC.Example.Api"},
		{"src/MC.Example.MigrationTool", "MC.Example.MigrationTool"},
		{"testing/MC.Example.AutomatedTests/Tests", "MC.Example.AutomatedTests"},
		{"Dockerfile", "Dockerfile"},         // no slash → return as-is
		{"single-segment", "single-segment"}, // no slash → return as-is
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, cievents.ExtractComponentName(tc.path))
		})
	}
}

// --- BuildEvent ---

func TestBuildEvent_PayloadShapeMatchesKafkaSensorMapping(t *testing.T) {
	ev, err := cievents.BuildEvent(cievents.BuildEventInput{
		Author:        "alice@mycarrier.io",
		CISystem:      "Argo",
		RepoURL:       "github.com/MyCarrier-Engineering/MC.Example",
		Organization:  "MyCarrier-Engineering",
		Branch:        "integration",
		CommitSHA:     "1f07594513e95942a38cb6db95691b1a9a0d53b3",
		BuildVersion:  "26.20.1f07594",
		CorrelationID: "corr-1",
		AppPath:       "src/MC.Example.Api",
		BuildArgs:     []string{"BASE_IMAGE_TAG=1.2.3"},
	})
	require.NoError(t, err)

	assert.Equal(t, "git.push", ev.Type())
	assert.Equal(
		t,
		"https://github.com/MyCarrier-Engineering/MC.Example/commit/1f07594513e95942a38cb6db95691b1a9a0d53b3",
		ev.Source(),
	)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ev.Data(), &data))
	assert.Equal(t, "alice@mycarrier.io", data["author"])
	assert.Equal(t, "github.com/MyCarrier-Engineering/MC.Example", data["repo"])
	assert.Equal(t, "refs/heads/integration", data["branch"]) // prefix added by the helper
	assert.Equal(t, "MyCarrier-Engineering", data["organization"])
	assert.Equal(t, "src/MC.Example.Api", data["path"])
	assert.Equal(t, "", data["extra_paths"])
	assert.Equal(t, "26.20.1f07594", data["version"])
	assert.Equal(t, "corr-1", data["correlation_id"])
	assert.Equal(t, "argo", data["ci_system"]) // lowercased
	assert.Equal(t, []any{"BASE_IMAGE_TAG=1.2.3"}, data["build_args"])
}

func TestBuildEvent_NilBuildArgsEmitsEmptyArray(t *testing.T) {
	ev, err := cievents.BuildEvent(cievents.BuildEventInput{
		RepoURL:   "github.com/Org/Repo",
		CommitSHA: "abcdef1234567",
		AppPath:   "src/Service",
		Branch:    "main",
	})
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ev.Data(), &data))
	// JSON unmarshal of an empty []string surfaces as an empty []any, not
	// nil — sensors that expect a JSON array won't trip on a missing field.
	assert.Equal(t, []any{}, data["build_args"])
}

// --- UnitTesterEvent ---

func TestUnitTesterEvent_PayloadShape(t *testing.T) {
	ev, err := cievents.UnitTesterEvent(cievents.UnitTesterEventInput{
		Author:        "alice@mycarrier.io",
		CISystem:      "Argo",
		RepoURL:       "github.com/MyCarrier-Engineering/MC.Example",
		Organization:  "MyCarrier-Engineering",
		Branch:        "integration",
		CommitSHA:     "1f07594513e95942a38cb6db95691b1a9a0d53b3",
		BuildVersion:  "26.20.1f07594",
		CorrelationID: "corr-1",
	})
	require.NoError(t, err)

	assert.Equal(t, "unit.tester", ev.Type())
	assert.Equal(
		t,
		"https://github.com/MyCarrier-Engineering/MC.Example/commit/1f07594513e95942a38cb6db95691b1a9a0d53b3",
		ev.Source(),
	)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ev.Data(), &data))
	assert.Equal(t, "alice@mycarrier.io", data["author"])
	assert.Equal(t, "refs/heads/integration", data["branch"])
	assert.Equal(t, "26.20.1f07594", data["version"])
	assert.Equal(t, "1f07594513e95942a38cb6db95691b1a9a0d53b3", data["commit_sha"])
	// unit.tester reuses the build payload shape but with empty path /
	// extra_paths — assert this so any future drift is loud.
	assert.Equal(t, "", data["path"])
	assert.Equal(t, "", data["extra_paths"])
}

// --- SecretScanEvent ---

func TestSecretScanEvent_PayloadShape(t *testing.T) {
	ev, err := cievents.SecretScanEvent(cievents.SecretScanEventInput{
		Author:           "alice@mycarrier.io",
		RepoName:         "MC.Example",
		Organization:     "MyCarrier-Engineering",
		Branch:           "integration",
		CommitSHA:        "1f07594513e95942a38cb6db95691b1a9a0d53b3",
		CorrelationID:    "corr-1",
		CommitSourceRepo: "github.com/MyCarrier-Engineering/MC.Example",
	})
	require.NoError(t, err)

	assert.Equal(t, "scan.secrets", ev.Type())
	assert.Equal(
		t,
		"https://github.com/MyCarrier-Engineering/MC.Example/commit/1f07594513e95942a38cb6db95691b1a9a0d53b3",
		ev.Source(),
	)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ev.Data(), &data))
	assert.Equal(t, "alice@mycarrier.io", data["author"])
	// secretscan reads `repository`, not `repo` — preserve the field name.
	assert.Equal(t, "MC.Example", data["repository"])
	assert.Equal(t, "MyCarrier-Engineering", data["organization"])
	assert.Equal(t, "refs/heads/integration", data["branch"])
	assert.Equal(t, "1f07594513e95942a38cb6db95691b1a9a0d53b3", data["commit_sha"])
	assert.Equal(t, "corr-1", data["correlation_id"])
	// No `repo` field — guard against accidentally adding one.
	_, hasRepoField := data["repo"]
	assert.False(t, hasRepoField, "secretscan payload must not include a 'repo' field")
}
