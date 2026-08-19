-- TASKS/adhoc/01-eliminate-file-based-agent-runtime.md.
--
-- Data-only reconciliation, no schema change. The file-based agent runtime
-- (internal/agent/convert.go's IsFileBasedID/SlugFromFileID/CanonicalID()'s
-- "file-<slug>" fallback, agentServiceImpl's fileDefs-wins-over-DB
-- resolution) is eliminated by this same task's Go changes. Before that
-- code is gone, agentServiceImpl.Get/resolveFileProfile returned a
-- *store.AgentProfile whose .ID field was the synthetic "file-<slug>"
-- string, never the real agent_profiles row's actual ID (a bug this
-- task's own investigation confirmed by direct read, not by trusting any
-- prior task's account) -- see agent.go's resolveFileProfile /
-- agent.OverlayDBFields, which overlays role_id/model_id/runtime_kind/
-- consumer_id/activation_mode/class/default_state onto the file-derived
-- profile but never overlays ID itself.
--
-- Every caller that persisted `resolved.ID` (session_agents.EnsureSessionAgent
-- on auto-assign, message authorship stamping, execution-metrics logging,
-- pin authorship, and the messaging system's auto-registered sender/
-- recipient identity) therefore wrote the literal string "file-<slug>"
-- into a handful of tables instead of the real agent_profiles.id -- direct
-- inspection of this deployment's live DB (copied via `sqlite3 ... .backup`,
-- never queried in place) on 2026-08-19 confirmed real, non-zero rows for
-- slugs backend/default/system-architect across session_agents.agent_id,
-- messages.agent_id, execution_metrics.agent_id, pinned_content.agent_id,
-- agent_messages.from_agent_id/to_agent_id, and user_settings.default_agent.
-- None of these columns carry a real FK to agent_profiles(id) (see
-- 001_schema.sql's explicit "no FK: agents may be file-based" comments on
-- session_agents/messages) -- that lack of enforcement is exactly why the
-- bad value was never caught at write time.
--
-- Once IsFileBasedID's resolution branch is gone, agentServiceImpl.Get
-- looks up an ID like "file-backend" as a literal DB primary key -- which
-- never existed as a real row -- and fails. Session resolution degrades
-- to the ultimate slug-"default" fallback for every affected row, silently
-- reassigning any session bound to a non-default builtin agent (e.g. a
-- session pinned to "backend" or "system-architect" via the stale
-- "file-<slug>" binding) back to the Default agent. That is a real,
-- observable regression, not merely a cosmetic identity mismatch -- fixed
-- here by rewriting every "file-<slug>" value in these columns to the real
-- agent_profiles.id for that slug, wherever one exists.
--
-- Fully slug-driven (not hardcoded to the three slugs found in this
-- deployment's data) so any other operator DB with a different set of
-- stale "file-<slug>" values self-heals the same way. A "file-<slug>"
-- value with no matching agent_profiles.slug (an agent since renamed or
-- deleted) is left untouched -- there is nothing correct to rewrite it to,
-- and silently NULLing/deleting it is a bigger, unrelated behavior change
-- this task does not own.

-- +goose Up

-- session_agents has PRIMARY KEY (session_id, agent_id) and is upserted via
-- ON CONFLICT(session_id, agent_id) (store.EnsureSessionAgent) -- a session
-- could in principle have accumulated a row under BOTH the "file-<slug>"
-- spelling and the real agent_profiles.id for the same logical agent (two
-- independent upsert keys before this fix). No such collision exists in
-- this deployment's own data as verified directly against a live-DB copy
-- on 2026-08-19, but the rename below is written to survive one on another
-- deployment rather than fail on a UNIQUE constraint: first promote the
-- real-id row to is_primary if the file-based row was primary, then drop
-- the now-redundant file-based row, then rename.
UPDATE session_agents AS real
   SET is_primary = 1
 WHERE is_primary = 0
   AND EXISTS (
     SELECT 1 FROM session_agents AS legacy
     JOIN agent_profiles p ON p.slug = substr(legacy.agent_id, 6)
     WHERE legacy.agent_id LIKE 'file-%'
       AND legacy.is_primary = 1
       AND legacy.session_id = real.session_id
       AND p.id = real.agent_id
   );

DELETE FROM session_agents
 WHERE agent_id LIKE 'file-%'
   AND EXISTS (
     SELECT 1 FROM agent_profiles p
     JOIN session_agents real
       ON real.session_id = session_agents.session_id AND real.agent_id = p.id
     WHERE p.slug = substr(session_agents.agent_id, 6)
   );

UPDATE session_agents
   SET agent_id = (SELECT id FROM agent_profiles WHERE slug = substr(session_agents.agent_id, 6))
 WHERE agent_id LIKE 'file-%'
   AND EXISTS (SELECT 1 FROM agent_profiles WHERE slug = substr(session_agents.agent_id, 6));

UPDATE messages
   SET agent_id = (SELECT id FROM agent_profiles WHERE slug = substr(messages.agent_id, 6))
 WHERE agent_id LIKE 'file-%'
   AND EXISTS (SELECT 1 FROM agent_profiles WHERE slug = substr(messages.agent_id, 6));

UPDATE execution_metrics
   SET agent_id = (SELECT id FROM agent_profiles WHERE slug = substr(execution_metrics.agent_id, 6))
 WHERE agent_id LIKE 'file-%'
   AND EXISTS (SELECT 1 FROM agent_profiles WHERE slug = substr(execution_metrics.agent_id, 6));

UPDATE pinned_content
   SET agent_id = (SELECT id FROM agent_profiles WHERE slug = substr(pinned_content.agent_id, 6))
 WHERE agent_id LIKE 'file-%'
   AND EXISTS (SELECT 1 FROM agent_profiles WHERE slug = substr(pinned_content.agent_id, 6));

UPDATE agent_messages
   SET from_agent_id = (SELECT id FROM agent_profiles WHERE slug = substr(agent_messages.from_agent_id, 6))
 WHERE from_agent_id LIKE 'file-%'
   AND EXISTS (SELECT 1 FROM agent_profiles WHERE slug = substr(agent_messages.from_agent_id, 6));

UPDATE agent_messages
   SET to_agent_id = (SELECT id FROM agent_profiles WHERE slug = substr(agent_messages.to_agent_id, 6))
 WHERE to_agent_id LIKE 'file-%'
   AND EXISTS (SELECT 1 FROM agent_profiles WHERE slug = substr(agent_messages.to_agent_id, 6));

UPDATE user_settings
   SET default_agent = (SELECT id FROM agent_profiles WHERE slug = substr(user_settings.default_agent, 6))
 WHERE default_agent LIKE 'file-%'
   AND EXISTS (SELECT 1 FROM agent_profiles WHERE slug = substr(user_settings.default_agent, 6));

-- +goose Down
-- No reversal attempted. Reconstructing "file-<slug>" values would require
-- knowing, per row, whether that row's current real agent_profiles.id was
-- ever "file-<slug>" before this Up ran, versus a row that already carried
-- a real ID from the start (true for several of these same builtin agents,
-- e.g. default/reviewer/system-architect, which were seeded with real
-- UUIDs long before this bug's write path existed) or a row created after
-- this Up ran (which never carried the synthetic form at all). A blanket
-- "any row currently pointing at a known builtin agent -> file-<slug>"
-- reversal would over-match both of those cases and actively reintroduce
-- broken references once paired with this task's Go-side removal of the
-- code that used to resolve them -- same "no operational value, don't
-- fabricate one" judgment 061/115's Downs already document for this
-- codebase's other post-goose data-shape changes.
