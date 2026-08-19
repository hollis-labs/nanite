# Develop `registers.panels[]` (rendering) and `registers.crud[]` (generic CRUD resource handlers)

**Phase:** 5
**Status:** not-started
**Depends on:** none
**Touches:** `internal/plugin/registrations.go:340-343` (`crud[]` deferred-skip stub), `:371-381` (`registerManifestPanels` — the real, already-wired panel *registration* path), plugin panel rendering (frontend — the missing half, locate during implementation), `internal/plugin/config.go` (`PanelRegistration{ID, Title, DefaultVisible, Icon, Order, Description}`)

## Context

TASKS.md Phase 5: *"Develop `registers.panels[]` and `registers.crud[]` (not urgent)."* Architecture doc `09-plugin-system.md`: *"`registers.panels[]` (right-rail tab entries — currently registers but the render function is a placeholder) and `registers.crud[]` (generic CRUD resource handlers — also unwired) are both real and worth developing, neither urgent. Build when there's a first real consumer or genuine downtime."*

Per TASKS.md's own "not urgent" framing and the kickoff's scope guidance for lighter-weight items, this task file is deliberately less exhaustive than this phase's other entries — real enough to hand to a worker, not forensic-depth research for something with no committed timeline.

### `panels[]` — more built than a one-line summary suggests

Panel *registration* is real and already wired: `registrations.go:371-381` calls `registerManifestPanels` when `len(reg.Panels) > 0`, registering plugin panels into the host's panel registry at tier=1 (built-ins are tier=0, plugins can't override built-in panel IDs). The gap is specifically panel *rendering* — the host's own comment confirms: *"the render function is a placeholder in v1 (plugin panel rendering is deferred to a follow-up ticket)."* A plugin panel shows up in the registry (and presumably the right-rail tab list) but has no real content behind it yet.

### `crud[]` — a clean stub, confirmed fully unwired

Same deferred-skip pattern as `registers.agent_profiles[]` (`10`): `registrations.go:340-343`, `host.logger.Info("manifest crud: yaml-driven registration deferred to B.5/B.6 proxy work", ...)`. Nothing to partially build on — a real greenfield build once picked up.

## What to do

1. **`panels[]`**: build the actual rendering mechanism — likely a generic frontend component that fetches/renders whatever a registered panel declares (a URL, a component reference, or a data-driven schema, depending on what `PanelRegistration`'s fields support today vs. what's needed). Verify with at least one real or test plugin panel rendering live content in the right rail.
2. **`crud[]`**: design and build the generic CRUD resource-handler registration — a plugin declares a resource shape and gets automatic REST CRUD routes wired through the host, following the same manifest-driven pattern the rest of the plugin system already uses.
3. Given the explicit "not urgent" framing, this task may reasonably be picked up only when a real first consumer exists — if no real consumer is identified by the time this task is dispatched, build a minimal test-plugin consumer for each to prove the mechanism works, rather than leaving it unverified.

## Done means

- A real (or test) plugin panel renders live content in the right rail, not a placeholder.
- A real (or test) plugin's declared CRUD resource shape produces working REST routes through the host.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass; `cd ui && npm run build` passes.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
