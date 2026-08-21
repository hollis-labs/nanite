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
- **Naming collisions get fixed when they cause real confusion, not preemptively everywhere.** But when a new system needs a name, check `GLOSSARY.md` first — this codebase has a real, repeated history of the same word meaning two things in two subsystems, and that history is expensive.
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

## What's genuinely still open

- ~~Whether durable agents should eventually run CLI-based instead of API-based.~~ **Resolved 2026-08-19**: keep both substrates. A dedicated review found the two paths much closer in behavior than originally assumed post-Phase-2-5, so there's no "which one wins" call to make. What remains is making the choice explicitly configurable: an app-level default (CLI), a system-wide override, and the existing per-agent override (`runtime_kind`, see [Agent Launching](02-agent-launching.md)) — `TASKS/phase-8/01-set-default-runtime-kind.md`.
- GUI's start-surface reconciliation against the new construction model — deferred to the dedicated frontend pass.
- A full archival pass on the docs this folder supersedes — see `../TASKS.md`.
- Whether Nanite adopts `go-agent-wrapper` as the shared agent host, and whether/how it drives underlying agents over ACP — see [Agent Host](16-agent-host.md) and [ACP](17-acp.md). Both are evidence-gathering reviews, not decisions; planning inherits an open call, not a foregone one.

## What this doesn't cover yet

The frontend doesn't have its own architecture doc yet — deliberately deferred until more of the backend work above lands, since it will resolve or inform a meaningful amount of frontend work.
