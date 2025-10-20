SHELL:=/bin/bash

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: lint
lint: install-tools
	@if [ -z "$(PKG)" ]; then \
		echo "Linting all modules..."; \
		for dir in argocdclient auth clickhouse github kafka logger otel vault yaml; do \
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
		for dir in argocdclient auth clickhouse github kafka logger otel vault yaml; do \
			if [ -d "$$dir" ]; then \
				echo "Testing $$dir module..."; \
				(cd $$dir && go mod download && go test -cover -coverprofile=../coverage-$$dir.out ./... && go tool cover -func=../coverage-$$dir.out); \
			else \
				echo "Directory $$dir not found, skipping..."; \
			fi; \
		done; \
	else \
		echo "Testing $(PKG) module..."; \
		(cd $(PKG) && go mod download && go test -cover -coverprofile=../coverage-$(PKG).out ./... && go tool cover -func=../coverage-$(PKG).out); \
	fi

.PHONY: fmt
fmt:
	@echo "Formatting all modules..."
	@for dir in argocdclient auth clickhouse github kafka logger otel vault yaml; do \
		if [ -d "$$dir" ]; then \
			echo "Formatting $$dir module..."; \
			(cd $$dir && gofmt -s -w .); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: bump
bump:
	@echo "Bumping module versions..."
	@for dir in argocdclient auth clickhouse github kafka logger otel vault yaml; do \
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
	@for dir in argocdclient auth clickhouse github kafka logger otel vault yaml; do \
		if [ -d "$$dir" ]; then \
			echo "Tidying $$dir module..."; \
			(cd $$dir && go mod tidy); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: install-go-test-coverage
install-go-test-coverage:
	go install github.com/vladopajic/go-test-coverage/v2@latest

.PHONY: check-coverage
check-coverage: install-go-test-coverage
	go test ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	${GOBIN}/go-test-coverage --config=./.testcoverage.yml

.PHONY: install-tools
install-tools:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b `go env GOPATH`/bin v2.5.0