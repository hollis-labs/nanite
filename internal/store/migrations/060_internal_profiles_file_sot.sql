-- 060_internal_profiles_file_sot.sql
-- CW-20260512-0111 (SP-20260512-0009 Wave 1 — internal agent profiles:
-- file source of truth).
--
-- ## Purpose
--
-- This migration lands two responsibilities in lockstep:
--
--   1. Flip the `source` column of the four canonical internal agent rows
--      (default, worker, planner, hint-selector) from any prior value
--      (typically 'builtin') to the new file-source-of-truth marker
--      'internal'. Wave 2 (CW-20260512-0112) deletes
--      agent_profiles WHERE source != 'internal' — without this flip the
--      Wave 2 cleanup would wipe these rows on the next operator boot.
--
--   2. Seed the four rows on FRESH-INSTALL databases that have never run
--      the deleted historical seed migrations (whatever shipped the
--      blt-worker-001 / blt-hint-selector-001 / blt-planner-001 rows in
--      the user's live DB). The seed uses INSERT OR IGNORE so it is a
--      true no-op on already-deployed DBs. The body matches
--      internal/agent/builtin/profiles/*.md so a fresh install lands the
--      same content the boot-time AutoIngestAgents pass would write on
--      the first start.
--
-- ## PROMPT-SYNC chain
--
-- The canonical source for each prompt is
-- internal/agent/builtin/profiles/<slug>.md. When that file changes,
-- re-flow the body here using the same rules as migration 027:
--   (1) replace backticks with plain text,
--   (2) escape single quotes by doubling them ('' → '''').
-- The boot-time sync (internal/service/ingest.go::AutoIngestAgents) will
-- write the freshest body on every Nanite restart regardless — this
-- migration only matters for the first-boot hydration of a fresh DB.
--
-- ## Idempotency
--
-- - The UPDATE...SET source flips are guarded by WHERE source != 'internal'
--   so re-runs are no-ops.
-- - The INSERT OR IGNORE rows are no-ops when the slug already exists.
-- - No semicolons inside string literals (the splitSQL helper at
--   internal/store/store.go is not literal-aware).
--
-- ## Order of operations
--
-- Source-flip first, then seed-on-empty. The flip MUST land before the
-- Wave 2 cleanup migration runs in any subsequent boot, so this file
-- (number 060) must remain numerically below CW-20260512-0112's
-- migration (≥ 061). The orchestrator and Wave 2 implementer share this
-- constraint.

BEGIN;

-- 1) Flip provenance for the four canonical internal slugs. These are the
--    rows that previously carried source='builtin' (or, in some operator
--    DBs, no value at all because they were hand-seeded by deleted older
--    migrations). They are the file source-of-truth targets going forward.
--
--    source_ref is normalized to the embedded path so the UI's "edit the
--    file" affordance points to the correct location.
UPDATE agent_profiles
   SET source = 'internal',
       source_ref = 'embedded:profiles/default.md',
       updated_at = datetime('now')
 WHERE slug = 'default'
   AND source != 'internal';

UPDATE agent_profiles
   SET source = 'internal',
       source_ref = 'embedded:profiles/worker.md',
       updated_at = datetime('now')
 WHERE slug = 'worker'
   AND source != 'internal';

UPDATE agent_profiles
   SET source = 'internal',
       source_ref = 'embedded:profiles/planner.md',
       updated_at = datetime('now')
 WHERE slug = 'planner'
   AND source != 'internal';

UPDATE agent_profiles
   SET source = 'internal',
       source_ref = 'embedded:profiles/hint-selector.md',
       updated_at = datetime('now')
 WHERE slug = 'hint-selector'
   AND source != 'internal';

-- 2) Seed the four rows on fresh-install DBs that lack them entirely.
--    On already-deployed DBs the INSERT OR IGNORE is a true no-op (the
--    slug already exists). The system_prompt bodies match
--    internal/agent/builtin/profiles/<slug>.md verbatim — keep the two in
--    sync per the PROMPT-SYNC rule above.
--
--    Each row carries kind='internal' (S7 T5 registry: provenance class
--    for the future capability broker), can_execute reflects the actual
--    profile (worker=true, hint-selector=false, default/planner=false),
--    parent_dispatch_allowlist is set on the chat-role default agent
--    only (matches CW-20260512-0107 / migration 059).
--
--    Deterministic IDs follow the pre-existing convention:
--      - default: a UUID was assigned by the legacy seed; we use
--        blt-default-001 on fresh installs to keep a stable identity.
--      - worker / planner / hint-selector: the blt-<slug>-001 IDs match
--        the live DB and migration 058's WHERE id = 'blt-worker-001'
--        anchor.

-- NOTE: `default_mode` column dropped from INSERT list (SP-20260512-0011
-- baseline fix). Migration 063 drops the column entirely; including it in
-- this INSERT would fail on the second invocation of the migration runner
-- (which re-executes all SQL files on every store.New). Migration 001 still
-- creates the column with DEFAULT 'default' for the duration of the 001→063
-- window on fresh installs.
INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-default-001',
    'Default',
    'default',
    'You are a helpful AI assistant embedded in the Nanite chat harness. You have access to tools — file system, HTTP, math, MCP servers, and Nanite''s own self-tools. Your job is to use them to help the user.

## Capability

- You have **meta-tools** for discovery (tool_describe), pre-flight validation (tool_validate), and learning capture (lesson_capture). Reach for them when a tool''s contract is unfamiliar or after a call fails — you don''t have to memorise every schema.
- For multi-step flows like rendering envelope cards, you don''t need to own the recipe — describe the intent and the harness routes you to a specialized executor.

## Style

- Be direct. Match the user''s terseness — no ceremony, no trailing summaries.
- Use Markdown when it earns its keep (lists, code, tables). Prose otherwise.
- Don''t narrate your tool plan unless the user asked for it.

## Judgment

- Ask one pointed question before a long tool chain when the scope is unclear.',
    'General-purpose chat agent',
    '[]',
    '[]', '{}', 0, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/default.md', 'chat',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '["researcher","planner","worker"]'
);

INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-worker-001',
    'Worker',
    'worker',
    'You are a Worker agent in the Nanite harness, dispatched by a parent agent to handle a specific scoped task — writing code, running tools, or completing well-bounded work. You have full tool access.

Stay within the assigned scope. Do not initiate new conversations or expand the task beyond what the parent dispatched. When the task is done, return the result. When you cannot complete it with the tools and paths available, return an explicit failure — the universal Refusal rules govern this (your reply is treated as authoritative by the parent).',
    'General-purpose execution agent dispatched by the Chat harness or other lead agents',
    '[]',
    '["engine","conduit"]', '{"allow_list":["*"]}', 1, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/worker.md', 'tool',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-planner-001',
    'Planner',
    'planner',
    'Planner role — identity TBD. Phase 6 cognition arc will define authoritative behavior. This stub reserves the slug for M3 reflex dispatch.',
    'Decomposition and sequencing agent — breaks open-scope tasks into structured plans (Phase 6 stub)',
    '[]',
    '[]', '{}', 0, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/planner.md', 'list',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

INSERT OR IGNORE INTO agent_profiles
    (id, name, slug, system_prompt, description, modes,
     mcp_servers, tool_permissions, can_execute, settings,
     created_at, updated_at,
     agent_hash, version, tools, directories, constraints, tags, status,
     source, source_ref, icon,
     kind, capabilities_json, limits_json, model_strategy,
     imported_at, origin_system, format,
     parent_dispatch_allowlist)
VALUES (
    'blt-hint-selector-001',
    'Hint Selector',
    'hint-selector',
    'You are a hint-selector agent. Your sole job is to choose which affordance hints are most relevant for the current agent turn.

You will receive a JSON object with:
  - user_input: the user''s current message (string)
  - scope_tier: one of trivial, small, medium, large, open (string)
  - reflex_match: the matched reflex ID if any, or "" (string)
  - hint_catalog: array of {id, affordance, body, priority} objects

Respond with ONLY a JSON array of hint IDs (strings), ordered from most to least relevant. Return at most 5 IDs. Return at least 1 ID.

Rules:
- Always include "scratchpad" for any scope_tier except trivial.
- Include "memory_recall" for small, medium, large, open tiers.
- Include "peer_query" only for large or open tiers.
- Include "use_handoff_stash" only if the user_input mentions compaction, context loss, recovery, or restart.
- Include "consult_skills" when the task involves a known agent skill.
- Include "scope_check" for open or large tiers where scope is ambiguous.
- Include "reviewer_gate" only for tasks producing artifacts for review.
- Prefer hints whose reflex_id_in intersects with the matched reflex.

Example response (no prose, no markdown — raw JSON only):
["scratchpad","memory_recall","peer_query","scope_check"]',
    'Classification-only peer agent that selects relevant affordance hints for the current turn''s think-tool block',
    '[]',
    '[]', '{"allow_list":[]}', 0, '{}',
    datetime('now'), datetime('now'),
    '', 1, '[]', '[]', '{}', '[]', 'active',
    'internal', 'embedded:profiles/hint-selector.md', 'zap',
    'internal', '[]', '{}', '',
    '', 'nanite', 'markdown',
    '[]'
);

COMMIT;
