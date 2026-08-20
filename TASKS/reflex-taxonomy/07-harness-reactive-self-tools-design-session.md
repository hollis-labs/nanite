# Harness-reactive self-tools — design session (not a build task)

**Phase:** 2 — Provenance & Telemetry (`TASKS/reflex-taxonomy`), parked
**Status:** not-started — **design/discussion deliverable, operator books a dedicated session directly, do not auto-dispatch as a routine batch item** (same handling as `TASKS/phase-4/10-reflex-architecture-review.md` originally got)
**Depends on:** none to start; independent of every other task in this folder
**Touches:** nothing yet — this is a design session, not an implementation task. Eventual implementation would touch `internal/mcp/` (new `nanite_*` self-tool definitions) and whatever self-tool-definition schema carries a category/kind field.

## Context

`docs/engineering/architecture/10-reflex-action-taxonomy.md`, "Harness-reactive self-tools (adjacent mechanism, deliberately not part of this taxonomy)": distinguished during the 2026-08-19 architecture-review session as a **sibling** mechanism to reflexes, not a reflex action kind. An agent-initiated "declare a fact, let the harness react" pattern (e.g. `nanite_report_task_updated(id, msg)` — the harness decides whether/how to react: a card, an internal API call, both, neither). It has no predicate/event/interval trigger — the agent's own tool call *is* the trigger — so it structurally can't be an `action_kind` row.

**Resolved shape, not yet built:** small, purpose-built `nanite_*` self-tools (the namespace already reserved for first-party tools per `docs/tool-naming-convention.md`, already outside the general MCP trust-tier/result-cache apparatus meant for arbitrary external reach — see `docs/mcp-trust-model.md`). Each self-tool's own definition should carry a category/kind field marking it harness-reactive — preferred over a bare boolean, for the same extensibility reason as Facet 1's category table in the main taxonomy doc (room to add more reaction shapes later without a schema change).

**Naming is not finalized.** Leading candidate: "harness-reactive self-tools." "Report tools" and "declare tools" were considered and explicitly rejected as *final* only in the sense that neither is locked yet — not rejected outright. Whatever name is chosen, **avoid "event" and "notify"** — both already carry distinct meaning elsewhere in this codebase's vocabulary (`trigger_kind='event'`, the `event_log` table; `notify_external`, a candidate deferred deterministic reflex kind) and reusing either would reintroduce the exact naming confusion the whole taxonomy session was trying to eliminate. Check `GLOSSARY.md` before locking any name, per `EXECUTION-PROCESS.md`'s standing instruction.

The design doc is explicit: *"This mechanism gets its own design pass when it's actually built — this doc captures the distinction and the naming constraint so the context isn't lost, not a full spec for it."* This task file exists for the same reason `TASKS/phase-4/10-reflex-architecture-review.md` existed — to hold a place in the tracker for a design session the operator will run directly, not to hand a worker a spec that doesn't exist yet.

## What to do

This is a design/discussion session, not a mechanical worker task — the operator shapes the actual approach live, the same way `TASKS/phase-4/10` was run. At minimum, the session should produce:

1. A final name for the mechanism (checked against `GLOSSARY.md` first).
2. The self-tool definition shape — specifically, what the category/kind field looks like on a `nanite_*` self-tool's own definition, and where that definition lives (a new column on whatever table backs self-tool registration today, or a manifest-level field — check how `nanite_*` self-tools are currently defined before assuming).
3. At least one concrete first self-tool designed end-to-end as a worked example (the design doc's own example, `nanite_report_task_updated(id, msg)`, is a reasonable candidate) — enough to prove the shape is buildable, not necessarily built in this session.
4. A decision on whether this session's output should become its own implementation task file(s) immediately, or wait for a concrete near-term consumer.

## Out of scope

- Actually building any self-tool — that's follow-up implementation work this session would inform, once scoped.
- Reopening any decision already settled in `docs/engineering/architecture/10-reflex-action-taxonomy.md` (the reflex taxonomy itself) — this is about the adjacent, deliberately-separated mechanism only.

## Done means

- A recorded design (in this file's Work Log, or a linked doc) that the operator has signed off on, matching the shape `TASKS/phase-4/10-reflex-architecture-review.md`'s own Work Log used.
- If the design decides implementation should happen soon, a new task file (or files) is created for it — this file's own scope stays design-only either way.

## Work log

<!-- Filled in only once the operator runs this session. -->

## Review notes

<!-- Not applicable unless this session produces a build task, which gets its own review. -->
