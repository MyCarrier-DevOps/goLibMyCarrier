package slippy

import "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator"

// SlippyMigrations returns the complete set of database migrations for the slippy library.
// These migrations are designed to be backwards compatible and reversible.
//
// The schema uses correlation_id as the unique identifier for routing slips.
// This aligns with MyCarrier's organization-wide use of correlation_id as the
// canonical identifier for jobs across all systems (Kafka, workflows, logging).
func SlippyMigrations() []clickhousemigrator.Migration {
	return []clickhousemigrator.Migration{
		migrationV1CreateRoutingSlipsTable(),
		migrationV2CreateHistoryMaterializedView(),
		migrationV3AddSecondaryIndexes(),
	}
}

// migrationV1CreateRoutingSlipsTable creates the main routing_slips table.
// This table uses ReplacingMergeTree to handle updates via INSERT.
//
// The correlation_id is the unique identifier for each routing slip and serves
// as the primary key for lookups. This ID persists through the entire slip
// lifecycle and links the slip to Kafka events, workflows, and all related systems.
func migrationV1CreateRoutingSlipsTable() clickhousemigrator.Migration {
	return clickhousemigrator.Migration{
		Version:     1,
		Name:        "create_routing_slips_table",
		Description: "Creates the main routing_slips table with denormalized step statuses for fast filtering",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS ci.routing_slips (
				-- Primary identifier: correlation_id is THE unique slip identifier
				-- This ID is used organization-wide to identify jobs across all systems
				correlation_id String,

				-- Repository metadata
				repository String,
				branch String,
				commit_sha String,

				-- Timestamps
				created_at DateTime64(3) DEFAULT now64(3),
				updated_at DateTime64(3) DEFAULT now64(3),

				-- Overall slip status
				status Enum8(
					'pending' = 1,
					'in_progress' = 2,
					'completed' = 3,
					'failed' = 4,
					'compensating' = 5,
					'compensated' = 6
				) DEFAULT 'pending',

				-- Components as JSON array for native querying
				-- Schema: [{"name": "...", "dockerfile_path": "...", "build_status": "...",
				--           "unit_test_status": "...", "image_tag": "..."}]
				components JSON DEFAULT '[]',

				-- Denormalized step statuses for query performance
				-- Each step can be: pending(1), held(2), running(3), completed(4),
				--                   failed(5), error(6), aborted(7), timeout(8), skipped(9)
				push_parsed_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				builds_completed_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				unit_tests_completed_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				secret_scan_completed_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				dev_deploy_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				dev_tests_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				preprod_deploy_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				preprod_tests_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				prod_release_created_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				prod_deploy_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				prod_tests_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				alert_gate_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				prod_steady_state_status Enum8(
					'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
					'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
				) DEFAULT 'pending',

				-- Step timestamps as JSON object for native querying
				-- Schema: {"step_name": {"started_at": "...", "completed_at": "..."}}
				step_timestamps JSON DEFAULT '{}',

				-- Full state history as JSON array for native querying
				-- Schema: [{"step": "...", "component": "...", "status": "...",
				--           "timestamp": "...", "actor": "...", "message": "..."}]
				state_history JSON DEFAULT '[]',

				-- Bloom filter indexes for fast lookups
				INDEX idx_repository repository TYPE bloom_filter GRANULARITY 1,
				INDEX idx_commit commit_sha TYPE bloom_filter GRANULARITY 1,
				INDEX idx_branch branch TYPE bloom_filter GRANULARITY 1
			)
			ENGINE = ReplacingMergeTree(updated_at)
			ORDER BY (correlation_id)
			PARTITION BY toYYYYMM(created_at)
			SETTINGS index_granularity = 8192
		`,
		DownSQL: `DROP TABLE IF EXISTS ci.routing_slips`,
	}
}

// migrationV2CreateHistoryMaterializedView creates a materialized view for historical tracking.
// This captures all slip state changes for auditing and analysis.
func migrationV2CreateHistoryMaterializedView() clickhousemigrator.Migration {
	return clickhousemigrator.Migration{
		Version:     2,
		Name:        "create_routing_slip_history_mv",
		Description: "Creates materialized view for historical tracking of all slip state changes",
		UpSQL: `
			CREATE MATERIALIZED VIEW IF NOT EXISTS ci.routing_slip_history_mv
			ENGINE = MergeTree()
			ORDER BY (correlation_id, timestamp)
			PARTITION BY toYYYYMM(timestamp)
			AS SELECT
				correlation_id,
				repository,
				JSONExtractString(entry, 'step') AS step,
				JSONExtractString(entry, 'component') AS component,
				JSONExtractString(entry, 'status') AS status,
				parseDateTimeBestEffort(JSONExtractString(entry, 'timestamp')) AS timestamp,
				JSONExtractString(entry, 'actor') AS actor,
				JSONExtractString(entry, 'message') AS message
			FROM ci.routing_slips
			ARRAY JOIN JSONExtractArrayRaw(state_history) AS entry
		`,
		DownSQL: `DROP VIEW IF EXISTS ci.routing_slip_history_mv`,
	}
}

// migrationV3AddSecondaryIndexes adds secondary indexes for common query patterns.
// These improve query performance for status-based filtering.
func migrationV3AddSecondaryIndexes() clickhousemigrator.Migration {
	return clickhousemigrator.Migration{
		Version:     3,
		Name:        "add_secondary_indexes",
		Description: "Adds secondary indexes for status columns to optimize common queries",
		UpSQL: `
			ALTER TABLE ci.routing_slips
				ADD INDEX IF NOT EXISTS idx_status status TYPE set(10) GRANULARITY 1,
				ADD INDEX IF NOT EXISTS idx_dev_deploy_held dev_deploy_status TYPE set(10) GRANULARITY 1,
				ADD INDEX IF NOT EXISTS idx_preprod_deploy_held preprod_deploy_status TYPE set(10) GRANULARITY 1,
				ADD INDEX IF NOT EXISTS idx_prod_deploy_held prod_deploy_status TYPE set(10) GRANULARITY 1
		`,
		DownSQL: `
			ALTER TABLE ci.routing_slips
				DROP INDEX IF EXISTS idx_status,
				DROP INDEX IF EXISTS idx_dev_deploy_held,
				DROP INDEX IF EXISTS idx_preprod_deploy_held,
				DROP INDEX IF EXISTS idx_prod_deploy_held
		`,
	}
}

// GetLatestMigrationVersion returns the highest migration version available.
// This is useful for checking if the schema is up to date.
func GetLatestMigrationVersion() int {
	migrations := SlippyMigrations()
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].Version
}
