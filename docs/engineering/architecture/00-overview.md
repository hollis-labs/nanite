# Architecture Overview

## Guiding principles

These recur across every subsystem doc in this folder — they're not independent choices, they're the same handful of judgment calls applied consistently.

- **One runtime, several doors.** GUI, CLI, API, MCP, and A2A are all consumers of the same underlying execution, not separate execution paths with their own rules. Everything converges on `agent.Boot` (CLI-based subprocess) or `provider.StreamChat` (direct HTTP).
- **The database is the source of truth.** Files exist only where they earn their keep: builtin/seed content (compiled in, used once to seed) and nothing else. No system re-ingests from disk on every boot.
- **Composition over flat definition.** An Agent is an assembly of independently-defined facets (Role, Scope, tools, skills, permissions), not a monolithic record. Applies beyond agents too — Cards are meant to be composed from a small set of primitives rather than growing a new top-level type per feature.
- **Real relational references, not free-text strings.** Tool names, skill names, model IDs are foreign keys against live catalogs, not pattern-matched strings in JSON blobs.
- **Hints, not control.** Steering nudges; it doesn't gate with a deterministic pre-decision layer. Hard-gating experiments in this codebase produced dead-end conversations — this isn't a style preference, it's an observed failure mode.
- **Kill dead code aggressively.** Standing policy (captured to durable memory: `user/chrispian/memory/decisions/aggressive_dead_code_removal_policy`). Zero callers or zero rows defaults to removal, not "flag and revisit."
- **MCP is the one external door for Nanite-aware consumers; A2A is a separate door for genuinely external callers; GUI/CLI stay first-party.** Not three overlapping options — three different jobs.
- **Naming collisions get fixed when they cause real confusion, not preemptively everywhere.** But when a new system needs a name, check `GLOSSARY.md` first — this codebase has a real, repeated history of the same word meaning two things in two subsystems, and that history is expensive. **Prefer namespacing a generic word over avoiding it.** A bare `status`/`scope`/`run` is fine reused across subsystems as long as it's never written unqualified where ambiguous — each owner's name prefixes the operation (`Turn.Cancel` vs. `Run.Cancel`; `Team Slot` vs. Context Broker slot), and `GLOSSARY.md` carries one disambiguation entry per overlapping word. Inventing a new word to dodge a collision is the last resort, not the first move — reach for it only when the two concepts are close enough in *meaning*, not just in name, that even a namespaced form would still mislead.
- **Hot-reload is worth deliberately considering for anything that changes with meaningful frequency or impact** — not a blanket requirement. Fine to skip for things that rarely change or don't matter if stale.

## The subsystems

1. [Agent Construction](01-agent-construction.md) — how an agent gets defined.
2. [Agent Launching](02-agent-launching.md) — how a defined agent becomes a running process or in-process turn.
3. [Steering](03-steering.md) — how the system decides what an agent does moment to moment.
4. [Harness](04-harness.md) — the shared turn-loop mechanics every launched agent runs through.
5. [Storage & Migrations](05-storage-and-migrations.md) — the persistence layer underneath everything above.
6. [Session Lifecycle & Recovery](06-session-lifecycle-and-recovery.md) — what happens when things go wrong, and how sessions persist across restarts.
7. [Inter-Agent Messaging](07-inter-agent-messaging.md) — how agents communicate with each other and with the external A2A protocol.
8. [Cards](08-cards.md) — the structured UI-card system (formerly "the envelope system").
9. [Plugin System](09-plugin-system.md) — how Nanite gets extended without a core code change.
10. [Reflex Action Taxonomy & Precedence](10-reflex-action-taxonomy.md) — how the six reflex job-types get resolved when they fire together (a [Steering](03-steering.md) detail doc).
11. [Harness-Reactive Self-Tools](11-harness-reactive-self-tools.md) — the agent-initiated "declare a fact, let the harness react" mechanism, sibling to reflexes.
12. [Scheduling](12-scheduling.md) — the `go-scheduler`-backed engine for running agents, workflows, and commands on a schedule.
13. [Memory & Knowledge Tools](13-memory-and-knowledge-tools.md) — the native memory/knowledge/recall tool surface: scratchpad, todo/plan, handoff, per-agent durable state, embedded Tesseract, chat search.
15. [Teams](15-teams.md) — configurable N-slot organizational shapes (routing, authority, gates) that generalize today's hardcoded three-role dispatch, compiling to `agentworkflow` runs rather than a new orchestration engine.
16. [Agent Host](16-agent-host.md) — the shared process/stdio/sandbox/env boundary underneath CLI agent launching, and where Nanite's current bespoke launch code stands against the portfolio's existing (unadopted) candidate for it.
17. [Agent Client Protocol (ACP)](17-acp.md) — ACP as a transport for driving underlying CLI agents (a companion to [Agent Host](16-agent-host.md)), distinct from Tether's already-shipped ACP-server role.
18. [Filesystem Snapshots](18-filesystem-snapshots.md) — shadow-git undo/audit mechanics for an agent's granted paths, living beside sandboxing in the host's responsibility list; a distinct axis from agent/session-state recovery ([Session Lifecycle & Recovery](06-session-lifecycle-and-recovery.md)).
19. [API / CLI Agent Runtime Parity](19-api-cli-runtime-parity.md) — confirms the harness (`generateResponse`) already unifies API- and CLI-backed agents at the event/tool-execution level; disambiguates `Turn.Cancel` from `Session.Stop` (a real, easy-to-conflate naming gap this review itself hit).
20. [Skills](20-skills.md) — procedural knowledge as a first-class, DB-backed system distinct from Reflexes (predicate-triggered) and Agent Workflows (prescribed DAGs).
21. [Loops](21-loops.md) — a goal-driven control layer above `agentworkflow`: `LoopRun` orchestrates a sequence of ordinary `WorkflowRun`s toward a first-class `Goal`'s target state, on a budget, with a deterministic-first continuation policy (CONTINUE/RETRY/REPLAN/REARCHITECT/WAIT/ESCALATE/COMPLETE/FAIL) — not a second workflow engine.
22. [Turn vs. Run](22-turn-vs-run.md) — resolves the harness's own conflation of one model invocation (Turn) with the full tool-settling loop (Run); explains why Agent Workflows already built a second, duplicate loop to get Turn-level control.
23. [Feedback-Carrying Denial](23-feedback-carrying-denial.md) — extends the existing `internal/recover` structured-error taxonomy to policy/permission denials, so a hard "no" can still carry a context-specific reason and suggestion back to the model.
24. [Typed Corruption / Recovery Taxonomy](24-typed-corruption-recovery-taxonomy.md) — documents a target taxonomy for conversation-state inconsistency detection (a genuine current gap); explicitly not decided to implement until real pressure exists.
25. [Plugin Conformance Harness](25-plugin-conformance-harness.md) — target design for executable contract tests against a real spawned plugin binary; the forbidden-capabilities portion depends on the capability model queued in `TASKS/plugin-system/`.
26. [Architecture Enforcement Tests](26-architecture-enforcement-tests.md) — findings only (provider-import and frontend-fetch boundary violations confirmed real) — flagged for its own dedicated architecture session, not designed here.
27. [Code Mode](27-code-mode.md) — confirms `python_run` already implements Code Mode's core mechanism; the open question is whether to expose it as a non-agentic primitive for workflows/reflexes.

