# A2A Protocol Adoption — Design

Status: design, ready for implementation. Scope: **Nanite only** — not portfolio-wide in this pass. Implements Torque task `CW-20260813-0002`.

Grounding research (Hadron's existing partial implementation, `go-messaging`'s current state, Nanite's actual wake-trigger mechanism, Tether's ACP precedent) was done before this doc was written — see the conversation history / Torque task comments for the full findings. Key facts that shape this design are cited inline below with file:line references, verified against real code, not assumed from the spec alone.

## What this is for

Nanite's durable agents have no way to wake in response to an inbound message from another agent, and no standard way for another agent (in this portfolio or outside it) to discover what Nanite can do or address it correctly. `go-messaging` gives the portfolio a durable envelope/delivery substrate, but no capability-discovery model and no task-lifecycle model — every consuming app either does without those concepts or (per Hadron) starts building an ad hoc, spec-partial version of them independently.

A2A (Agent2Agent, a2a-protocol.org, Linux Foundation, v1.0) is the open standard for exactly this: Agent Card discovery + a real Task lifecycle + push notifications for disconnected callers. This adopts it for Nanite's inbound surface — hosting an Agent Card, accepting Task submissions, reporting Task state — layered on top of the existing durable delivery substrate, not replacing it.

## The one principle everything else follows from

**A2A is a protocol adapter, not a new execution substrate.** Every Task Nanite serves is fulfilled by exactly one of the two execution paths Nanite already has: a Workflow run (the Agent Workflows pillar, `docs/architecture/agent-workflows-design.md`) or a durable-agent wake. The `a2a_tasks` record is bookkeeping and protocol translation — external-facing identity, state derivation, push notification config — never a third parallel execution mechanism with its own independent run loop.

This mirrors the discipline Agent Workflows established for `StepExecutor`: don't build a second thing that does what an existing thing already does correctly. A2A's job is to translate an external Task request into a call against `WorkflowLauncher` or `DurableWake.Wake()`, and to translate that execution's real status back into a spec-shaped `TaskState` — never to independently decide what "done" or "failed" means.

## Why this reshapes the motivating case more than expected

The original framing (see the research thread) assumed A2A task submission would *replace* an existing bespoke message-triggered wake. It doesn't — there isn't one. Confirmed via `internal/service/durable_wake.go` and `cmd/nanite/main.go:1006-1073`: nothing inside the Nanite binary calls `Wake()`/`RunDue()` on a timer or in response to an inbound message. The only wake triggers wired today are a manual HTTP call (`POST /api/durable-agents/{id}/wake`) and an externally-scheduled poll (`POST /api/durable-agent-wake/run-due`, called by something outside the repo). `DurableAgentWakeExternalMessage` (`durable_agents.go:30`) is a defined-but-dark enum value — the only other reference to it anywhere in the codebase is a UI dropdown label (`internal/api/frontend_readiness.go:466`).

So this work is building the *first* message-driven wake trigger, A2A-shaped from the start — not migrating one. No legacy shape to preserve, but also no existing plumbing to lean on beyond the wake mechanism's other pieces, which are real and solid: `wakeSkipReason` (`durable_wake.go:270-288`, guards against redundant/invalid wakes) and `durableAgentLaunchPolicyFor` (`durable_agents.go:639`, maps `LifecycleClass` to session policy) are both live, tested, and need no changes.

**Second finding that changes scope:** `DurableAgentWakePayload.Prompt` (`durable_agents.go:33-38`) is captured on every wake call but never becomes the launched session's first turn. It only feeds template-placeholder substitution in the recipe UI (`durable_agent_recipes.go:940-941`). The cold-boot system-prompt path (`chat_boot_drive.go`) is a completely separate mechanism (`BootPrompt`/`bootPromptOverride` from a boot profile's `LaunchSpec`) with zero connection to `WakePayload.Prompt` — confirmed via grep, no references to `Wake` anywhere in `chat_boot_drive.go`. An A2A task's message content has to actually reach the woken session as real turn content, or waking on it accomplishes nothing. Fixing this is in scope here (see "Wake integration" below), not a separate pre-req ticket — it's load-bearing for the whole point of this work.

