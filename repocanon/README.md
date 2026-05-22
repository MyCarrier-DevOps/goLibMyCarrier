[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/repocanon.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/repocanon) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/repocanon)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/repocanon)

# repocanon

Single source of truth for canonical MyCarrier repository-name normalization.
Maps any of the many input shapes a MyCarrier repo identifier can appear as
(full GitHub name, lowercase dotted, kebab, `owner/repo`, full clone URL, mixed
case) into two canonical outputs:

- **`Service`** — kebab form used for ArgoCD app names, K8s Service DNS, pod
  labels (e.g. `mycarrier-frontend`).
- **`Repository`** — dotted lowercase form used as the join key in
  `ci.repoproperties.repository` (e.g. `mc.mycarrier.frontend`).

Eliminates the 8+ places in DevOps repos where this transform has been
re-implemented and diverged.

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/repocanon
```

## Quick Start

```go
package main

import (
    "fmt"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/repocanon"
)

func main() {
    names := repocanon.FromRaw("MC.MyCarrier.Frontend")
    fmt.Println(names.Service)    // "mycarrier-frontend"
    fmt.Println(names.Repository) // "mc.mycarrier.frontend"
}
```

## Accepted Input Shapes

| Input | `Service` | `Repository` |
|---|---|---|
| `MC.MyCarrier.Frontend` | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `mc.mycarrier.frontend` | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `mycarrier.frontend` | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `mycarrier-frontend` | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `MyCarrier.Frontend` | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `MC.Order` | `order` | `mc.order` |
| `address` | `address` | `mc.address` |
| `mc-environment` | `environment` | `mc.environment` |
| `MC.Order.Events` | `order-events` | `mc.order.events` |
| `MyCarrier-Engineering/MC.MyCarrier.Frontend` | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `https://github.com/MyCarrier-Engineering/MC.MyCarrier.Frontend.git` | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `https://github.com/.../MC.MyCarrier.Frontend/` (trailing slash) | `mycarrier-frontend` | `mc.mycarrier.frontend` |
| `""` (empty) | `""` | `""` |

Empty / prefix-only input (`""`, `"mc."`, `"mc-"`) returns the zero-value
`Names{}` — callers should validate emptiness.

## Unsupported Shapes

The transform is **lossless only when the dot-or-hyphen boundary is preserved
in the input**. The following input shapes silently produce wrong-but-
non-empty output, and callers must avoid them:

### Collapsed form (boundary already lost)

| Input | `Service` (actual) | `Repository` (actual) | Canonical (expected) |
|---|---|---|---|
| `mycarrierfrontend` | `mycarrierfrontend` | `mc.mycarrierfrontend` | `mycarrier-frontend` / `mc.mycarrier.frontend` |

Once the boundary is collapsed, the transform **cannot** recover it — there
is no dictionary lookup. Callers must pass the **original un-stripped form**:
the full GitHub repo name (`MC.MyCarrier.Frontend`), a URL, or any
dot/hyphen-preserving shape. **Never pre-collapse hyphens before calling
`FromRaw`.**

This case is pinned by `TestFromRaw_CollapsedFormUnrecoverable` so it
surfaces in test reports if anyone introduces a regression that masks it.

### Leading-dot / multi-dot input

| Input | `Service` (actual) | `Repository` (actual) |
|---|---|---|
| `.foo` | `-foo` | `mc..foo` |
| `mc..foo` | `-foo` | `mc..foo` |

