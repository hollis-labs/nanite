-- +goose Up
-- Additive DDL from released libs/core/util v0.4.0 scheduler/sqlstore
-- (schema/002_retention.sql and schema/003_options.sql), owned by Nanite goose.
-- 173 is not edited. Every statement is idempotent. The 001_init tables are
-- unchanged between go-scheduler v0.3.0 and util v0.4.0.

-- Compact permanent occurrence fence: never removed with a schedule or fire.
-- After pruning, occurrences at or before through_at cannot be materialized.
CREATE TABLE IF NOT EXISTS gosched_pruned (
    schedule_id TEXT PRIMARY KEY,
    through_at TEXT NOT NULL
);

-- Additive metadata preserves the original schedule and fire schema. Empty
-- metadata gives legacy rows the documented defaults.
CREATE TABLE IF NOT EXISTS gosched_schedule_options (
    schedule_id TEXT PRIMARY KEY,
    options_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS gosched_fire_options (
    fire_id TEXT PRIMARY KEY,
    options_json TEXT NOT NULL DEFAULT '{}'
);

-- +goose Down
-- Rollback drops the three tables above. Refuse while they hold durable
-- material (the pruned fence is permanent by design; options rows carry
-- schedule/fire policy that cannot be reconstructed) and, like 173's own guard,
-- while schedules, fires or an authority marker exist: otherwise this Down
-- would drop the options tables and 173's Down would then refuse, leaving a
-- half-downgraded schema. Restore the pre-cutover backup instead.
-- +goose StatementBegin
CREATE TEMP TABLE scheduler_options_down_guard (empty INTEGER CHECK(empty = 0));
INSERT INTO scheduler_options_down_guard
SELECT (SELECT COUNT(*) FROM gosched_pruned) +
       (SELECT COUNT(*) FROM gosched_schedule_options) +
       (SELECT COUNT(*) FROM gosched_fire_options) +
       (SELECT COUNT(*) FROM gosched_schedules) +
       (SELECT COUNT(*) FROM gosched_fires) +
       (SELECT COUNT(*) FROM scheduler_storage_authority);
DROP TABLE scheduler_options_down_guard;
-- +goose StatementEnd
DROP TABLE gosched_fire_options;
DROP TABLE gosched_schedule_options;
DROP TABLE gosched_pruned;
