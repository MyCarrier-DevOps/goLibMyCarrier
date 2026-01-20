# goLibMyCarrier AI Development Instructions

## Additional Instructions

**You MUST also follow all applicable instruction files in `.github/instructions/`:**
- [go.instructions.md](.github/instructions/go.instructions.md) - Idiomatic Go patterns, naming conventions, error handling

For the slippy package specifically, read [slippy/CLAUDE.md](slippy/CLAUDE.md) for shadow mode, migration, and client patterns.

## Architecture Overview

This is a **multi-module Go monorepo** where each subdirectory is an independent Go module with its own `go.mod`. All modules share unified versioning via CI (e.g., `clickhouse/v1.3.43`, `slippy/v1.3.43`).

### Key Packages
- **clickhouse** - ClickHouse client with retry logic and session management
- **slippy** - Routing slip orchestration for CI/CD pipelines (most complex - see `slippy/CLAUDE.md`)
- **logger** - Zap-based structured logging with `LOG_LEVEL` and `LOG_APP_NAME` env vars
- **kafka**, **vault**, **github**, **auth**, **otel** - Infrastructure integrations

## Validation Workflow

**CRITICAL: Never bypass linting rules.** Do not use `//nolint` directives, `_ =` to ignore errors, or any other mechanism to circumvent lint errors without explicit user permission. Always fix the underlying issue properly.

**CRITICAL: Update existing functions, never create new versions unless explicitly instructed to do so.** When changing a function's signature or behavior, modify the existing function directly. Never create variants like `FooWithWarnings`, `FooV2`, or `FooNew`. Update all call sites and tests to use the new signature. Use result structs to add return values without breaking callers.

Always keep the repo in a clean, validated state before handing work back. Run **repo-wide** commands when changes span multiple modules:

- `make fmt`
- `make tidy`
- `make lint`
- `make test`
- `make check-sec` (security sweep when touching dependencies or external inputs)

When work is limited to a single module, you may additionally run the targeted variants (`make fmt PKG=<module>`, `make lint PKG=<module>`, `make test PKG=<module>`), but the repo-wide suite above must still be green before completion. Lint settings live in `.github/.golangci.yml`; rely on that config instead of ad-hoc rules. Every command listed here must finish with **zero errors** before you consider any task complete.

## Critical Patterns

### 1. Config Loading with Error Capture
Use viper with defaults; capture load errors immediately rather than retrying in validation:

```go
// Good: Capture error on first attempt
chConfig, err := ch.ClickhouseLoadConfig()
if err != nil {
    cfg.clickhouseLoadErr = err  // Store for later surfacing
} else {
    cfg.ClickHouseConfig = chConfig
}

// Bad: Silent swallowing with retry later
if chConfig, err := ch.ClickhouseLoadConfig(); err == nil {
    cfg.ClickHouseConfig = chConfig
}  // Error lost!
```

### 2. Interface & Test Conventions
Follow the shared guidance in `.github/instructions/go.instructions.md` for interface design, mocking strategy, and the `z_*.go` test file ordering pattern. Keep any repo-specific deviations documented there.

### 3. Environment Variable Defaults
Optional vars should have `viper.SetDefault()` before unmarshaling:

```go
v := viper.New()  // Always use isolated instance in library code
v.SetEnvPrefix("MYPREFIX")
v.SetDefault("port", "9440")
v.SetDefault("skipverify", "false")
v.AutomaticEnv()
v.Unmarshal(&config)  // Defaults apply if env not set
```

### 4. Isolated Viper Instances (CRITICAL)
**Never use global viper functions in library packages.** Global state causes cross-package interference when multiple packages use different env prefixes.

```go
// Good: Isolated instance - does not affect other packages
func LoadConfig() (*Config, error) {
    v := viper.New()
    v.SetEnvPrefix("CLICKHOUSE")
    v.BindEnv("hostname", "CLICKHOUSE_HOSTNAME")
    // ...
}

// Bad: Global state - breaks other packages using viper
func LoadConfig() (*Config, error) {
    viper.SetEnvPrefix("CLICKHOUSE")  // Affects ALL viper.GetString() calls!
    viper.BindEnv("hostname", "CLICKHOUSE_HOSTNAME")
    // ...
}
```

## Slippy-Specific Guidance

Highlights from the slippy instructions (see Additional Instructions for the link) that most often impact changes:
- **Shadow mode** (`SLIPPY_SHADOW_MODE`) controls blocking vs non-blocking error handling
- **Validate-first migrations** - check schema version before running migrations
- **Nil client safety** - all operations must handle nil client gracefully
- **Correlation ID** is the canonical slip identifier throughout lifecycle

## CI/Release Process

- GitVersion calculates semantic versions from commit history
- Release creates tags for root module (`v1.3.43`) AND all submodules (`slippy/v1.3.43`)
- pkg.go.dev refresh is automatic post-release
- **75% test coverage threshold** enforced per module

## Project State Documentation

**You MUST maintain `.github/PROJECT_STATE.md` as working memory for this project.**

### At Session Start
- **Read `.github/PROJECT_STATE.md`** to understand current state, recent changes, and pending work
- Review any in-progress items or architectural decisions

### During Work
Update PROJECT_STATE.md whenever:
- Implementing new features or systems
- Making architectural decisions or changes
- Discovering technical debt or issues
- Completing significant milestones
- User provides direction that affects project structure

### Required Sections
```markdown
# Project State — goLibMyCarrier

> **Last Updated:** [Date]
> **Status:** [Brief current state summary]

## Overview
[What this library does, key characteristics]

## Implemented Systems
[Per-module breakdown of completed functionality]

## Recent Changes
[What changed in recent sessions, new components/systems/patterns]

## Current Focus
[What's being worked on now, immediate next steps]

## Architectural Decisions
[Key design choices made, rationale, tradeoffs]

## Technical Debt / Known Issues
[Outstanding problems, things to fix later]

## Next Steps (Not Yet Implemented)
[Planned features, improvements, user requests pending]
```

### Update Discipline
- Keep entries **concise but specific** (reference file paths, function names)
- **Date entries** in Recent Changes section
- Remove outdated items from "Next Steps" as they're completed
- If PROJECT_STATE.md doesn't exist, **create it** with current project state

