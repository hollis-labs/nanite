-- Phase D beta: disable subagent approval gate and enable developer_mode.
-- Migration 019 added subagent_approval_required with DEFAULT 1 which gates
-- every spawn behind a manual approval card. For the dev beta auto-approval
-- is the desired behaviour (ModeInteractive still requires approval explicitly).
--
-- WHERE guard: only apply to rows still at the pre-migration defaults so
-- this migration is safe to re-run on every boot (no schema_migrations table)
-- without overriding a user who explicitly turned these settings back off.
UPDATE user_settings
   SET subagent_approval_required = 0, developer_mode = 1
 WHERE subagent_approval_required = 1;
