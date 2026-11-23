# Contributing to goLibMyCarrier

goLibMyCarrier is a set of Go modules that MyCarrier services import. Each top-level
directory with a `go.mod` is its own module (`slippy`, `vault`, `logger`, ...). Every merge to
`main` that CI runs on releases all of them at one shared version (see
[RELEASING.md](RELEASING.md)), so treat a merge as a release.

## Owners and review rules

[`.github/CODEOWNERS`](.github/CODEOWNERS) names the owners: @hpyvovar-mycarrier and the
`MyCarrier-DevOps/devsecops` team. The ruleset on `main` requires:

* a pull request, merged with **squash**;
* one approval from the `devsecops` team, and a code owner review;
* every review thread resolved;
* **signed commits**. Commits made in the GitHub web editor or through the GitHub API are
  signed for you; a local commit needs a signing key set up in Git;
* the checks "Unit tests status check", "Lint status check" and
  "Vulnerability scan status check", on a branch that is up to date with `main`.

`integration-test-slippy` is not a required check, but the release job waits for it. Do not
merge while it is red: the merge would release nothing.

CI ignores pull requests that change only `docs/**`, `.claude/**`, Markdown files at the top
level (`README.md`, `CLAUDE.md`, `AGENTS.md`, this file, ...) or files under `.github/` other
than `ci.yml` and `Gitversion.yml`. Such a pull request gets no checks, so the required checks
never report. Only a member of the `architects` or `devsecops` team can merge it, with the
ruleset bypass. That includes a pull request that edits only the agent instructions in
`CLAUDE.md` or `AGENTS.md`.

## Make a change

1. Create a branch from `main`. A branch named `feature/…`, `feat/…`, `fix/…` or `hotfix/…`
   also gets CI on every push and a prerelease tag ([RELEASING.md](RELEASING.md)). Any other
   name gets CI only through its pull request.
2. Write tests next to the code. Run them with the race detector:

   ```bash
   make test PKG=<module>
   ```

   `slippy` has integration tests that need Docker:

   ```bash
   cd slippy && go test -tags integration -count=1 -timeout 10m ./...
   ```

3. Run the other checks CI runs:

   ```bash
   make fmt
   make lint PKG=<module>
   make tidy
   make check-sec
   make check-coverage PKG=<module>
   ```

   CI fails "Unit tests status check" when a module's total coverage is below 75%.
   `make check-coverage` runs the same gate, with the same threshold and the same exclusion
   of test fixture packages.

4. Open a pull request to `main`.

The `go` directive of every module follows the latest Go minor, and a new minor raises the
minimum Go version of every consumer. [CLAUDE.md](CLAUDE.md) ("Go version policy") explains
what to move together.

## Changes between modules

Some modules use other modules of this repository through `replace => ../<module>` in their
`go.mod` (`slippy`, `github`, `clickhousemigrator`, `postgresmigrator`). Inside the repository
they build against the sibling code on the branch. A consumer gets the sibling version
written in the `require` line instead. When you change a module that another module needs,
check that the `require` line points at a release with your change. DEVOPS-616 removes these
`replace` directives.

## Breaking changes

A change that breaks a consumer is a **minor** release, not a patch:

* put `+semver: minor` in the squash commit message, or follow the one-release floor that
  [RELEASING.md](RELEASING.md) describes;
* add a section to "Breaking changes" in [README.md](README.md) with a migration table, as
  the existing sections do.

## Dependabot

Dependabot opens update pull requests every Monday at 04:00 UTC:

* one pull request for all minor and patch updates of the Go modules, across every module.
  Updates to the modules of this repository itself are ignored;
* a separate pull request for each major update;
* one pull request for GitHub Actions minor and patch updates.

Major updates of `gittools/actions` are held back until the release job moves to
GitVersion 6.2 or newer (DEVOPS-631). That rule also holds back security updates of
`gittools/actions`, so apply an advisory for it by hand.

At 07:00 UTC, the workflow `Dependabot digest` in libMyCarrier sends the owner a Teams card
that lists the open Dependabot pull requests. Dependabot pull requests are never merged
automatically: an owner reviews, approves and merges them. A Dependabot merge releases all
modules like any other merge.
