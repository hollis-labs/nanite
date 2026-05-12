-- 059_agent_parent_dispatch_allowlist.sql
-- CW-20260512-0107 (SP-20260512-0008 W2A — Agent Broker describe-with-allowlist).
--
-- Adds `parent_dispatch_allowlist` to agent_profiles: a JSON array of role
-- slugs the agent (as a *parent*) is permitted to dispatch to via
-- task_execute. The Tool Broker Describe hook (W1B, CW-20260512-0105) reads
-- this through service.SelectForAgent -> describer.CallerAgent.DispatchAllowlist
-- and renders the list into the task_execute description the LLM reads.
--
-- ## Why a new column instead of reusing tool_permissions
--
-- `tool_permissions` (JSON) carries allow/deny rules over *tool* names —
-- semantically distinct from "which agent-role slugs can I dispatch to".
-- Mixing them muddies the schema and forces a name-collision check between
-- tool-name globs and role slugs at every read site. A small dedicated
-- column matches the established pattern (`tools`, `directories`, `tags`
-- are all JSON-array TEXT columns) and keeps each concept on its own
-- query path.
--
-- ## Sharp edges
--
-- - NULLability: TEXT NOT NULL DEFAULT '[]'. Existing rows get '[]' (no
--   permitted dispatch — graceful degrade: the Describer renders the
--   baseline description). New rows that omit the field also default to
--   '[]'. No risk of a NULL panic in JSON parsers.
-- - Idempotent: ADD COLUMN errors with "duplicate column" on a second
--   run — the migration runner already swallows that per repo convention
--   (see migrations 035, 015). No schema_migrations table.
-- - The seed for the chat-role default agent (slug 'default') uses
--   ['researcher', 'planner', 'worker'] — the canonical 3-role allowlist
--   per the ticket Acceptance. Worker / planner / role profiles are NOT
--   seeded: they do not dispatch (they ARE the dispatch target), and the
--   chat-surface filter excludes task_execute from their visible surface
--   anyway. WHERE clause guards idempotency.
-- - 'researcher' is listed even though no `researcher` agent_profile row
--   exists yet. This is intentional and matches the user framing: the
--   *agent* picks an intent fit ("researcher" = "someone who can read
--   this codebase") and the Agent Broker resolves to the actual profile.
--   The reflex catalog (`internal/reflex/catalog.go:165`) already routes
--   `researcher-mention` -> `worker` as the fallback profile when no
--   dedicated `researcher` profile exists. Surfacing "researcher" in the
--   description preserves the intent-fit framing without forcing the
--   agent to memorize that the resolution is currently worker.

-- 1) Add the column. Idempotent — migration runner swallows duplicate-column
--    errors per existing ALTER TABLE convention (see migration 035).
ALTER TABLE agent_profiles ADD COLUMN parent_dispatch_allowlist TEXT NOT NULL DEFAULT '[]';

-- 2) Seed the chat-role default agent with the canonical 3-role allowlist.
--    WHERE-guarded: only updates the file-default row that still has the
--    default empty allowlist, so re-running this migration is a true
--    no-op (and any manual / API-driven adjustment of the allowlist is
--    preserved).
UPDATE agent_profiles
   SET parent_dispatch_allowlist = '["researcher","planner","worker"]',
       updated_at = datetime('now')
 WHERE slug = 'default'
   AND parent_dispatch_allowlist = '[]';
