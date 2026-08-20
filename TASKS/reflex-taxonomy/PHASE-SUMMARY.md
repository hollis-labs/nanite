# Phase Summary — Reflex Action Taxonomy batch

Standalone follow-through on the operator-signed-off architecture doc `docs/engineering/architecture/10-reflex-action-taxonomy.md` (produced by `TASKS/phase-4/10-reflex-architecture-review.md`, 2026-08-19). Not part of the Phase 0-9 sequence — tracked in its own `TASKS/reflex-taxonomy/` folder, same treatment as `TASKS/adhoc/`. Resolves `docs/engineering/architecture/03-steering.md`'s former "Watch item": the reflex system's six job-types had no real internal structure.

## What shipped, in plain terms

The reflex system's six action kinds (`inject_reminder`, `force_tool_choice`, `send_message`, `halt_session`, `add_schedule`, `dispatch_to_agent`) now have a real internal taxonomy instead of one overloaded `priority` column and a free-text `created_by` string:

- **Category** (`system_message` advisory vs. `execute_action` deterministic), **combining algorithm** (which one wins when multiple fire in the same pass), **provenance tier** (who's allowed to declare which kind), and **recurrence** (cooldown cascade) are all now real, FK-backed lookup data — two new migrations (`124`, `125`), three new lookup tables, one new join table, two new columns on `agent_reflexes`.
- **One shared decision engine** (`Resolve()`, `internal/agent/reflexes/resolve.go`) now decides "which fired reflex actually gets applied" for all three places reflexes get evaluated — the main per-turn pass, the chat-turn dispatch path, and the `task_execute` self-tool dispatch path — instead of three independently hand-rolled loops. This is the batch's core deliverable; everything else builds on it.
- **Both bugs the original design review found are fixed**: two `force_tool_choice` reflexes naming different tools can no longer both fire and confuse the LLM with contradictory instructions; a fired `halt_session` reflex now actually stops the current turn before it reaches the LLM, and preempts every other reflex in the same evaluation pass — previously it only stamped a database flag checked on a *later* request, while the turn that triggered it completed and reached the LLM anyway.
- **Provenance is now enforced, not just recorded** — writing a new reflex definition is rejected if the writer's authority tier (system/operator/plugin) isn't on that action kind's allow-list. Seeded conservatively: only `halt_session` and `dispatch_to_agent` are restricted away from `plugin`-tier for now, explicitly documented as an adjustable default, not locked policy.
- **Telemetry is unified** — every reflex firing, from any of the three evaluation paths, now writes one consistent trace record to `event_log`, bumps the same `fired_count`/`last_fired_at` fields, and fires the same plugin observability hooks. Previously the `task_execute` self-tool path wrote to a completely separate, narrower table and never updated the operator-facing fired counters at all — a reflex could be actively routing turns while the UI showed it as never having fired.

All of this is currently sitting in the working tree on `main`, uncommitted except for a planning-artifacts-only commit (`80385a1c`) made before the batch was dispatched. The actual code has not yet been committed to git history — see `HANDOFF-TO-NEXT.md`'s note on this for the mechanical detail; flagging here since it affects when this becomes durable.

## What got escalated, and how it resolved

- **Task `08` — `Resolve()`'s silent fail-open** (`TASKS/ESCALATIONS.md` doesn't carry a standalone entry for this one; it's recorded in `TASKS/INDEX.md`'s Phase 1 review note, lines 320). The fresh reviewer found `Resolve()`'s per-kind lookup, on failure, silently falls back to `all_applicable` with zero logging anywhere — meaning a transient cache/DB hiccup could silently regress `halt_session` back to "doesn't actually preempt," the exact bug this batch exists to fix, with no way for anyone to notice. Real finding, fixed same-day as its own task (`08-fix-resolve-fail-open-visibility.md`), not patched inline into `03`. The fallback behavior itself wasn't changed (still `all_applicable`, a deliberate design choice) — only visibility was added (warning-level logging). Reviewed clean afterward.
- **`ApprovePendingReflex` bypasses the provenance-tier gate** — `TASKS/ESCALATIONS.md`'s final entry (2026-08-20, "Reflex Action Taxonomy Phase 2 review: `ApprovePendingReflex` bypasses the Facet-3 provenance gate — HEADS-UP, not blocking, not fixed here"). One code path that approves an agent-proposed pending reflex writes directly to the database without going through the new provenance-tier check. Explicitly out of scope for the task that built the gate (its own task file only asked it to preserve existing behavior, not extend the gate to this path) and harmless today — every action kind currently allows `operator` tier, so this path always lands on an already-permitted combination by construction. It only becomes a real gap if a future operator later tightens the allow-list to restrict `operator` tier from some specific kind. Logged as a non-blocking follow-up, not fixed, and already captured to Vanta memory (`user/chrispian/memory/followups/reflex_taxonomy_approve_pending_reflex_provenance_gate_bypass`) so it isn't lost.

No other escalations were raised during this batch.

## Anything still flagged, deferred, or needing attention before further work

- **The `ApprovePendingReflex` gap above** — no urgency, but worth fixing whenever `pending_reflexes` next gets real attention (it's already flagged elsewhere as incomplete/untested more broadly, per `docs/engineering/architecture/03-steering.md`).
- **Task `07` (harness-reactive self-tools design session) remains parked** — never dispatched, not blocking anything. It's a separate, sibling mechanism to reflexes (an agent-initiated "declare a fact, let the harness react" self-tool pattern), deliberately scoped out of this taxonomy from the start. Ready for the operator to book directly whenever there's a concrete near-term need for it.
- **This batch's code is uncommitted** (see above) — worth committing before it's at risk of being lost or tangled with unrelated future work in the same working tree.
- **One documented, deliberately-accepted behavior narrowing**: a `first_applicable` reflex whose top candidate fails to apply (a malformed `action_spec`) no longer falls back to the next-priority candidate the way the old ad hoc loops did. Low risk — the CRUD write path already rejects malformed `action_spec` JSON before it can reach this state — but noted in case it ever surfaces as a "reflex silently didn't fire" report.

## Current `TASKS/INDEX.md` state for this batch

Per `TASKS/INDEX.md`'s "Reflex Action Taxonomy" section (lines 299-325):

| Task | Status |
|---|---|
| `01-taxonomy-schema-foundation` | reviewed |
| `02-recurrence-cascade` | reviewed |
| `03-shared-decision-engine` | reviewed |
| `04-halt-turn-synchronicity` | reviewed |
| `05-provenance-tier-enforcement` | reviewed |
| `06-unified-reflex-telemetry` | reviewed |
| `08-fix-resolve-fail-open-visibility` | reviewed |
| `07-harness-reactive-self-tools-design-session` | not-started, parked (operator books directly) |

All eight real build/fix tasks (`01`-`06`, `08`) are `reviewed` and closed — confirmed by both the Orchestrator's own live validation against the deployed `nanite-api-service` (real migration runs, real `sqlite3` spot-checks) and a fresh Reviewer with no shared context with the implementing workers, in two rounds (Phase 1: `01`-`04`, one real finding fixed as `08`; Phase 2: `05`/`06`/`08`, no blocking findings). `07` is intentionally untouched and not part of what's being reported as shipped here.
