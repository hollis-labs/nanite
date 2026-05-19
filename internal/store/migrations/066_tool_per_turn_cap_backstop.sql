-- CW-20260519-0115: Raise tool_per_turn_cap from 10 → 150 (high backstop).
--
-- Context: Session c267 was blocked at 10 of 13 operator-requested
-- torque_task_create calls — intentional work, not a runaway. The prior
-- default (10) was punishing legitimate bulk work, not catching runaways.
--
-- Runaway is detected by *pattern*, not *count*:
--   - consecutive_fail_cap (3, soft warn) + runaway_fail_cap (10, hard
--     terminate) — chat-loop consecutive tool failures
--   - detectStuckLoop — same-result-repeated detector (blocks after 2
--     identical results from the same tool)
--   - idle_timeout (900s interactive, 300s subagent) — wall-clock
--     no-progress
--
-- 150 is the new high backstop: high enough that legitimate bulk work
-- (50 tasks, 30 file reads, full-project enumeration) won't trip it,
-- low enough that a pathological infinite same-tool loop still has a
-- terminal failsafe -- but the pattern detectors above will always trip
-- first on a real runaway.
--
-- Only rows still at the prior defaults (10 or the 100 interim raised
-- per the c267 audit) are updated, so any explicit operator
-- customization above the floor is preserved. Operators who set 0
-- (explicit no-cap) also keep that setting.

UPDATE user_settings SET tool_per_turn_cap = 150
    WHERE tool_per_turn_cap IN (10, 100);
