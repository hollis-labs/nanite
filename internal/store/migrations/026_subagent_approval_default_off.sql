-- Phase D beta: disable subagent approval gate by default.
-- Migration 019 added subagent_approval_required with DEFAULT 1 which gates
-- every spawn behind a manual approval card. For the dev beta auto-approval
-- is the desired behaviour (ModeInteractive still requires approval explicitly).
UPDATE user_settings SET subagent_approval_required = 0, developer_mode = 1;
