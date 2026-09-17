[![CI Status](https://github.com/MyCarrier-DevOps/goLibMyCarrier/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/MyCarrier-DevOps/goLibMyCarrier/actions/workflows/ci.yml)
# goLibMyCarrier

`goLibMyCarrier` is a Go library that provides utilities for authentication, configuration management, interaction with ClickHouse and OpenTelemetry integration. It is designed to streamline the development of Go applications by providing essential components for these common tasks.

## Overview

`goLibMyCarrier` includes the following components:

-   **`argocdclient`**: Provides a client to fetch ArgoCD application and manifest data with retry logic and error handling.
-   **`auth`**: Provides authentication middleware for Gin framework.
-   **`cievents`**: Constructs the CloudEvents (`git.push`, `unit.tester`, `scan.secrets`) that drive MyCarrier's CI pipeline. Compose-only — no transport — so both pushhookparser and pipeline-api can share the canonical payload shape.
-   **`clickhouse`**: Provides utilities to connect and query ClickHouse database.
-   **`github`**: Provides utilities to authenticate and interact with Github.
-   **`kafka`**: Provides utilities to produce and consume messages from Kafka.
-   **`logger`**: Provides a pre-configured logger using `go.uber.org/zap`.
-   **`otel`**: Integrates with OpenTelemetry for distributed tracing and logging.
-   **`vault`**: Provides utilities to interact with HashiCorp Vault.
-   **`yaml`**: Provides utilities to read and write yaml files.

## Installation

To install `goLibMyCarrier`, use the following Go command:

```bash
go get -u github.com/MyCarrier-DevOps/goLibMyCarrier/<module>@<version>
```

### Example

```bash
go get -u github.com/MyCarrier-DevOps/goLibMyCarrier/kafka@v1.3.6
```

## Go Pkg + Reports

| Package | Go Reference | Go Report Card |
|---------|--------------|----------------|
| `argocdclient` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient) |
| `auth` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/auth.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/auth) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/auth)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/auth) |
| `cievents` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/cievents) |
| `clickhouse` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse) |
| `github` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/github.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/github) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/github)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/github) |
| `kafka` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka) |
| `logger` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/logger.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/logger) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/logger)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/logger) |
| `otel` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/otel.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/otel) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/otel)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/otel) |
| `vault` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/vault.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/vault) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/vault)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/vault) |
| `yaml` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml) |

## Development

### Running Tests

Run the full test suite (all modules) with the race detector enabled:

```bash
make test
```

Run tests for a single module:

```bash
make test PKG=slippy
```

Run tests manually with the race detector for a single module:

```bash
cd slippy && go test -race -count=1 -timeout 120s ./...
```

The `-race` flag instruments the binary to detect concurrent memory access violations at runtime. It adds ~2–10× CPU overhead but catches data races that would otherwise produce non-deterministic bugs. **Always run with `-race` locally before opening a PR.**

### Other Make Targets

| Target | Description |
|--------|-------------|
| `make lint` | Run `golangci-lint` across all modules |
| `make fmt` | Format all modules |
| `make tidy` | Run `go mod tidy` across all modules |
| `make check-sec` | Scan for known vulnerabilities |
| `make test PKG=<module>` | Test a single module |
| `make lint PKG=<module>` | Lint a single module |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

When contributing:
1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Write tests for your changes
4. Run lint checks (`make lint`)
5. Ensure all tests pass (`make test`)
6. Cleanup go deps (`make tidy`)
7. Commit your changes (`git commit -m 'Add some amazing feature'`)
8. Push to the branch (`git push origin feature/amazing-feature`)
9. Open a Pull Request

## Versioning

