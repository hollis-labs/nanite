-- +goose Up
-- Phase 0 item 29 (TASKS/phase-0/29-cut-prompt-templates.md): cut
-- `prompt_templates`/`agent_prompt_templates` in full. The mechanism's one
-- real-world consumer — the `file-default` chat agent's system-prompt
-- composition via `ComposePromptForAgent` — was relocated first: every
-- agent now composes its system prompt from `agent.SystemPrompt` directly
-- (internal/chat/context_client.go, assembleAgentSlotContent), which was
-- already the fallback path every other agent used. The compaction-
-- disclosure content that also lived in this table (migration
-- 030_compaction_disclosure_prompts.sql, 4 mode-branched variants) was
-- relocated to a single hardcoded Go string in internal/chat/context.go
-- (compactionDisclosureTemplate) in the same task, ahead of this migration.
--
-- Drop agent_prompt_templates first (references prompt_templates(id) ON
-- DELETE CASCADE) then prompt_templates itself — dropping in dependency
-- order rather than relying on the FK cascade.
--
-- Do not edit or delete the historical migrations that create/seed/mutate
-- these tables (001, 027, 029, 030, 046, 048, 050, 052, 054, 056, 057, 059)
-- — per this codebase's established convention (063_drop_agent_profiles_
-- default_mode.sql's "no compat shims, clean break" precedent for column
-- drops, applied here at the table level), they remain in the migration
-- history unchanged. Correction to the task brief's framing: this
-- codebase's goose ledger (09-adopt-goose-migrations, legacyMigrationCutoverVersion
-- = 94 in internal/store/store.go) means those historical migrations do NOT
-- "rerun every boot" — goose applies each one exactly once (real execution
-- on a fresh install; marked already-applied without re-execution for a
-- pre-existing database via seedLegacyLedger). That does not change what
-- this migration does: it still runs once, after all of them, in numeric
-- order, and still must not edit or renumber them.
--
-- Go-side removal in the same task (not this migration): internal/store/
-- prompt_templates.go, internal/api/prompt_templates.go (deleted); the 8
-- REST routes (internal/api/api.go); the TemplateStore interface (internal/
-- service/store.go); PromptTemplateIDs capability handling (internal/api/
-- agent_builder.go, internal/api/types.go); the agent_prompt_templates
-- cleanup statement in DeleteAgent (internal/store/agents.go, now
-- unnecessary since the table is gone); the prompt_template Builder
-- (internal/builders/prompt_template_builder.go, deleted) and its
-- builder_start/builder_step tool-description wiring (internal/mcp/
-- self_tools.go); the SeedBuiltinPromptTemplates() boot call (cmd/nanite/
-- main.go); and the frontend surface (PromptTemplateEditor.tsx,
-- PromptDetailView.tsx deleted; AgentProfileManager/AgentDetailView/
-- AgentCapabilitiesPanel/AgentBuilderWizard/SystemPromptsViewer trimmed).

DROP TABLE IF EXISTS agent_prompt_templates;
DROP TABLE IF EXISTS prompt_templates;

-- +goose Down
-- Recreates both tables with their original shape (migrations/001_schema.sql
-- — no ALTER TABLE ever touched either table's columns across its lifetime,
-- so 001's shape is also the final pre-drop shape). Structure only, no seed
-- data: the historical content (the chat-role-harness canonical prompt, the
-- 4 compaction-disclosure variants, the 5 BuiltinPromptTemplates, the
-- planner-role-harness prompt) was produced by a long chain of INSERT/UPDATE
-- migrations (027/029/030/046/048/050/052/054/056/057/059); hand-copying
-- that chain's cumulative result into this Down risks exactly the kind of
-- stale-copy drift this session hit twice already (see TASKS/ESCALATIONS.md).
-- DROP TABLE is inherently lossy for data either way — recreating empty,
-- queryable tables (rather than refusing to recreate them at all) is the
-- honest middle ground, consistent with 109_drop_workspaces_table.sql's Down
-- precedent.

CREATE TABLE IF NOT EXISTS prompt_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    scope TEXT NOT NULL CHECK(scope IN ('system','mode','skill','context')),
    template TEXT NOT NULL,
    variables TEXT NOT NULL DEFAULT '[]',
    priority INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0,
    icon TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_prompt_templates (
    agent_id TEXT NOT NULL,  -- no FK: agents may be file-based
    template_id TEXT NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, template_id)
);
