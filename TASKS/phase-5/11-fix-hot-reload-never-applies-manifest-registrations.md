# Fix: the API-driven install/enable/reload hot-load path never calls `applyManifestRegistrations`

**Phase:** 5
**Status:** implemented
**Depends on:** `TASKS/phase-5/02-build-plugin-installed-enabled-state-model.md`, `TASKS/phase-5/03-wire-registers-agent-profiles.md`, `TASKS/phase-5/04-close-cli-install-hot-reload-asymmetry.md`, `TASKS/phase-5/05-develop-registers-panels-and-crud.md` (all already landed on `main` — this task fixes a real, pre-existing gap the fresh Phase 5 Reviewer found, now newly load-bearing because of what this phase built on top of it)
**Touches:** `internal/api/plugins.go` (`runPluginLoadIntoHost`), `cmd/nanite/plugin_cmd.go` (`pluginDisable`'s stale comment, `pluginList`'s silent-drop bug)

## Context

Found by the fresh, independent Phase 5 Reviewer (no shared context with the workers), reviewing the merged `2a7e15b4..HEAD` diff, 2026-08-19. The Orchestrator independently re-verified the core finding via direct code trace before writing this task (not just trusting the review). This is three real findings from the same review pass, bundled into one task because they're all in the same small plugin-lifecycle territory and each is individually small — not because they're the same bug.

### Finding 1 (the real one) — hot-reload/install never applies manifest registrations

`applyManifestRegistrations` (`internal/plugin/registrations.go:227`) is the single function every `registers.*` manifest category funnels through — envelopes, components, slots, keybindings, commands, events, `crud[]` (task 05), `http_routes`, `mcp_servers`, `agent_profiles[]` (task 03), card_rules, panels. Confirmed via a full-repo grep: it has exactly two non-test callers, `internal/plugin/loader.go:239` (`LoadDiscovered`) and `internal/plugin/loader.go:386` (`LoadRegisteredBuiltins`) — both invoked only from `cmd/nanite/main.go`'s boot-time `discoverAndLoadPlugins`.

`internal/api/plugins.go`'s `runPluginLoadIntoHost` — the function backing every API-driven plugin lifecycle action (`handleReload`, `handleEnable`, `handleInstallLocal`, `handleInstallRemote`, catalog-install, and — as of `TASKS/phase-5/04-close-cli-install-hot-reload-asymmetry.md` — the CLI's new `triggerHotReload` → `POST /api/plugins/reload` default path for a subprocess plugin install/update/enable) — calls `pms.pluginHost.LoadPlugin(p)` directly and returns. It never calls `applyManifestRegistrations`. `Host.LoadPlugin` (`internal/plugin/host.go:1180`) doesn't call it either — it calls `p.Load(h)` (the plugin's own `Load` method) and its own comment explicitly names `applyManifestRegistrations` as the mechanism that's supposed to separately apply yaml-authoritative registrations, without ever invoking it. `SubprocessPlugin.Load` (`internal/plugin/subprocess/plugin.go:182`) explicitly documents that it does NOT self-apply declarative registrations — "The host applies them from plugin.yaml via applyManifestRegistrations; the plugin no longer returns them on Load."

**Confirmed by direct trace, not just the review's claim** (Orchestrator, before writing this task): the grep above found exactly the two loader.go call sites, nothing else. `runPluginLoadIntoHost`'s own doc comment claims *"loads a plugin into the running host so its agent profiles and MCP tools become available immediately"* — false for agent profiles (`registers.agent_profiles[]`, task 03) specifically, since that requires `applyManifestRegistrations` to run and it never does on this path.

**Practical effect**: a subprocess plugin installed via `nanite plugin install` (task 04's new default, no-restart-needed path) or reloaded via `POST /api/plugins/reload`, declaring `registers.crud[]` (task 05) or `registers.agent_profiles[]` (task 03), spawns successfully and completes its init/load handshake — the CLI prints *"Plugin %q is live — no restart needed"* — but **none of its manifest-declared registrations actually take effect** until a real process restart, which is exactly the scenario task 04 was built to eliminate. This directly contradicts `docs/engineering/architecture/09-plugin-system.md`'s claim that hot-reload "genuinely avoids a restart," and undermines the reachability of tasks 03/05's own new registration categories via the very hot-reload path task 04 just wired the CLI onto.

**Root cause predates Phase 5** — inherited from an earlier build cycle, not new code in this phase's diff — but is newly load-bearing because of what this phase built on top of it (three real registration categories that now depend on this path working, plus task 04's own explicit no-restart deliverable). Closing it serves this phase's own Done-means bar, not a tangential concern.

