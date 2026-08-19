# Develop `registers.panels[]` (backend/manifest half only) and `registers.crud[]` (generic CRUD resource handlers)

**Phase:** 5
**Status:** implemented
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

**Scope respected.** Per the 2026-08-19 scope correction banner, this task built `registers.crud[]` only. No `ui/` file was touched, no `.tsx` file was edited, and `npm run build` was never run.

### `panels[]` — confirmed already fully wired, nothing built

Read `internal/plugin/panels.go` in full as instructed. `registerManifestPanels` (called from `applyManifestRegistrations` at the `reg.Panels` branch) is genuinely complete on the backend/manifest side: it validates each `panels[]` entry (`ID` required), defaults `Order` to 100+ for plugin panels, builds a `PanelEntry`, and calls `host.RegisterPanel`. `panelRegistry` (same file) implements the tier=0 (builtin, registered at host startup) vs. tier=1 (plugin, registered at load time) precedence the architecture doc describes, plus `removeByPlugin` (unload sweep) and `GetPanels` (snapshot for the registry endpoint). There is no backend gap independent of rendering — the only missing piece is the frontend render function, which is out of scope per the correction. No escalation needed; this matches the "expected finding" the task file predicted.

### `crud[]` — design and what was actually built

**The real starting state was better than `registrations.go`'s stub comment suggested.** The stub at `registrations.go:363-366` (`"manifest crud: yaml-driven registration deferred to B.5/B.6 proxy work"`) implied no proxy machinery existed at all. In fact the full CRUD *handler* proxy mechanism was already built and just never wired to the manifest path:

- `internal/plugin/host.go`'s `Host.RegisterCRUDHandler(resourceType, handler)` already auto-wires a complete REST route set — `GET /api/plugins/{resource}` (list), `POST /api/plugins/{resource}` (create), `GET/PUT/DELETE /api/plugins/{resource}/{id}` (read/update/delete) — into the host's mutable plugin mux, and the unload sweep (`host.go:1444-1446`) already removes a plugin's CRUD handlers on unload.
- `internal/plugin/crud.go` already implements the five HTTP handlers (`handleCRUDList/Create/Read/Update/Delete`) that call into a `plugin.CRUDHandler` interface (`Create/Read/Update/Delete/List`, from `plugin-sdk` v0.3.0).
- `internal/plugin/subprocess/plugin.go` already had an *unexported* `subprocessCRUDHandler` implementing `plugin.CRUDHandler` by proxying each of the five operations over the plugin's JSON-RPC transport (`MethodCRUDCreate/Read/Update/Delete/List` — also already present as protocol constants in `subprocess/protocol.go`, backed by real wire types `CRUDParams`/`CRUDResult`/`CRUDListResult` in `plugin-sdk`). Grepping the whole tree confirmed `subprocessCRUDHandler{...}` was never constructed anywhere — genuinely dead code, exactly matching the architecture doc's "also unwired" framing, just further along than the registrations.go comment implied.

So the actual gap was narrower than "build a generic CRUD resource-handler registration from scratch": it was **wiring `registers.crud[]` manifest entries to the handler proxy and host registry that already existed**, following the exact same pattern every other subprocess-only registration category in `registrations.go` already uses (commands, events, http_routes, mcp_servers — each type-asserts `p.(*subprocess.SubprocessPlugin)`, no-ops with a log for builtins since builtins register directly from `Load()`, and wires the subprocess path via `sp.Transport()`).

**What was built:**
1. `internal/plugin/subprocess/plugin.go`: exported `NewCRUDHandler(resourceType string, transport *Transport) plugin.CRUDHandler` — a free function (mirrors the existing `NewEventHook` shape) that constructs the already-existing `subprocessCRUDHandler`. This is the only production surface needed to make the dead proxy reachable from the parent `plugin` package.
2. `internal/plugin/registrations.go`: replaced the `reg.Crud` deferred-skip stub with a call to a new `registerManifestCrud(host, pluginID, reg.Crud, p)`, matching `registerManifestHTTPRoutes`/`registerManifestMCPServers` in structure — type-asserts for `*subprocess.SubprocessPlugin`, no-ops with a log for builtins, errors if the transport isn't ready or a `resource` field is empty, and otherwise calls `host.RegisterCRUDHandler(entry.Resource, subprocess.NewCRUDHandler(entry.Resource, transport))` per declared resource. Also corrected the stale doc comment above `applyManifestRegistrations` that still listed `crud` among the categories "logged as TODO and deferred to B.5/B.6."
3. **Deliberate non-decision, logged for the record**: `CRUDRegistration.Methods` (`[]string`, e.g. `["list","create"]`) is accepted by the manifest schema but not enforced — `Host.RegisterCRUDHandler` has no per-method opt-out and always wires all five routes. A plugin declaring `methods: [list]` still gets all five routes registered; its own `CRUDHandler` implementation is free to reject unsupported operations from the plugin side. Narrowing this is a reasonable follow-up if a real consumer needs it, but building selective route registration into the host for a field no consumer yet exercises would be speculative complexity — noted here rather than silently built or silently ignored.

