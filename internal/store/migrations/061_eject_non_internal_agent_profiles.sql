-- 061_eject_non_internal_agent_profiles.sql
-- CW-20260512-0112 (SP-20260512-0009 Wave 2 — eject non-internal agent profiles,
-- clean slate).
--
-- ## Purpose
--
-- Pre-launch clean-slate: with Wave 1 (migration 060) establishing the four
-- canonical internal profiles (default, worker, planner, hint-selector) as
-- the file source of truth (source='internal'), every other row in
-- agent_profiles is now ambiguous (vestigial auto-discovery stubs, prior
-- iterations, user-created profiles). The orchestrator + user have locked
-- the literal interpretation: wipe everything that does NOT carry the
-- file-SOT marker.
--
-- Project-managed file-backed profiles (source='project', sourced from
-- .nanite/agents/*.md) are now durable config source-of-truth and MUST
-- survive reboots. Empty auto-stubs (source='auto') and legacy/user-authored
-- ambiguous rows still rehydrate or re-create as needed. This is the
-- updated posture
-- (feedback_no_compat_shims, feedback_plan_is_best_effort).
--
-- ## Dependency chain
--
-- Migration 060 MUST run before this migration. 060 either flips the four
-- canonical rows from source='builtin' → source='internal' (on already-
-- deployed DBs) or INSERT-OR-IGNORE seeds them with source='internal' (on
-- fresh-install DBs). Without 060's flip/seed, this migration's DELETE
-- would wipe the four canonical rows on first boot of a previously-deployed
-- DB. Migration order (060 → 061) enforces this.
--
-- ## Reversibility
--
-- Irreversible by design. Deleted rows are not preserved (no JSON dump, no
-- archive table). Pre-launch clean slate per feedback_no_compat_shims —
-- the user's UI re-create path is the recovery story. The down-migration
-- body below is therefore a no-op comment for documentation only — the
-- migration runner does not invoke down-migrations.
--
-- ## Idempotency
--
-- The DELETE is naturally idempotent — subsequent boots find zero non-
-- keep-list rows and the statement is a no-op. Safe to re-run on every
-- migration pass.

BEGIN;

DELETE FROM agent_profiles
 WHERE source NOT IN ('internal', 'project');

COMMIT;

-- Down-migration: irreversible. Wave 2 eject is a pre-launch clean-slate
-- operation — deleted rows are not preserved. See CW-20260512-0112.