The resulting `Service` value (`"-foo"`) is **not** a valid RFC 1123 DNS
label (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`) and will be rejected by Kubernetes
and ArgoCD. `FromRaw` does **not** reject these inputs — callers MUST
validate output against RFC 1123 before passing `Service` to k8s/ArgoCD
APIs. Pinned by `TestFromRaw_LeadingDotFootGun`.

## `ArgoCDAppName` method

`Names.ArgoCDAppName(environment, chartType) string` composes the canonical
ArgoCD application name from a normalized `Service` plus the deploy
environment and chart-type. The rules mirror
`workflow-core/workflows/templates/render-deploy-core.yaml` exactly so
that workflow YAML and Go consumers stay in lock-step.

### Derivation rules (priority order)

| Condition | Result | Notes |
|---|---|---|
| `chartType == "mc-environment"` | `{svc}-{env}` | New unified scheme (any env) |
| `environment` starts with `feature` | `{svc}-offload-{env}` | Legacy feature offload |
| `environment == "prod"` | `production-csp-prod-{svc}` | Legacy prod cluster selector |
| _otherwise_ | `development-{env}-{svc}` | Legacy dev / preprod default |

`environment` and `chartType` are lowercased + whitespace-trimmed before
matching. The canonical `Service` form (kebab) is preserved verbatim.
Empty `Service` returns `""` — callers must validate emptiness before
passing the result to Kubernetes / ArgoCD APIs.

### Worked examples

| `Service` | `environment` | `chartType` | Result |
|---|---|---|---|
| `mycarrier-frontend` | `feature20` | `mc-environment` | `mycarrier-frontend-feature20` |
| `mycarrier-frontend` | `dev` | `mc-environment` | `mycarrier-frontend-dev` |
| `mycarrier-frontend` | `prod` | `mc-environment` | `mycarrier-frontend-prod` |
| `mycarrier-frontend` | `feature20` | `mycarrier-helm` | `mycarrier-frontend-offload-feature20` |
| `mycarrier-frontend` | `prod` | `mycarrier-helm` | `production-csp-prod-mycarrier-frontend` |
| `mycarrier-frontend` | `preprod` | `mycarrier-helm` | `development-preprod-mycarrier-frontend` |
| `order` | `dev` | `mycarrier-helm` | `development-dev-order` |
| `""` | `dev` | `mc-environment` | `""` (empty Service → empty result) |

### Locked-in edge cases

- **Empty environment** falls through to the legacy default branch and
  yields `development--{svc}` (locked in by `TestArgoCDAppName`). Callers
  should validate environment non-empty before invoking.
- **Mixed-case input** is normalized (`"FeatureX"` → matches feature
  prefix, output `{svc}-offload-featurex`).
- **Unknown chartType** (anything other than `mc-environment`) triggers
  the legacy 3-branch ladder.

```go
names := repocanon.FromRaw("MC.MyCarrier.Frontend")
appName := names.ArgoCDAppName("feature20", "mc-environment")
// → "mycarrier-frontend-feature20"
```

## Why not strip dots in `Repository`?

The `ci.repoproperties.repository` column in ClickHouse stores GitHub's
literal repository name lowercased — for `MC.MyCarrier.Frontend` that is
`mc.mycarrier.frontend`, dots included. Stripping the dots would break the
join against `ci.repoproperties` that every downstream consumer (DeployVerifier,
deploy-reporter, slippy-api repoproperties lookups) relies on. The dotted form
**is** the join key.

The kebab `Service` form, in contrast, is what ArgoCD, Kubernetes, and Helm use
for resource names (DNS-safe). Both forms must be derivable from the same
input.

## Replaces

This library replaces the override-registry pattern previously needed in
`MC.TestEngine/TestEngine.Worker/pkg/worker/service_name.go`
(`CanonicalDeployTargets`). The override map was needed because the input
shape (`TestConfig.StackName`) collapsed the dotted form to a single token
("mycarrierfrontend"), losing the boundary needed to reconstruct the kebab.
By applying the canonical transform to a shape that **preserves** the
boundary (dots or hyphens), the override registry is no longer required.

## Known Consumer List

Planned / in-flight integrators:

| Repo / path | Use site | Purpose |
|---|---|---|
| `MC.TestEngine/TestEngine.Worker` | `pkg/worker/jobenvvars.go` | Derives `SERVICE` and `REPOSITORY` env vars from the source repo for ArgoCD application lookup. Replaces the `CanonicalDeployTargets` override registry. |
| `autotriggertests` | `utils.go` (`GetStackName`) | Derives the canonical stack name for the kafka payload that triggers downstream pipelines. |
| `Slippy` | `ci/Slippy/internal/cli/canonical_name.go` | CLI subcommand exposing `FromRaw` to GitHub Actions workflows so YAML can render the canonical `Service` / `Repository` without re-implementing the rule. |

> **Future integrators:** please add yourself to this list when you wire
> `repocanon.FromRaw` into a new caller. It makes future migrations (rename,
> rule changes, deprecation of input shapes) significantly easier to scope.

## Deliberate Divergence

Not every repo-name transform in DevOps should call `FromRaw`. Some have
intentionally different semantics:

- **`autotriggertests/utils.go: extractRepoName`** — extracts the leaf repo
  name from a webhook payload's `repository.full_name` for use as a slip
  correlation tag. It does NOT apply the `mc.` strip or kebab transform; the
  raw GitHub name (case preserved) is the tag value by design. Do not migrate
  this caller.

If you add a new consumer whose semantics intentionally differ, document the
reason inline at the call site.

## Testing

```bash
cd repocanon
go test -race -covermode=atomic -count=1 ./...
```

Or from the repo root:

```bash
make test PKG=repocanon
make lint PKG=repocanon
```
