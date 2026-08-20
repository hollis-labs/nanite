# Teams — implementation

Implements `docs/engineering/architecture/15-teams.md`, the design produced by a dedicated architecture-alignment session (2026-08-20, operator-signed-off) that reviewed an external proposal against Nanite's real code before proposing anything. That session changed **no code or schema** — design only, and its own "Status" section explicitly deferred implementation, "not yet filed." This folder is that follow-up planning pass's output.

**Not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence.** A sibling to `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, and `TASKS/scheduling/`, not nested inside any of them — kept in its own top-level `TASKS/` subfolder for the same reason those are: this work wasn't part of the original plan.

## Read before starting any task here

1. `docs/engineering/architecture/15-teams.md` — the full design: the core split (Agent Workflow = execution, Team = organization, TeamRun = a WorkflowRun), Decision 1 (gates are literal `StepKindGate` steps, no new hard-block mechanism) and Decision 2 (TeamRun IS a WorkflowRun, requiring one real new step kind, `StepKindFlex`), the SME illustrative example, the "New mechanism vs. reuse" ledger, the Guardrail diagram, and the explicit "What this session did not decide" list.
2. **This batch's task files each carry real, load-bearing corrections to the design doc, found by this planning session's own research against the actual codebase — read the specific task's Context before assuming the design doc's prose is literally accurate.** In short, so you don't have to rediscover them: `agent_parent_dispatch_allowlist` is a column (JSON role-slug array on `agent_profiles`), not a table, and is advisory-only (rendered into an LLM-facing tool description), not enforced (task `04`); `agent_profiles.durable` is an unrelated eject-survival flag, not a signal for whether to wake an existing durable identity (task `08`); the real messaging self-tool is `message_send`, not `send_message` (task `09`); `workflow_run_steps.kind` has a hardcoded DB CHECK that blocks `StepKindFlex` without a real migration (task `03`); "Slot" already has an established, unrelated, load-bearing meaning in `internal/context/slot.go` (task `01`).
3. `docs/engineering/GLOSSARY.md` — check before introducing any new name, per this repo's standing discipline; task `01` adds Team/Team Slot/TeamRun entries, including the Slot-collision disambiguation above.
4. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer discipline, and escalation rules every task file below follows.

## What this batch does NOT do

Per the design doc's own explicit "What this session did not decide" list, and this planning session's own scoping calls — do not expand any task below to cover these without a fresh operator conversation:

- **Mid-run elastic slot growth** (spawning e.g. a second `engineer` member after a TeamRun has already started). The design doc left open whether this needs a dedicated Team-level self-tool or reuses `task_execute`'s existing dispatch surface. This planning session's research found a concrete, current blocker for the latter: `task_execute` is hard-capped at recursion depth 0 (only a root/non-subagent session may call it), and a Team orchestrator slot triggering this growth is itself very often a dispatched, non-root session. `08` resolves `min`/`max` members eagerly at launch only — no mid-run growth in this batch.
- **The final authority verb set beyond `may_spawn`/`may_message`/`may_not_review`** (`may_delegate`/`may_approve`/`may_signal`, or per-slot tool-surface restriction) — `04` builds a schema that absorbs a future verb as a CHECK widening, not a new mechanism, but doesn't add any verb beyond the three the design doc names.
- **A final, locked default for multi-member slot addressing** (`@engineer` when a slot resolves to 3+ members) — `09` picks a working default (broadcast to all active members) and flags it explicitly as provisional, per the design doc's own "no syntax or default is chosen here, deliberately" framing.
- **Whether Team (organization) and Workflow (execution shape) eventually become two independently-authored, combinable definitions** — `01`/`07` keep the phase sequence as its own addressable sub-structure (per the design doc's explicit forward-compat instruction) but do not build the actual split.
- **Exact schema DDL beyond what each task file specifies** — the design doc is architecture-level agreement; illustrative shapes in each task file are the planning session's own concrete proposal, and workers document their own call where they deviate, same discipline as the scheduling batch.

## Task sequence

**Phase 1 — Schema & storage foundation.** New tables/columns and Go types only — no runtime behavior. All five tasks touch `internal/store/migrations/`; real cross-batch numbering-collision risk (same pattern the scheduling/harness-reactive-self-tools batches hit each other with) — whichever task is dispatched, or whichever other in-flight batch lands first, must re-list the migrations directory and renumber. Latest migration at this planning session's authoring time (2026-08-20) is `127_schedule_runs_and_retry_policy.sql`; this batch provisionally claims `128` onward in the order below.

| Task | Depends on |
|---|---|
| `01-team-definition-schema.md` | none |
| `02-team-run-members-table.md` | none directly (parallel-safe with `01`/`03`/`04`/`05` — different table) |
| `03-stepkindflex-schema.md` | none directly (parallel-safe — different file/table surface) |
| `04-team-authority-schema.md` | `01` (Team Slot vocabulary) |
| `05-agent-reflexes-run-scoping.md` | none directly (parallel-safe — independent column) |

**Phase 2 — Runtime engine.** The compiler and launcher stitch together every Phase 1 primitive — strict-ish order, each task is a real prerequisite for the next.

| Task | Depends on |
|---|---|
| `06-stepkindflex-executor.md` | `03` |
| `07-team-compiler.md` | `01`, `02`, `03`, `04`, `05`, `06` |
| `08-team-run-launcher.md` | `07`, `04` |

**Phase 3 — Routing & messaging.**

| Task | Depends on |
|---|---|
| `09-team-routing.md` | `05`, `08` |

**Phase 4 — Definition & launch API surface.**

| Task | Depends on |
|---|---|
| `10-team-crud-api.md` | `01` |
| `11-team-run-launch-api.md` | `08`, `10` |

**Two stress tests the design doc's own "Validating this design" section calls "likely the messiest runtime edges of the whole design" and requires concrete answers for, not just a passing mention** — each is a required, tested "Done means" item on its owning task, not an open design question left for a worker to notice or skip: the **phase-closure race** (a flex step's exit trigger fires while other active members are still working) belongs to `06`; **routing-target resolution failure** (a message routes to an unavailable/failed slot member) belongs to `09`.

See `TASKS/INDEX.md`'s own new section for status tracking as these land.
