# Enforce granted capabilities at the subprocess RPC-proxy layer

**Phase:** 3 — Tier 2 capability model: subprocess plugins (`TASKS/plugin-system`)
**Status:** not-started
**Depends on:** `04` (granted-capability storage). Parallel-safe with `05` — disjoint files
(`05` touches `internal/plugin/install/`; this task touches the four proxy call sites below).
**Touches:** `internal/plugin/subprocess/plugin.go` (`NewCRUDHandler`, `CallTool`,
`NewEventHook`), `internal/mcp/plugin_transport.go` (`PluginMCPTransport.CallTool`),
`internal/plugin/registrations.go` (`newSubprocessHTTPHandler`, `registerManifestCrud`,
`registerManifestEvents`, `registerManifestHTTPRoutes`), `internal/plugin/host.go`
(`UnloadPlugin`'s event-hook cleanup — the audit's finding 06, folded in here per the
architecture doc), the `plugin.EventHook` SDK interface (extend with `PluginID()`).

## Context

`docs/engineering/architecture/09-plugin-system.md`: **"Live, per-call, at the RPC-proxy
layer: each existing subprocess proxy (`NewCRUDHandler`, `CallTool`, `NewEventHook`,
`newSubprocessHTTPHandler`) checks the requested resource/tool/event against the plugin's
granted set before forwarding, instead of trusting the wire vocabulary's narrowness alone.
This is where CRUD-route scoping (audit finding 05) and event-hook plugin ownership (finding
06 — needed anyway for unload cleanup) both get resolved as instances of the same mechanism
rather than one-off fixes."**

This planning session's own research independently located and confirmed all four proxy call
sites — all four of the doc's guessed names are exact matches to real code, and **all four are
unconditional today, zero authorization checks anywhere**:

- **(a) CRUD** — `subprocess.NewCRUDHandler` (`internal/plugin/subprocess/plugin.go:641`),
  returning a `*subprocessCRUDHandler` whose `Create`/`Read`/`Update`/`Delete`/`List`
  (`plugin.go:654-725`) each unconditionally forward via `CallResult[...]`. Wired from the
  manifest via `registerManifestCrud` (`internal/plugin/registrations.go:569-590`), also
  unconditional. Note: `CRUDRegistration.Methods` (`config.go:257`) is already
  declared-but-unenforced (registrations.go:561-568 explicitly documents this) — this task's
  enforcement should subsume that field's intent rather than leave two parallel
  "which-CRUD-verbs" mechanisms.
