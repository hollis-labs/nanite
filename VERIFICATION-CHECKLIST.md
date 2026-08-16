# CW-20260814-0017 Verification Checklist

## Pre-merge Verification

### Code Quality
- [ ] `go build ./...` succeeds without errors
- [ ] `go test ./...` passes all tests
- [ ] `go vet ./...` reports no issues
- [ ] `go fmt` has been run on all modified files (DONE - verified in worker)

### Database Migration
- [ ] Migration 090 runs cleanly on an existing database
- [ ] `gate_input` column appears in `workflow_run_steps` table
- [ ] Existing workflow runs are unaffected

### State Derivation
- [ ] Create a workflow run with status `waiting_on_gate`
- [ ] Verify `deriveFromWorkflowRun()` returns `TaskStateInputRequired`
- [ ] Verify A2A Task.GetTask() shows `state: input-required`

### Gate Resolution
- [ ] Create a gate step in `waiting_on_gate` status
- [ ] Call `store.ResolveGate(runID, stepID, "test input")`
- [ ] Verify step status changes to `completed`
- [ ] Verify `gate_input` column contains "test input"
- [ ] Verify `output` column contains "Gate resolved: test input"

### Workflow Resume
- [ ] Launch a workflow with a gate step (e.g., `worker-reviewer-gate`)
- [ ] Verify workflow pauses with status `waiting_on_gate`
- [ ] Verify task shows `state: input-required`
- [ ] Call `TaskManager.ProvideTaskInput(ctx, taskID, "approved")`
- [ ] Verify workflow resumes and continues execution
- [ ] Verify task transitions to `working` or `completed`

### GetWaitingGates Query
- [ ] Create multiple steps: waiting gate, completed gate, non-gate
- [ ] Call `store.GetWaitingGates(runID)`
- [ ] Verify only the waiting gate is returned

### Unit Tests
- [ ] `TestDeriveFromWorkflowRun_WaitingOnGate` passes
- [ ] `TestResolveGate_UpdatesStepStatus` passes
- [ ] `TestGetWaitingGates_ReturnsOnlyWaitingGates` passes

### Integration (Manual or via CW-20260814-0016)
- [ ] Submit A2A task targeting a workflow with a gate
- [ ] Wait for task to reach `input-required` state
- [ ] Provide input via A2A API (once CW-20260814-0016 is done)
- [ ] Verify workflow completes successfully
- [ ] Verify final task state is `completed`

## Documentation
- [x] Gate resolution mechanism documented (gate-resolution-mechanism.md)
- [x] Implementation summary created
- [x] Coordination notes with CW-20260814-0016

## Coordination
- [ ] Confirm with CW-20260814-0016 implementer: use `ProvideTaskInput(ctx, taskID, input string)` method signature
- [ ] Verify A2A API handler exposes this method
- [ ] Confirm JSON-RPC method name (suggested: `task.provideInput` or `task.continue`)

## Performance
- [ ] Verify `GetWaitingGates` query uses index on `(workflow_run_id, status)`
- [ ] Verify `ResolveGate` update affects only one row (uses WHERE id = ? AND status = 'waiting_on_gate')

## Edge Cases
- [ ] Providing input to a non-workflow-backed task returns appropriate error
- [ ] Providing input to a task with no waiting gates returns appropriate error
- [ ] Resolving a gate that doesn't exist returns appropriate error
- [ ] Multiple waiting gates: verify first gate is resolved (document behavior)

## Post-merge
- [ ] Update Torque task CW-20260814-0017 to "review" or "done"
- [ ] Announce completion to team
- [ ] Monitor for any runtime issues in production/staging
