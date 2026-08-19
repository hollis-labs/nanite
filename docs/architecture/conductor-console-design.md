# Conductor Console — Central Orchestrating Chat Agent

Status: design, ready for task breakdown. Scope: **Nanite only**. Working name for the new agent is **"Conductor"** — not finalized, just the label used throughout design; rename freely later without touching the architecture below.

## What this is for

The user runs work across 5+ apps/projects simultaneously and today is the sole liveness-poller and message-router between them, by hand. Concrete evidence from live session-transcript research (2026-08-16): **6 simultaneous Claude Code sessions across 5 apps (nanite, fragments-engine ×2, nil, ion, loom ×2) in one afternoon**, with the user manually relaying status between them and stating outright: *"I'll add them to my personal queue to fire off when other agents finish working."* The queueing mental model already exists; there's no tooling under it.

The ask: a single chat-facing agent that doesn't do work itself — it delegates, explores technical topics conversationally, captures notes, relays distilled status from delegated work, and surfaces approvals — without the confusion that comes from mixing many unrelated threads (5 technical topics, agent approvals, injected notes) into one context. It delegates *down* to existing per-project execution (Orchestrator/Torque) and to next-level orchestrators (Tether/mux for cross-app work) rather than re-implementing dispatch itself.

Non-negotiable constraint carried into this design: the user runs **one active worker per project at a time** (sequential, not parallel, execution within a project) — Conductor's job is letting the user drive *multiple projects* concurrently, not parallelizing work *within* a project.

## Current state — building blocks already in place

This design deliberately reuses existing, proven infrastructure. Nothing below needs to be rebuilt:

| Component | Status | Reference |
|---|---|---|
| Deterministic phrase-router | **Retired** — `internal/promptrouter` no longer exists (`TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`). Its phrase-matching job migrated onto `internal/agent/reflexes`'s DB-backed `dispatch_to_agent` reflex rows (`internal/agent/reflexes/seeds.go`) — the FU-30 predicate/event/interval steering engine this row used to call "unrelated" is now the *only* deterministic phrase-match system; there is no longer a second one to distinguish it from. See `docs/promptrouter-catalog.md` (retired pointer doc) for the historical mapping. |
| Task tracking + approvals | Live | Torque: `torque_task_checkpoint_*`, `torque_sprint_approve` already cover "agent approvals" — no new approval primitive needed. |
| Per-project execution discipline | Live | `internal/service/durable_agent_recipes.go` — Orchestrator (`harness`, polls Torque task status, dispatches via `workflow_run`/`subagent_spawn`), Planner, Project Manager (advisory), Reviewer. Keeps one-worker-per-project sequencing without Conductor re-implementing it. |
| Cross-app live coordination | Live | Tether/mux (`mcp__mux__*`): SSE event bus (`/events*`), `mux_message_notify` (best-effort wake of a live recipient session), `mux_events_wait` (bounded long-poll), `mux_session_*` (cross-app launch/monitor), `tether_registry_*`/`tether_group_*` (agent directory, group channels). Backed by a real ADR lineage (message-routing contract, federation, registry, group messaging) — actively maturing, not legacy. |
| Deterministic orchestration-timing (future home) | Live substrate, no content yet | `internal/agentworkflow` — fully wired, not the "zombie" earlier docs described. `workflow_definitions_path` set by default; `examples/workflow-definitions/worker-reviewer-gate.yaml` is real and loadable today. |
| Nanite-native in-daemon wake | Partial, closing | `SendAgentMessage` (`internal/service/chat.go:824-851`) and subagent/workflow-completion via `TriggerHarnessTurn` (gated by `subagentCompletionPolicy: auto_summarize`) already live-wake sessions in-process. The third path, `internal/messaging.Service.SendMessage` (backs `nanite_a2a_send`/`message_send`), is poll-only today — unification tracked as `CW-20260816-0065`. |

## The architectural split (key decision from this design pass)

**Nanite-to-Nanite agent coordination uses Nanite's own native in-daemon wake — no Tether dependency.** Nanite is a persistent daemon that fully controls both sides of its own internal messaging; once `CW-20260816-0065` lands, all three send paths (direct API nudge, subagent/workflow completion, self-tool `message_send`) wake a live recipient session consistently and in-process. Nanite shipping as a product cannot be dependent on an external app (Tether) for its own internal agent-to-agent communication.

**Tether/mux remains the tool for genuinely cross-app coordination** — talking to Torque-launched CLI workers, Nil, Fragments Engine, or any other separate process/app. This is already validated and in production use for that purpose.

