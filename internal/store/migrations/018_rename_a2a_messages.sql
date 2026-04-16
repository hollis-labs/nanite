-- Rename the legacy `a2a_messages` table to `agent_messages`.
--
-- The S7 rename (a2a → messaging) kept the table name under its legacy
-- `a2a_` prefix. This migration closes that gap.
--
-- Target name is `agent_messages`, NOT `messages` — the existing
-- `messages` table stores session-chat history (user ↔ assistant
-- turns) and is a separate concern. `agent_messages` matches the
-- agent-to-agent/user-to-agent semantic of the subsystem and avoids
-- the collision. This is the same reason S7 deviation D-2 pinned
-- HTTP routes to `/api/messaging/*` instead of `/api/messages/*`.
--
-- Clean break — no consumers outside nanite, no aliases retained.
--
-- SQLite's ALTER TABLE ... RENAME TO preserves FK references automatically
-- (the self-referencing `reply_to` column, which REFERENCES a2a_messages(id),
-- is rewritten to reference agent_messages(id)). Indexes migrate with the
-- table but keep their idx_a2a_* names — we drop and recreate them with
-- idx_agent_messages_* names to match.

-- The migration runner has no schema_migrations table — every migration
-- re-runs on every boot, relying on idempotent DDL. On boot N+1 this
-- migration fires against a DB where a2a_messages has already been
-- renamed to agent_messages, BUT migration 001 re-creates an empty
-- a2a_messages shell each boot (its CREATE TABLE IF NOT EXISTS sees no
-- conflict). We handle both:
--   1. The runner swallows "already another table" errors on ALTER
--      TABLE ... RENAME TO (see store.go).
--   2. We explicitly DROP the re-created shell after the (maybe-failed)
--      rename so it doesn't stick around.

ALTER TABLE a2a_messages RENAME TO agent_messages;

DROP TABLE IF EXISTS a2a_messages;

DROP INDEX IF EXISTS idx_a2a_thread;
DROP INDEX IF EXISTS idx_a2a_to_session_agent;
DROP INDEX IF EXISTS idx_a2a_from_session_agent;
DROP INDEX IF EXISTS idx_a2a_messages_channel;

CREATE INDEX IF NOT EXISTS idx_agent_messages_thread
  ON agent_messages(thread_id, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_messages_to_session_agent
  ON agent_messages(to_session_id, to_agent_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_messages_from_session_agent
  ON agent_messages(from_session_id, from_agent_id);

CREATE INDEX IF NOT EXISTS idx_agent_messages_channel
  ON agent_messages(to_session_id, to_agent_id, channel, status, created_at DESC);
