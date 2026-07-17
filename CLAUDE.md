# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

This repo's Go workflow — the idiomatic-Go conventions, the RED-test-first delivery loop, the security preflight, and the coverage gate — is owned by the **go-devkit** plugin (an apm dependency declared in [apm.yml](apm.yml); its machinery is installed outside the repo tree under `apm_modules/` via `apm install`). The go-devkit block below is the authoritative description of that workflow and is kept in sync by `/go-repo-init` — do not edit it by hand. The sections after it record the project-specific facts the plugin cannot know: this repo's module layout, coverage policy, and house rules.

<!-- BEGIN go-devkit -->
## Development workflow (go-devkit)

This repository uses the **go-devkit** Claude Code plugin. The idiomatic Go
conventions this project enforces (naming, error handling, package layout,
concurrency, HTTP clients, testing, security) live in the plugin and are loaded
by the `go-tdd` skill — read them before writing Go code.

For features and bugfixes, use the **`go-tdd`** skill — it drives the full loop:

1. **RED test first (code changes only).** Write a failing table-driven test
   before the implementation, then make it pass, then refactor — confirm it
   fails for the intended reason first. This does **not** apply to meta changes
   (renaming the app / `APPLICATION`, config, docs, dependency bumps); make those
   directly and verify with `/go-verify`.
2. **Preflight before coding.** Run `/go-preflight` (`make check-sec`) after
   planning and before implementing. If `govulncheck` flags a Go standard-library
   CVE, upgrade the toolchain (`brew upgrade go`, or `mise use -g go@latest`)
   before continuing; if it flags a dependency, `make bump`.
3. **Verify after.** Run `/go-verify` when the task is done — it runs `make fmt`,
   `make lint`, `make test`, and the plugin's coverage gate (which reads the CI
   `threshold-total` live so local and CI never drift).

**Pin the Go version to a full patch release, and keep it in sync.** The `go`
directive in the module's `go.mod` (e.g. `go 1.26.5`, not `go 1.26`) and the
builder image in the Dockerfile (`golang:1.26.5`) must name the same patch. CI
intentionally floats on the patch level (`go-version: "1.26"`); bump it by hand
for a new minor or major.
<!-- END go-devkit -->

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->


## Build & Test

**Always use Makefile targets, NOT raw `go` commands.** The Makefile encodes the canonical lint config, coverage thresholds, and tool versions used by CI.

```bash
make lint            # golangci-lint (NOT raw `golangci-lint run`)
make test            # go test w/ race + count + coverage flags (NOT raw `go test ./...`)
make fmt             # gofmt/goimports (NOT raw `gofmt -l`)
make tidy            # go mod tidy across all modules
make check-sec       # gosec scan (NOT raw `gosec ./...`)
make check-coverage  # coverage threshold gate
make bump            # version bump helper
```

Available targets: `make help` (if defined) or `grep -E "^[a-z_-]+:" Makefile`.

**Scope to a single package with `PKG=<module>`.** By default every target runs
across all modules in `LIB_DIRS`. When you're working in one package, pass
`PKG=<module>` to run just that one instead of the whole repo — e.g.
`make test PKG=slippy`, `make lint PKG=clickhouse`, `make check-sec PKG=vault`.
Only `lint`, `test`, and `check-sec` honor `PKG=`; `fmt`, `tidy`, and `bump`
always run across all modules.

**Raw `go build ./...` / `go vet ./...` are acceptable for quick verification** but final gate before commit must run `make lint && make test`.

**For subagents:** when briefing, include the Makefile targets explicitly. Don't let an agent reach for `golangci-lint run` or `go test ./...` directly — the Makefile flags differ from defaults and CI compares against Makefile output.

This applies to **all** Go-based MyCarrier tools that have a Makefile (slippy-api, MC.TestEngine, Slippy CLI, etc.).

## Architecture Overview

`goLibMyCarrier` is a **multi-module Go library** — a collection of independently
versioned packages, not a single service or binary. The root `go.mod`
(`github.com/MyCarrier-DevOps/goLibMyCarrier`) is a meta-module for version
tracking; consumers import each package by its own module path
(`.../goLibMyCarrier/slippy`, `.../goLibMyCarrier/clickhouse`, `.../goLibMyCarrier/logger`, …).

There are 16 modules, each with its own `go.mod` (the root `.` plus the dirs in
the Makefile's `LIB_DIRS`): `argocdclient`, `auth`, `cievents`, `clickhouse`,
`clickhousemigrator`, `github`, `kafka`, `logger`, `otel`, `pollyapi`,
`repocanon`, `slippy`, `slippyapi`, `vault`, `yaml`. The `make` targets iterate
`LIB_DIRS`, and CI discovers modules dynamically (`find . -name go.mod`) and fans
out per-module matrix jobs for test, lint, and vuln, then tags and releases every
module at one shared version.

## Conventions & Patterns

### Modules are listed in `LIB_DIRS`, not `APPLICATION`

The go-devkit block above refers to an `APPLICATION` variable — that's the
single-service Makefile pattern and **does not exist in this repo**. Because this
is a multi-module library, the Makefile enumerates its modules in a **`LIB_DIRS`**
variable instead, and every target (`lint`, `test`, `fmt`, `bump`, `tidy`,
`check-sec`) loops over `LIB_DIRS` (the `lint`, `test`, and `check-sec` targets
also accept `PKG=<module>` to scope to a single package — see Build & Test).
CI ignores `LIB_DIRS` and discovers modules dynamically via
`find . -name go.mod`. When you add or remove a module, update `LIB_DIRS` in the
Makefile — there is no `APPLICATION` line to touch.

### Go version policy (library exception to the go-devkit block above)

The go-devkit workflow block above prescribes pinning the `go` directive to a full
patch (`go 1.26.5`) and matching a Dockerfile builder tag — that guidance is for
single-binary **services**. This repo is a **published library** and deliberately
diverges:

- The `go` directive in every module stays at the **minor** (`go 1.26`). For a
  library the `go` directive is the *minimum* Go version a consumer must have, so
  keeping it at the minor keeps the compatibility floor as low as possible. Do
  **not** run `/go-repo-init`'s `pin-version` step here (it would raise the floor
  for all downstream consumers).
- There is **no Dockerfile** — nothing to pin a `golang:` builder tag against.
- CI floats on the minor via `go-version: ^1.26` with `check-latest: true`; bump
  the minor by hand for a new Go release.