- **(b) Tool calls** — two layers, confirm you're gating the one actually used for a
  subprocess plugin's declared tool dependencies: `SubprocessPlugin.CallTool`
  (`internal/plugin/subprocess/plugin.go:362`, documented in its own comment as "the
  single-server path") vs. `PluginMCPTransport.CallTool`
  (`internal/mcp/plugin_transport.go:77`) — **the latter is the one actually used for a
  subprocess plugin's declared `mcp_servers[].tools`** per the research; verify this against
  current callers before assuming which one to gate, don't gate the wrong layer.
- **(c) Event hooks** — `subprocess.NewEventHook` (`internal/plugin/subprocess/plugin.go:531`),
  whose `Handle` (`plugin.go:549-620`) unconditionally forwards every dispatched event of a
  subscribed type. The `filter`/`envelopeFilter` parameter here is an **envelope schema
  validator** (shape-checks emitted envelopes against declared schemas) — not an authorization
  gate; don't mistake it for one or assume it already does part of this job.
- **(d) HTTP routes** — `newSubprocessHTTPHandler`
  (`internal/plugin/registrations.go:680`), which enforces a request-body size cap
  (`maxPluginHTTPBodyBytes`, a DoS guard) but no authorization check, wired unconditionally via
  `registerManifestHTTPRoutes` (`registrations.go:645-672`).
- **Finding 06's event-hook cleanup gap, confirmed still broken and exactly as the audit
  described**: `plugin.EventHook` has no `PluginID()` method; `UnloadPlugin`'s cleanup loop
  (`internal/plugin/host.go`, per the audit's original citation ~`L1042-1052`, re-verify exact
  current line at implementation time) rebuilds the hooks slice for each event type but keeps
  every hook — removing nothing, since it has no way to tell which hook belongs to the plugin
  being unloaded. The **working contrast pattern already in this codebase**:
  `FilterRegistry.RemoveByPlugin(pluginID string) int` (`internal/plugin/filter.go:179-196`,
  confirmed still present and functioning — iterates every named filter chain under lock,
  filters out entries matching the plugin ID, returns a removed count). Follow this exact
  pattern for event hooks: extend `plugin.EventHook` with a `PluginID()` method (or wrap hooks
  in an owner-tracking struct, matching `filterEntry`'s shape), then give `Host` a
  `RemoveEventHooksByPlugin(id)` mirroring `RemoveByPlugin`.
- **Enforcement should be a straightforward "is this resource in the plugin's granted set"
  check** against `04`'s storage — no new authorization framework needed, this is a lookup
  against data `04`/`05` already populate. Gate only ever applies to `kind='subprocess'`
  plugins (`plugins.kind`, migration `121`) — builtins are never subject to this, per the
  two-tier trust model; if you find yourself writing a check that could ever apply to a
  builtin, that's a sign the gate is in the wrong place.

## What to do

1. Extend `plugin.EventHook` (the SDK interface) with a `PluginID() string` method (or the
   equivalent owner-tracking wrapper, matching `filterEntry`'s pattern in `filter.go:76-81`).
   Update `subprocessEventHook` (and any other `EventHook` implementation) to carry and return
   the owning plugin's ID.
2. Add `Host.RemoveEventHooksByPlugin(id string) int` mirroring
   `FilterRegistry.RemoveByPlugin` exactly (same locking discipline, same "iterate every event
   type's slice, filter, return count" shape). Wire it into `UnloadPlugin`'s cleanup path,
   replacing the current no-op loop. This closes finding 06 independent of — but using the
   same mechanism as — the capability check itself.
3. At each of the four proxy points (a-d above), before forwarding: look up the calling
   plugin's granted capability set (`04`'s storage, keyed by `plugin_id` + resource kind +
   resource identifier) and reject with a clear, specific error (naming the plugin and the
   specific resource it wasn't granted) if the requested resource/tool/event/route isn't in
   the granted set. For (c) specifically, use the same `PluginID()` plumbing from step 1 to
   identify which plugin's hook is firing.
4. Confirm which of `SubprocessPlugin.CallTool` vs. `PluginMCPTransport.CallTool` is the real
   production path for a subprocess plugin's declared tool capability before gating — trace
   actual current callers, don't assume from the doc's naming alone (the research flagged this
   as needing verification, not as settled).
5. Builtins never pass through any of these four proxy points (they're subprocess-only
   mechanisms by construction — `NewCRUDHandler`/`NewEventHook`/etc. are all in
   `internal/plugin/subprocess/` or explicitly subprocess-branch code in `registrations.go`) —
   confirm this remains true after your changes; don't accidentally route a builtin's
   registration through a newly-gated code path.

## Done means

- A subprocess plugin's CRUD/tool-call/event-hook/HTTP-route request for a resource **not** in
  its granted set is rejected with a clear error — verified by four negative tests, one per
  proxy point.
- The same requests **succeed** when the resource **is** granted — verified by four positive
  tests, confirming the gate doesn't just fail-closed everything.
- Unloading a plugin removes exactly its own event hooks, verified by a test asserting another
  plugin's hooks for the same event type survive the unload (regression test for finding 06,
  matching `FilterRegistry.RemoveByPlugin`'s existing test coverage shape if one exists, or
  written fresh to the same standard).
- `CRUDRegistration.Methods`'s previously-inert declaration is now either genuinely enforced by
  this mechanism or explicitly superseded by it — no lingering dead field pretending to gate
  something it doesn't.
- Builtin plugins are unaffected — confirmed by a test loading a real builtin plugin and
  verifying its registration path never touches the new grant-check code.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
