-- 049_chat_harness_card_discovery_hint.sql
-- CW-20260429-0020: strengthen the chat-role-harness prompt to require
-- the agent call tool_describe BEFORE the first card_show
-- invocation per envelope type when it doesn't already have a recent
-- successful example for that type.
--
-- Background (c110 evidence): the agent invented `change`, `trend`,
-- `context` fields on report-card metrics and a top-level `sections`
-- field. None of those exist in the per-type schema. The existing
-- "Discover before failing" bullet (added in migration 046) was too
-- generic — agents skipped it. This migration adds an explicit
-- "Card discovery" bullet immediately after it, mirroring the new
-- bullet seeded by migration 027 for fresh installs.
--
-- The anchor is the full "Discover before failing" bullet text, which
-- is guaranteed to exist post-PR #94 (migration 046). We splice the new
-- "Card discovery" bullet immediately after it so the two form a
-- coherent "discover schemas → discover card examples" pair before the
-- "Recover with awareness" bullet introduced by migration 048.
--
-- Idempotent. Safe to re-run: the WHERE guard skips rows that already
-- contain "Card discovery", so a second run is a no-op.
-- IMPORTANT: no semicolons inside comments.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- **Discover before failing.** If you''re unsure about a tool''s input shape, call tool_describe(name="<tool>") first. It returns the schema plus 1-3 golden examples — cheaper than failing the real call repeatedly. Or call tool_validate(tool_name, args) to pre-flight check args before invoking — it returns structured errors with fix hints.',
        '- **Discover before failing.** If you''re unsure about a tool''s input shape, call tool_describe(name="<tool>") first. It returns the schema plus 1-3 golden examples — cheaper than failing the real call repeatedly. Or call tool_validate(tool_name, args) to pre-flight check args before invoking — it returns structured errors with fix hints.
- **Card discovery.** Before your first card_show call for an envelope type you haven''t successfully rendered this session, call tool_describe(name="card_show") and read the golden examples for the type you want. The per-type schemas use additionalProperties: false, so any field you invent will be rejected. Examples cover report-card, info-card, metric-card, list-card, table-card, timeline-card, diff-card, progress-card, document-viewer, giphy-modal.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%- **Discover before failing.**%'
  AND template NOT LIKE '%Card discovery%';
