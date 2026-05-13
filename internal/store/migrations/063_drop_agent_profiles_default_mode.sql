-- Migration 063 — Drop agent_profiles.default_mode
--
-- SP-20260512-0009 W5 (CW-20260512-0115) review round 1 cleanup.
--
-- Background
-- ----------
-- The column was added in migration 001 as a "preferred mode" hint for new
-- sessions. W5's investigation (commit 28ba3c4) confirmed that NO production
-- code path consumes the value at session creation, runtime, slot assembly,
-- mode resolution, or dispatch. sessions.current_mode_id is the sole source
-- of truth and the broker re-reads it on every turn. The column is written
-- by agent/convert.go and store/agents.go INSERT/UPDATE and round-tripped
-- by api/agents.go, but never read for behavior.
--
-- Per feedback_no_compat_shims (pre-launch, no consumers) we drop the
-- column in the same PR that codifies the session-attribute contract. No
-- aliases, no deprecation shim, no follow-up ticket — clean break.
--
-- This migration supersedes the Vanta follow-up
-- followups_nanite_cw_0115_delete_default_mode_col captured in W5 round 0.
--
-- Reversibility
-- -------------
-- Irreversible by design. The column data is "default" for every internal
-- profile row and was never load-bearing, so recovering it serves no
-- purpose. If a future feature ever needs a profile-level mode hint, a
-- fresh column with explicit read-side wiring is the correct shape, not a
-- resurrection of this dead-code column.
--
-- Transaction shape
-- -----------------
-- BEGIN/END (not BEGIN/COMMIT) so splitSQL's depth counter closes the
-- block cleanly, matching the convention from migration 008. The
-- migration runner swallows "no such column" on DROP COLUMN (store.go
-- gate around lines 111-122), so re-running on a DB where 063 already
-- landed is a no-op.
--
-- splitSQL note
-- -------------
-- The runner splits the file on every semicolon regardless of context
-- (it is not literal- or comment-aware). Keep that punctuation out of
-- the header prose so that the first real statement remains BEGIN.

BEGIN;

ALTER TABLE agent_profiles DROP COLUMN default_mode;

END;
