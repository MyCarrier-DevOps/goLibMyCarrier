package goLibMyCarrier

// Package goLibMyCarrier is a collection of reusable Go libraries for MyCarrier DevOps infrastructure.
//
// # Available Packages
//
// This is a meta-module for version tracking. Import individual packages directly:
//
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/auth - Authentication utilities
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient - ArgoCD client
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse - ClickHouse database client
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator - ClickHouse schema migrations
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/github - GitHub API client
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/kafka - Kafka messaging client
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/logger - Structured logging interfaces
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/otel - OpenTelemetry instrumentation
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/slippy - Routing slip orchestration
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/vault - HashiCorp Vault client
//   - github.com/MyCarrier-DevOps/goLibMyCarrier/yaml - YAML utilities
//
// # Installation
//
// Install a specific package using go get:
//
//	go get github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse@v1.3.42
//
// # Documentation
//
// See README.md and individual package documentation for detailed usage information.

// Version is set at build time via ldflags or can be determined from module info.
// Use debug.ReadBuildInfo() to get the actual version at runtime.
var Version = "dev"
