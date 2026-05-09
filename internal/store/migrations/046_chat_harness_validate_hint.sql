-- 046_chat_harness_validate_hint.sql
-- A1 (CW-20260429-0005) + B1 (CW-20260429-0006): adds the discover-and-
-- pre-flight self-introspection bullet to the seeded chat-role-harness
-- prompt template on already-deployed databases. Migration 027 ran
-- INSERT OR IGNORE, so existing installs carry the older text — this
-- migration UPDATEs the row in place to surface the new bullet.
--
-- The bullet advertises two complementary self-tools:
--   - tool_describe(name="<tool>") — A1 (CW-20260429-0005)
--   - tool_validate(tool_name, args)    — B1 (this ticket)
--
-- Both sentences ship together so the prompt remains coherent on every
-- deployment cohort. The anchor is the last bullet of the pre-PR
-- "Tool cadence" section, which is guaranteed to exist on every
-- previously-seeded template (it has been there since migration 027
-- first landed).
--
-- Idempotent. Safe to re-run: the WHERE guard skips rows that already
-- contain the new bullet's tool name, so a second run is a no-op even
-- if a prior run was partially applied.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- **Parallelize independent calls.** If two lookups don''t depend on each other, request them in the same turn.',
        '- **Parallelize independent calls.** If two lookups don''t depend on each other, request them in the same turn.
- **Discover before failing.** If you''re unsure about a tool''s input shape, call tool_describe(name="<tool>") first. It returns the schema plus 1-3 golden examples — cheaper than failing the real call repeatedly. Or call tool_validate(tool_name, args) to pre-flight check args before invoking — it returns structured errors with fix hints.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%- **Parallelize independent calls.** If two lookups don''t depend on each other, request them in the same turn.%'
  AND template NOT LIKE '%tool_describe%';
