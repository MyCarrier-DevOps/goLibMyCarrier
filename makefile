SHELL:=/bin/bash

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
LIB_DIRS := argocdclient auth cievents clickhouse clickhousemigrator github kafka logger otel pollyapi postgres postgresmigrator repocanon slippy slippyapi teamsbot vault yaml

# Tool versions, pinned so local and CI judge identically. CI installs these via
# `make install-tools` / `make check-sec`, so this file is the single source of truth.
#
# GOLANGCI_LINT_VERSION must be built with a Go >= the toolchain CI resolves
# (ci.yml uses `go-version: ^1.26` + `check-latest`, so a new Go minor is picked up
# automatically). A linter built with an older Go panics on the newer stdlib:
# "file requires newer Go version go1.27 (application built with go1.26)". When Go
# ships a new minor, bump this to a release whose notes list that version's support
# (`golangci-lint version` prints the Go it was built with).
GOLANGCI_LINT_VERSION := v2.13.1
GOVULNCHECK_VERSION   := v1.7.0
GO_TEST_COVERAGE_VERSION := v2.19.0

# Coverage floor. MUST match `threshold-total` in .github/workflows/ci.yml: CI enforces it
# with the vladopajic/go-test-coverage action, and `make check-coverage` enforces the same
# number locally so the two cannot disagree. `make doctor` verifies they still match.
COVERAGE_THRESHOLD_TOTAL := 75

# Mutation testing (mutest). Pinned so local and CI judge identically.
MUTEST_VERSION     := v0.6.0
MUTATION_BASE      ?= origin/main
MUTATION_THRESHOLD ?= 100

.PHONY: lint
lint: install-tools
	@if [ -z "$(PKG)" ]; then \
		echo "Linting all modules..."; \
		for dir in $(LIB_DIRS); do \
			if [ -d "$$dir" ]; then \
				echo "Linting $$dir module..."; \
				(cd $$dir && go mod tidy && golangci-lint run --config ../.github/.golangci.yml --timeout 5m ./...); \
			else \
				echo "Directory $$dir not found, skipping..."; \
			fi; \
		done; \
	else \
		echo "Linting $(PKG) module..."; \
		(cd $(PKG) && go mod tidy && golangci-lint run --config ../.github/.golangci.yml --timeout 5m ./...); \
	fi

.PHONY: test
test:
	@if [ -z "$(PKG)" ]; then \
		echo "Testing all modules..."; \
		for dir in $(LIB_DIRS); do \
			if [ -d "$$dir" ]; then \
				echo "Testing $$dir module..."; \
				(cd $$dir && go mod download && go test -race -covermode=atomic -count=1 -timeout 120s -cover -coverprofile=../coverage-$$dir.out ./... && go tool cover -func=../coverage-$$dir.out); \
			else \
				echo "Directory $$dir not found, skipping..."; \
			fi; \
		done; \
	else \
		echo "Testing $(PKG) module..."; \
		(cd $(PKG) && go mod download && go test -race -covermode=atomic -count=1 -timeout 120s -cover -coverprofile=../coverage-$(PKG).out ./... && go tool cover -func=../coverage-$(PKG).out); \
	fi

.PHONY: fmt
fmt:
	@echo "Formatting all modules..."
	@for dir in $(LIB_DIRS); do \
		if [ -d "$$dir" ]; then \
			echo "Formatting $$dir module..."; \
			(cd $$dir && golangci-lint fmt --config ../.github/.golangci.yml ./...); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: bump
bump:
	@echo "Bumping module versions..."
	@for dir in $(LIB_DIRS); do \
		if [ -d "$$dir" ]; then \
			echo "Bumping $$dir module..."; \
			(cd $$dir && go get -u && go mod tidy ); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: tidy
tidy:
	@echo "Tidying up module dependencies..."
	@for dir in $(LIB_DIRS); do \
		if [ -d "$$dir" ]; then \
			echo "Tidying $$dir module..."; \
			(cd $$dir && go mod tidy); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: install-govulncheck
