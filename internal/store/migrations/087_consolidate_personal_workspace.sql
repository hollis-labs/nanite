-- 087_consolidate_personal_workspace.sql
-- CW-20260815-0010: consolidate the two-workspace ("default", "personal")
-- setup down to one. Multi-workspace GUI complexity is deferred by explicit
-- project-owner decision, Torque projects are the organizing concept going
-- forward, not Nanite workspaces. This migration is the data half of that
-- consolidation (the seed-time half is in store/seed.go, which no longer
-- inserts a "personal" row on a fresh DB).
--
-- "personal" had 22 real sessions (architecture reviews, planning --
-- confirmed via a live-data read before writing this migration, not
-- assumed empty/disposable) with no activity after 2026-05-26, versus
-- "default"'s continuous activity through August 2026. Operator decision:
-- migrate, not delete or leave-orphaned -- re-point personal's sessions to
-- default, then remove the personal workspace row. workspace_role_trust
-- rows for "personal" were pure duplicates of "default"'s (same seed
-- timestamp, migration_036_dogfood_seed) and are removed via the existing
-- ON DELETE CASCADE FK on workspace_id -- nothing unique is lost. "personal"
-- had zero projects and zero workflows at consolidation time, so no other
-- table needed a re-point.
--
-- Every migration in this codebase re-runs on every boot (no
-- schema_migrations table -- see store.go migrate()), so both statements
-- below are written to be naturally idempotent: the UPDATE matches zero
-- rows and the DELETE matches zero rows on every run after the first.
--
-- NOTE: the migration runner splits SQL statements on the semicolon
-- character INCLUDING inside comments (066 finding, also called out in
-- 073's migration file) -- this file deliberately uses none in any
-- comment line.

UPDATE sessions SET workspace_id = 'default' WHERE workspace_id = 'personal';

DELETE FROM workspaces WHERE id = 'personal';
