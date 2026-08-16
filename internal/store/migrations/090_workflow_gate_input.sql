-- 090_workflow_gate_input.sql
-- Add gate_input column to workflow_run_steps for CW-20260814-0017.
--
-- When a workflow gate step is resolved externally (via A2A task input), the
-- resolution input is stored here. The workflow engine reads this when
-- Resume() is called to unblock the gate and make the input available to
-- downstream steps via template resolution ({{ steps.gate_id.gate_input }}).

ALTER TABLE workflow_run_steps ADD COLUMN gate_input TEXT NOT NULL DEFAULT '';