install-govulncheck:
	@installed=$$(command -v govulncheck >/dev/null 2>&1 && govulncheck -version 2>&1); \
	case "$$installed" in \
		*$(GOVULNCHECK_VERSION)*) ;; \
		*) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ;; \
	esac

.PHONY: check-sec
check-sec: install-govulncheck
	@if [ -z "$(PKG)" ]; then \
		echo "Checking for known vulnerabilities in all modules..."; \
		for dir in $(LIB_DIRS); do \
			if [ -d "$$dir" ]; then \
				echo "Checking $$dir module..."; \
				(cd $$dir && go mod download && govulncheck -show verbose -test=false ./...) || exit 1; \
			else \
				echo "Directory $$dir not found, skipping..."; \
			fi; \
		done; \
	else \
		echo "Checking $(PKG) module for known vulnerabilities..."; \
		(cd $(PKG) && go mod download && govulncheck -test=false ./...) || exit 1; \
	fi

.PHONY: install-go-test-coverage
install-go-test-coverage:
	@if ! command -v go-test-coverage >/dev/null 2>&1 || \
		! go-test-coverage --version 2>&1 | grep -q "$(GO_TEST_COVERAGE_VERSION)"; then \
		echo "Installing go-test-coverage $(GO_TEST_COVERAGE_VERSION)..."; \
		go install github.com/vladopajic/go-test-coverage/v2@$(GO_TEST_COVERAGE_VERSION); \
	fi

# Coverage gate, deliberately a mirror of the CI job rather than a second opinion:
# same per-module scope, the same fixture-package exclusion (`go list ./... | grep -v test`),
# and the same COVERAGE_THRESHOLD_TOTAL as ci.yml's `threshold-total`. It is therefore
# meaningful to run before pushing — a pass here predicts a pass in CI.
#
# It does NOT reuse `make test`'s profile: `make test` covers every package including the
# fixture ones (loggertest, slippytest, migratortest), which CI excludes, so its numbers are
# a different measurement and would judge against the wrong denominator.
#
# Like CI, it fails if the tests fail — coverage is not evaluated on a red suite. The
# postgres and postgresmigrator modules need a container runtime for their testcontainers
# integration tests; on Rancher Desktop that means
#   DOCKER_HOST=unix://$$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true make check-coverage
.PHONY: check-coverage
check-coverage: install-go-test-coverage
	@if [ -z "$(PKG)" ]; then \
		echo "Checking coverage for all modules (threshold-total $(COVERAGE_THRESHOLD_TOTAL)%)..."; \
		for dir in $(LIB_DIRS); do \
			if [ -d "$$dir" ]; then \
				$(MAKE) --no-print-directory check-coverage-one PKG=$$dir || exit 1; \
			else \
				echo "Directory $$dir not found, skipping..."; \
			fi; \
		done; \
	else \
		$(MAKE) --no-print-directory check-coverage-one PKG=$(PKG); \
	fi

# Single-module coverage check. Not meant to be called directly — use `check-coverage`
# (optionally with PKG=<module>), which handles the all-modules loop.
.PHONY: check-coverage-one
check-coverage-one:
	@echo "Checking $(PKG) coverage (threshold-total $(COVERAGE_THRESHOLD_TOTAL)%)..."
	@cd $(PKG) && go mod download && \
		PKGS=$$(go list ./... | grep -v 'test' || echo "./...") && \
		{ out=$$(go test -cover -coverprofile=../coverage-$(PKG).out -covermode=atomic $$PKGS 2>&1) \
			|| { echo "$$out"; echo "check-coverage: $(PKG) tests failed — coverage not evaluated"; exit 1; }; } && \
		go-test-coverage \
			--profile=../coverage-$(PKG).out \
			--source-dir=. \
			--threshold-total=$(COVERAGE_THRESHOLD_TOTAL)

