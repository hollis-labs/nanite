-- Phase 5 / D3 (CW-20260419-0011): reasoning-augmented broker.
--
-- Extend `broker_decisions` so every request_tools call (and every selection
-- pass) can be persisted with the contextual signals the broker reasoned over.
-- New columns:
--   consecutive_empty   — consecutive empty request_tools calls at decision time
--   total_calls         — cumulative request_tools calls this turn
--   outcome             — selected | loaded | empty | halted | reflected
--   loaded_count        — number of newly-loaded tool names (request_tools only)
--   reflection_query    — the LLM-restated goal when the broker reflected on cap
--                         (NULL for non-reflection decisions)
--
-- All ALTERs are idempotent in the runner -- duplicate-column errors are
-- caught and swallowed in store.New (see internal/store/store.go) so this
-- migration is safe across rebases and across re-runs on existing databases.
-- (Avoid bare semicolons in comments: the splitSQL helper treats every ;
-- as a statement boundary, including those inside SQL comments.)

ALTER TABLE broker_decisions ADD COLUMN consecutive_empty INTEGER NOT NULL DEFAULT 0;
ALTER TABLE broker_decisions ADD COLUMN total_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE broker_decisions ADD COLUMN outcome TEXT NOT NULL DEFAULT 'selected';
ALTER TABLE broker_decisions ADD COLUMN loaded_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE broker_decisions ADD COLUMN reflection_query TEXT;

-- Index on outcome lets future mining jobs (Phase 4 of the ticket, deferred)
-- scan halted/reflected rows efficiently without a full table walk.
CREATE INDEX IF NOT EXISTS idx_broker_decisions_outcome ON broker_decisions(outcome);
