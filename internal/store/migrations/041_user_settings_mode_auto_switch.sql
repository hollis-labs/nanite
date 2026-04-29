-- B3 (CW-20260428-0011): user-level preference for auto-applying classifier
-- mode suggestions. Empty string = unset → triggers first-use prompt.
-- Allowed values: "" (unset), "always", "ask", "never".
-- IMPORTANT: no semicolons inside comments.
ALTER TABLE user_settings
    ADD COLUMN mode_auto_switch_pref TEXT NOT NULL DEFAULT '';
