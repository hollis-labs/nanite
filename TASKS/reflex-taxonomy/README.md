# Reflex Action Taxonomy — implementation

Implements `docs/engineering/architecture/10-reflex-action-taxonomy.md`, the design produced by the dedicated architecture-review session `TASKS/phase-4/10-reflex-architecture-review.md` (2026-08-19, operator-signed-off). That session changed **no code or schema** — design only. This folder is the follow-up implementation work the design doc's own "Status" section calls out as deferred and not yet filed.

**Not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence.** Kept in its own subfolder (same pattern as `TASKS/adhoc/`) rather than force-fit into an existing phase, since this work wasn't in the original plan — it's a follow-on from Phase 4's own review finding the reflex system's six job-types needed real internal structure (see `docs/engineering/architecture/03-steering.md`'s now-resolved "Watch item").

## Read before starting any task here

1. `docs/engineering/architecture/10-reflex-action-taxonomy.md` — the full design: the emit/react spine, the four facets (category, combining algorithm, provenance tier, recurrence cascade), the per-kind reclassification table, the shared-decision-engine target shape, the halt-synchronicity fix, the telemetry requirement, the harness-reactive self-tool split-out, and the explicit "what this session did not decide" list.
2. `TASKS/phase-4/10-reflex-architecture-review.md` — the review session's own Work Log; same content as (1) but framed as a decision trail, useful for the *why* behind each facet's chosen shape.
3. `docs/engineering/architecture/03-steering.md` — one paragraph, the resolved Watch item, points back to (1).
4. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer discipline, and escalation rules every task file below follows.

## What this batch does NOT do

Per the design doc's own explicit scope boundaries — do not expand any task below to cover these without a fresh operator conversation:

- **Exact DDL beyond what each task file specifies.** The design doc is architecture-level agreement, not locked migration-ready schema — each task below makes a concrete, documented schema call, but it is an implementation call, not a re-litigation of the design.
- **The specific per-tier action-kind allow-list values beyond the doc's own named candidates** (`halt_session`/`dispatch_to_agent` restricted from `plugin` tier) — seeded as the default in `05-provenance-tier-enforcement.md`, explicitly flagged there as adjustable, not final security policy.
- **A third "fire once ever" recurrence mode** — `02-recurrence-cascade.md` builds the duration-or-none cascade only.
- **The harness-reactive self-tool mechanism's actual build** — `07-harness-reactive-self-tools-design-session.md` is a design-session placeholder, not a build task, matching how `TASKS/phase-4/10` itself was tracked. Do not dispatch it as a routine batch item.
- **Plugin-registered action kinds** — documented as a deferred seam in the design doc; no task exists for it here because no concrete plugin need exists today.

## Task sequence

**Phase 1 — Core engine.** Fixes the two concrete, confirmed bugs (contradictory `force_tool_choice` directives, `halt_session` not actually halting) by building the real taxonomy underneath them. Strict order — each depends on the last.

| Task | Depends on |
|---|---|
| `01-taxonomy-schema-foundation.md` | none |
| `02-recurrence-cascade.md` | `01` |
| `03-shared-decision-engine.md` | `01`, `02` |
| `04-halt-turn-synchronicity.md` | `03` |

**Phase 2 — Provenance & telemetry.** Hardening/observability layered on top of Phase 1's engine.

| Task | Depends on |
|---|---|
| `05-provenance-tier-enforcement.md` | `01` (parallel-safe with `02`-`04`, see its own Touches) |
| `06-unified-reflex-telemetry.md` | `03`, `04` |

**Parked, not scheduled.**

| Task | Depends on |
|---|---|
| `07-harness-reactive-self-tools-design-session.md` | none — operator books directly, do not auto-dispatch |

See `TASKS/INDEX.md`'s own new section for status tracking as these land.
