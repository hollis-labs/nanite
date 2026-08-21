# Plugin System

The primary mechanism for adding custom use-case logic, app-specific integrations, custom agents, and providers without a core code change. Genuinely well-architected — a single YAML-manifest-driven registration path (`applyManifestRegistrations`) treats compiled-in (**builtin**) and separate-process (**subprocess**) plugins identically, with a real signed install/verify/stage/commit state machine and a proper unload sweep across every registry a plugin can touch.

See also [25-plugin-conformance-harness.md](25-plugin-conformance-harness.md) — a follow-up design for executable contract tests against a real spawned plugin binary. Its "forbidden capabilities" coverage depends directly on this doc's target capability model (below) landing first, via the queued `TASKS/plugin-system/04`–`06` batch.

## What's real and already deep

**Hooks/filters reach further into the core than a first read of the plugin registration table suggests.** A real filter chain spans the whole turn loop: `FilterUserMessage` → `FilterSystemPrompt` → `FilterContextWindow` → `FilterToolSelection` → `FilterToolResult` → `FilterAssistantResponse` → `FilterEnvelopeData`. Reflexes have their own `FilterReflexState`/`FilterReflexAction` and `EmitReflexFired`/`EmitReflexActionStaged`. Plus a real spread of lifecycle events (session archived, agent switched, artifact created, bookmark changed, config changed). Comprehensive — no known gap in filter coverage.

