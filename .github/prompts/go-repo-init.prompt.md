---
description: Bootstrap a Go repo for the go-devkit workflow — permissions, CLAUDE.md, generated Makefile/lint/CI, CI hardening, and Go version pinning (idempotent, auto-fix)
---

# /go-repo-init

Apply the per-repo configuration that the go-devkit plugin cannot ship (a plugin
cannot deliver a project's `.claude/settings.json` permissions). Run this once
inside a Go service repository. Every step is **verify-then-act and idempotent**
— re-running never duplicates entries or clobbers customizations. The
deterministic file transforms live in `${CLAUDE_PLUGIN_ROOT}/scripts/repo-init.sh`;
this command supplies the live inputs and handles the prose-level merges.

Work from the repository root. Use `${CLAUDE_PROJECT_DIR:-.}` as the project dir
and `${CLAUDE_PLUGIN_ROOT}` for bundled scripts/templates. Announce each change;
at the end, print a summary and the diffs — do **not** commit automatically.

## Steps

Create a todo per step and work them in order.

### 1. Detect the layout

Determine the module directory and which scaffold files already exist:

```bash
ls Makefile go.mod app/go.mod .github/workflows/ci.yml .github/.golangci.yml 2>/dev/null
```

- If a `Makefile` with an `APPLICATION` variable exists, the module dir(s) come
  from it: `"${CLAUDE_PLUGIN_ROOT}"/scripts/coverage-gate.sh --list-modules`.
- Otherwise look for a `go.mod` (commonly in `app/` for template-derived repos,
  or at the repo root). That directory is the module dir.
- If there is **no** `go.mod` anywhere, this is not a Go module yet — warn and
  stop. `/go-repo-init` configures an existing Go service; it does not scaffold
  application code.

Call the resolved module directory `MOD` below (e.g. `app`).

### 2. Generate missing build tooling (generate-if-absent)

Use the bundled templates for any file that does not already exist. `ensure-file`
never overwrites an existing file, so this is safe to run every time:

```bash
"${CLAUDE_PLUGIN_ROOT}"/scripts/repo-init.sh ensure-file \
  --src "${CLAUDE_PLUGIN_ROOT}/templates/Makefile"   --dest Makefile
"${CLAUDE_PLUGIN_ROOT}"/scripts/repo-init.sh ensure-file \
  --src "${CLAUDE_PLUGIN_ROOT}/templates/golangci.yml" --dest .github/.golangci.yml
"${CLAUDE_PLUGIN_ROOT}"/scripts/repo-init.sh ensure-file \
  --src "${CLAUDE_PLUGIN_ROOT}/templates/ci.yml"       --dest .github/workflows/ci.yml
```

If you just generated the `Makefile` and the module dir is **not** `app`, update
its `APPLICATION := app` line to name the real module dir(s) — the Makefile,
coverage gate, and CI all key off it. Report that you did so.

### 3. Permissions

Union-merge the plugin's allow/deny lists into the repo's `.claude/settings.json`
(created if absent). Existing entries and any pre-existing hooks are preserved;
nothing is duplicated. **Do not add a SessionStart hook here** — the plugin ships
that globally.

```bash
"${CLAUDE_PLUGIN_ROOT}"/scripts/repo-init.sh merge-permissions \
  --project-dir "${CLAUDE_PROJECT_DIR:-.}" \
  --template "${CLAUDE_PLUGIN_ROOT}/templates/settings.json"
```

### 4. Reconcile CLAUDE.md

Make the repo's `CLAUDE.md` reflect that the Go workflow is now owned by the
go-devkit plugin. This is a **prose reconciliation, not a mechanical append** —
do it with the editor, reading the file first. Two bundled inputs drive it:

- the ownership preamble — `${CLAUDE_PLUGIN_ROOT}/templates/claude-preamble.md`
- the authoritative workflow block — `${CLAUDE_PLUGIN_ROOT}/templates/claude-workflow-section.md`,
  delimited by `<!-- BEGIN go-devkit -->` / `<!-- END go-devkit -->`

Structure the result as: title/intro → **preamble** → **workflow block** →
the repo's existing project-specific sections (reconciled). Every part is
idempotent — re-running must not duplicate or fight prior runs.

1. **No `CLAUDE.md`** → create it: a short title/intro, then the preamble, then
   the workflow block. Done.

2. **`CLAUDE.md` exists** → reconcile in place:
   - **Preamble.** If no paragraph already attributes the workflow to the
     go-devkit plugin (look for "owned by the **go-devkit** plugin"), insert the
     preamble right after the file's intro. Otherwise refresh that paragraph to
     match the bundled wording.
   - **Workflow block.** If the `<!-- BEGIN go-devkit -->` marker is present,
     replace the marked region with the current block (so plugin updates
     propagate). If not, insert the block immediately after the preamble.
   - **Reconcile stale references in the rest of the doc.** Preserve all
     project-specific content, but rewrite anything that describes machinery the
     plugin now owns so it points at the plugin instead of repo-local paths:
     - `.claude/scripts/coverage-gate.sh` → "the go-devkit coverage gate (run by
       `/go-verify`)".
     - `.claude/reference/go-conventions.md` → "the Go conventions bundled with
       the go-devkit plugin".
     - `.claude/hooks/session-start.sh` and other bundled machinery → note it
       ships inside the plugin under `apm_modules/`, not in the repo tree; the
       repo keeps only the per-repo bits `/go-repo-init` deploys (`settings.json`
       and the committed slash commands).
     - "this template ships / already ships …" framing → "the go-devkit plugin
       ships …".
     - Where `/go-preflight`, `/go-verify`, or `/go-repo-init` are mentioned,
       make clear they are delivered by the plugin.
   - **House-rules note.** If the doc has a "What not to do" / house-rules
     section, ensure it says not to hand-edit the go-devkit block (change it in
     the plugin and re-run `/go-repo-init`). If there is no such section, the
     preamble already carries that instruction — do not invent one.