**Idempotency check already done (Orchestrator, before writing this task)**: `handleReload` (`internal/api/plugins.go:701`) already calls `pms.unloadPluginFromHost(manifestPath)` — a real teardown sweep — *before* `runPluginLoadIntoHost`. This should make the reload path's re-registration safe once the missing call is added (prior registrations are already torn down by the time the new ones would be applied). The fresh-install path (no prior unload) has no pre-existing registration to conflict with either. Confirm this reasoning is actually correct by tracing `Host.UnloadPlugin`'s sweep coverage yourself — don't just trust this note — before implementing.

### Finding 2 — `pluginDisable`'s comment is now factually false

`cmd/nanite/plugin_cmd.go`'s `pluginDisable` (~line 608-615) still reads: *"DisablePlugin already renamed plugin.yaml -> plugin.yaml.disabled, so POST /api/plugins/reload would 404..."* — but `TASKS/phase-5/02-build-plugin-installed-enabled-state-model.md` made `DisablePlugin`/`EnablePlugin` pure DB writes; the file is never renamed anymore (confirmed: `internal/plugin/manage.go`'s own doc comment states the file-rename mechanism is "fully retired"). The Orchestrator's own Phase 5 `#02` merge-fixup commit (`d46af6af`) already caught and fixed the parallel stale comment in `pluginEnable` a few lines below, but missed this one in `pluginDisable`.

Today's behavior (`pluginDisable` keeps `triggerRestart()` unconditional, never hot-reloads) is still *correct* — but for a different, more serious, currently-undocumented reason than the stale comment states: once Finding 1 above is fixed, `POST /api/plugins/reload` would actually apply the plugin's manifest and reload it live — meaning if `pluginDisable` were ever wired onto the hot-reload path (a plausible future "fix" by someone trusting the current, now-doubly-stale comment), a disabled plugin would silently reactivate. Fix the comment to state the real reason plainly, so this doesn't get "fixed" wrong later.

### Finding 3 — `pluginList` silently drops every disabled plugin from its output

`cmd/nanite/plugin_cmd.go`'s `pluginList()` (~line 664-674, backing `nanite plugin list`) computes `status := plugin.PluginStatus(dir, name)` (correctly DB-backed post-`02`), then on `status == "disabled"` falls back to reading `plugin.yaml.disabled` — a file the new DB-backed model never creates. `ParseManifest` fails on the nonexistent path and the loop `continue`s, so a disabled plugin never prints at all. Same bug class `TASKS/phase-5/02`'s own Work Log already found and documented as a known follow-up for `internal/api/plugins.go`'s `handleListManaged` — the parallel instance in `plugin_cmd.go` was missed. A real, live, silent-failure regression an operator hits the first time they disable a plugin via the CLI and run `nanite plugin list`.

## What to do

1. **Finding 1 (primary fix)**: add the missing `applyManifestRegistrations` call to `runPluginLoadIntoHost`, after `pms.pluginHost.LoadPlugin(p)` succeeds, passing the already-parsed `manifest`/loaded `p`/`pluginDir`. Confirm the idempotency reasoning above by tracing `Host.UnloadPlugin`'s sweep coverage yourself first. If you find the unload sweep does NOT actually cover every `registers.*` category `applyManifestRegistrations` can populate (a real gap, not assumed), that's a second, deeper problem — document it and escalate rather than shipping a fix that silently double-registers something.
2. **Finding 2**: fix `pluginDisable`'s stale comment to state the real, current reason `triggerRestart()` stays unconditional there (once Finding 1 lands: hot-reloading a disabled plugin would silently reactivate it, since `POST /api/plugins/reload` would now genuinely apply its manifest).
3. **Finding 3**: fix `pluginList` so a disabled plugin still appears in the output (with its disabled status correctly shown), instead of silently disappearing. Read the DB-backed state directly rather than falling back to a file path the new model never creates.
4. Real live verification for Finding 1, per this whole batch's standing discipline: install a real subprocess test plugin declaring `registers.crud[]` (or `registers.agent_profiles[]`) via the CLI's default (no-restart) path, confirm the declared REST route (or agent) is genuinely absent immediately after "hot-loaded" prints — wait, confirm it's genuinely PRESENT and working immediately after, without any process restart. If a test plugin already exists from task 05's own test suite, reuse its shape rather than inventing a new one.
5. Real live verification for Finding 3: disable a plugin via the CLI, confirm `nanite plugin list` still shows it (correctly marked disabled), not silently dropped.

