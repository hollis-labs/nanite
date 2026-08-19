-- Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md):
-- agent_context_resolvers -- the DB-configurable home for the `cmd`/`http`
-- dynamic-resolver capability architecture/02-agent-launching.md names as one
-- of the two pieces that carry forward from the (retiring) boot-profile
-- catalog as a first-class mechanism "available to every agent, not gated
-- behind a separate catalog system":
--
--   "The `cmd`/`http` dynamic-resolver capability (fetch live data at
--    launch time and fold it into assembled context)."
--
-- Pre-port, this lived ONLY as a boot-profile-catalog YAML slot
-- (internal/bootprofile/requirements.go + slots.go), reachable only for a
-- session whose provider was an encoded "bootprofile:<id>" id -- i.e.
-- gated behind opting into the (now-retired) catalog system, not available
-- to a normal DB-defined agent. This table removes that gate: a resolver is
-- now a first-class row scoped to a real agent_profiles.id, resolved at
-- CLI-session launch time by internal/runtime/agent.ResolveContextBlocks
-- (see internal/service/chat_boot_drive.go's resolveAgentContextForBoot).
--
-- Deliberately narrower than the pre-port catalog's four deferred-slot
-- kinds (cmd / http / role_summary / skill_index): this task's scope, per
-- the architecture doc, is cmd/http only. role_summary/skill_index have no
-- equivalent here -- see this task's Work Log for why (they're solvable
-- through the new construction model's own data -- a role's system_prompt,
-- an agent's agent_skills join -- rather than needing a general-purpose
-- dynamic-fetch mechanism).
--
-- kind CHECK is intentionally narrower than the shared go-agent-context
-- library's SlotSourceKind taxonomy (which also has static_file/static_dir/
-- inline/role_summary/skill_index) -- those kinds solve problems this
-- table isn't trying to solve; a CHECK constraint keeps a future accidental
-- row insert from silently landing in a kind this table's Go-layer
-- conversion (internal/runtime/agent/context_resolver.go) doesn't handle.
--
-- Column shape mirrors the shared agentcontext.SlotSource sub-structs
-- (CmdSource: run/cwd/timeout; HTTPTextSource/HTTPJSONSource: url/headers/
-- timeout/jsonpath) so contextResolverToSlotSpec's conversion is a
-- straight field-for-field mapping -- see that file's doc comment for the
-- full mapping table. response_format distinguishes the two HTTP resolver
-- kinds (http_text vs. http_json) the same way the pre-port
-- Requirement.ResponseFormat field did.
--
-- UNIQUE(agent_id, slot_name): a slot name is the key the resolved content
-- folds into the boot prompt under (see internal/runtime/agent/prompt.go's
-- appendDynamicContext) -- two resolvers writing the same slot for the same
-- agent would silently race on which one's content wins the final render;
-- the constraint surfaces that as a create-time conflict instead.
--
-- enabled lets an operator pause a resolver (e.g. a flaky endpoint) without
-- deleting its configuration -- ListEnabledAgentContextResolvers only reads
-- enabled=1 rows at boot time.

-- +goose Up
CREATE TABLE IF NOT EXISTS agent_context_resolvers (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agent_profiles(id) ON DELETE CASCADE,
    slot_name       TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('cmd', 'http')),
    run             TEXT NOT NULL DEFAULT '',
    cwd             TEXT NOT NULL DEFAULT '',
    timeout         TEXT NOT NULL DEFAULT '',
    url             TEXT NOT NULL DEFAULT '',
    headers_json    TEXT NOT NULL DEFAULT '{}',
    response_format TEXT NOT NULL DEFAULT 'text' CHECK (response_format IN ('text', 'json')),
    json_path       TEXT NOT NULL DEFAULT '',
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (agent_id, slot_name)
);

CREATE INDEX IF NOT EXISTS idx_agent_context_resolvers_agent
    ON agent_context_resolvers(agent_id, enabled);

-- +goose Down
DROP TABLE IF EXISTS agent_context_resolvers;
