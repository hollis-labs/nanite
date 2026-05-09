-- 055_rename_tool_names_in_seeded_prompts.sql
-- CW-20260508-0016 (parent CW-20260502-0008 rename arc).
--
-- Reflows the nanite_* → singular-noun tool rename into already-seeded
-- prompt_templates rows. Migrations 027 + 046 + 048 + 049 + 050 + 051 +
-- 053 all carry tool-name strings (nanite_tool_describe, nanite_validate,
-- nanite_remember, nanite_chat_search, nanite_show_card) inside their
-- INSERTed/UPDATEd template bodies. Those migrations have been edited
-- in place to use the new names for fresh installs, but already-deployed
-- databases past 053 have the OLD names baked into their prompt_templates
-- rows (the prior migrations ran INSERT OR IGNORE / UPDATE under their
-- own guard predicates, so a no-op re-run leaves the old strings).
--
-- This migration is the in-place UPDATE that converges those existing
-- installs onto the new tool-name surface. After 055, both fresh and
-- pre-existing databases carry the new names.
--
-- Tool-name mapping (per docs/tool-naming-audit.md Appendix A):
--   nanite_tool_describe → tool_describe
--   nanite_validate      → tool_validate
--   nanite_remember      → lesson_capture
--   nanite_chat_search   → chat_search
--   nanite_show_card     → card_show
--
-- Scope: prompt_templates rows seeded by 027 (chat-role harness),
-- 030 (four compaction-disclosure templates) plus the in-place mutations
-- by 046/048/049/050/051/053 against blt-chat-harness-001.
--
-- Approach: chained REPLACE calls so a single UPDATE per row swaps every
-- old token. Idempotent — REPLACE on an absent substring is a no-op,
-- so re-running this migration is safe.
--
-- IMPORTANT: no semicolons inside comments. Bare UPDATEs (no BEGIN/COMMIT),
-- so the literal SQL terminator inside string literals would be interpreted
-- as a statement boundary by splitSQL — none of the tool names contain
-- semicolons, so this is safe.

-- Chat-role harness template (blt-chat-harness-001).
UPDATE prompt_templates
SET template = REPLACE(
                 REPLACE(
                   REPLACE(
                     REPLACE(
                       REPLACE(template,
                         'nanite_tool_describe', 'tool_describe'),
                       'nanite_validate', 'tool_validate'),
                     'nanite_remember', 'lesson_capture'),
                   'nanite_show_card', 'card_show'),
                 'nanite_chat_search', 'chat_search'),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND (template LIKE '%nanite_tool_describe%'
       OR template LIKE '%nanite_validate%'
       OR template LIKE '%nanite_remember%'
       OR template LIKE '%nanite_show_card%'
       OR template LIKE '%nanite_chat_search%');

-- Compaction disclosure templates (four rows seeded by migration 030).
UPDATE prompt_templates
SET template = REPLACE(template, 'nanite_chat_search', 'chat_search'),
    updated_at = datetime('now')
WHERE id IN (
        'blt-compact-disclose-gen-001',
        'blt-compact-disclose-code-001',
        'blt-compact-disclose-plan-001',
        'blt-compact-disclose-research-001'
      )
  AND template LIKE '%nanite_chat_search%';
