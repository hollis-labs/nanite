-- Phase 5 / E2 (CW-20260419-0028): Memory-as-grounding consultation log.
--
-- Adds two tables that record the pre-strategy memory recall step introduced
-- by the grounding layer (internal/grounding).
--
-- grounding_consultations: one row per memory hit per user turn. Records both
--   consumed (surfaced to the LLM) and discarded (below threshold) hits so
--   the grounding analytics can measure recall quality.
--
-- grounding_outcomes: one row per surfaced consultation row, written after
--   the user's follow-up turn is observed. The outcome is derived by the
--   next-turn refinement heuristic (internal/grounding/outcome.go).
--
-- Columns — grounding_consultations:
--   id              autoincrement primary key
--   session_id      the Chat session that triggered the recall
--   turn_id         optional user-turn message ID (NULL when not tracked)
--   memory_key      Conduit memory key of the recalled hit
--   namespace       Conduit namespace of the recalled hit
--   summary         summary text of the recalled hit (first 512 chars)
--   similarity      similarity score [0.0, 1.0] from Vanta relevance ranking
--   consumed        1 = surfaced to LLM, 0 = fetched but below threshold
--   consulted_at    UTC timestamp of the recall event
--
-- Columns — grounding_outcomes:
--   id                  autoincrement primary key
--   consultation_id     FK → grounding_consultations.id
--   outcome_kind        "accepted" | "refined" | "unknown"
--   follow_up_excerpt   first 200 chars of the user follow-up turn
--   seconds_since_ack   wall-clock gap between assistant ack and follow-up
--   pruning_words       comma-separated pruning words found (empty when none)
--   recorded_at         UTC timestamp of outcome write
--
-- All CREATE ... IF NOT EXISTS so this migration is safe to replay.

CREATE TABLE IF NOT EXISTS grounding_consultations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id   TEXT    NOT NULL,
    turn_id      TEXT,
    memory_key   TEXT    NOT NULL DEFAULT '',
    namespace    TEXT    NOT NULL DEFAULT '',
    summary      TEXT    NOT NULL DEFAULT '',
    similarity   REAL    NOT NULL DEFAULT 0.0,
    consumed     INTEGER NOT NULL DEFAULT 0,   -- boolean: 0/1
    consulted_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- Index on session_id for per-session analytics queries.
CREATE INDEX IF NOT EXISTS idx_grounding_consultations_session
    ON grounding_consultations(session_id);

-- Index on consumed for ratio analysis (surfaced vs discarded).
CREATE INDEX IF NOT EXISTS idx_grounding_consultations_consumed
    ON grounding_consultations(consumed);

CREATE TABLE IF NOT EXISTS grounding_outcomes (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    consultation_id  INTEGER NOT NULL REFERENCES grounding_consultations(id),
    outcome_kind     TEXT    NOT NULL DEFAULT 'unknown',
    follow_up_excerpt TEXT   NOT NULL DEFAULT '',
    seconds_since_ack REAL   NOT NULL DEFAULT 0.0,
    pruning_words    TEXT    NOT NULL DEFAULT '',  -- comma-separated
    recorded_at      DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- Index on consultation_id for FK joins.
CREATE INDEX IF NOT EXISTS idx_grounding_outcomes_consultation
    ON grounding_outcomes(consultation_id);

-- Index on outcome_kind for acceptance-rate analytics.
CREATE INDEX IF NOT EXISTS idx_grounding_outcomes_kind
    ON grounding_outcomes(outcome_kind);
