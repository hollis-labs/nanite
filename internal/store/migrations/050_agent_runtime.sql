-- 050_agent_runtime.sql
-- Phase 4c.1 of agent-boot adoption (CW-20260508-0002).
--
-- agent_runtime tracks the lifecycle row internal/runtime/agent.Boot
-- writes for every spawned agent process. Distinct from store.Session
-- (which tracks chat sessions): not every Boot owns a chat row —
-- ModeBackground tasks, scheduler-dispatched executors, and nested
-- subagents all create runtime rows without owning chat history.
--
-- State machine (column `state`):
--   launching → running → done|failed|orphaned
-- `orphaned` is set by SweepOrphans at daemon bootstrap when a
-- persisted PID is no longer alive.
--
-- Mode column mirrors agent.Mode.String() values from
-- internal/runtime/agent/agent.go.
--
-- Schema decisions (locked, see Vanta
-- decisions.nanite.architecture.runtime_store_distinct_from_session
-- rev 01KR3W3RYNPTXRW9P6DMEED9KV):
--   - Distinct from subagent_runs (which has its own status/mode CHECK
--     constraints around the approval flow).
--   - parent_session_id is the calling chat session for ModeSubagent;
--     empty for top-level boots.
--   - provider_session_id is the adapter-supplied id (claude's
--     session_id, codex's, etc.) populated via OnSessionID.

CREATE TABLE IF NOT EXISTS agent_runtime (
    id TEXT PRIMARY KEY,
    agent_profile TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'long_lived'
        CHECK(mode IN ('long_lived','one_shot','resume','subagent','background','unknown')),
    workdir TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'launching'
        CHECK(state IN ('launching','running','done','failed','orphaned')),
    pid INTEGER NOT NULL DEFAULT 0,
    parent_session_id TEXT NOT NULL DEFAULT '',
    provider_session_id TEXT NOT NULL DEFAULT '',
    meta_json TEXT NOT NULL DEFAULT '{}',
    failure_reason TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_agent_runtime_state
    ON agent_runtime(state, started_at);

CREATE INDEX IF NOT EXISTS idx_agent_runtime_parent
    ON agent_runtime(parent_session_id, started_at);

-- agent_runtime_checkpoints stores ModeResume payloads so a daemon
-- restart or a fresh process can rehydrate the provider session id.
-- Empty until a Manager-level Checkpoint API lands in go-agent-sessions
-- tracked as a Vanta follow-up. Until then GetCheckpoint returns
-- ErrNotFound.

CREATE TABLE IF NOT EXISTS agent_runtime_checkpoints (
    id TEXT PRIMARY KEY,
    runtime_id TEXT NOT NULL,
    provider_session_id TEXT NOT NULL DEFAULT '',
    captured_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_runtime_checkpoints_runtime
    ON agent_runtime_checkpoints(runtime_id, captured_at);

-- CW-20260429-0021: surface the "acknowledge failed tool calls" rule onto
-- already-deployed databases. Migration 027 ran INSERT OR IGNORE, so existing
-- installs carry the older Style section text -- this migration UPDATEs the
-- row in place to append the new bullet at the end of the Style section.
--
-- Why: c110 evidence showed the agent making 4 failing card_show
-- calls, then a 5th successful one, then responding "Perfect! I''ve created
-- a demo report card..." with no mention of the failed attempts. The
-- structured tool_calls array already records the failures with status
-- error -- the prompt rule asks the agent to surface them.
--
-- The anchor is the "Do not narrate your tool plan unless the user asked
-- for it." bullet -- the last entry in the Style section since 027 first
-- landed, which keeps the new bullet at the bottom of Style. Anchoring on
-- a stable, unique line keeps the substring REPLACE deterministic.
--
-- The example sentence inside the new bullet uses a comma instead of a
-- semicolon ("...not allowed, corrected payload below.") because splitSQL
-- in internal/store/store.go splits the script on the SQL terminator,
-- and a literal one inside a string literal would be interpreted as a
-- statement boundary.
--
-- Idempotent. Safe to re-run: the WHERE guard skips rows that already
-- contain the new bullet phrase, so a second run is a no-op even if a
-- prior run was partially applied.
-- IMPORTANT: no semicolons inside comments.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- Do not narrate your tool plan unless the user asked for it.',
        '- Do not narrate your tool plan unless the user asked for it.
- **Acknowledge failed tool calls.** If any tool call this turn returned an error before you found a working approach, mention it in one short sentence in your response. Example: "First attempt rejected for additional properties not allowed, corrected payload below." This keeps the user oriented and surfaces lens activity (retry, repair) so we can diagnose recurring failure modes.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%- Do not narrate your tool plan unless the user asked for it.%'
  AND template NOT LIKE '%Acknowledge failed tool calls%';