**Closed**: the former gap — a filter for tool *results* (post-execution) but none for tool *selection* — was closed by `FilterToolSelection` (`internal/plugin/filter.go`, wired at `internal/service/chat_generate.go`'s `applyToolSelectionFilter`, right after `ToolService.SelectForAgent` resolves the final tool set; see `TASKS/phase-4/06-add-filter-tool-selection.md`). A plugin can now add, remove, or reshape which tools get offered to the model for a turn.

**Hot-reload is real** (`POST /api/plugins/reload`, dev-mode `window.__nanite_reloadPlugin`) — genuinely avoids a restart. **Closed**: the CLI-install/hot-reload asymmetry this section used to flag as an open decision was resolved by `TASKS/phase-5/04-close-cli-install-hot-reload-asymmetry.md` (routed CLI install/update/enable for subprocess plugins through the same reload path, `cmd/nanite/plugin_cmd.go`'s `triggerActivation`), then made fully load-bearing by `TASKS/phase-5/11-fix-hot-reload-never-applies-manifest-registrations.md` (the API-driven path wasn't actually running `applyManifestRegistrations` either, until this landed) and `TASKS/phase-5/12-fix-unload-plugin-wrong-identifier.md` (a reload-of-a-reload bug the `11` fix newly exposed). All three are `reviewed`. Two exceptions remain, both intentional, neither a gap: a **builtin** install/update still restarts (structurally required — it's compiled-in Go code); `uninstall`/`disable` still restart for both plugin kinds (by the time restart fires, the manifest `handleReload` needs is already gone, so wiring these to hot-reload would either 404 or silently reactivate a disabled plugin).

## Middleware — exists, but only at the HTTP layer, and not plugin-extensible

`internal/server/server.go` has a real, standard `net/http` middleware chain (recover → logging → CORS → auth → caller-identity → body-limit). **Decision: don't build a second, general "middleware" concept** — the turn-loop's filter/hook system already functionally serves that role for the chat-turn domain, and a second name for the same pattern would recreate exactly the naming-collision problem this whole review exists to fix. Instead: **make the existing HTTP middleware chain plugin-extensible** — today a plugin can register new routes but can't inject into the chain wrapping every request. Complementary to the filter/hook system, not a replacement.

**Shape decision: builtins only, priority-ordered.** This chain wraps every request including auth, so it follows the capability model's trust split below rather than the CRUD/MCP/events precedent — only Tier 1 (builtin) plugins can register into it, mirroring the filter chain's priority-ordered registration pattern. Subprocess plugins keep using manifest-declared routes only; no chain injection for Tier 2, ever — auth-wrapping middleware is too high-stakes a surface to extend to less-trusted code via a capability grant.

## Since wired up

**`registers.agent_profiles[]`** is now live (`internal/plugin/agent_profiles.go`, invoked from `registrations.go`) — the seam [Agent Construction](01-agent-construction.md) needed for plugin-provided agents to be a real source. Loom's agents route through this path.

**Builtin plugin enable/disable** is now DB-backed (`plugins` table, migration `121_plugins_installed_enabled_state.sql`) — installed/enabled as separate flags, WordPress-style, working uniformly across builtin and subprocess plugins via GUI/CLI/API. The old file-rename mechanism (subprocess-only, silently inapplicable to compiled-in builtins) is fully retired; a one-time migration shim restores any leftover `plugin.yaml.disabled` state into the new model.

**`registers.crud[]`** (generic CRUD resource handlers) is now wired (`registerManifestCrud`) — no longer deferred.

**`registers.panels[]`** (right-rail tab entries) still only has its registration half wired — the render function remains a placeholder. Registration/manifest/backend is done; the render half is frontend work, deferred to the separate frontend pass.

## Target design: capability model

No declared/granted/used capability system exists today. Builtin plugins get the SDK's `Host` interface (advisory, 13 methods) but nothing stops a type-assertion to the concrete `*Host` struct (~53 exported methods) or an unscoped `GetService("store")`/`GetService("mcp")` call returning the raw `*sql.DB` or the full MCP manager. Subprocess plugins are isolated, but only as a side effect of the JSON-RPC wire vocabulary being narrow, not because of any grant/deny negotiation. A prior internal audit (`docs/audits/2026-04-11-plugin-capability-model/`) mapped the full gap in detail (findings 01–13, capability map in `14-capability-map.md`) and concluded the architecture already has a de facto two-tier trust model — this section formalizes it as the target, feature-complete design.

**Decision: keep the two-tier model, don't build a uniform per-plugin capability system.** Builtins stay at the same trust tier as core code (an explicit, already-made design choice — reviewed at PR time, not runtime); only subprocess plugins get capability enforcement. An untrusted-but-in-process tier (sandboxed builtins) is out of scope here — it's the audit's own suggested follow-up (`plugin-sandbox-design`), pursued only if a real need for it shows up.

- **Trust tier is structural, not self-declared.** The `plugins` table already has a `kind` column (`builtin`/`subprocess`, migration `121_plugins_installed_enabled_state.sql`) set by how a plugin is loaded, not by anything in its own `plugin.yaml`. That column *is* the trust tier — a plugin can't claim builtin trust for itself.

- **Tier 1 (builtin) gets scoped service proxies, not enforcement.** Replace raw `GetService("store")`/`GetService("mcp")` with narrow, typed proxies (`PluginStore`, `PluginMCPClient` — resolves audit findings 01, 03, 07). This is blast-radius hygiene, not a security boundary: a builtin is still trusted and could still reach further via type assertion if it tried. The point is that an ordinary, non-adversarial builtin plugin no longer *casually* gets the raw `*sql.DB` just by typing `"store"`.

  **`PluginStore`'s concrete shape: scoped SQL under a host-managed prefix.** Not a KV/JSON downgrade — plugins keep real SQL, but confined to tables under a `plugin_<id>_*` namespace that the host creates and migrates on the plugin's behalf, folding plugin schemas into the real migration system instead of ad hoc, unmanaged `InitSchema`-style calls against the raw DB. Resolves the audit's noticed-but-out-of-scope finding about builtins creating their own unmanaged SQLite schemas.

- **Tier 2 (subprocess) gets a manifest `capabilities` declaration, enforced twice.** A plugin lists what it needs (MCP tools, CRUD resources, event types) in `plugin.yaml`. Enforcement happens at two points:
  - *Install-time gate*: the operator sees the declared list and approves it (WordPress-style permission prompt), recorded alongside the existing installed/enabled state — extends the install state machine (`internal/plugin/install/`) and the `plugins` table rather than replacing either.
  - *Live, per-call, at the RPC-proxy layer*: each existing subprocess proxy (`NewCRUDHandler`, `CallTool`, `NewEventHook`, `newSubprocessHTTPHandler`) checks the requested resource/tool/event against the plugin's granted set before forwarding, instead of trusting the wire vocabulary's narrowness alone. This is where CRUD-route scoping (audit finding 05 — no plugin-scoped auth today) and event-hook plugin ownership (finding 06 — needed anyway for unload cleanup) both get resolved as instances of the same mechanism rather than one-off fixes.

- **"Used" is an audit log, not an enforcement point.** Record actual capability invocations per plugin for over-declaration hygiene and incident forensics. Enforcement already happens via the granted-set check above; usage tracking is observability on top, not blocking.

**Explicitly out of scope for this design** (adjacent hardening from the same audit, tracked separately, not folded in): pre-hook cancellation DoS (finding 09), subprocess entrypoint argument injection (finding 10), per-plugin OS-level resource limits (finding 12), sandboxing untrusted in-process plugins (`plugin-sandbox-design`, future work only if needed).

**Already closed, not part of this batch's remaining scope: panic recovery in event hook dispatch (finding 04).** The audit's proposed fix — wrapping `Host.EmitEvent` and `EmitPreHook`'s hook dispatch in `recover()` — landed the day after the audit, in commit `ce40fbcf7` (2026-04-12, the repo-wide `safego` adoption sweep, `TASK-013`/`TASK-014`), well before this design review. Both dispatch paths (`internal/plugin/host.go`'s `EmitEvent`, `internal/plugin/events.go`'s `EmitPreHook`) now go through `safego.Go`/`safego.Call`, confirmed live by a real dogfeed-caught panic (`TASKS/INDEX.md`'s Phase 4 validation note) and a dedicated regression test (`internal/plugin/host_panic_test.go`).

## Target design: registration atomicity

Today, `LoadDiscovered` starts a subprocess plugin (spawns the process, runs the `plugin/init`/`plugin/load` handshake) *before* `applyManifestRegistrations` runs its conflict checks (envelope/command/route/CRUD collisions, checked inline per-category during registration, not as a separate pre-flight phase). Failure still rolls back cleanly via `UnloadPlugin`, so this isn't a live-state-corruption risk — but it doesn't match the handoff doc's intended pipeline order (discover → parse → validate → resolve deps → **build plan → conflict/policy validation** → start/handshake → atomic commit), and it means a plugin that's guaranteed to fail registration still pays for a full process spawn before finding out.

**Decision: add a pre-flight validation pass before handshake.** Dry-run the conflict/collision checks against the manifest (and see below — the compat-range check belongs in this same pass) before the loader ever spawns a subprocess or calls a builtin constructor. Bounded, mechanical change on top of the existing loader — the registration logic itself doesn't change, just when the conflict-detection half of it runs.

**Folded into the same pre-flight pass: `NaniteCompat` enforcement.** `NaniteCompat{Min,Max}` (`internal/plugin/config.go`) is parsed from every manifest today but never checked against the running host version anywhere in the loader. Add the check here: refuse to load a plugin whose declared compat range excludes the current host version, surfaced as a normal pre-flight validation failure rather than a separate mechanism.

## Frontend trust boundary — accepted limitation, not designed here

The two-tier trust model above governs backend host-API access only. A plugin's frontend bundle (`registers.components`, panels, envelope renderers — `UI ManifestUI` bundle dir/entry/stylesheet) runs as ordinary JavaScript in the host page, with no sandboxing (no iframe/postMessage boundary, no CSP or Trusted Types restriction) — full DOM, cookie, and fetch access regardless of the plugin's backend tier. A Tier 2/subprocess plugin's backend calls are capability-scoped; its UI bundle, if it has one, is not isolated at all.

**Decision: document as an accepted limitation, don't design isolation now.** State plainly: frontend plugin code always runs at full trust in the host page, independent of backend tier. No current plugin needs an isolated frontend, and iframe/CSP/Trusted-Types work is real, speculative scope with no consumer today. Revisit only if a genuine untrusted-frontend use case shows up.

## Cut

`trigger_rules` (an early version of what became reflexes) and `custom_actions` (meant to be slash-command-triggered UI actions) — both zero-caller, zero-row. Whatever real need either was reaching for is served by reflexes and the existing, real plugin `commands[]` registration going forward.

The support-ticket reference plugin — extensively documented but no source or manifest exists anywhere in this workspace. Confirmed a demo; doc references get cleaned up alongside the Cards cuts.
