-- ClickHouse reporting surface for the slippy Postgres table slip_rollbacks (migration v7,
-- DEVOPS-320), in the same form as the existing routing_slips_pg, slip_component_states_pg and
-- slip_ancestry_pg tables.
--
-- Apply by hand, once per ClickHouse database, AFTER slippy-migrator has applied v7 to the
-- matching Postgres database:
--   ci_test -> the dev pipeline instance's slippy Postgres (slippy-api-test)
--   ci      -> the prod pipeline instance's slippy Postgres (slippy-api)
--
-- Replace {database} with ci_test or ci, and the four connection placeholders with the values
-- the existing *_pg tables in THAT database use (same host, database, user and password).
-- Never commit real values.
--
-- Every Postgres column is NOT NULL, so no Nullable types are needed here.
CREATE TABLE IF NOT EXISTS {database}.slip_rollbacks_pg
(
    id             Int64,
    correlation_id String,
    workflow       String,
    recorded_at    DateTime64(6),
    environment    String,
    reason         String,
    source         String,
    rollout_phase  String,
    pod_hash       String,
    gitops_repo    String,
    gitops_commit  String,
    revert_commit  String,
    status         String,
    forced         Bool,
    message        String,
    outcome        String
)
ENGINE = PostgreSQL('{postgres_host_port}', '{postgres_database}', 'slip_rollbacks', '{postgres_user}', '{postgres_password}');
