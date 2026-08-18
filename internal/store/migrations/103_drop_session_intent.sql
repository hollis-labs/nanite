-- +goose Up
-- Phase 0 item 28 (TASKS/phase-0/28-cut-session-intent-classifier.md): drop
-- sessions.intent, the column backing the now-deleted
-- ClassifySessionIntent/ScoreIntent classifier
-- (internal/service/session_intent.go, added by
-- migrations/053_session_intent.sql). Its only real consumer was
-- IsLongRunning gating the Glass-4 proactive stash + post-compaction inject
-- in internal/service/chat_generate.go; Glass-4 is now made universal (fires
-- for every session, gate removed) alongside this cut, per decision log §23
-- and docs/engineering/architecture/06-session-lifecycle-and-recovery.md's
-- "Handoffs, scratchpad, and Glass-4" section.
--
-- Mechanism note (corrects this task's own task-file rationale for the
-- record, per EXECUTION-PROCESS.md worker step 7 -- the cut itself is not in
-- question, only how big a hammer dropping it requires): the task file
-- describes sessions.intent as needing the SQLite rename-recreate-copy
-- dance because it carries a CHECK constraint, citing
-- 074_agent_profiles_multi_agent.sql / 083_durable_agent_stopped_status.sql
-- as precedent. Verified directly against the SQLite version this codebase
-- actually embeds (modernc.org/sqlite v1.54.0 -> SQLITE_VERSION 3.53.3,
-- confirmed via lib/sqlite.go) with a standalone sqlite3 3.43.2 CLI probe
-- (same DROP COLUMN code path, older but still well past the relevant
-- SQLite 3.35.0 DROP COLUMN feature and its later CHECK-constraint-drop
-- refinement): SQLite's ALTER TABLE DROP COLUMN natively drops a CHECK
-- constraint that references ONLY the column being dropped, along with the
-- column itself. sessions.intent's constraint --
-- `CHECK (intent IS NULL OR intent IN (...))` -- refers to nothing but
-- `intent`, so it qualifies. A live round-trip (create table with the exact
-- constraint text, insert rows, DROP COLUMN, re-ADD COLUMN with the same
-- CHECK, confirm the constraint still rejects an out-of-enum value) matched
-- this exactly. The 074/083 precedent predates goose adoption and rebuilt
-- durable_agent_instances for a different reason (WIDENING a CHECK
-- constraint's enum in place, which SQLite genuinely cannot do without a
-- rebuild) -- not applicable to a straight column drop. This migration
-- instead follows its immediate predecessor's precedent,
-- 102_drop_session_compaction_summary_fields.sql (also a Phase 0 cut, also
-- post-goose-adoption): a bare ALTER TABLE ... DROP COLUMN, no rebuild, plus
-- a real tested Down.

ALTER TABLE sessions DROP COLUMN intent;

-- +goose Down
-- Re-adds sessions.intent with its original nullable-TEXT-plus-CHECK shape
-- (migrations/053_session_intent.sql). Structure only, not data -- DROP
-- COLUMN is inherently lossy and nothing in this codebase read
-- sessions.intent back into a durable log, so an empty (NULL-filled) column
-- of the correct shape is the honest, achievable Down here, matching
-- 102's Down precedent.

ALTER TABLE sessions ADD COLUMN intent TEXT NULL CHECK (intent IS NULL OR intent IN ('long-running', 'per-turn', 'ephemeral'));
