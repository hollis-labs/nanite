-- B1 (CW-20260428-0009): session-level mode pointer.
-- Nullable, null = fall back to agent's assigned mode (back-compat).
-- IMPORTANT: no semicolons inside comments.

ALTER TABLE sessions
    ADD COLUMN current_mode_id TEXT REFERENCES modes(id);
