-- 046_chat_harness_validate_hint.sql
-- B1 (CW-20260429-0006): adds the pre-flight self-introspection hint to
-- the seeded chat-role-harness prompt template. Migration 027 ran
-- INSERT OR IGNORE, so already-deployed DBs carry the older text — this
-- migration UPDATEs the row in place to surface the new hint.
--
-- The hint advertises two complementary self-tools:
--   - nanite_describe_tool(tool_name) — A1 (CW-20260429-0005, parallel)
--   - nanite_validate(tool_name, args) — B1 (this ticket)
--
-- Both sentences ship together so the prompt remains coherent
-- regardless of which ticket lands first. A1 may have written its own
-- sentence already (orchestrator will resolve conflicts post-merge).
--
-- Idempotent. Safe to re-run: the UPDATE is a literal substring replace.
-- The conditional protects against the (unlikely) case where the prompt
-- has been re-flowed and no longer contains the anchor string — we
-- silently no-op rather than corrupt the row.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- **Parallelize independent calls.** If two lookups don''t depend on each other, request them in the same turn.',
        '- **Parallelize independent calls.** If two lookups don''t depend on each other, request them in the same turn.
- **Pre-flight unfamiliar contracts.** When a tool''s input shape isn''t obvious from the surface description, call nanite_describe_tool(tool_name) to read its declared input schema before invoking. Or call nanite_validate(tool_name, args) to pre-flight check before invoking. It returns structured errors with fix hints.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%- **Parallelize independent calls.** If two lookups don''t depend on each other, request them in the same turn.%'
  AND template NOT LIKE '%nanite_validate(tool_name, args)%';
