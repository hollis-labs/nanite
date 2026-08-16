# Gate Resolution Mechanism Documentation

## Overview

This document describes how paused workflow gate steps are resolved in Nanite's Agent Workflows, specifically in the context of A2A protocol adoption (CW-20260814-0017).

## Gate Resolution Path

### 1. Gate Step Execution

When the built-in workflow engine (`internal/service/workflow_engine.go`) encounters a `gate` step:

1. The step is marked as `running` in `workflow_run_steps`
2. Since `Kind == agentworkflow.StepKindGate`, the engine immediately transitions it to `waiting_on_gate` status
3. The workflow run's overall status becomes `waiting_on_gate`
4. Execution pauses - no downstream steps depending on this gate will execute

```go
// From workflow_engine.go:312-318
if step.Kind == agentworkflow.StepKindGate {
    if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
        WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: "waiting_on_gate",
    }); err != nil {
        return stepRunOutcome{Err: fmt.Errorf("mark waiting_on_gate: %w", err)}
    }
    return stepRunOutcome{Kind: outcomeWaiting, Result: agentworkflow.StepResult{StepID: step.ID, Kind: step.Kind}}
}
```

### 2. A2A Task State Mapping

When a workflow-backed A2A Task's run reaches `waiting_on_gate` status, the Task transitions to `input-required`:

```go
// From a2a_task_manager.go:deriveFromWorkflowRun
switch run.Status {
case "running":
    return a2a.TaskStateWorking
case "waiting_on_gate":
    return a2a.TaskStateInputRequired  // ← The key mapping
case "completed":
    return a2a.TaskStateCompleted
case "failed":
    return a2a.TaskStateFailed
}
```

This makes the gate visible to external callers via the A2A protocol.

### 3. Providing Input to Resolve a Gate

External callers provide input via `TaskManager.ProvideTaskInput()`:

```go
err := taskManager.ProvideTaskInput(ctx, taskID, "user approved: proceed with deployment")
```

This method:

1. Retrieves the waiting gate step(s) for the workflow run
2. Calls `store.ResolveGate()` to:
   - Update the gate step's `gate_input` column with the provided input
   - Transition the step from `waiting_on_gate` → `completed`
   - Store the input in the step's `output` field for visibility
3. Resumes the workflow run by calling `engine.Resume()`

```go
// From store/workflow_runs.go:ResolveGate
UPDATE workflow_run_steps
SET gate_input = ?,
    status = 'completed',
    output = ?,
    completed_at = ?,
    updated_at = ?
WHERE id = ? AND kind = 'gate' AND status = 'waiting_on_gate'
```

### 4. Workflow Resume

After the gate is resolved, the workflow resumes:

1. `TaskManager.resumeWorkflowRun()` calls `engine.Resume()` with the run ID
2. The engine's `Resume()` method:
   - Loads all persisted step results from `workflow_run_steps`
   - Skips steps still in `pending` or `running` status (will be re-executed)
   - Loads completed steps (including the now-resolved gate) into the results map
   - Continues execution from where it left off
3. Downstream steps that depend on the gate can now execute
4. The workflow progresses to completion (or the next gate, if any)

```go
// From workflow_engine.go:Resume
for _, row := range persisted {
    switch row.Status {
    case "pending", "running":
        continue  // Will be re-run
    case "waiting_on_gate":
        waiting[row.StepID] = true  // Still blocked (unless just resolved)
        continue
    }
    sr, decodeErr := stepResultFromRow(row)
    results[row.StepID] = sr  // Include in results for downstream template resolution
}
```

## Database Schema

The `workflow_run_steps.gate_input` column stores the resolution input:

```sql
-- Migration 090_workflow_gate_input.sql
ALTER TABLE workflow_run_steps ADD COLUMN gate_input TEXT NOT NULL DEFAULT '';
```

## Gate Context

Gate steps carry context in their `config` field (a map[string]any). The typical shape:

```yaml
- id: approve
  kind: gate
  depends_on: [worker]
  config:
    description: "Final human approval — worker output passed independent review; approve before it counts as done."
```

The `description` field explains what the gate is asking for. This can be surfaced in the A2A Task status to help external callers provide appropriate input.

## Example: Full Gate Flow

1. **Workflow launch**: Task submitted targeting `worker-reviewer-gate` workflow
2. **Worker step**: Completes successfully
3. **Reviewer verify**: Independent review passes
4. **Gate step**: Workflow pauses at `approve` gate
   - `workflow_runs.status` → `waiting_on_gate`
   - `workflow_run_steps.status` (for `approve`) → `waiting_on_gate`
5. **Task state**: External GetTask returns `state: input-required`
6. **Input provided**: External caller provides `"approved by human operator"`
   - `TaskManager.ProvideTaskInput(ctx, taskID, "approved by human operator")`
7. **Gate resolves**:
   - `workflow_run_steps.gate_input` → `"approved by human operator"`
   - `workflow_run_steps.status` → `completed`
8. **Workflow resumes**: Engine picks up from the resolved gate
9. **Completion**: Any steps depending on the gate now execute; workflow finishes
10. **Final task state**: `state: completed`

## Non-workflow Tasks

Non-workflow-backed tasks (direct instance wakes) never reach `input-required` state. This is expected and correct - gates are a workflow-engine concept, not a general durable-agent feature.

## Coordination with CW-20260814-0016

This ticket (CW-20260814-0017) is independent of CW-20260814-0016 (A2A transport layer), except for coordinating on the exact "continue task" method name/shape. The method name chosen is:

```go
func (tm *TaskManager) ProvideTaskInput(ctx context.Context, taskID, input string) error
```

This will be exposed via the A2A API handler in CW-20260814-0016.

## Testing

See `internal/service/a2a_gate_integration_test.go` for unit tests covering:

- State derivation: `waiting_on_gate` → `input-required`
- Gate resolution: input storage and status transition
- Waiting gates query: filtering by kind and status

Full end-to-end integration testing requires a complete workflow stack (launcher, engine, step executor) and is done via the existing workflow smoke tests.

## References

- Design doc: `apps/nanite/docs/architecture/a2a-protocol-design.md` (Gate ↔ input-required mapping section)
- Agent Workflows design: `docs/architecture/agent-workflows-design.md` (Built-in engine section)
- Workflow engine: `internal/service/workflow_engine.go`
- TaskManager: `internal/service/a2a_task_manager.go`
- Store: `internal/store/workflow_runs.go`
