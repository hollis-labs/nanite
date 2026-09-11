# Example Workflow Definitions

A real, loadable `WorkflowDefinition` catalog `agentworkflow.Registry` can
load at startup — the Worker → Reviewer → Gate shape as a shipped definition
rather than a Go test fixture: a Worker step produces work, an independent
Reviewer checks it, and a Gate decides whether it proceeds.

## Layout

```
examples/workflow-definitions/
  worker-reviewer-gate.yaml   — Worker (llm) → verify:agent Reviewer → gate approval
```

## Wiring it in

Set `workflow_definitions_path` in your Nanite config (user-level
`~/.config/nanite/config.yaml` or project-level `./nanite.yaml`):

```yaml
workflow_definitions_path: ~/dev/hollis-labs/apps/nanite/examples/workflow-definitions
```

Restart `nanite-api`. `agentworkflow.LoadRegistryDir` reads every `*.yaml`
file directly under this directory (no recursion) and indexes each by its
`name` field — `worker-reviewer-gate` becomes callable via the
`workflow_run` self-tool:

```
workflow_run(workflow_name: "worker-reviewer-gate", params: {task: "<what the worker should do>"})
```

When unset (the default), the registry stays empty and `workflow_run`
calls fail with "unknown workflow" — no other behavior changes ("no
catalog → no behavior change", matching `boot_profile_catalog_path`).

## What the example exercises

- A `kind: llm` Worker step with a `verify: {mode: agent}` modifier — the
  shared host runs the worker's turn, then dispatches an independent,
  capability-restricted reviewer turn (different prompt, different
  narrow tool surface, same underlying `ExecuteLLMStep` primitive) and
  parses its `PASS`/`FAIL` verdict. A failed verify marks the worker step
  failed, same as any other step-level failure.
- A `kind: gate` step depending on the worker — the engine marks it
  `waiting_on_gate` and the run returns `Status: waiting_on_gate`
  immediately. This is a durable pause, not a stall: approval resolves the
  persisted wait and resumes the exact plan and StepKind catalog that launched
  the run.

## Running the smoke

`go test ./internal/service -run TestWorkflowDefinitionsSharedSmoke`. The
smoke test loads this exact directory via `agentworkflow.LoadRegistryDir`
(a real file, not an in-memory literal), then launches
`worker-reviewer-gate` end to end through the real
`WorkflowLauncher`/shared `go-workflow` host/`WorkflowStepExecutor` stack —
a scripted LLM provider stands in for the worker and reviewer turns (no
live Anthropic credentials needed) — and asserts the run reaches
`Status: waiting_on_gate` with the worker step's verify recorded as passed and
the gate step persisted as `waiting_on_gate`. The authenticated approval API
can then resolve that durable wait and resume the same immutable plan.
