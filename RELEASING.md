# Releasing goLibMyCarrier

Releases are automatic. A push to `main` that CI runs on tags every module of this
repository with one new version. Nobody tags by hand.

## What starts a release

The workflow [`Build And Release Pkgs`](.github/workflows/ci.yml) runs on a push to `main`
unless the push changes only `docs/**`, `.claude/**`, Markdown files at the top level
(`README.md`, this file, ...) or files under `.github/` other than `ci.yml` and
`Gitversion.yml`. When it runs and these jobs pass, the job
"Create Release" releases:

* unit tests of every module;
* lint and the vulnerability scan of every module;
* `integration-test-slippy`.

A push that CI skips releases nothing. Re-run the workflow from the Actions tab
(`workflow_dispatch` on `main`) to release the current `main`. The tags are created only if
they do not exist yet, so a re-run never tags twice.

The release job tags the tip of the branch when the job starts, not the commit that started
the run. An existing tag is skipped, never moved. So when two merges land close together, the
first run can tag the second commit, and the second run then finds its tag and skips, still
green.

## The version

GitVersion 6.0 computes the version from the tags, with
[`.github/Gitversion.yml`](.github/Gitversion.yml):

* **Patch** is the default: each release on `main` raises the patch number (`v1.5.20` →
  `v1.5.21`).
* **Minor**: put `+semver: minor` in the squash commit message. Use it for a breaking change
  (see [CONTRIBUTING.md](CONTRIBUTING.md)).
* **One-release floor**: set `next-version` in `Gitversion.yml`. The `ConfiguredNextVersion`
  strategy makes it effective. Delete `next-version` once that version is tagged on `main`:
  after that it does nothing, and it does not protect the next breaking change.

## What a release creates

* the tag `vX.Y.Z` for the repository and the tag `<module>/vX.Y.Z` for every module, at the
  same commit. Every module gets the new tag, even one that did not change;
* a GitHub Release `vX.Y.Z` that lists the modules;
* a request to the Go module proxy and to pkg.go.dev for each module, so the new version
  shows up there quickly.

## Prereleases

A push to a branch named `feature/…`, `feat/…`, `fix/…` or `hotfix/…` releases a prerelease of
every module: `vX.Y.Z-<branch>.<n>`, where `<branch>` is the branch name with `/` replaced by
`-`, and `<n>` is the number of commits since `main`. A `feature/…` branch raises the minor
number, so after `v1.5.20` its prereleases are `v1.6.0-…`. Use a prerelease to try a change
in a consumer before you merge:

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/slippy@v1.6.0-feature-devops-580-teams-threads.5
```

The prerelease tags stay after the merge; the next release on `main` does not count them.

## After a release

Consumers do not get the new version by themselves. Each consumer repository raises the
version in its own `go.mod`:

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/<module>@vX.Y.Z
go mod tidy
```

## Known limits

* Every release tags all modules, even the ones that did not change.
* Modules that use `replace => ../<module>` give consumers the sibling version in their
  `require` line, not the code CI tested (DEVOPS-616).
* The release job pins GitVersion 6.0 and `gittools/actions` v3 (DEVOPS-631).
* The release job checks out `github.event.ref`, not `github.sha`, and the workflow has no
  `concurrency` group (see "What starts a release"). On a feature branch, `<n>` counts
  commits, so after a force-push with the same number of commits the prerelease tag stays on
  the old code.
