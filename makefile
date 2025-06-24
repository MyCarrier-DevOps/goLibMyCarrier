SHELL:=/bin/bash

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: lint
lint: install-tools
	@echo "Linting all modules..."
	@for dir in auth clickhouse github kafka logger otel vault; do \
		if [ -d "$$dir" ]; then \
			echo "Linting $$dir module..."; \
			(cd $$dir && go mod tidy && golangci-lint run --config ../.github/.golangci.yml --timeout 5m ./...); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: test
test:
	@echo "Testing all modules..."
	@for dir in auth clickhouse github kafka logger otel vault; do \
		if [ -d "$$dir" ]; then \
			echo "Testing $$dir module..."; \
			(cd $$dir && go mod download && go test -v ./...); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: fmt
fmt:
	@echo "Formatting all modules..."
	@for dir in auth clickhouse github kafka logger otel vault; do \
		if [ -d "$$dir" ]; then \
			echo "Formatting $$dir module..."; \
			(cd $$dir && gofmt -s -w .); \
		else \
			echo "Directory $$dir not found, skipping..."; \
		fi; \
	done

.PHONY: install-tools
install-tools:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s v2.1.6