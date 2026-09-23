module github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator

go 1.26.0

toolchain go1.26.5

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.48.0
	github.com/MyCarrier-DevOps/goLibMyCarrier/logger v1.4.1
)

replace github.com/MyCarrier-DevOps/goLibMyCarrier/logger => ../logger

require (
	github.com/ClickHouse/ch-go v0.74.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.8.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/paulmach/orb v0.13.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)