## Package shape

New package `internal/a2a/`, structured the way Tether's ACP adapter was deliberately structured (`apps/tether/internal/acpadapter/`, per ADR-0034: "designed for portfolio extraction... zero imports of Mux-internal types"): the wire-protocol types carry no Nanite-internal type imports, so this package can be promoted to a shared `libs/go-a2a` later, once a second consumer (Hadron migrating off its own partial implementation, or another app) validates the shape. **Not extracting to `libs/` in this pass** — same reasoning as Agent Workflows' external-engine mechanism: build it once, validate it against a real need, don't design a portfolio abstraction speculatively.

```
internal/a2a/
  types.go       — AgentCard, Skill, Task, TaskState, PushNotificationConfig,
                   TaskSubmitRequest/Response — pure wire types, zero nanite-internal imports
  jsonrpc.go     — JSON-RPC 2.0 envelope (request/response/error shapes)
  address.go     — canonical msg:// Address derivation (wraps go-messaging's Address/ParseURN)
```

The real implementation — routing a submitted Task to a Workflow launch or a durable-agent wake, deriving `TaskState` from real execution status, persisting `a2a_tasks` rows — lives in `internal/service` (a `internal/service/a2a_*.go` area), as the fourth thing to follow the pattern already proven three times over (`StepExecutor`, `harness_v1`'s handlers, `nanite chat`'s CLI client are all thin wrappers over `internal/service`). New HTTP handlers in `internal/api` for the Agent Card endpoint and the JSON-RPC task endpoint are thin wrappers over that service layer, same as everywhere else in this codebase.

## Agent Card and self-addressing

Served at `/.well-known/agent-card.json` — the current A2A v1.0 spec path. (Hadron currently serves at the older draft path `/.well-known/agent.json`, `apps/hadron/internal/agentcard/agentcard.go:15` / `apps/hadron/internal/api/server.go:81` — worth a small fast-follow fix in Hadron once this lands, tracked separately, not blocking here.)

**One Agent Card represents the Nanite host**, not one per durable-agent instance — mirrors Hadron's own shape (one card, skills enumerate the launchable things). Skills are derived from two existing registries, not invented:
- Named workflow definitions from the Agent Workflows pillar's `agentworkflow.Registry` — a skill per registered `WorkflowDefinition`, `InputSchema` derived the same way Hadron derives one from blueprint inputs (`apps/hadron/internal/agentcard/agentcard.go:128`, `SkillFromBlueprint` — same idea, different source).
- Durable-agent boot profiles/templates that can be started fresh via a Task (not every already-running instance — an instance is targeted by address on submission, not discovered as a separate skill per instance).

**Self-addressing** (closes `CW-20260519-0069` as a byproduct): a single `address.go` resolver becomes the one place an agent's canonical `msg://agent/<authority>/<id>` gets derived, replacing the hand-rolled duplicate constant in `internal/store/agents.go:18` (`urnPrefix`, whose own comment admits it's "the store-side mirror" of a value defined elsewhere to avoid an import cycle — exactly the kind of convention-guessed duplication the ticket flagged). Build a `whoami` MCP self-tool and a matching HTTP endpoint that resolve and return the caller's own canonical address via this one path. Boot-time auto-injection into a session's system-prompt context (so an agent never has to call the tool) is a reasonable stretch goal within this same ticket, not a hard requirement — don't let a slot-injection design debate block closing the core self-discovery gap.

## Task lifecycle and routing

`Task` submission targets either a workflow skill name or an existing durable-agent instance's `msg://` address. Routing:

