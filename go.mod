module github.com/MyCarrier-DevOps/goLibMyCarrier

go 1.25

// This is a meta-module that provides version tracking for all submodules.
// Individual packages should be imported directly:
//   github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse
//   github.com/MyCarrier-DevOps/goLibMyCarrier/slippy
//   github.com/MyCarrier-DevOps/goLibMyCarrier/logger
// etc.

require github.com/google/go-github/v82 v82.0.0 // indirect
