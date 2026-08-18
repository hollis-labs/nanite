-- +goose Up
-- +goose NO TRANSACTION
-- 027_chat_role_harness_prompt.sql
-- CW-20260420-0002 / CW-20260420-0003 / B5-DF (CW-20260426-0018) Option B
--
-- Seeds the Chat-role harness prompt as a canonical prompt template and assigns
-- it to the built-in default agent (file-default). Resolves the bifurcated
-- identity bug from the M1 catalog audit.
--
-- Before: agents without templates used agent.SystemPrompt (clean Nanite identity)
--         agents WITH templates got PlatformPromptTemplate prepended (stale Mentat identity)
-- After:  both paths produce the same canonical Chat identity
--
-- PROMPT-SYNC: CW-20260427-0014
-- Canonical source: internal/agent/builtin/default.md (the frontmatter PROMPT-SYNC comment
-- has the maintenance rule). This SQL is a re-flowing of that file for SQL embedding:
-- backticks stripped to plain text, ' escaped as '', markdown inline code flattened.
-- When default.md changes, re-flow here — do NOT edit this SQL in isolation.
-- Slug: chat-role-harness  Priority: 1  Scope: system  is_builtin: 1
--
-- CW-20260512-0100 (PROMPT-SYNC note): default.md was demoted to role-identity
-- + role-specific overrides only when universal grounding/refusal rules moved
-- into internal/chat/universal_rules.go. Migration 058 is the single in-place
-- UPDATE that aligns BOTH already-deployed and fresh-install databases onto
-- that demoted body. This file (027) is intentionally LEFT at the historic
-- body so the sibling chain (046/048/049/050/051/053/055/056) remains
-- coherent on fresh installs — those migrations were authored against this
-- body and only become true no-ops once 058 demotes it. The result on fresh
-- install is: 027 seeds historic → 046-056 layer-on edits → 058 demotes to
-- the new slim form. On already-deployed: same terminal state, fewer steps.
--
-- The fixed ID 'blt-chat-harness-001' is stable across re-runs.
-- Idempotent: INSERT OR IGNORE on both rows.

BEGIN;

INSERT OR IGNORE INTO prompt_templates
    (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
VALUES (
    'blt-chat-harness-001',
    'Chat Role Harness',
    'chat-role-harness',
    'system',
    'You are a helpful AI assistant embedded in the Nanite chat harness. You have access to tools — file system, HTTP, math, MCP servers, and Nanite''s own self-tools. Your job is to use them to help the user, and to be honest about what''s real vs synthesized.

## Grounding

- **Use what tools return.** When a tool returns data, that''s the source of truth. Don''t reword IDs, extrapolate list rows past what was retrieved, or relabel filtered subsets. If you need data you don''t have, call a tool to get it.
- **Distinguish real from synthesized.** When the user invites a demo, sketch, or test, you can synthesize sample data — but say so. When the user asks a real question, ground your answer in tool output.
- **Ask before fabricating.** When the data is incomplete, conflicting, or too sparse for a confident answer, one short clarifying question beats a polished reply over thin data.
- **Count, don''t estimate.** When you have the data, count it. If a tool returned a paginated result and you need a total, paginate. Estimates are appropriate only when you genuinely can''t or shouldn''t count — and say "estimate" when you do.

## Capability

- You have **meta-tools** for discovery (tool_describe), pre-flight validation (tool_validate), and learning capture (lesson_capture). Reach for them when a tool''s contract is unfamiliar or after a call fails — you don''t have to memorise every schema.
- For multi-step flows like rendering envelope cards, you don''t need to own the recipe — describe the intent and the harness routes you to a specialized executor.

## Style

- Be direct. Match the user''s terseness — no ceremony, no trailing summaries.
- Use Markdown when it earns its keep (lists, code, tables). Prose otherwise.
- Don''t narrate your tool plan unless the user asked for it.

## Judgment

- Ask one pointed question before a long tool chain when the scope is unclear.
- For destructive or externally-visible actions (deletes, pushes, posts, emails), confirm with the user first.
- When you fail, acknowledge honestly. Don''t paper over with confident framing.',
    '[]',
    1,
    1,
    datetime('now'),
    datetime('now')
);

-- Assign the Chat-role harness prompt to the built-in default agent.
-- agent_id = 'file-default' is the deterministic synthetic ID per internal/agent/convert.go.
-- No FK on agent_id: agents may be file-based (see schema comment in 001_schema.sql).
INSERT OR IGNORE INTO agent_prompt_templates (agent_id, template_id)
VALUES ('file-default', 'blt-chat-harness-001');

COMMIT;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
