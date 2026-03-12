# Mentat behavior rules (v1.0 — unified)

## Non-negotiables

1. **Volon is the task system of record** for this repo.
2. **Update tasks immediately** when a task changes state.
3. **Record decisions before proceeding** using an ADR in `adr/`.
4. Prefer **reusable primitives** over one-off scripts.
5. **Decompose before executing.** Every prompt with `expected_deliverables` must produce child tasks — one per deliverable — before work begins.
6. **Attempt Hadron first** for automatable work (cloning, building, deploying, linting). If no blueprint exists or it fails, create a backlog task to fix it, then proceed manually.

## Project Tiamat

Mentat is one component of a larger agent framework:

| Component | Role |
|-----------|------|
| **Volon** | Orchestration layer — tasks, workflows, PCC, sprints |
| **Hadron** | Scheduling & blueprint runner — YAML → shell tasks |
| **Mentat** | Meta-agent + chat app — drives the planning and execution loop |
| **Cortex** | Memory layer — context registry, namespace-safe working memory |
| **Nanite** | Notes & documentation — task/note capture, user and agent facing |

All components have GUI + CLI/API/MCP interfaces. Mentat coordinates them.

## Task discipline

- Each prompt becomes:
  - 1 parent triage task (classify + choose workflow)
  - N child execution tasks (one per deliverable or work unit)
  - 1 closeout task (docs + state externalization)
- Child tasks must be created **before** starting work, not after.
- Work happens through child tasks, not the parent.
- Parent task closes only when all children are done.

## Execution discipline

When executing a child task:

1. **Check for applicable Hadron blueprint** first. If one exists, use Hadron MCP tools (`hadron_run_enqueue`) to execute.
2. **If no blueprint exists** for an automatable operation, create a backlog task to add one, then proceed manually.
3. **If work is manual** (reviews, writing, analysis), proceed directly but stay within the child task scope.
4. **Update task status immediately** on every state change (doing → blocked → done).
5. **Write a run log entry** for each completed task.

## Priority defaults

- **If the user does not specify urgency or priority, assume backlog.** Do not auto-promote to sprint tasks unless explicitly told "urgent", "P1", or similar.
- Acknowledge backlog captures briefly so the user knows it was received, then move on.
- Only sprint-promote when the user says so or when a task is blocking active work.

## Deterministic outputs

Every tick must:
- leave a clear "next action" in either:
  - the active Volon task update, or
  - a run log entry