## Done means

- A subprocess plugin declaring `registers.crud[]` (or `registers.agent_profiles[]`), installed via the CLI's default no-restart path, has its declared REST route (or agent) genuinely live and working immediately after install — verified against a real running instance, not just a unit test.
- A second hot-reload of an already-loaded plugin does not double-register or error on anything `applyManifestRegistrations` would re-apply — verified by reloading the same test plugin twice.
- `pluginDisable`'s comment accurately states why it doesn't hot-reload.
- `nanite plugin list` shows a disabled plugin (correctly marked), not silently drops it — verified live.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

### 1. Independent `UnloadPlugin` sweep-coverage trace (done before writing any code, per the task's explicit instruction not to trust the task file's own idempotency note)

Read `internal/plugin/host.go`'s `UnloadPlugin` (lines ~1240-1585) end to end and cross-referenced every category `applyManifestRegistrations` (`internal/plugin/registrations.go`) can populate against the sweep:

| `registers.*` category | Applied via | Swept by `UnloadPlugin` |
|---|---|---|
| envelopes | `host.RegisterEnvelope` | step 9 (side-map) + step 9b (compiled schemas, `envelopeRegistry.UnregisterPlugin`) |
| components | `host.RegisterUIComponent` | step 3 (`h.uiOwners`/`h.uiComponents`) |
| slots | `host.RegisterSlot` | step 4 (`h.slots` filtered by `PluginID`) |
| keybindings | `host.RegisterKeybinding` | step 5 (`h.kbOwners`) |
| commands | `registerManifestCommands` → `host.RegisterCommand` | step 15 (`cmdReg.RemoveByPlugin`, outside `h.mu`) |
| events | `registerManifestEvents` → `host.RegisterEventHook` | step 2 (`h.eventHooks` filtered by `pluginID`) |
| crud | `registerManifestCrud` → `host.RegisterCRUDHandler` | step 11 (`h.crudHandlers` filtered by `entry.pluginID`) |
| http_routes | `registerManifestHTTPRoutes` → `host.RegisterHTTPHandler` → `h.pluginMux` | step 10 (`h.pluginMux.RemoveByPlugin`) |
| mcp_servers | `registerManifestMCPServers` → `mcpRegistrar.AddPluginServer` | top-of-function block (`mcpReg.RemoveServersByPlugin`, before the numbered steps) |
| agent_profiles | `registerManifestAgentProfiles` → `applyPluginAgentProfile` (DB rows, not an in-memory side-map) | step 18, `host.SweepPluginAgentProfiles` — a DB-authoritative sweep via `ListAgentsByPluginID`/`ListRolesByPluginID`, independent of in-memory ownership maps |
| card_rules | `registerManifestCardRules` → `host.RegisterCardRule` | step 16 (`host.UnregisterPluginCardRules`, own-mutex registry) |
| panels | `registerManifestPanels` → `host.RegisterPanel` | step 17 (`host.UnregisterPluginPanels`, own-mutex registry) |

Also verified the *ownership-tagging* mechanism the sweep depends on is actually wired correctly for every category that reads `h.activePlugin` implicitly (`RegisterCommand`'s `source`, `RegisterEventHook`'s `pluginID`, `RegisterCRUDHandler`'s `crudHandlerEntry.pluginID`, `registerRouteLocked`'s pluginMux owner) — `applyManifestRegistrations` sets `host.activePlugin = pluginID` before calling any of the inner `Register*` functions and restores the previous value via `defer`, so every one of these ownership reads resolves to the correct plugin id during a single `applyManifestRegistrations` call. Categories that instead take an explicit `PluginID` field (envelopes, slots, agent_profiles, card_rules, panels) are passed the same `pluginID` value directly, not through `h.activePlugin`.