This library follows [Semantic Versioning](https://semver.org/).

## Breaking changes

These are breaking, so the release is a **minor** bump, not a patch: the merge commit must be
tagged `slippy/v1.4.0` (and the sibling modules likewise, since every module in this repo
releases at one shared version). `.github/Gitversion.yml` carries `next-version: 1.4.0` plus
the `ConfiguredNextVersion` strategy that makes that floor effective, so main's `Patch`
increment cannot land these on consumers pinned to `v1.3.x` with no signal. **The floor is
for this release only: delete `next-version` (and `ConfiguredNextVersion`) once `v1.4.0` is
tagged on main.** Left in place it would let the NEXT breaking `SlipStore` change ship as a
`1.4.x` patch to consumers pinned to `1.4` — the same defect it was added to fix.

### `slippy`: the claim joins `SlipStore` — since `v1.4.0` (DEVOPS-367)

This is the break the `v1.4.0` floor above exists for, and it is the one a consumer meets
first: `slippy.SlipStore` gained three methods, so any out-of-repo implementation (a
`var _ slippy.SlipStore = (*fakeStore)(nil)` assertion, a hand-rolled test double) fails to
compile until all three exist. Full contracts are on the interface in `slippy/interfaces.go`
and the model is in `.github/STATE_MACHINE_V3.md`; the migrations are covered by
`slippy/CLAUDE.md`'s rollout-order section.

| What changed | Migration |
|---|---|
| `ClaimSlip(ctx, correlationID string, expected []SlipStatus, claimedBy, reason string) (ClaimOutcome, error)` — **new on `SlipStore`**, and it returns an outcome rather than a bare error | Implement it. Route the decision through `slippy.DecideClaim` so it cannot drift from the store; a store that cannot claim returns `ErrClaimUnsupported` (wrapped), as `ClickHouseStore` does. `ClaimOutcome{Claimed, Prior, InFlight}`: `Claimed=false` is the idempotent repeat with nothing written, not a failure, and a caller that must not dispatch onto running work branches on `InFlight`. |
| `ReleaseClaim(ctx, correlationID, releasedBy, reason string) (ReleaseOutcome, error)` — **new on `SlipStore`**, also an outcome | Implement it via `slippy.DecideRelease`. `ReleaseOutcome{Released, Status}`: `Released=false` means work is in flight and the claim was KEPT with nothing written — the arm all but the last of a run's N post-job releases take. |
| `ProbeSchema(ctx) error` — **new on `SlipStore`** | Implement it. A store with no schema of its own returns `nil`. Consumers reach it through `Client.ProbeSchema` and treat `ErrSchemaBehind` as "not ready". |
| `ErrRunInFlight` — **removed** | Delete the `errors.Is(err, slippy.ErrRunInFlight)` branch and read `ReleaseOutcome.Released` instead. Work in flight stopped being an error because it is the normal outcome, not a failure. |
| `ReleaseMarker(status SlipStatus, releasedBy, reason string)` — **signature changed** | Drop the old `restored` argument at the call site. A release never restores anything; the status it records is the slip's status at release time. |
| `DecideClaim(status, claimedFrom SlipStatus, inFlight bool, expected []SlipStatus)` — **signature changed** within `v1.4.0` | Pass `slippy.RunInFlight(slip)` read from the same locked row, and put that same value in `ClaimOutcome.InFlight`. The live-run refusal reads that evidence rather than the status name, so a `pending` slip with a step running is refused and an `in_progress` slip with nothing running is claimable. |

`slippytest.MockStore.CommitIndex` was also removed in the same release (DEVOPS-231); see
`slippy/CLAUDE.md` for why deleting the line is usually — but not always — the whole fix.

### `postgresmigrator` / `clickhousemigrator`: `MigrationError.Unwrap()` returns `[]error` — since `v1.4.0` (DEVOPS-344)

`MigrationError.Unwrap()` now returns `[]error` instead of a single `error`: the
`ErrMigrationFailed` sentinel, plus `ErrMigrationRevertFailed` for a down in **either**
migrator, then the underlying cause. `errors.Is` and `errors.As` see all of them, so sentinel
checks and `errors.As(err, &pgErr)` keep working — and `errors.Is(err, ErrMigrationFailed)`
starts working, which is the point of the change.

What breaks: `errors.Unwrap` only calls the single-error `Unwrap() error` form, so
`errors.Unwrap(err)` and a direct `migErr.Unwrap()` no longer hand back the cause. Use
`migErr.Cause()` for that.

## Support

For issues, questions, or contributions, please visit the [GitHub repository](https://github.com/MyCarrier-DevOps/go-client-langfuse).
