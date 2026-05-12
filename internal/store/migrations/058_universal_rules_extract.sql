-- 058_universal_rules_extract.sql
-- CW-20260512-0100 (SP-20260512-0007 nanite agent fabrication / trust cleanup).
--
-- Demotes the chat-role-harness prompt template (blt-chat-harness-001) to
-- role-identity + role-specific overrides only, after the universal grounding
-- / refusal / honesty rules were extracted into the auto-injected universal-
-- rules layer at internal/chat/universal_rules.go (CW-20260512-0100).
--
-- Also retunes the worker agent_profiles row to remove execute-or-bust
-- framing — the universal Refusal section in the auto-injected preamble
-- now covers the failure-affordance gap that the previous worker prompt
-- left open (deep-dive §4: refusal affordance gap, §6: subagent task
-- framing). c160 fabrication chain regression target.
--
-- ## Architectural background
--
-- Before this migration: universal rules (refusal, evidence-bias, "use what
-- tools return", "ask before fabricating", honesty when failing) lived
-- inside the file-default chat-role-harness template. Eight other agent
-- profiles (worker 464 chars, planner 138 char stub, researcher empty +
-- analyst/backend/background-job/file-backend/fragments-engine all empty)
-- had no template assignment and skipped those rules entirely. The c160
-- fabrication chain (researcher subagent fabricating 8.1KB of analysis
-- when it had no codebase access) was the predictable consequence.
--
-- After this migration: universal rules live in
-- internal/chat/universal_rules.go and are prepended to SlotSystem for every
-- agent in chat.ContextClient.AssembleSlotSources. Agent profile bodies
-- carry role-identity + role-specific overrides only. New profiles inherit
-- the universal layer automatically — no template assignment required.
-- This supersedes deep-dive R1+R2 (per-profile template assignment) per the
-- user's architectural direction.
--
-- ## PROMPT-SYNC chain
--
-- This migration is the in-place UPDATE for fresh installs (past 056) and
-- already-deployed databases alike. Migration 027 has been intentionally
-- LEFT at the historic body so the sibling chain (046/048/049/050/051/053/
-- 055/056) remains coherent on fresh installs — those migrations were
-- authored against the 027 body. After 056 the body is well-defined: the
-- 051 slim body + the 053 dedup + the 056 executor-handoff bullet. This
-- migration UPDATEs that known terminal body to the demoted form.
--
-- internal/agent/builtin/default.md is the canonical markdown source and
-- has been demoted in parallel (PROMPT-SYNC: CW-20260427-0014 +
-- CW-20260512-0100). When the demoted body changes, edit default.md AND
-- the SQL strings below in lockstep.
--
-- ## Idempotency
--
-- Two surgical REPLACE operations + one full UPDATE, each guarded by a
-- WHERE clause that matches only the pre-demotion body. A second run is
-- a true no-op:
--
--   1) Strip the "## Grounding" section (4 bullets — the universal core
--      that moved to the auto-injected layer).
--   2) Strip the "- When you fail, acknowledge honestly..." bullet from
--      the Judgment section (now covered by universal Refusal).
--   3) UPDATE the worker agent_profiles row to remove execute-or-bust
--      framing.
--
-- IMPORTANT: no semicolons inside comments. Bare UPDATEs (no BEGIN/COMMIT)
-- because splitSQL is string-literal-unaware and would interpret the
-- literal SQL terminator inside a string as a statement boundary. The new
-- template body deliberately uses periods and em-dashes instead of
-- semicolons.

-- 1) Strip the Grounding section from the chat-role-harness body. The
--    universal Grounding rules now ship from internal/chat/universal_rules.go
--    via the auto-injected preamble. The anchor REPLACE is on the full
--    "## Grounding" header through the trailing blank line before
--    "## Capability" so the substitution is deterministic.
UPDATE prompt_templates
SET template = REPLACE(
        template,
        'You are a helpful AI assistant embedded in the Nanite chat harness. You have access to tools — file system, HTTP, math, MCP servers, and Nanite''s own self-tools. Your job is to use them to help the user, and to be honest about what''s real vs synthesized.

## Grounding

- **Use what tools return.** When a tool returns data, that''s the source of truth. Don''t reword IDs, extrapolate list rows past what was retrieved, or relabel filtered subsets. If you need data you don''t have, call a tool to get it.
- **Distinguish real from synthesized.** When the user invites a demo, sketch, or test, you can synthesize sample data — but say so. When the user asks a real question, ground your answer in tool output.
- **Ask before fabricating.** When the data is incomplete, conflicting, or too sparse for a confident answer, one short clarifying question beats a polished reply over thin data.
- **Count, don''t estimate.** When you have the data, count it. If a tool returned a paginated result and you need a total, paginate. Estimates are appropriate only when you genuinely can''t or shouldn''t count — and say "estimate" when you do.

## Capability',
        'You are a helpful AI assistant embedded in the Nanite chat harness. You have access to tools — file system, HTTP, math, MCP servers, and Nanite''s own self-tools. Your job is to use them to help the user.

## Capability'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%## Grounding%'
  AND template LIKE '%honest about what''s real vs synthesized%';

-- 2) Strip the "When you fail, acknowledge honestly" bullet from the
--    Judgment section. Universal Refusal covers acknowledge-honesty. The
--    "destructive or externally-visible actions" bullet ALSO moved to
--    universal Verification, so strip both. Single REPLACE that removes
--    both trailing bullets so Judgment is left with just the scope-clarity
--    bullet — the only role-specific Judgment rule.
UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- Ask one pointed question before a long tool chain when the scope is unclear.
- For destructive or externally-visible actions (deletes, pushes, posts, emails), confirm with the user first.
- When you fail, acknowledge honestly. Don''t paper over with confident framing.',
        '- Ask one pointed question before a long tool chain when the scope is unclear.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%When you fail, acknowledge honestly%';

-- 3) Demote the worker agent_profiles row. Remove execute-or-bust framing
--    that lacked a failure-affordance escape valve. The universal Refusal
--    section in the auto-injected preamble now governs failure behavior.
--    Worker keeps role identity + scope discipline only.
--
--    Idempotent guard: skip rows whose system_prompt no longer contains the
--    pre-CW-20260512-0100 phrase "Your job is to execute, not converse".
UPDATE agent_profiles
SET system_prompt = 'You are a Worker agent in the Nanite harness, dispatched by a parent agent to handle a specific scoped task — writing code, running tools, or completing well-bounded work. You have full tool access.

Stay within the assigned scope. Do not initiate new conversations or expand the task beyond what the parent dispatched. When the task is done, return the result. When you cannot complete it with the tools and paths available, return an explicit failure — the universal Refusal rules govern this (your reply is treated as authoritative by the parent).',
    updated_at = datetime('now')
WHERE id = 'blt-worker-001'
  AND system_prompt LIKE '%Your job is to execute, not converse%';
