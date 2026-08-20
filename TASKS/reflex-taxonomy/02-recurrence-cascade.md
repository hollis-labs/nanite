# Recurrence cascade — system default → kind override → reflex override

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`)
**Status:** not-started
**Depends on:** `01-taxonomy-schema-foundation.md` (needs `reflex_action_kinds.default_recurrence_seconds` and `agent_reflexes.recurrence_override_seconds`)
**Touches:** `internal/agent/reflexes/engine.go` (`recentlyFired`, `EvaluateState`'s debounce call at line ~141), `internal/service/chat_reflex_dispatch.go` (`attemptReflexDispatch`, its header-comment design note 2), `internal/mcp/self_tools_dispatch.go` (`matchDispatchToAgentReflex`).

## Context

`docs/engineering/architecture/10-reflex-action-taxonomy.md`, "Facet 4 — Recurrence": cascading, least-to-most-specific — **system default → per-action-kind override → per-reflex override.** Today's system default (15 minutes) and `dispatch_to_agent`'s override (none) both exist, but as *code*, not data, and in two different places:

- `internal/agent/reflexes/engine.go:141` — `if recentlyFired(r, now, 15*time.Minute) { continue }`, a bare literal inside `EvaluateState`'s loop. Applies to all five non-`dispatch_to_agent` kinds (Phase 4 #09 already excludes `dispatch_to_agent` from this loop entirely — see the comment at `engine.go:101-128`).
- `internal/service/chat_reflex_dispatch.go`'s header comment (design note 2, lines ~26-42) documents `attemptReflexDispatch` deliberately **not** going through `EvaluateState`/`recentlyFired` at all, specifically to avoid the 15-minute debounce — a routing decision needs fresh re-evaluation every turn, not a cooldown built for a nudge. `internal/mcp/self_tools_dispatch.go`'s `matchDispatchToAgentReflex` (lines ~296-410) does the same, independently, for the same reason.

The design doc's exact framing: *"Once that's data instead of code, the only remaining reason for `dispatch_to_agent` to have its own invocation points is the import-cycle constraint below — not a recurrence-semantics difference."* This task makes it data. It does **not** collapse the two `dispatch_to_agent` call sites into one — that's explicitly out of scope; the doc is clear their separate `State`-building (from live, not-yet-persisted classification data) is a legitimate, permanent reason to keep them distinct. Only the cooldown-bypass *justification* goes away.

**Open, not this task's job to decide:** whether recurrence needs a third "fire once ever, for the life of the session" mode (e.g. for `task_complete_self_terminate`/`task_timeout`-style lifecycle kinds). Build the two-level duration-or-none cascade only; leave the third mode as a documented future extension if you notice a concrete need, don't build it speculatively.

## What to do

1. Add an exported constant in `internal/agent/reflexes` — e.g. `DefaultReflexCooldown = 15 * time.Minute` — replacing the bare literal at `engine.go:141`. This is the system-level default; it stays a Go constant, not a DB row (only kind- and reflex-level overrides need to be data — the design doc's cascade language doesn't require the system default itself to be editable).

2. Add an exported cascade-resolution function, e.g.:
   ```go
   func EffectiveCooldown(kindDefaultSeconds, reflexOverrideSeconds *int64) time.Duration
   ```
   Semantics: `reflexOverrideSeconds` non-nil wins (including `0` → `time.Duration(0)`, meaning "no cooldown"); else `kindDefaultSeconds` non-nil wins (same zero-means-none rule); else `DefaultReflexCooldown`. Unit-test all three levels explicitly, including the zero-override case (must NOT be treated as "unset" — a nil pointer is unset, a pointer to `0` is an explicit override).

3. Replace `engine.go`'s hardcoded `recentlyFired(r, now, 15*time.Minute)` call with one that resolves the reflex's actual kind (via `01`'s new lookup) and calls `EffectiveCooldown` with that kind's `default_recurrence_seconds` and the reflex row's own `recurrence_override_seconds`. Since kind metadata changes rarely, load `reflex_action_kinds` once (e.g. at `Engine` construction, alongside `NewEngine`) into a small in-memory map rather than querying per-reflex-per-turn — consistent with `docs/engineering/architecture/00-overview.md`'s guiding principle ("hot-reload is worth deliberately considering for anything that changes with meaningful frequency... fine to skip for things that rarely change"). If you want a manual refresh hook for when an operator edits `reflex_action_kinds` (not currently editable via any API — `01` deliberately didn't build CRUD for it), a simple `Engine.RefreshActionKindCache(ctx)` method is enough; don't over-build this.

4. Wire an actual (currently-always-zero, but real) cooldown check into `attemptReflexDispatch` and `matchDispatchToAgentReflex` using the same `EffectiveCooldown` function, so a future non-zero `recurrence_override_seconds` set on a specific `dispatch_to_agent` reflex row genuinely takes effect at both call sites, not just in theory. Concretely: before treating a fired `dispatch_to_agent` candidate as a winner, check `EffectiveCooldown(...)` against `LastFiredAt` the same way `recentlyFired` does — reuse that helper (export it if it isn't already, or factor a small shared function both `engine.go` and the two call sites can call) rather than reimplementing the time-window comparison a third time.

5. Update `chat_reflex_dispatch.go`'s header comment (design note 2) to reflect the corrected reasoning: the debounce-bypass is no longer "we skip the pipeline to dodge its behavior," it's "the kind-level default is seeded at `0`, and this call site now applies that same cascade explicitly." Keep the real remaining reason (import-cycle + live in-flight `State` construction `StateCollector` can't do) intact and don't remove or weaken that part of the comment — it's still true and still the reason two call sites exist.

6. Do not touch `fired_count`/`last_fired_at` bump *timing* or telemetry emission in this task — that's `06-unified-reflex-telemetry.md`. This task only changes *eligibility* (does a fired trigger get suppressed by cooldown), not what happens once something is allowed to fire.

## Done means

- `EffectiveCooldown` (or equivalently named/shaped function) is exported from `internal/agent/reflexes`, unit-tested for all three cascade levels including the explicit-zero-override case.
- `EvaluateState`'s debounce reads from the cascade; a fixture test confirms the five non-`dispatch_to_agent` kinds still debounce at the system default (15 min) unchanged, and a kind- or reflex-level override actually changes the observed debounce window in a test.
- `attemptReflexDispatch` and `matchDispatchToAgentReflex` both apply a real cooldown check via the same shared function. A regression test proves: setting a non-zero `recurrence_override_seconds` on a specific `dispatch_to_agent` reflex row suppresses re-firing within that window, at both call sites independently.
- No change in observed behavior for any existing reflex under default settings (every current row's `recurrence_override_seconds` is `NULL` post-migration, so nothing's cooldown actually changes) — confirm via existing test suite passing unmodified plus a new before/after comparison note in the Work Log.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

<!-- Worker fills in as it goes. -->

## Review notes

<!-- Reviewer fills in. -->
