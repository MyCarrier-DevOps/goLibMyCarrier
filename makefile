SHELL:=/bin/bash

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: lint
lint: install-tools
	go mod tidy 
	golangci-lint -v run --timeout 5m ./...

.PHONY: test
test:
	go mod download
	go test -v ./...

.PHONY: install-tools
install-tools:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b `go env GOPATH`/bin v1.63.0