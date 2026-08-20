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
	go install github.com/vladopajic/go-test-coverage/v2@latest

.PHONY: check-coverage
check-coverage: install-go-test-coverage
	go test ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	${GOBIN}/go-test-coverage --config=./.testcoverage.yml

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

# Mutation-test only the lines changed vs MUTATION_BASE — the pre-merge gate.
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

.PHONY: help
help:
	@echo "Targets (all accept PKG=<module> where noted):"
	@echo "  make lint           - golangci-lint            (PKG=)"
	@echo "  make test           - tests w/ race + coverage (PKG=)"
	@echo "  make fmt            - gofmt/goimports, all modules"
	@echo "  make tidy           - go mod tidy, all modules"
	@echo "  make check-sec      - govulncheck vuln scan    (PKG=)"
	@echo "  make check-coverage - coverage threshold gate"
	@echo "  make mutation       - mutation-test lines changed vs $(MUTATION_BASE) (PKG=)"
	@echo "  make mutation-all   - mutation-test a module in full; periodic audit (PKG=)"
	@echo "  make bump           - version bump helper"
	@echo ""
	@echo "Pinned tools: golangci-lint $(GOLANGCI_LINT_VERSION), govulncheck $(GOVULNCHECK_VERSION), mutest $(MUTEST_VERSION)"
	@echo "Mutation vars: MUTATION_BASE=$(MUTATION_BASE) MUTATION_THRESHOLD=$(MUTATION_THRESHOLD)"

.PHONY: install-tools
install-tools:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b `go env GOPATH`/bin $(GOLANGCI_LINT_VERSION)