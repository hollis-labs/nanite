-- +goose Up
-- 048_chat_harness_repair_note.sql
-- C2 (CW-20260429-0008): teach the chat-role-harness prompt about the
-- repair_note that arrives on a tool result whose args were reshaped by
-- the LLM-augmented auto-repair pipeline.
--
-- The agent should:
--   1. Read the tool result inside `result` / `result_text` as the
--      authoritative response (the call did succeed).
--   2. Read repair_note.lesson_hint to learn what the harness reshaped
--      so future calls don't repeat the same mistake.
--   3. When the lesson_capture self-tool is available (D1 ships
--      separately), persist the lesson_hint to durable memory.
--
-- The sentence is anchored after the "discover before failing" hint
-- introduced by 046. That anchor was chosen because it forms a
-- coherent "discover → validate → recover" cluster of harness
-- self-tooling guidance.
--
-- Idempotent. Safe to re-run: the UPDATE is a literal substring replace
-- with a "NOT LIKE" guard so a second run is a no-op.
-- IMPORTANT: no semicolons inside comments.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- **Discover before failing.** If you''re unsure about a tool''s input shape, call tool_describe(name="<tool>") first. It returns the schema plus 1-3 golden examples — cheaper than failing the real call repeatedly. Or call tool_validate(tool_name, args) to pre-flight check args before invoking — it returns structured errors with fix hints.',
        '- **Discover before failing.** If you''re unsure about a tool''s input shape, call tool_describe(name="<tool>") first. It returns the schema plus 1-3 golden examples — cheaper than failing the real call repeatedly. Or call tool_validate(tool_name, args) to pre-flight check args before invoking — it returns structured errors with fix hints.
- **Recover with awareness.** If a tool result carries a `repair_note`, your input was reshaped by the auto-repair pipeline so the call could succeed. Read the actual response from `result` / `result_text` as authoritative. Then read `repair_note.lesson_hint` — it is a one-sentence note describing what the harness fixed. Call `lesson_capture` with the lesson when that tool is available so future calls avoid the same mistake.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%- **Discover before failing.**%'
  AND template NOT LIKE '%repair_note%';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