# Mutation testing uses mutest, which mutates and runs the tests IN PLACE inside the
# module directory. That matters here: several modules (slippy, slippyapi, ...) use
# relative `replace ... => ../<module>` directives, so any mutation tool that copies the
# module elsewhere before testing cannot resolve them — every build fails, every mutant
# is scored "killed", and the run reports a meaningless 100%. If you ever swap tools,
# validate the replacement first: add a function whose only test asserts nothing and
# confirm the tool reports its mutants as SURVIVED, not killed.
.PHONY: install-mutest
install-mutest:
	@installed=$$(command -v mutest >/dev/null 2>&1 && mutest -version 2>&1); \
	case "$$installed" in \
		*$(MUTEST_VERSION)*) ;; \
		*) go install github.com/fchimpan/mutest@$(MUTEST_VERSION) ;; \
	esac

# Mutation-test only the lines changed vs MUTATION_BASE.
#
# Run this locally before opening a PR: no workflow in .github/ invokes it, so it is a
# convention rather than an enforced gate. Wiring it into CI was considered and declined
# for now — mutation runs are slow and the 100% threshold would block on pre-existing
# survivors the moment it ran unscoped.
# Surviving mutants mean an assertion is missing; add tests rather than lowering
# MUTATION_THRESHOLD. Scope to one module with PKG=<module>.
.PHONY: mutation
mutation: install-mutest
	@if [ -z "$(PKG)" ]; then \
		echo "Mutation testing code changed vs $(MUTATION_BASE) (threshold $(MUTATION_THRESHOLD)%)..."; \
		for dir in $(LIB_DIRS); do \
			if [ -d "$$dir" ]; then \
				echo "Mutation testing $$dir module..."; \
				(cd $$dir && go mod download && mutest -diff $(MUTATION_BASE) -threshold $(MUTATION_THRESHOLD) ./...) || exit 1; \
			else \
				echo "Directory $$dir not found, skipping..."; \
			fi; \
		done; \
	else \
		echo "Mutation testing $(PKG) module changed vs $(MUTATION_BASE) (threshold $(MUTATION_THRESHOLD)%)..."; \
		(cd $(PKG) && go mod download && mutest -diff $(MUTATION_BASE) -threshold $(MUTATION_THRESHOLD) ./...); \
	fi

# Mutation-test a module in full, ignoring the diff — a periodic audit, not a pre-merge
# gate. Expect survivors in legacy code, so pass an explicit threshold when auditing,
# e.g. `make mutation-all PKG=slippy MUTATION_THRESHOLD=80`.
.PHONY: mutation-all
mutation-all: install-mutest
	@if [ -z "$(PKG)" ]; then \
		echo "Mutation testing all modules in full (threshold $(MUTATION_THRESHOLD)%)..."; \
		for dir in $(LIB_DIRS); do \
			if [ -d "$$dir" ]; then \
				echo "Mutation testing $$dir module..."; \
				(cd $$dir && go mod download && mutest -threshold $(MUTATION_THRESHOLD) ./...) || exit 1; \
			else \
				echo "Directory $$dir not found, skipping..."; \
			fi; \
		done; \
	else \
		echo "Mutation testing $(PKG) module in full (threshold $(MUTATION_THRESHOLD)%)..."; \
		(cd $(PKG) && go mod download && mutest -threshold $(MUTATION_THRESHOLD) ./...); \
	fi