- **Target = workflow skill** → `WorkflowLauncher.Launch` (existing, from Agent Workflows) starts a new `template`-class durable-agent instance running that workflow. The `a2a_tasks` row stores the resulting `durable_agent_instance_id` (and, transitively, the `workflow_runs` row it's driving) — no separate execution tracking.
- **Target = existing instance address** → `DurableWake.Wake()` is called with `Reason: DurableAgentWakeExternalMessage` (making that enum value real for the first time) and `WakePayload.Prompt` set from the Task's message content. The `a2a_tasks` row stores that instance's ID.

**`TaskState` is derived from the real execution status it's tracking, not maintained independently:**

| Trigger | TaskState |
|---|---|
| Task accepted, routing not yet started | `submitted` |
| Target instance status is `starting`/`active`/`resume_requested` (`store.DurableAgentStatus*`, confirmed real via `wakeSkipReason`'s switch, `durable_wake.go:270-288`), or workflow run status is running | `working` |
| Workflow-backed task, current step is a paused `gate` (see below) | `input-required` |
| Workflow run / wake completes successfully | `completed` |
| Workflow run / wake ends in error | `failed` |
| Task canceled before/during execution | `canceled` |
| Submission fails validation (unknown target, target archived/stopped and not resumable) | `rejected` |
| — | `auth-required` |

`auth-required` is defined in the `TaskState` wire-type constant set for spec conformance (a valid JSON-RPC response needs a value from the full spec-defined enum, and this package may need it later as an A2A *client* — see Outbound below) but **zero code paths in this pass produce it**. This is explicitly not the `DurableAgentWakeExternalMessage`-style "reserved but silently dark" pattern this same review cycle just found and cleaned up (`CW-20260814-0004`/`-0005`) — the distinction: this is a spec-mandated wire vocabulary value documented here as not-yet-reachable, not an internal enum with a doc comment quietly lying about production callers. If a future pass adds it, this doc's table is where that gets updated.

Non-workflow-backed tasks (a Task that wakes an existing instance directly, not through a workflow) have coarser completion semantics — `working` until the woken session's turn finishes, then `completed`/`failed` off that turn's outcome — matching Hadron's own simple queued/running/success/failed model for that case. The richer `input-required` state is only reachable through the workflow-backed path.

## Wake integration (the fix that makes any of this useful)

Two concrete changes to existing wake code, both required for an A2A-triggered wake to actually work:

1. Wire `DurableAgentWakeExternalMessage` as a real `Reason` value a caller can set — today it's constructed nowhere (confirmed via portfolio-wide grep, its only other hit is the UI dropdown label). The A2A routing path (above) is the first real caller.
2. Fix `WakePayload.Prompt` injection. Read `internal/service/durable_wake.go`'s `Wake()` flow and `selectOrCreateLaunchSession` (`durable_agents.go:679-716`) in full before changing anything — today the created session's `Metadata` only ever contains `{"durable_agent_instance_id": ...}`; the prompt never becomes message content. Find where the launched/resumed session actually gets driven after `Wake()` returns (this is a real code-reading task, not assumed — trace it rather than guessing the call site) and inject `WakePayload.Prompt` as that session's first real user-turn message when present. This is a general wake-mechanism fix, not A2A-specific — it benefits manual wakes too — but it's a hard prerequisite for A2A tasks to mean anything.

## Gate ↔ input-required mapping

Agent Workflows' `gate` step kind (human-in-the-loop pause, `docs/architecture/agent-workflows-design.md`'s "Built-in engine" section) is the natural source for `input-required`. When a workflow-backed Task's run reaches a paused `gate` step, the Task transitions to `input-required` and its surfaced status should carry enough of the gate's own context (what's being asked) for an external caller to act on it. Resolving the gate — an external caller providing the requested input — should route back through whatever mechanism the Workflow pillar already uses to resume a paused `gate` step.

**This needs real investigation, not assumption**: the Agent Workflows design doc specifies `gate` as a step kind but doesn't fully spec its resolution API (how a paused gate actually gets un-paused today). The implementing ticket for this piece must read `internal/service/workflow_engine.go` and the `workflow_run_steps` handling in full to find (or, if genuinely missing, define) that resolution path before wiring A2A's "continue this task with new input" call to it.

## Transport

JSON-RPC 2.0 over HTTP, per the A2A spec's Task methods. **Exact method name strings and payload field names must be verified against the live spec at a2a-protocol.org at implementation time** — spec wording is a moving external target, and Hadron's own implementation is already known to be non-conformant in places (REST-ish, not JSON-RPC at all — `apps/hadron/internal/a2a/handler.go`, confirmed via grep for `jsonrpc` returning zero hits). Don't hand-copy Hadron's shape or this doc's illustrative names without checking the current spec first.

Illustrative shape (verify before implementing): a single JSON-RPC endpoint accepting task-submit, task-get, and task-cancel methods, plus the well-known Agent Card endpoint as plain HTTP GET (not JSON-RPC — matches spec convention for discovery).

## Push notifications

`PushNotificationConfig` (url, token, auth) registered at task submission or via a follow-up config-set call. Delivery is an HTTP POST to the registered URL on each `TaskState` transition — **best-effort accelerant over the durable `a2a_tasks` record, same philosophy as Tether's existing `/messages/notify`**, not the source of truth. No documented delivery guarantee in the spec itself, so don't build one.

Concretely: a small `a2a_push_deliveries` table (task ID, target state, attempt count, last error) and a bounded number of retries (e.g. 3, backing off) driven by one more entry in `cmd/nanite/main.go`'s existing `startBackgroundWorkers` ticker set (`main.go:1006-1073` already runs several tickers for comparable bounded-retry/cleanup jobs — this is one more of those, not a new mechanism).

## Persistence

New table `a2a_tasks`: task ID, target kind (`workflow` | `instance`), target reference, caller-supplied message, `durable_agent_instance_id` (set once routing resolves — either newly created via workflow launch, or the existing instance targeted directly), derived `TaskState` (cached, refreshed on read and on push-delivery trigger), push notification config, created/updated timestamps. This table is bookkeeping and translation only, per the one principle above — it does not duplicate `workflow_runs`/`durable_agent_instances`' own state, it points at them.

## Explicit non-goals for this pass

- **No outbound A2A client** — Nanite calling another agent's A2A endpoint (submitting a task elsewhere, receiving a push notification for a task Nanite itself submitted). Real, needed eventually, but deferred — captured now as its own ticket while this design context is fresh (see task breakdown), not scheduled in this pass.
- No extraction to `libs/go-a2a` yet — validate in Nanite first, matching the Tether-ACP and original `go-messaging` extraction precedents.
- No `go-messaging` version bump (Nanite is pinned to v0.2.1, Hadron/Torque are on v0.3.0) bundled into this work — real, known tech debt, but nothing in this design needs `v0.3.0`'s `Router`/`KindGroup` additions. Track separately if a real need surfaces.
- No portfolio-wide simultaneous rollout — Nanite only, per explicit instruction.
- No streaming (SSE) support — matches Hadron's current gap; a future addition, not attempted here.
- No `auth-required` `TaskState` producer — see the lifecycle table above.
- Not fixing Hadron's Agent Card path/transport/PushNotificationConfig gaps — worth small fast-follow tickets in Hadron, out of scope for Nanite-first work here.
- Not re-litigating `go-messaging`'s Store/Inbox delivery substrate — A2A's Task/Agent-Card/push vocabulary sits on top of it unchanged.

## Prior art this design draws from

- **Hadron** (`apps/hadron/internal/agentcard`, `internal/a2a`) — a real, live, tested subset (Agent Card generation, simple task submit/poll) built directly against Hadron's own blueprint-run model. Confirms the shape is viable and worth adopting properly; also the clearest evidence of what to fix rather than copy — old draft Agent Card path, REST-ish (not JSON-RPC) transport, no `PushNotificationConfig`, only 5 of 8 task states.
- **Tether's ACP adapter** (`apps/tether/internal/acpadapter`, ADR-0034) — the "designed for portfolio extraction, zero internal-type imports, accepted-and-shipping-but-consciously-MVP-scoped" shape this design's package structure and non-goals directly follow.
- **Agent Workflows** (`docs/architecture/agent-workflows-design.md`) — the "protocol/engine layer only sequences or translates, real execution always happens through one existing implementation" principle this design applies to Task routing; `WorkflowLauncher` and the `gate` step kind are reused directly rather than reimplemented.
- **`go-messaging`** (`libs/go-messaging`) — the durable envelope/delivery substrate and `Address`/`ParseURN` types this design builds address derivation on top of, left otherwise unchanged.
