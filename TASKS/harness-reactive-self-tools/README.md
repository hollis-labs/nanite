# Harness-Reactive Self-Tools — implementation

Implements `docs/engineering/architecture/11-harness-reactive-self-tools.md`, the design produced by the dedicated design session `TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md` (2026-08-20, operator-signed-off). That session changed **no code or schema** — design only, and explicitly deferred filing any implementation task ("document now, build once a concrete consumer exists"). This folder is that follow-up implementation work, filed once the operator decided to move forward — see the forward-pointer notes added to the design doc's "Status" section and the design session's own Work Log.

**Not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence, and a sibling to `TASKS/reflex-taxonomy/`, not nested inside it.** The harness-reactive self-tools mechanism was deliberately scoped as an *adjacent* mechanism to Reflexes, not a Reflex action kind — see `docs/engineering/architecture/10-reflex-action-taxonomy.md`'s "Harness-reactive self-tools" stub and doc 11's "The mechanism, recapped" section for why it structurally can't be a reflex `action_kind` row (a self-tool call *is* the trigger; there's no predicate/event/interval for a reflex evaluation loop to watch). Kept in its own top-level `TASKS/` subfolder for the same reason `TASKS/reflex-taxonomy/` is: this work wasn't part of the original plan.

## Read before starting any task here

1. `docs/engineering/architecture/11-harness-reactive-self-tools.md` — the full design: the package boundary (`internal/selftools` + `internal/selftools/reactions`), the reactive-layer-only DB scope (`selftool_reaction_kinds` + `selftool_reactions`, deliberately no combining-algorithm column), the import-cycle constraint that splits `render_card` (needs a caller to surface it) from the other three reaction kinds (execute fully inside the engine), the halt-synchronicity precedent it independently re-verified before relying on it, the `event_log` telemetry decision, the `task_update_report(id, msg)` worked example, and the explicit "what this session did not decide" list.
2. `TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md` — the design session's own Work Log; same content as (1) but framed as a decision trail (why `internal/agent/selftools` and `internal/selftools/action` were both considered and rejected, why `internal_api_call`/`external_api_call` stay separate kinds, etc.).
3. `docs/engineering/architecture/10-reflex-action-taxonomy.md`'s "Harness-reactive self-tools (adjacent mechanism, deliberately not part of this taxonomy)" stub — one paragraph, points back to (1).
4. `docs/engineering/GLOSSARY.md`'s "Harness-reactive self-tools" and "Self-tool reaction" entries — the locked naming and why neither collides with Reflexes' own `action`/`action_kind` vocabulary or with "event"/"notify".
5. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer discipline, and escalation rules every task file below follows.
6. `docs/tool-naming-convention.md` — the `<concept>_<verb>` convention and the `nanite_*` reserved-namespace rule the worked example (`07`) follows.

## What this batch does NOT do

Per the design doc's own explicit "What this session did not decide" list — do not expand any task below to cover these without a fresh operator conversation:

- **Exact DDL beyond what each task file specifies.** The design doc is architecture-level agreement (illustrative table/column shapes), not migration-ready schema — each task below makes a concrete, documented schema call, but it is an implementation call, not a re-litigation of the design.
- **Real execution semantics for `internal_api_call`/`external_api_call`/`callback`** — auth, retries, idempotency, which internal endpoints are safe to expose this way. `03-reaction-engine-core.md` builds a minimal, illustrative `internal_api_call` executor only (matching the worked example's own illustrative config shape); `external_api_call` and `callback` are seeded as documented-but-`implemented=false` rows and get no execution code in this batch.
- **Provenance/authority tiering for reaction registration** (which tier may register which reaction kind on which tool) — the design doc names this as a future extension (the `category` column on `selftool_reaction_kinds` anticipates it) but explicitly does not design it, since no plugin-registered self-tool exists today to need it. No task here builds it.
- **A real integration for `task_update_report`.** The worked example (`07`) stays illustrative, proving the shape is buildable — not wired to Nanite's actual todo/plan store or any other real consumer.
- **`docs/tool-naming-convention.md`'s own `nanite_*`-reservation wording revisit** — flagged during the design session's worked example as worth revisiting, tracked separately by the operator, out of scope here.

## Task sequence

**Phase 1 — Core mechanism.** The package move, the DB schema, and the engine that resolves a fired self-tool's configured reactions, including the one reaction kind (`render_card`) that structurally can't execute inside the engine itself.

| Task | Depends on |
|---|---|
| `01-move-self-tools-to-internal-selftools.md` | none |
| `02-reactive-layer-schema.md` | none (parallel-safe with `01` — different file surface) |
| `03-reaction-engine-core.md` | `01`, `02` |
| `04-render-card-construction.md` | `03` |

**Phase 2 — Telemetry, consumer cleanup, worked example.** Observability and the proof-of-shape example, layered on top of Phase 1's engine.

| Task | Depends on |
|---|---|
| `05-selftool-reaction-telemetry.md` | `03` |
| `06-collapse-envelope-marker-consumers.md` | none (independent DRY cleanup of pre-existing code; not required for `04`'s render_card path to work, since the three existing consumers are already marker-agnostic — see that task's own Context) |
| `07-worked-example-task-update-report.md` | `01`, `03`, `04`, `05` (`06` not required — see its own Depends-on note) |

See `TASKS/INDEX.md`'s own new section for status tracking as these land.