### Test-plugin consumer (mechanism verification)

No plugin in this repo (builtin or subprocess) declares `registers.crud[]` yet — confirmed via `grep -rl "crud:" **/plugin.yaml` across the whole tree (zero matches; the only builtins present are `adapter-*`, `agentwidgets`, `bookmarks`, `card-rules-demo`, `contextwidgets`, `debugwidgets`, `observabilitywidgets`, none of which declare `crud`). Per the task's explicit instruction, built a minimal test-plugin consumer rather than leaving the mechanism unverified:

- `internal/plugin/subprocess/plugin.go`: added `NewSubprocessPluginForTest(id string, transport *Transport) *SubprocessPlugin` — an exported, test-only constructor that bypasses the real `Load()` handshake (which spawns an actual subprocess) so tests outside the `subprocess` package can exercise a genuine `*SubprocessPlugin` against an in-process transport. This mirrors the existing `internal/plugin`-package precedent of `UnregisterPluginForTest` (an exported-but-test-only symbol in a non-`_test.go` file). It was necessary because `registerManifestCrud` (like every other subprocess-only registration function in `registrations.go`) does a concrete type assertion on `*subprocess.SubprocessPlugin`, which no interface-based fake can satisfy from another package.
- `internal/plugin/registrations_test.go`: added `TestApplyManifestRegistrations_Crud_Subprocess`, a genuine end-to-end test — builds a manifest declaring `registers.crud: [{resource: things, methods: [list, create, read, update, delete]}]`, runs it through the real `applyManifestRegistrations` against a `*subprocess.SubprocessPlugin` wired to an in-process-pipe mock "plugin" (same technique the pre-existing `TestNewSubprocessHTTPHandler` uses for `http_routes`), then dispatches real HTTP requests through the actual `*http.ServeMux` (the same core-router → `pluginMux` forwarder path production traffic uses) for all five REST routes and asserts each round-trips correctly to the mock plugin's canned JSON-RPC responses. This also incidentally proves the double-mux forwarder preserves Go 1.22 `{id}` path-value extraction for `GET/PUT/DELETE .../{id}}`, which had no prior test coverage.
- Adjusted the existing `TestApplyManifestRegistrations_DeferredCategoriesNoOp` test's comment (it still passes unchanged behaviorally) to stop describing `crud` as "still requires proxy scaffolding" and to clarify only `AgentProfiles` remains genuinely deferred — `Commands`/`Events`/`Crud`/`HttpRoutes`/`McpServers` all correctly no-op for a *builtin* plugin (`fakePlugin`, not a subprocess) because builtins register those directly from their own `Load()`.

### Baseline checks

- `go build ./cmd/nanite/` — passes.
- `go vet ./...` — two pre-existing, unrelated failures in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` "not used on all paths" warnings) confirmed present on this worktree's base commit before any of this task's changes (verified via `git stash` + re-run). Nothing in `internal/plugin/*` flagged.
- `go test ./...` — all packages pass, including the new `internal/plugin` CRUD test and the full `internal/plugin/subprocess` suite.

### Concurrency note

Per the dispatch brief, `TASKS/phase-5/03-wire-registers-agent-profiles.md` runs concurrently in a separate worktree and also touches `internal/plugin/registrations.go` (the `agent_profiles[]` stub, a different branch of the same function/file). No live collision occurred since this task ran in its own isolated worktree; the merge step will need to reconcile both diffs against `applyManifestRegistrations` and `TestApplyManifestRegistrations_DeferredCategoriesNoOp` (both files/functions are touched by both tasks, on non-overlapping branches — `Crud` here, `AgentProfiles` there). Nothing escalated.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
