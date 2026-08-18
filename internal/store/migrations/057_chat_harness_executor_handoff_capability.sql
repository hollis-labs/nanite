-- +goose Up
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
-- Idempotent + resilient: the WHERE clause LIKE-matches the EXACT substring
-- REPLACE will replace (meta-tools bullet + blank line + ## Style header), so
-- the UPDATE only fires when REPLACE will actually succeed — no perpetual
-- updated_at bumps on cohorts where the needle isn't present (user-edited
-- templates, alternate whitespace, future header reorderings). The NOT LIKE
-- guard skips rows that already carry the new bullet, so re-running the
-- migration on an already-updated row is a true no-op.
--
-- This addresses PR #114 review (comments 3213258308, 3213258313): the
-- previous form guarded only on '%memorise every schema.%' which a row could
-- satisfy without the multi-line REPLACE needle being present, causing
-- silently-no-op UPDATEs that still touched updated_at every boot.
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
  AND template LIKE '%- You have **meta-tools** for discovery (tool_describe), pre-flight validation (tool_validate), and learning capture (lesson_capture). Reach for them when a tool''s contract is unfamiliar or after a call fails — you don''t have to memorise every schema.

## Style%'
  AND template NOT LIKE '%routes you to a specialized executor%';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
