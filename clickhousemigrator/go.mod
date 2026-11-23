module github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator

go 1.27.0

toolchain go1.27.3

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.51.0
	github.com/MyCarrier-DevOps/goLibMyCarrier/logger v1.4.1
)

replace github.com/MyCarrier-DevOps/goLibMyCarrier/logger => ../logger

require (
	github.com/ClickHouse/ch-go v0.75.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.8.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/paulmach/orb v0.14.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.33 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.6.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)