Leave everything the plugin cannot know — module layout, coverage policy,
project house rules — intact. Report a concise list of what you added vs. what
you rewrote.

### 5. CI hardening (auto-fix)

Resolve the latest major of each action live, and the installed Go minor, then
run the hardener. It adds a top-level `permissions: {contents: read}` if missing,
bumps the action majors, pins `go-version`, and adds `- '!.claude/**'` to the
`paths:` filters — all idempotent.

```bash
CHECKOUT_MAJOR="$(gh api repos/actions/checkout/releases/latest --jq .tag_name | sed -E 's/^v?([0-9]+).*/\1/')"
SETUPGO_MAJOR="$(gh api repos/actions/setup-go/releases/latest  --jq .tag_name | sed -E 's/^v?([0-9]+).*/\1/')"
GO_MINOR="$(go version | sed -E 's/.*go([0-9]+\.[0-9]+).*/\1/')"

"${CLAUDE_PLUGIN_ROOT}"/scripts/repo-init.sh harden-ci \
  --ci .github/workflows/ci.yml \
  --checkout "$CHECKOUT_MAJOR" --setup-go "$SETUPGO_MAJOR" --go-version "$GO_MINOR"
```

If `gh` is unavailable or unauthenticated, fall back to the majors already in the
workflow (leave them as-is) and say so; never guess a major that doesn't exist.
CI floats on the patch level (`go-version: "1.26"`) on purpose — pin only the
minor here; bump the minor/major by hand for a new Go release.

### 6. Go version pinning (auto-fix)

Pin the reproducible-build inputs to the installed full patch:

```bash
GO_PATCH="$(go version | sed -E 's/.*go([0-9]+\.[0-9]+\.[0-9]+).*/\1/')"

"${CLAUDE_PLUGIN_ROOT}"/scripts/repo-init.sh pin-version \
  --project-dir "${CLAUDE_PROJECT_DIR:-.}" --module-dir "MOD" --go-patch "$GO_PATCH"
```

This sets the `go` directive in `MOD/go.mod` to the full patch and the `golang:`
builder tag in `MOD/Dockerfile` (if present) to match. Report both diffs.

### 7. .gitignore

```bash
"${CLAUDE_PLUGIN_ROOT}"/scripts/repo-init.sh ensure-gitignore \
  --project-dir "${CLAUDE_PROJECT_DIR:-.}"
```

### 8. Summary

Print exactly what changed (files created, files hardened, versions pinned,
permissions added). Remind the user to review the diffs, run
`make fmt lint test check-sec`, and commit. Do not commit for them.

## Assumptions

- A Go module exists (a `go.mod` under the module dir). This command configures
  an existing service; it does not scaffold application source.
- The consumer environment has `go` (for version detection) and, for step 5,
  `gh` authenticated against GitHub. Missing `gh` degrades gracefully (step 5 is
  skipped with a note); everything else still runs.
- `jq` is available (used by the permissions merge).