**Finding: the idempotency reasoning in this task file is correct.** All 12 `registers.*` categories are torn down by `UnloadPlugin`, and ownership tagging is consistent between apply and sweep. There is no second, deeper problem here — `handleReload`'s existing `unloadPluginFromHost` call before `runPluginLoadIntoHost` genuinely neutralizes the double-registration risk once the missing `applyManifestRegistrations` call is added. This was also independently confirmed by the existing `internal/plugin/unload_sweep_test.go::TestUnloadPlugin_FullTeardown` (16 of the categories it directly covers) plus a live double-hot-reload check (below) exercising `crud` end to end through the real API path.

### 2. Finding 1 (primary fix) — `runPluginLoadIntoHost` now applies manifest registrations

- Added an exported wrapper `plugin.ApplyManifestRegistrations(host, manifest, p, pluginDir) error` in `internal/plugin/registrations.go` (thin pass-through to the existing unexported `applyManifestRegistrations` — the function itself was not touched, since `loader.go`'s two existing call sites still needed to keep working unchanged and unexported).
- `internal/api/plugins.go`'s `runPluginLoadIntoHost` now calls `naniteplugin.ApplyManifestRegistrations(pms.pluginHost, manifest, p, pluginDir)` immediately after `pms.pluginHost.LoadPlugin(p)` succeeds. On failure, it rolls back via `pms.pluginHost.UnloadPlugin(manifest.Name)` (best-effort, logged on its own failure) before returning `false` — mirrors `loader.go`'s `LoadDiscovered` rollback-on-manifest-apply-failure behavior for the boot-time path, so a partially-registered plugin never stays "loaded" with none of its declared registrations live.
- Since `runPluginLoadIntoHost` backs `handleInstallLocal`/`handleInstallRemote`(via `handleInstall`)/`handleEnable`/`handleReload` uniformly, this one change closes the gap for every API-driven lifecycle action at once, including the CLI's `triggerHotReload`/`triggerActivation` no-restart default (task 04) that POSTs to `/api/plugins/reload`.
- Updated `runPluginLoadIntoHost`'s doc comment: it previously claimed agent profiles "become available immediately," which was false pre-fix for `registers.agent_profiles[]`/`registers.crud[]` — the comment now states this plainly as history and describes the new true behavior/failure mode.

### 3. Finding 2 — `pluginDisable`'s stale comment fixed

`cmd/nanite/plugin_cmd.go`'s `pluginDisable` still had the pre-Phase-5-#02 "file rename means reload would 404" reasoning. Replaced with the real, current reason `triggerRestart()` must stay unconditional there, post-Finding-1: `POST /api/plugins/reload` now genuinely calls `Host.LoadPlugin` (which, for a subprocess plugin, spawns its process) *before* `applyManifestRegistrations`'s own DB-backed enabled gate ever runs — so wiring this disable path onto hot-reload would silently reactivate a disabled plugin's process even though its DB row still says disabled. Only `loader.go`'s `seedAndCheckEnabled` boot-time gate (which runs *before* `Host.LoadPlugin` is ever called) actually respects the disabled flag pre-load, and a full restart is the only way to reach that gate today.

### 4. Finding 3 — `pluginList`'s silent-drop bug fixed

`cmd/nanite/plugin_cmd.go`'s `pluginList()` no longer branches on `status == "disabled"` to read a `plugin.yaml.disabled` path that the DB-backed model never creates. It now reads `plugin.yaml` unconditionally — `PluginStatus` (called immediately before, in the same loop iteration) already migrates any pre-Phase-5-#02 legacy `plugin.yaml.disabled` into `plugin.yaml` in place via `resolvePluginIdentity`, so `plugin.yaml` is authoritative by the time `pluginList` reads it. Verified live (below): a disabled plugin now appears in `nanite plugin list`'s output correctly marked `disabled`, instead of silently vanishing.

**Scope note**: `internal/api/plugins.go`'s `handleListManaged` has the exact same bug class (already documented as a known, deliberately-deferred follow-up in `TASKS/phase-5/02`'s own Work Log, under a different worktree-boundary constraint at the time). This task's own `Touches` list and "What to do" section explicitly scope Finding 3 to `pluginList` in `cmd/nanite/plugin_cmd.go` only, not `handleListManaged` — left untouched here to stay inside this task's stated scope; still open as a real, separately-tracked follow-up.

