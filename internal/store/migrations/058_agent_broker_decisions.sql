-- +goose Up
-- 057_agent_broker_decisions.sql
-- CW-20260509-0047 (SP-20260429-0001 broker-v1) — agent broker decision log.
--
-- Telemetry table for the agent broker (decisions.nanite.architecture.agent_broker_v1).
-- Every chat turn that runs through broker.Decide writes one row here so the
-- v2 peer-agent escape hatch decision can be made on data, not vibes.
--
-- Naming note: the existing `broker_decisions` table (migration 001 + 031)
-- is the *tool* broker's decision log (request_tools / progressive discovery).
-- That table's columns (intent, layer_reached, selected_tools, signals,
-- consecutive_empty, total_calls, outcome, loaded_count, reflection_query)
-- are scoped to tool selection. The *agent* broker decides which agent
-- profile owns the turn (chat-handle vs dispatch-to-worker vs dispatch-to-
-- planner) and reasons over a different signal bundle (mode + scope-tier +
-- reflex). Two different concerns, two different tables — this one is named
-- `agent_broker_decisions` to disambiguate. The CW-20260509-0047 boot prompt
-- and decision summary both used the bare name `broker_decisions` — that
-- name was already taken upstream by the tool broker, so the agent-prefixed
-- form is the surfacing of that collision.
--
-- Columns mirror the broker.Input + broker.Decision pair from
-- github.com/hollis-labs/go-agent-broker/broker so call sites can write rows
-- 1:1 from the structs without an intermediate mapping layer:
--   session_id       — host session
--   turn_id          — host turn (one row per Decide call)
--   user_input_hash  — sha256 of broker.Input.UserText (lets us correlate
--                      identical replays without storing raw user text)
--   mode_signal      — broker.Input.SessionMode ("", "chat", "plan", "work")
--   scope_tier       — broker.Input.ScopeTier ("", "trivial", "small",
--                      "medium", "large", "open")
--   reflex_id        — broker.Input.ReflexMatchID (empty when no reflex hit)
--   decision         — broker.Decision.AgentProfile ("" = chat handles it,
--                      else "worker" / "planner" / custom slug)
--   reason           — broker.Decision.Reason (human-readable)
--   confidence       — broker.Decision.Confidence (0..1, REAL)
--   created_at       — server-side timestamp, datetime('now')
--
-- Append-only: no UPDATE / DELETE call sites planned. The append-only invariant
-- is enforced by convention (no helpers expose mutation), not by trigger.
--
-- Idempotent: CREATE TABLE IF NOT EXISTS + CREATE INDEX IF NOT EXISTS so the
-- migration runner re-applies cleanly on every boot per the codebase's
-- no-schema_migrations convention. No semicolons in comments (splitSQL is
-- string-literal-unaware).

CREATE TABLE IF NOT EXISTS agent_broker_decisions (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    user_input_hash TEXT NOT NULL,
    mode_signal TEXT NOT NULL DEFAULT '',
    scope_tier TEXT NOT NULL DEFAULT '',
    reflex_id TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Session lookup is the dominant read path (admin CLI per CW-20260509-0049,
-- FE inspector follow-up). Pair with id DESC for the recent-N query.
CREATE INDEX IF NOT EXISTS idx_agent_broker_decisions_session
    ON agent_broker_decisions(session_id, id DESC);

-- Created-at index covers the "recent across all sessions" admin query
-- (ListRecent) without forcing a full table scan.
CREATE INDEX IF NOT EXISTS idx_agent_broker_decisions_created_at
    ON agent_broker_decisions(created_at DESC);

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
