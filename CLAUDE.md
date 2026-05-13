# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

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

**Raw `go build ./...` / `go vet ./...` are acceptable for quick verification** but final gate before commit must run `make lint && make test`.

**For subagents:** when briefing, include the Makefile targets explicitly. Don't let an agent reach for `golangci-lint run` or `go test ./...` directly — the Makefile flags differ from defaults and CI compares against Makefile output.

This applies to **all** Go-based MyCarrier tools that have a Makefile (slippy-api, MC.TestEngine, Slippy CLI, etc.).

## Architecture Overview

_Add a brief overview of your project architecture_

## Conventions & Patterns

_Add your project-specific conventions here_