### 5. Real live verification (built a throwaway real subprocess plugin, not just unit tests)

No existing on-disk subprocess plugin fixture declaring `registers.crud[]`/`registers.agent_profiles[]` existed anywhere in the repo to reuse (checked `plugins/`, `examples/`, and every `internal/plugin/builtin/*/plugin.yaml` — task 05's own test coverage for `registerManifestCrud` is an in-package unit test using fake in-memory pipes, not a real on-disk plugin+binary). Built one from scratch, used it for live verification, then deleted it before finishing (never committed):

- A tiny real Go subprocess plugin (`subprocess.Serve` + `CRUDHandler` from `github.com/hollis-labs/plugin-sdk/subprocess`, an in-memory `widgets` resource) and a `plugin.yaml` declaring `registers.crud: [{resource: widgets, methods: [list,create,read,update,delete]}]`.
- A scratch `nanite serve` instance (built from this worktree, isolated `NANITE_DB_PATH`/`NANITE_PLUGINS_DIR` under the session scratchpad, never touching any real tracked file or the shared/deployed `nanite-api-service`).
- `nanite plugin install <local-src>` (the CLI's default path, no `--no-restart`/`triggerRestart` needed since the manifest is `runtime: subprocess`) printed `Plugin "crud-verify" is live — no restart needed.` Confirmed via server log: `loaded plugin` → `registered plugin crud resource ... resource=widgets` → `plugin-api: hot-loaded plugin`, i.e. `LoadPlugin` **and** `applyManifestRegistrations` both ran.
- Immediately after (no restart): `GET /api/plugins/widgets` → `{"count":0,"items":[]}`, `POST /api/plugins/widgets {"name":"gadget"}` → `{"id":"1","name":"gadget"}`, `GET` again → item present. The declared CRUD route is genuinely live and functioning.
- Double hot-reload idempotency: ran `nanite plugin reload crud-verify` twice back-to-back. Both succeeded (exit 0, `"status":"reloaded"`), the server log showed a clean `unloaded plugin` → `loaded plugin` → `registered plugin crud resource` cycle each time with no errors or "already registered"/"already loaded" messages, and the CRUD route kept working correctly after both reloads (fresh subprocess state each time, as expected for an in-memory-backed plugin).
- Finding 3 live check: `nanite plugin disable crud-verify` (DB write; the restart attempt itself failed harmlessly — no `cerberus` daemon manages this scratch instance, which is expected and irrelevant to the check), then `nanite plugin list` showed `crud-verify ... disabled ...` — present and correctly marked, not silently dropped. Re-enabled afterward (`nanite plugin enable`) as a bonus sanity check: hot-reloaded live again, `nanite plugin list` showed `active`, and the CRUD route worked again with fresh state.
- Cleanup: scratch server killed, the temporary `cmd/verifycrudplugin/` source directory removed from the worktree before running the final baseline checks (`git status --short` confirms only the three intended files are modified).

### 6. Baseline checks

- `go build ./cmd/nanite/` — passes.
- `go vet ./...` — fails with 4 pre-existing findings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` possible context leak), confirmed via `git stash` to exist identically on the pre-task tree — unrelated to this task, not touched.
- `go test ./...` — all packages pass, including `internal/plugin`, `internal/api`, `cmd/nanite`.

### Deviations from the plan

None of substance. The only elaboration beyond the task's literal "What to do" list: built a real subprocess test plugin (rather than reusing an existing on-disk fixture, since none existed) for the live verification, and additionally verified the disable→enable round-trip as a bonus sanity check beyond what "Done means" required. `internal/api/plugins.go`'s `handleListManaged` was deliberately left untouched (same bug class as Finding 3, but outside this task's stated `Touches`/scope) — noted above, not escalated, since it's a pre-existing, separately-documented, non-security/non-data-integrity display bug.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
