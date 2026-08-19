-- +goose Up
-- +goose NO TRANSACTION
-- 029_planner_role_prompt_stub.sql
-- CW-20260426-0016 (Arc M2 — special agent pattern catalog)
--
-- Seeds a placeholder Planner role-prompt template to reserve the
-- 'planner-role-harness' slug for M3 reflex dispatch. Full Planner identity
-- is defined in Phase 6 (cognition arc). This stub ensures the slug exists
-- and AssignRole can target the 'planner' agent profile without surface-area
-- changes when Phase 6 lands.
--
-- Deliberately NOT assigned to any agent (no agent_prompt_templates row):
-- no agent owns the Planner identity yet — leaving it unassigned is correct
-- for a reservation-only migration.
--
-- Idempotent: INSERT OR IGNORE on both the template row and (if added later)
-- any assignment row. Safe to re-run.

BEGIN;

INSERT OR IGNORE INTO prompt_templates
    (id, name, slug, scope, template, variables, priority, is_builtin, created_at, updated_at)
VALUES (
    'blt-planner-harness-001',
    'Planner Role Harness (stub)',
    'planner-role-harness',
    'system',
    'Planner role — identity TBD. Phase 6 cognition arc will define authoritative behavior. This stub reserves the slug for M3 reflex dispatch.',
    '[]',
    1,
    1,
    datetime('now'),
    datetime('now')
);

COMMIT;

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
