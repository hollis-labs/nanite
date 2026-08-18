-- +goose Up
-- 090_workflow_gate_input.sql
-- Add gate_input column to workflow_run_steps for CW-20260814-0017.
--
-- When a workflow gate step is resolved externally (via A2A task input), the
-- resolution input is stored here. The workflow engine reads this when
-- Resume() is called to unblock the gate and make the input available to
-- downstream steps via template resolution ({{ steps.gate_id.gate_input }}).

ALTER TABLE workflow_run_steps ADD COLUMN gate_input TEXT NOT NULL DEFAULT '';

-- +goose Down
-- No down migration: this file predates goose adoption (see
-- docs/engineering/architecture/05-storage-and-migrations.md, "Migrations:
-- adopting a real ledger"). Every pre-cutover migration ships a
-- deliberately empty Down section rather than a hand-derived rollback --
-- reconstructing the exact pre-migration schema/data shape for 94 files
-- retroactively isn't worth doing when the historical state it would
-- recreate has no operational value. New migrations going forward are
-- expected to carry a real, tested Down.