This corrects course from earlier in this design pass: initial research (docs-only) concluded Nanite had "no bidirectional channel" and recommended routing all live coordination through Tether. A direct code-level audit showed that claim was accurate only for spawned subagent/PTY children mid-tool-call — a narrower case — and had been wrongly generalized to session-to-session messaging between two durable Nanite agents, where native wake already substantially works. Lesson encoded here for future readers: verify architecture claims about this system against source, not docs — the docs lagged the implementation by months in more than one place during this investigation.

## Conductor: proposed shape

A new `harness`-class durable agent, mirroring the Orchestrator precedent: deterministic scaffolding around an LLM turn, not a freeform planner/reviewer/operator. Chat-facing — the one surface the user talks to.

**Tool surface:**
- `mux_session_*` / `mux_message_*` / `tether_registry_*` — cross-app session launch, monitoring, messaging, directory.
- `torque_task_*` / `torque_project_*` / `torque_sprint_approve` / `torque_task_checkpoint_*` — task state, tracking, approvals.
- `memory_write` / `memory_recall` (Vanta) — durable notes and decisions.
- Native Nanite messaging tools (`message_send`/`nanite_a2a_send` family) once `CW-20260816-0065` lands — for Nanite-to-Nanite coordination without leaving the daemon.

**Default behavior:** routine completions queue (pull-based — "what's pending?" surfaces a digest on request); approvals and blockers interrupt. This mirrors the existing `subagentCompletionPolicy` pattern rather than inventing a new push/pull model.

**Output shape:** distilled summary + link to the full session, using the existing `report-card`/`session-task` envelope types — never a raw transcript dump. This is an existing UI primitive, not new work.

**What Conductor is not:** not a worker (doesn't do engineering work itself), not a per-project sequencer (that stays with Torque + the existing Orchestrator agent), not a replacement for Tether/mux's cross-app plumbing (it's a client of both, not a reimplementation of either).

## Deterministic input middleware — status update (retired mechanism)

This section originally proposed extending `internal/promptrouter/` with a rule set for the user's actual directive vocabulary (note-capture, dispatch-to-project, non-linear "research X, drop results in Y", approval phrasing), authored as `~/.nanite/reflexes/conductor-*.yaml` user-level overrides. That package and its YAML-override loader are retired in full (`TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`) — see `docs/conductor-usage.md`'s "Deterministic reflex triggers" section for the full correction, including the finding that this mechanism's actual production effect was already limited to an audit-log row (not a routing guarantee) even before retirement, and that Conductor's real behavior for these phrasings is (and always was) driven by its own system prompt, not this middleware layer.

The raw-vs-rewritten audit gap this section flagged was closed (`CW-20260816-0068`, `playbook_match_log`'s `raw_input_text`/`sent_input_text` columns) before the retirement above — that part of the design shipped and the columns are still live, just no longer populated by conductor-specific phrase matches (nothing populates them for Conductor now, since the loader that fed them is gone).

## Non-goals for v1

- **CLI/PTY-agent steering.** Already solved via Tether; a separate, already-closed topic, not revisited here.
- **New cross-app plumbing.** Tether/mux already provides session launch, messaging, and directory services — Conductor is a client, not a rebuild.
- **A dedicated queue/approvals UI panel.** Start conversational (ask Conductor "what's pending?"); add a UI surface later only if the conversational interface proves insufficient.
- **Agent-Workflows-based deterministic orchestration-timing rules.** Explicitly deferred — "eventually," not day one, per the user's own framing. The substrate (`internal/agentworkflow`) is ready when this is prioritized (see task T6).
- **Nil/Fragments Engine note-capture specifics.** Their actual MCP tool surface for adding notes/fragments hasn't been explored yet — first pass is investigation, not a finished integration (see task T5).

## Prior art

- `.nanite/agents/orchestrator.md` — the Torque-task-status dispatch harness this design explicitly does not duplicate; Conductor delegates to it rather than reimplementing per-project sequencing.
- `docs/architecture/agent-roles-design.md` — the Architect/Planner/PM/Orchestrator/Worker/Reviewer role family this design extends with one new role.
- `docs/research/user-workflow-observed.md` and `docs/research/gaps-and-opportunities.md` (2026-04-05) — first proposed a "parent-orchestrator session primitive" (B2) monitoring N child sessions with structured status, which this design finally delivers, ~4.5 months later, once the underlying live-wake and cross-app substrate had actually matured enough to support it.