# Report where the local toolchain disagrees with what CI will use, and exit non-zero if it
# does. This exists because of a real incident: when Go 1.27.0 shipped, every lint job broke
# and the fix took two CI rounds because local gates were silently judging against different
# tools than CI —
#
#   * GOROOT was exported to an older Go install, which OVERRIDES whichever `go` binary you
#     invoke, so running a newer go directly still used the old compiler and produced a
#     baffling `compile: version "goX" does not match go tool version "goY"` for every module;
#   * a version manager shimmed golangci-lint AHEAD of $(go env GOPATH)/bin on PATH, so
#     `make install-tools` installed the pinned version and it was then shadowed — the pin
#     was inert locally.
#
# Net effect: local said clean, CI panicked. Anything this target reports is a reason your
# local gate result does not predict CI's.
.PHONY: doctor
doctor:
	@status=0; \
	echo "== Go =="; \
	echo "  go on PATH:          $$(command -v go || echo '(not found)')"; \
	echo "  go version:          $$(go version 2>/dev/null || echo '(not found)')"; \
	echo "  go env GOROOT:       $$(go env GOROOT 2>/dev/null)"; \
	if [ -n "$$GOROOT" ]; then \
		echo "  GOROOT (exported):   $$GOROOT"; \
		if [ -x "$$GOROOT/bin/go" ] && \
			[ "$$($$GOROOT/bin/go version 2>/dev/null)" != "$$(go version 2>/dev/null)" ]; then \
			echo "  MISMATCH: GOROOT is exported and its toolchain ($$($$GOROOT/bin/go version)) differs"; \
			echo "            from the go on PATH. GOROOT wins, so the go you invoke is NOT the"; \
			echo "            compiler that runs. Unset GOROOT (or point your version manager at"; \
			echo "            one version) before trusting any local gate."; \
			status=1; \
		fi; \
	fi; \
	ci_go=$$(grep -m1 -oE 'go-version: *[^ ]+' .github/workflows/ci.yml 2>/dev/null | awk '{print $$2}'); \
	echo "  ci.yml go-version:   $$ci_go (with check-latest, so CI installs the newest matching)"; \
	echo "== Pinned tools (Makefile vs installed) =="; \
	for spec in "golangci-lint|$(GOLANGCI_LINT_VERSION)|version" \
	            "govulncheck|$(GOVULNCHECK_VERSION)|-version" \
	            "mutest|$(MUTEST_VERSION)|-version" \
	            "go-test-coverage|$(GO_TEST_COVERAGE_VERSION)|--version"; do \
		tool=$$(echo "$$spec" | cut -d'|' -f1); \
		want=$$(echo "$$spec" | cut -d'|' -f2); \
		flag=$$(echo "$$spec" | cut -d'|' -f3); \
		path=$$(command -v $$tool 2>/dev/null); \
		if [ -z "$$path" ]; then \
			printf "  %-18s want %-10s NOT INSTALLED (run: make install-tools / make install-mutest)\n" "$$tool" "$$want"; \
			continue; \
		fi; \
		got=$$($$tool $$flag 2>&1 | tr '\n' ' ' | sed 's/  */ /g'); \
		want_bare=$$(echo "$$want" | sed 's/^v//'); \
		if echo "$$got" | grep -qE "(^|[^0-9.])v?$$want_bare([^0-9.]|$$)"; then \
			printf "  %-18s want %-10s OK\n" "$$tool" "$$want"; \
		else \
			printf "  %-18s want %-10s MISMATCH: %s\n" "$$tool" "$$want" "$$(echo $$got | cut -c1-90)"; \
			echo "                     resolved from: $$path"; \
			status=1; \
		fi; \
	done; \
	echo "== Coverage threshold (Makefile vs ci.yml) =="; \
	ci_thr=$$(grep -m1 -oE 'threshold-total: *[0-9]+' .github/workflows/ci.yml 2>/dev/null | grep -oE '[0-9]+'); \
	if [ "$$ci_thr" = "$(COVERAGE_THRESHOLD_TOTAL)" ]; then \
		echo "  both $(COVERAGE_THRESHOLD_TOTAL)% OK"; \
	else \
		echo "  MISMATCH: Makefile $(COVERAGE_THRESHOLD_TOTAL)% vs ci.yml $$ci_thr% — local gate would"; \
		echo "            not predict CI. Reconcile COVERAGE_THRESHOLD_TOTAL with ci.yml."; \
		status=1; \
	fi; \
	echo; \
	if [ $$status -ne 0 ]; then \
		echo "doctor: local toolchain does not match CI (see MISMATCH above)."; \
	else \
		echo "doctor: local toolchain matches CI."; \
	fi; \
	exit $$status

