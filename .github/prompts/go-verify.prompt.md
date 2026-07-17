---
description: Post-task verification — fmt, lint, test, and the CI coverage gate
---

# /go-verify

Run this **after** completing a task, before handing it off. Run it from the
repository root. Stop at the first failure and surface the real output — never
report success without the passing evidence.

## Steps

1. Format (auto-fixes gofmt / goimports / golines):

   ```bash
   make fmt
   ```

2. Lint — must be zero errors:

   ```bash
   make lint
   ```

3. Test with coverage (writes a `coverage.out` in each module directory):

   ```bash
   make test
   ```

4. Coverage gate — fail if total coverage is below the threshold declared in CI.
   The threshold is read live from `.github/workflows/ci.yml` and the module
   dir is auto-detected from the Makefile's `APPLICATION`, so this gate and CI
   can never drift (and it survives renaming the module directory). The gate
   ships with the plugin; run it from the repo root so it resolves the project's
   `Makefile` and `ci.yml`:

   ```bash
   "${CLAUDE_PLUGIN_ROOT}"/scripts/coverage-gate.sh
   ```

If the coverage gate fails, add table-driven tests for the uncovered code. Keep
`main()` a one-liner and test the `run()` helper instead. Do **not** lower the
threshold to pass.

## Optional

Re-run the security scan if dependencies or the toolchain changed since preflight:

```bash
make check-sec
```
