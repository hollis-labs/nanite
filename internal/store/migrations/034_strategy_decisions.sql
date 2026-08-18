-- +goose Up
-- Phase 5 / E3 (CW-20260419-0026): Strategy loop decision log.
--
-- Adds the strategy_decisions table that records the v1 strategy planner's
-- decisions per turn. The chat loop's iteration ceiling is now sourced from
-- this row's max_turns column (E4 absorption — CW-20260419-0020).
--
-- Columns:
--   id                          autoincrement primary key
--   session_id                  the Chat session that produced this strategy
--   turn_id                     optional user-turn message ID (NULL when not tracked)
--   approach                    one of: direct_chain | subagent_delegation |
--                               ask_to_clarify | already_answered_from_cache
--   rationale                   short human-readable reason text (first 1024 chars)
--   max_turns                   initial loop turn budget chosen by the planner
--   escalation_budget           v2-reserved extension budget (always 0 in v1)
--   reflex_match_id             matched reflex ID, NULL when none
--   playbook_hit                consulted playbook ID, NULL when none (always NULL in v1)
--   grounding_consultation_ids  JSON array of row IDs from grounding_consultations
--   created_at                  UTC timestamp of the decision
--
-- Idempotent (CREATE ... IF NOT EXISTS) so this migration is safe to replay.

CREATE TABLE IF NOT EXISTS strategy_decisions (
    id                         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id                 TEXT    NOT NULL,
    turn_id                    TEXT,
    approach                   TEXT    NOT NULL DEFAULT 'direct_chain',
    rationale                  TEXT    NOT NULL DEFAULT '',
    max_turns                  INTEGER NOT NULL DEFAULT 0,
    escalation_budget          INTEGER NOT NULL DEFAULT 0,
    reflex_match_id            TEXT,
    playbook_hit               TEXT,
    grounding_consultation_ids TEXT    NOT NULL DEFAULT '[]', -- JSON array
    created_at                 DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- Index on session_id for per-session analytics queries.
CREATE INDEX IF NOT EXISTS idx_strategy_decisions_session
    ON strategy_decisions(session_id);

-- Index on approach for distribution analysis.
CREATE INDEX IF NOT EXISTS idx_strategy_decisions_approach
    ON strategy_decisions(approach);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
