-- +goose Up
-- Phase 1 item 02 (TASKS/phase-1/02-add-agents-composition-columns.md):
-- adds the remaining composition columns architecture/01-agent-construction.md
-- names for `agents` (still `agent_profiles` under the hood, per decision
-- log Section 6) that aren't already covered by a sibling Phase 1 task --
-- role_id (FK -> roles, added by 01-add-roles-table-and-cascade-
-- resolution.md/migration 114), model_id (FK -> models, only meaningful
-- once 06-fix-models-table-sync-target.md's DB-authoritative sync landed),
-- and runtime_kind (cli|api, populated but deliberately not yet consulted
-- for routing -- that's Phase 2's job). consumer_id was added separately by
-- 03-add-consumers-table.md/migration 112; class already existed
-- (migration 074).
--
-- Both new FK columns are nullable during transition -- existing rows have
-- no role/model bound yet until 10-data-migrate-nanite-agents-md.md (out of
-- Phase 1 scope) backfills them; a NOT NULL constraint here would break
-- every pre-existing row. No CHECK/rewrite dance is needed for either (see
-- 112_add_consumers_table.sql's identical precedent for a nullable FK added
-- via plain ADD COLUMN).
ALTER TABLE agent_profiles ADD COLUMN role_id TEXT REFERENCES roles(id);
ALTER TABLE agent_profiles ADD COLUMN model_id TEXT REFERENCES models(id);

-- runtime_kind is the one new column in this task that gets a real DB-level
-- CHECK (unlike class/activation_mode/default_state, which migration 074's
-- own comment says were deliberately left Go-layer-only because SQLite
-- ALTER TABLE ADD COLUMN can't carry an idempotent CHECK after the column
-- exists -- irrelevant here since this ADD COLUMN only ever runs once,
-- tracked by goose, same non-issue 053_session_intent.sql's
-- `ADD COLUMN intent TEXT NULL CHECK (...)` already established as safe
-- precedent for this exact shape). NULL is allowed at the column level (a
-- fresh ADD COLUMN with no DEFAULT starts every existing row NULL) but the
-- UPDATE immediately below backfills every row to a real 'cli'/'api' value
-- before this migration finishes, and internal/store/agents.go's
-- applyMultiAgentDefaults ensures every row created afterward gets one too
-- -- so runtime_kind is NULL only for the instant between these two
-- statements, never in steady state.
ALTER TABLE agent_profiles ADD COLUMN runtime_kind TEXT
    CHECK (runtime_kind IS NULL OR runtime_kind IN ('cli', 'api'));

-- Backfill runtime_kind for every existing row from its current
-- default_provider, mirroring chat.IsCLIProvider's exact classification
-- (name == 'pty' OR has prefix 'pty-' OR has prefix 'sub-' => cli, else
-- api) -- internal/store can't import internal/chat directly (chat already
-- imports store; the reverse would cycle), so this SQL CASE and
-- agents.go's Go-side inferRuntimeKind mirror are kept in lockstep by hand,
-- same "mirror without an import cycle" pattern agents.go's own
-- urnPrefix/generateAgentURN already uses for internal/agent.
UPDATE agent_profiles
   SET runtime_kind = CASE
       WHEN default_provider = 'pty'
         OR default_provider LIKE 'pty-%'
         OR default_provider LIKE 'sub-%'
       THEN 'cli'
       ELSE 'api'
   END;

-- Real design decision (documented at length in this task's Work Log):
-- extend the *existing* agent_profiles.activation_mode column (migration
-- 074, currently a Go-layer-validated 'singleton'/'instance' 2-value enum
-- that is NOT consulted by any behavior anywhere -- confirmed by exhaustive
-- grep) to the real 3-value enum architecture/01-agent-construction.md
-- calls instance_mode ('singleton' / 'fresh-per-wake' / 'concurrent'),
-- rather than adding a second, competing column. internal/service/
-- durable_wake.go's wakeSkipReason is rewired (same commit) to read this
-- column instead of its previous hardcoded
-- `lifecycle_class != 'process'` special case.
--
-- Backfill derives the new value from `class`, not from the pre-existing
-- activation_mode text -- verified against a real copy of the production
-- backup (main.db.pre-execution-backup-20260818-132726) that
-- activation_mode's old value never actually correlated 1:1 with the real
-- wake-skip behavior every row already had: two rows (torque-supervisor,
-- process; task-planner, template) already carried activation_mode=
-- 'singleton' despite their class exempting them from the old hardcoded
-- block, while three others (atlas-curator, loom-curator: process;
-- content-writer: template) carried 'instance'. Since activation_mode has
-- never been read by any behavior before this migration, `class` is the
-- only column that ever carried real, currently-live wake-skip intent, so
-- deriving the backfill from `class` (not the old activation_mode value)
-- is what actually preserves every row's current real behavior --
-- reproducing durable_wake.go's old `lifecycle_class == 'process'`
-- exemption for process, and deliberately extending the same exemption to
-- template (both use a fresh/one-shot session policy with no reuse
-- collision to guard against -- durableAgentLaunchPolicyFor's
-- SessionPolicyFreshPerWake vs SessionPolicyFreshOneShot), closing a live,
-- latent instance of the same CW-20260817 "wakeable exactly once" bug for
-- content-writer that the prior fix's `!= process` check never covered.
-- advisor/harness (or anything else) keep the blocking 'singleton'
-- default, unchanged from today's real behavior.
UPDATE agent_profiles
   SET activation_mode = CASE
       WHEN class IN ('process', 'template') THEN 'fresh-per-wake'
       ELSE 'singleton'
   END;

-- +goose Down
-- Structure-only, matching this codebase's established precedent for
-- lossy value-remap migrations (105/115's Downs): the activation_mode
-- value rewrite above is not reverted -- there is no reliable way to
-- recover which pre-migration rows were 'instance' vs 'singleton' once
-- collapsed into the new 3-value space, and (per the Up comment above)
-- the old value was never behaviorally authoritative anyway. Only the
-- three columns this migration actually added are dropped, in the reverse
-- order they were created.
ALTER TABLE agent_profiles DROP COLUMN runtime_kind;
ALTER TABLE agent_profiles DROP COLUMN model_id;
ALTER TABLE agent_profiles DROP COLUMN role_id;
