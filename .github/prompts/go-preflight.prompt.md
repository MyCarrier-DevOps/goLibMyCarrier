---
description: Security preflight before implementing — run govulncheck and gate on Go-toolchain / dependency CVEs
---

# /go-preflight

Run this **before** starting implementation on a feature or bugfix (after planning).

## Steps

1. Run the vulnerability scan:

   ```bash
   make check-sec
   ```

2. Read the `govulncheck` output and classify any findings:

   - **Standard-library / toolchain findings** — the affected module is the Go
     standard library (paths like `crypto/tls`, `net/http`) and the report says
     `Fixed in: goX.Y.Z`. The remedy is to **upgrade the Go toolchain**. Show the
     target version and the upgrade commands, then ask the user to upgrade
     before continuing:

     ```bash
     # Homebrew
     brew upgrade go            # or: brew install go

     # mise
     mise use -g go@latest      # installs and pins the latest Go globally
     # (or: mise install go@latest)

     # Official installer (alternative)
     # https://go.dev/dl/
     ```

     After upgrading, re-run `make check-sec` and confirm the finding is gone.

   - **Dependency findings** — the affected module is a third-party dependency.
     Fix it with a bump (the default remedy):

     ```bash
     make bump          # go get -u ./... && go mod tidy across all modules
     ```

     If a full bump is undesirable, target just the fixed version from inside
     the module directory (the folder named by `APPLICATION` in the Makefile):

     ```bash
     go get <module>@<fixed-version> && go mod tidy
     ```

     Re-run `make check-sec` afterward.

3. If the scan is clean, say so and proceed to implementation.

## Notes

- `govulncheck` reports only vulnerabilities reachable from this code's call
  graph, so a clean result is meaningful — not just "no vulnerable versions."
- Do not silence findings or lower any threshold to get past this gate.
