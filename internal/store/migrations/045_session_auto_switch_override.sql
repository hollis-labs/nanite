-- F2 (CW-20260429-0002): per-session override for the auto-mode-switch
-- behavior. Nullable BOOLEAN — NULL means inherit the user-level
-- mode_auto_switch_pref (introduced in 041). A non-null value wins over
-- the user pref for that session.
--
-- "1" = force ON for this session (still does NOT bypass first-use prompt).
-- "0" = force OFF for this session (suppress all auto-switches).
-- NULL = inherit user pref.
--
-- IMPORTANT: no semicolons inside comments.
ALTER TABLE sessions
    ADD COLUMN auto_switch_override INTEGER DEFAULT NULL;
