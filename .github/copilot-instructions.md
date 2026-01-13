# goLibMyCarrier AI Development Instructions

## Additional Instructions

**You MUST also follow all applicable instruction files in `.github/instructions/`:**
- [go.instructions.md](.github/instructions/go.instructions.md) - Idiomatic Go patterns, naming conventions, error handling

For the slippy package specifically, also reference:
- [slippy/CLAUDE.md](slippy/CLAUDE.md) - Shadow mode, migrations, client patterns

## Architecture Overview

This is a **multi-module Go monorepo** where each subdirectory is an independent Go module with its own `go.mod`. All modules share unified versioning via CI (e.g., `clickhouse/v1.3.43`, `slippy/v1.3.43`).

### Key Packages
- **clickhouse** - ClickHouse client with retry logic and session management
- **slippy** - Routing slip orchestration for CI/CD pipelines (most complex - see `slippy/CLAUDE.md`)
- **logger** - Zap-based structured logging with `LOG_LEVEL` and `LOG_APP_NAME` env vars
- **kafka**, **vault**, **github**, **auth**, **otel** - Infrastructure integrations

## Development Commands

```bash
# Single module
make test PKG=slippy          # Test specific module
make lint PKG=clickhouse      # Lint specific module

# All modules
make test                     # Test all modules
make lint                     # Lint all (requires golangci-lint v2.5.0)
make tidy                     # go mod tidy for all
make check-sec                # govulncheck on all modules
```

**Linting uses** `.github/.golangci.yml` - max line length is 120 chars (golines enforces this).

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

### 2. Interface-Based Testing
Define interfaces for external dependencies to enable mocking:

```go
// clickhouse/clickhouse.go re-exports driver types
type Conn = driver.Conn
type Rows = driver.Rows

// slippy/interfaces.go defines mockable boundaries
type SlipStore interface { ... }
type GitHubAPI interface { ... }
```

### 3. Test File Naming
Test files are prefixed with `z_` to run after main tests: `z_config_test.go`, `z_main_test.go`.

### 4. Environment Variable Defaults
Optional vars should have `viper.SetDefault()` before unmarshaling:

```go
viper.SetDefault("chport", "9440")
viper.SetDefault("chskipverify", "false")
viper.UnmarshalKey(...)  // Defaults apply if env not set
```

## Slippy-Specific Guidance

The `slippy` package is the most complex. See **`slippy/CLAUDE.md`** for detailed patterns including:
- **Shadow mode** (`SLIPPY_SHADOW_MODE`) controls blocking vs non-blocking error handling
- **Validate-first migrations** - check schema version before running migrations
- **Nil client safety** - all operations must handle nil client gracefully
- **Correlation ID** is the canonical slip identifier throughout lifecycle

## CI/Release Process

- GitVersion calculates semantic versions from commit history
- Release creates tags for root module (`v1.3.43`) AND all submodules (`slippy/v1.3.43`)
- pkg.go.dev refresh is automatic post-release
- **75% test coverage threshold** enforced per module

## Required Validation Before Completing Work

**You MUST run these commands and ensure zero errors before considering any task complete:**

```bash
make fmt                      # Format all modules
make tidy                     # Clean up dependencies
make lint                     # Lint all modules - MUST pass with 0 issues
make test                     # Test all modules - ALL tests MUST pass
```

If working on a specific module, at minimum run:
```bash
make fmt PKG=<module>
make lint PKG=<module>
make test PKG=<module>
```

**Non-negotiable requirements:**
- All lint checks must pass with **0 issues**
- All tests must pass with **0 failures**
- No skipped validations - if a command fails, fix the issue before proceeding

## Common Mistakes to Avoid

1. **Don't silence config errors** - surface actual load failures, not generic "required" messages
2. **Don't create "WithGracefulFallback" wrappers** - use shadow mode pattern instead
3. **Run linter with project config**: `golangci-lint run --config ../.github/.golangci.yml`
4. **Use absolute imports**: `github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse`

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

