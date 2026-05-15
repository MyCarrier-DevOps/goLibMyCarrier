[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents)
# CI Events

The `cievents` package owns the canonical CloudEvents that drive MyCarrier's CI pipeline. Two services produce these events today:

- **`pushhookparser`** fans them out across Kafka when a GitHub push webhook arrives. The workflow-core sensors pick them up and instantiate `buildkit` / `unit-test` / `secretscan` workflows.
- **`pipeline-api`** mints the same shapes when a developer fires a job manually, so it can either replay them through the existing sensors or apply the sensor's parameter mapping locally before submitting the WorkflowTemplate directly.

This package is **compose-only**: it returns `cloudevents.Event` values and does nothing with Kafka, OpenTelemetry, or logging. Callers wrap the returned events with whatever transport and tracing they need.

## Why this exists

The shape of every CI-triggering event is a public contract between the producing service and the workflow-core sensors. Keeping that contract in one place avoids two implementations drifting and breaking workflows in subtle ways (wrong `commit` form, wrong `version` format, missing `build_args`, etc.). The conventions encoded here mirror the production payloads:

| Field | Convention |
|---|---|
| `repo` | `host/owner/name` (e.g. `github.com/Org/Repo`) — buildkit/unit-test labelsFrom does `split(repo, "/")[2]`. |
| `branch` | `refs/heads/<branch>` — helpers prepend the prefix. |
| `version` | `YY.WW.SHORT_SHA` — `BuildVersion()` derives it from a commit SHA and a UTC timestamp. The build script does `cut -d'.' -f3` to extract the SHA. |
| `source` | The GitHub commit URL — `CommitURL()` builds it. The buildkit sensor pipes this onto `workflow.parameters.commit`. |
| `repository` | Bare repo name (no owner prefix) — used by secretscan instead of `repo`. |

## API

```go
import "github.com/MyCarrier-DevOps/goLibMyCarrier/cievents"
```

### Helpers

- `BuildVersion(commitSHA string, t time.Time) (string, error)` — `YY.WW.SHORT_SHA` from a SHA and an event timestamp; UTC-normalised.
- `BuildVersionFromString(commitSHA, timestamp string) (string, error)` — same, but parses the timestamp from RFC3339 (or GitHub's `2006-01-02T15:04:05Z` fallback).
- `CommitURL(repo, commitSHA string) string` — canonical GitHub commit URL.
- `ExtractComponentName(path string) string` — pulls the component name out of a build path (`src/MC.Foo` → `MC.Foo`).

### Event constructors

Each constructor takes a typed input struct and returns a fully composed `cloudevents.Event` ready to be sent or applied to a sensor mapping.

- `BuildEvent(BuildEventInput) (cloudevents.Event, error)` — `git.push` (per component).
- `UnitTesterEvent(UnitTesterEventInput) (cloudevents.Event, error)` — `unit.tester` (one per push).
- `SecretScanEvent(SecretScanEventInput) (cloudevents.Event, error)` — `scan.secrets` (one per push).

## Example

```go
import (
    "time"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/cievents"
)

version, err := cievents.BuildVersion(commitSHA, slipCreatedAt)
if err != nil { /* ... */ }

ev, err := cievents.BuildEvent(cievents.BuildEventInput{
    Author:        "alice@mycarrier.io",
    CISystem:      "argo",
    RepoURL:       "github.com/MyCarrier-Engineering/MC.Example",
    Organization:  "MyCarrier-Engineering",
    Branch:        "integration",
    CommitSHA:     commitSHA,
    BuildVersion:  version,
    CorrelationID: correlationID,
    AppPath:       "src/MC.Example.Api",
})
if err != nil { /* ... */ }

// ev.Source() returns the commit URL; ev.Data() is the JSON payload the
// buildkit kafka-sensor's dataKey mappings extract parameters from.
```

## Testing

```bash
make test PKG=cievents
make lint PKG=cievents
```