.PHONY: help
help:
	@echo "Targets (all accept PKG=<module> where noted):"
	@echo "  make lint           - golangci-lint            (PKG=)"
	@echo "  make test           - tests w/ race + coverage (PKG=)"
	@echo "  make fmt            - gofmt/goimports, all modules"
	@echo "  make tidy           - go mod tidy, all modules"
	@echo "  make check-sec      - govulncheck vuln scan    (PKG=)"
	@echo "  make check-coverage - coverage gate, mirrors CI (PKG=)"
	@echo "  make mutation       - mutation-test lines changed vs $(MUTATION_BASE) (PKG=)"
	@echo "  make mutation-all   - mutation-test a module in full; periodic audit (PKG=)"
	@echo "  make bump           - version bump helper"
	@echo "  make doctor         - report local toolchain drift vs CI; non-zero on mismatch"
	@echo ""
	@echo "Pinned tools: golangci-lint $(GOLANGCI_LINT_VERSION), govulncheck $(GOVULNCHECK_VERSION), mutest $(MUTEST_VERSION), go-test-coverage $(GO_TEST_COVERAGE_VERSION)"
	@echo "Coverage floor: $(COVERAGE_THRESHOLD_TOTAL)% (must match ci.yml threshold-total)"
	@echo "Mutation vars: MUTATION_BASE=$(MUTATION_BASE) MUTATION_THRESHOLD=$(MUTATION_THRESHOLD)"

# install-tools installs golangci-lint at the pinned version, skipping the download when it
# is already present.
#
# The installer is fetched at the SAME tag as the binary it installs, not from HEAD. HEAD is a
# mutable ref, so piping it to sh executes whatever that branch contains at fetch time. The
# script already verifies the tarball it downloads (golangci-lint-<version>-checksums.txt +
# sha256), so pinning the tag closes the one unverified link left in the chain: the script that
# does the verifying.
#
# The already-installed short-circuit matters for the same reason, and matches what
# install-mutest and install-govulncheck already do. `lint` depends on this target, and CI runs
# `make install-tools` inside the per-module lint matrix — 19 modules — so without the check
# that is 19 fetch-and-pipe-to-sh executions per push. Fewer curl | sh runs is a smaller
# window as well as a faster build.
#
# The version comparison extracts the version FIELD and compares for equality, rather than
# pattern-matching the whole banner. `golangci-lint version` prints four fields, and a regex
# built from a dotted version carries unescaped `.` wildcards — so a loose match can be
# satisfied by the build-SHA or Go-toolchain field instead of the version. Reachability is tiny
# at a 2.x pin, but this check GATES whether the pinned binary is fetched (in `doctor` the same
# pattern only prints a status line), and a collision would hand the lint gate to an unpinned
# binary. Equality on the extracted field keeps the property the previous regex had — 2.13.10
# does not satisfy a 2.13.1 pin — and fails closed: no match yields an empty `got`, so it
# installs. The message prints what was FOUND, so a surprise is visible rather than silent.
#
# Deliberately NOT shared with `doctor`, whose one pattern spans four tools with four banner
# shapes; a golangci-lint-specific extraction cannot generalize there.
#
# The check reads the binary PATH resolves, because that is the one `lint` will actually run,
# while the install writes to `go env GOPATH`/bin. Those are the same thing in CI (setup-go
# puts GOPATH/bin on PATH) but can diverge locally — e.g. a mise-shimmed golangci-lint ahead
# of GOPATH/bin, which is a real drift this repo has hit. When they diverge, this target can
# report "already installed" while GOPATH/bin holds something else, or reinstall on every run
# without ever changing what lint uses. `make doctor` is what reports that; it prints the
# resolved path alongside the wanted version precisely so the divergence is visible.
.PHONY: install-tools
install-tools:
	@want_bare=$$(echo "$(GOLANGCI_LINT_VERSION)" | sed 's/^v//'); \
	got=$$(command -v golangci-lint >/dev/null 2>&1 && golangci-lint version 2>&1 \
		| sed -n 's/.*has version \([0-9][0-9.]*\).*/\1/p'); \
	if [ "$$got" = "$$want_bare" ]; then \
		echo "golangci-lint $$got already installed ($$(command -v golangci-lint)), skipping download"; \
	else \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh \
			| sh -s -- -b `go env GOPATH`/bin $(GOLANGCI_LINT_VERSION); \
	fi