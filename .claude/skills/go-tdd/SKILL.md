---
name: go-tdd
description: Use when implementing a feature or fixing a bug in a Go service repo — enforces RED-test-first TDD, a security preflight before coding, and post-task verification against the CI coverage gate. Trigger on "implement", "add a feature", "fix this bug", or any change to Go code under the module directory.
---

# Go TDD workflow

The delivery loop for every feature and bugfix in a go-devkit repository. It is a
thin, Go-specific layer over `superpowers:test-driven-development`; if that skill
is available, follow its discipline for the RED/GREEN/REFACTOR mechanics.

**Scope: code development only.** This loop applies when you change program
behavior — features and bugfixes in the Go module. It does **not** apply to meta
changes: renaming the app / `APPLICATION`, editing config (`.claude/`, Makefile,
CI, `.golangci.yml`), docs/README, or dependency bumps that don't change
behavior. Make those directly and verify with `/go-verify`.

Before writing any Go code, read the conventions bundled with this plugin at
`${CLAUDE_PLUGIN_ROOT}/reference/go-conventions.md` — naming, error handling,
package layout, concurrency, HTTP clients, testing, and security. They are
non-negotiable and the linter enforces most of them.

## The loop

Create a todo per step and work them in order.

1. **Preflight.** Run `/go-preflight` (i.e. `make check-sec`). If it reports a
   Go standard-library CVE, surface the brew/mise upgrade commands and get the
   user to upgrade before coding. Start from a clean working tree and a green
   `make test`.

2. **RED — write the failing test first.** Add a table-driven test next to the
   code under test (a `_test.go` file in the same package). Cover the success
   path and the error/edge cases. Run it and **confirm it fails for the intended
   reason** (assertion failure, not a compile error in unrelated code):

   ```bash
   cd <module-dir> && go test ./... -run <YourTest> -count=1
   ```

   (`<module-dir>` is the folder named by `APPLICATION` in the Makefile.)

3. **GREEN — minimal implementation.** Write the least code that makes the test
   pass. Keep `main()` a one-liner delegating to a testable `run(...)`; inject
   dependencies (logger, clients, config) as arguments — no globals. Wrap errors
   with `fmt.Errorf(... %w ...)`; return early; keep the happy path left-aligned.

4. **REFACTOR.** Clean up names, extract small focused functions, remove
   duplication — with the tests staying green.

5. **VERIFY.** Run `/go-verify` (`make fmt` → `make lint` → `make test` →
   the coverage gate). If coverage dropped below the CI threshold, add more
   table-driven tests — never lower the threshold.

## Guardrails

- No stubs, TODOs, or placeholder implementations in production code — finish or ask.
- Do not introduce a second logger, CLI framework, or config loader; use the
  `otel.NewAppLogger()` dependency-injection pattern (a small local interface for
  the logger, injected into `run(...)`).
- Only use `//nolint:<linter> // <reason>` with a documented, genuine reason.
- Do not bypass pre-commit hooks with `--no-verify`; fix the root cause.
