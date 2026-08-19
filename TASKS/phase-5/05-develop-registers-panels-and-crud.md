# Develop `registers.panels[]` (backend/manifest half only) and `registers.crud[]` (generic CRUD resource handlers)

**Phase:** 5
**Status:** not-started
**Depends on:** none
**Touches:** `internal/plugin/registrations.go:340-343` (`crud[]` deferred-skip stub), `internal/plugin/config.go` (`PanelRegistration{ID, Title, DefaultVisible, Icon, Order, Description}`, only if the backend/manifest schema itself needs a field this task's `crud[]`-equivalent design surfaces a gap in)

## ⚠️ Scope correction, 2026-08-19 (Orchestrator, applying the same standing operator instruction already banner'd on `TASKS/phase-5/01-build-assignment-api.md`)

**No frontend work is part of any phase — this task's original file (below, kept for context) asked for `registers.panels[]`'s rendering half, which is real frontend work (a "generic frontend component" + `cd ui && npm run build` in the original Done means) and directly contradicts that standing instruction. It also contradicts `docs/engineering/TASKS.md`'s own already-corrected Phase 5 text, which explicitly says: "`registers.panels[]`'s rendering half is frontend, deferred to the separate frontend pass, not this phase."** This task file was evidently not updated when that correction landed elsewhere (task `01` got its own banner for the same class of issue) — treating this as the same known contamination pattern, not a new design decision.

**This task's real, corrected scope is `registers.crud[]` only.** `registers.panels[]`'s registration/manifest path is already fully wired per this file's own Context section below (`registrations.go:371-381`'s `registerManifestPanels`, already calling into the host's panel registry) — there is no remaining backend gap to close for `panels[]` once its frontend rendering half is correctly excluded. Do not build any frontend component, do not touch anything under `ui/`, and do not run `npm run build` as an acceptance check. If you find yourself about to edit a `.tsx` file, stop — that's not this task's job in this phase.

---

*(Original file content preserved below for context on why `panels[]` registration is already wired — read it, but its `panels[]` rendering instructions are superseded by the correction above.)*

## Context

TASKS.md Phase 5: *"Develop `registers.panels[]` and `registers.crud[]` (not urgent)."* Architecture doc `09-plugin-system.md`: *"`registers.panels[]` (right-rail tab entries — currently registers but the render function is a placeholder) and `registers.crud[]` (generic CRUD resource handlers — also unwired) are both real and worth developing, neither urgent. Build when there's a first real consumer or genuine downtime."*

Per TASKS.md's own "not urgent" framing and the kickoff's scope guidance for lighter-weight items, this task file is deliberately less exhaustive than this phase's other entries — real enough to hand to a worker, not forensic-depth research for something with no committed timeline.

### `panels[]` — more built than a one-line summary suggests

Panel *registration* is real and already wired: `registrations.go:371-381` calls `registerManifestPanels` when `len(reg.Panels) > 0`, registering plugin panels into the host's panel registry at tier=1 (built-ins are tier=0, plugins can't override built-in panel IDs). The gap is specifically panel *rendering* — the host's own comment confirms: *"the render function is a placeholder in v1 (plugin panel rendering is deferred to a follow-up ticket)."* A plugin panel shows up in the registry (and presumably the right-rail tab list) but has no real content behind it yet.

### `crud[]` — a clean stub, confirmed fully unwired

Same deferred-skip pattern as `registers.agent_profiles[]` (`10`): `registrations.go:340-343`, `host.logger.Info("manifest crud: yaml-driven registration deferred to B.5/B.6 proxy work", ...)`. Nothing to partially build on — a real greenfield build once picked up.

## What to do

1. **`panels[]`**: NOT this task's job (see the 2026-08-19 scope correction above) — its registration/manifest half is already fully wired, and its rendering half is frontend, deferred to the separate frontend pass. Do not build anything for `panels[]` in this task. If, while reading `registrations.go:371-381` to orient yourself, you find the backend registration path is genuinely incomplete in some way that has nothing to do with rendering (a real gap, not the already-known rendering placeholder), note it in the Work Log and escalate rather than silently building past it — but the expected finding is "already wired, nothing to do here."
2. **`crud[]`**: design and build the generic CRUD resource-handler registration — a plugin declares a resource shape and gets automatic REST CRUD routes wired through the host, following the same manifest-driven pattern the rest of the plugin system already uses. This is this task's real, actionable scope.
3. Given the explicit "not urgent" framing, if no real first consumer exists for `crud[]` by the time this task is dispatched, build a minimal test-plugin consumer to prove the mechanism works, rather than leaving it unverified.

## Done means

- `panels[]`'s backend/manifest registration path is confirmed already-wired (a Work Log statement, not new code) — no frontend rendering work attempted.
- A real (or test) plugin's declared CRUD resource shape produces working REST routes through the host.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass. No `ui/` file touched; `npm run build` is not part of this task's acceptance bar.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
