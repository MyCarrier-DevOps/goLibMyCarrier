// Package postgresmigrator provides version-controlled schema migrations for
// Postgres, mirroring the goLibMyCarrier/clickhousemigrator interface.
//
// It manages two kinds of schema change:
//
//   - Versioned migrations (run once, gated by a persisted integer version in a
//     "{prefix}_schema_version" table). Each migration is applied inside a
//     transaction — Postgres has transactional DDL, so a failed migration rolls
//     back atomically.
//   - Idempotent ensurers (run every time, after migrations). Use these for
//     dynamic schema derived from runtime config (e.g. one column per pipeline
//     step) with statements like ADD COLUMN IF NOT EXISTS.
//
// The migrator operates over any pgx querier (Querier), which *pgxpool.Pool
// satisfies directly, so it composes with the goLibMyCarrier/postgres session.
package postgresmigrator
