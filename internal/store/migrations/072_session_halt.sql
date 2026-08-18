-- +goose Up
-- FU-21: per-session halt for the monitor-loop circuit breaker.
--
-- When the monitor loops detectors fire on a degenerate Supervisor session
-- (cache-miss-echo, identical-output verbatim echo, or repeated generation
-- interrupted markers), the loop POSTs /api/sessions/{id}/halt which stamps
-- this column. Subsequent pre-tick health checks see halted_at != NULL and
-- skip the tick, stopping the burn on a broken session within 3 consecutive
-- matches (target SLA -- less than ~45 minutes at a 15-min tick interval,
-- vs. the 8-hour live failure mode we observed 2026-05-20 0445-1219).
--
-- halted_reason carries the detector tag (A, B, C) and a short
-- machine-readable summary so postmortems do not need to cross-reference
-- event_log to know which detector tripped. The event_log row still carries
-- the full evidence blob.
--
-- Chose a column on sessions over a session_halts table because the spike
-- only needs the is-this-session-halted boolean plus the why tag. A
-- separate table would buy history (multiple halt-resume cycles) we do
-- not need for the spike. Halt cycle history lives in event_log instead.
--
-- The migration runner has no schema_migrations table, so every migration
-- re-runs on every boot. ALTER TABLE ADD COLUMN is handled by the runners
-- duplicate column tolerance (see internal/store/store.go), so the plain
-- ALTER without a guard is idempotent in practice. NOTE -- the runner
-- splits on the semicolon character, including inside comments, so this
-- file deliberately uses none.

ALTER TABLE sessions ADD COLUMN halted_at DATETIME;
ALTER TABLE sessions ADD COLUMN halted_reason TEXT;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
