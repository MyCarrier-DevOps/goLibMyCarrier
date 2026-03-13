module github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator

go 1.26

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.42.0
	github.com/MyCarrier-DevOps/goLibMyCarrier/logger v1.3.63
)

replace github.com/MyCarrier-DevOps/goLibMyCarrier/logger => ../logger

require (
	github.com/ClickHouse/ch-go v0.70.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.3 // indirect
	github.com/paulmach/orb v0.12.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	go.opentelemetry.io/otel v1.39.0 // indirect
	go.opentelemetry.io/otel/trace v1.39.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.40.0 // indirect
)
