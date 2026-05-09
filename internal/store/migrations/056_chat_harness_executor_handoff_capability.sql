-- 056_chat_harness_executor_handoff_capability.sql
-- B5 (CW-20260429-0034, SP-20260429-0001 Phase B) — executor-handoff prompt cue.
--
-- Adds a single Capability-section bullet to the chat-role harness prompt that
-- acknowledges the executor handoff capability landed by B3 (CW-20260429-0032,
-- DispatchExecutor seam + envelope_render pilot at internal/executor/
-- envelope_render). Per the lessons-doc decision rules:
--
--   Rule 1: identity/capability framing belongs in the prompt (not per-task).
--   Rule 6: the addition expands what the agent knows it can do (handcuffs off).
--
-- Migration 027 carries the canonical fresh-install seed, re-flowed from
-- internal/agent/builtin/default.md per the PROMPT-SYNC rule. This file is
-- the in-place UPDATE for databases past 055 (the rename-arc reflow) — they
-- need the new bullet appended into the Capability block.
--
-- Idempotent: REPLACE matches the pre-B5 capability block exactly (the meta-
-- tools bullet alone, after 053's dedup). The LIKE guard skips rows that
-- already carry the new bullet, so re-running this migration is safe.
--
-- IMPORTANT: no semicolons inside comments. Bare UPDATE (no BEGIN/COMMIT),
-- so the literal SQL terminator inside a string literal would be interpreted
-- as a statement boundary by splitSQL. The bullet text contains no
-- semicolons, so this is safe.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- You have **meta-tools** for discovery (tool_describe), pre-flight validation (tool_validate), and learning capture (lesson_capture). Reach for them when a tool''s contract is unfamiliar or after a call fails — you don''t have to memorise every schema.

## Style',
        '- You have **meta-tools** for discovery (tool_describe), pre-flight validation (tool_validate), and learning capture (lesson_capture). Reach for them when a tool''s contract is unfamiliar or after a call fails — you don''t have to memorise every schema.
- For multi-step flows like rendering envelope cards, you don''t need to own the recipe — describe the intent and the harness routes you to a specialized executor.

## Style'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%memorise every schema.%'
  AND template NOT LIKE '%routes you to a specialized executor%';