## What's genuinely still open

- ~~Whether durable agents should eventually run CLI-based instead of API-based.~~ **Resolved 2026-08-19**: keep both substrates. A dedicated review found the two paths much closer in behavior than originally assumed post-Phase-2-5, so there's no "which one wins" call to make. What remains is making the choice explicitly configurable: an app-level default (CLI), a system-wide override, and the existing per-agent override (`runtime_kind`, see [Agent Launching](02-agent-launching.md)) — `TASKS/phase-8/01-set-default-runtime-kind.md`.
- GUI's start-surface reconciliation against the new construction model — deferred to the dedicated frontend pass.
- A full archival pass on the docs this folder supersedes — see `../TASKS.md`.
- `go-agent-wrapper` host adoption and ACP driving of underlying agents ([Agent Host](16-agent-host.md), [ACP](17-acp.md)): planned and approved, executing under `TASKS/agent-host-acp/`.
- Filesystem snapshots ([18-filesystem-snapshots.md](18-filesystem-snapshots.md)): reviewed and aligned, not yet planned.
- `CancelActiveGeneration`→`Run.Cancel` rename ([22-turn-vs-run.md](22-turn-vs-run.md), supersedes the earlier `Turn.Cancel` target name once the Turn/Run split landed): recommended, not yet executed.
- Loops ([21-loops.md](21-loops.md)): reviewed and aligned (three load-bearing forks resolved with operator sign-off), not yet planned. Depends on Scheduling's own `go-scheduler` adoption ([12-scheduling.md](12-scheduling.md)) landing first for the `loop_run_tick` trigger path.
- Turn as a callable harness primitive, and the `workflow_step_executor.go` consolidation it would enable ([22-turn-vs-run.md](22-turn-vs-run.md)): target architecture only, no implementation plan.
- Feedback-carrying denial ([23-feedback-carrying-denial.md](23-feedback-carrying-denial.md)): target shape decided (extend `internal/recover`'s taxonomy to policy denials), four extension points identified, not yet built.
- Typed corruption/recovery taxonomy ([24-typed-corruption-recovery-taxonomy.md](24-typed-corruption-recovery-taxonomy.md)): documented, explicitly not decided to implement — revisit only if a real incident demonstrates the gap.
- Plugin conformance harness ([25-plugin-conformance-harness.md](25-plugin-conformance-harness.md)): target design set; the forbidden-capabilities portion is blocked on `TASKS/plugin-system/04`–`06` landing.
- Architecture enforcement tests ([26-architecture-enforcement-tests.md](26-architecture-enforcement-tests.md)): real findings recorded (provider-import and frontend-fetch boundaries both have live violations), but the mechanism/CI design needs its own dedicated session.
- Provider compatibility/quirks (compat-flags near protocol/model descriptors instead of forking adapters): audited and deferred — Nanite has only 2 real adapters (`internal/llm/anthropic`, `internal/llm/openai`) and one quirk total today, so a compat-flag system would be premature abstraction. Revisit if/when the documented intent to add Ollama/OpenRouter/OpenCode-Zen (`docs/architecture-agents-2026-08-18.md`) actually lands — that's the trigger, not before.
- Code Mode ([27-code-mode.md](27-code-mode.md)): core mechanism confirmed already built (`python_run`); whether to expose it as a non-agentic primitive for workflows/reflexes is open, no current pressure to build it.

## What this doesn't cover yet

The frontend doesn't have its own architecture doc yet — deliberately deferred until more of the backend work above lands, since it will resolve or inform a meaningful amount of frontend work.
