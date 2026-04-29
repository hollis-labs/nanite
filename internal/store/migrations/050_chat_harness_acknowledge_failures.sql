-- 050_chat_harness_acknowledge_failures.sql
-- CW-20260429-0021: surface the "acknowledge failed tool calls" rule onto
-- already-deployed databases. Migration 027 ran INSERT OR IGNORE, so existing
-- installs carry the older Style section text — this migration UPDATEs the
-- row in place to append the new bullet at the end of the Style section.
--
-- Why: c110 evidence showed the agent making 4 failing nanite_show_card
-- calls, then a 5th successful one, then responding "Perfect! I''ve created
-- a demo report card..." with no mention of the failed attempts. The
-- structured tool_calls array already records the failures with status
-- error -- the prompt rule asks the agent to surface them.
--
-- The anchor is the "Do not narrate your tool plan unless the user asked
-- for it." bullet -- the last entry in the Style section since 027 first
-- landed, which keeps the new bullet at the bottom of Style. Anchoring on
-- a stable, unique line keeps the substring REPLACE deterministic.
--
-- The example sentence inside the new bullet uses a comma instead of a
-- semicolon ("...not allowed, corrected payload below.") because splitSQL
-- in internal/store/store.go splits the script on the SQL terminator,
-- and a literal one inside a string literal would be interpreted as a
-- statement boundary.
--
-- Idempotent. Safe to re-run: the WHERE guard skips rows that already
-- contain the new bullet phrase, so a second run is a no-op even if a
-- prior run was partially applied.
-- IMPORTANT: no semicolons inside comments.

UPDATE prompt_templates
SET template = REPLACE(
        template,
        '- Do not narrate your tool plan unless the user asked for it.',
        '- Do not narrate your tool plan unless the user asked for it.
- **Acknowledge failed tool calls.** If any tool call this turn returned an error before you found a working approach, mention it in one short sentence in your response. Example: "First attempt rejected for additional properties not allowed, corrected payload below." This keeps the user oriented and surfaces lens activity (retry, repair) so we can diagnose recurring failure modes.'
    ),
    updated_at = datetime('now')
WHERE id = 'blt-chat-harness-001'
  AND template LIKE '%- Do not narrate your tool plan unless the user asked for it.%'
  AND template NOT LIKE '%Acknowledge failed tool calls%';
