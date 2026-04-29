-- C2 (CW-20260429-0008): user-level preference for the LLM-augmented
-- auto-repair pipeline. Nullable TEXT — empty string is the sentinel
-- "unset" value, treated as "always" by the runtime gate.
--
-- Allowed values:
--   ""       — unset, behaves like "always" (default policy).
--   "always" — auto-repair recoverable tool errors when wired and the
--              env var NANITE_AUTO_REPAIR is not set to a falsy value.
--   "never"  — bypass the repair pipeline entirely. The C1 structured
--              error envelope is surfaced directly to the agent.
--
-- The env var NANITE_AUTO_REPAIR is an operator-level kill switch and
-- takes precedence over this column when both disagree.
--
-- IMPORTANT: no semicolons inside comments.
ALTER TABLE user_settings
    ADD COLUMN auto_repair_pref TEXT NOT NULL DEFAULT '';
