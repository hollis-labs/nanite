# Fix: the API-driven install/enable/reload hot-load path never calls `applyManifestRegistrations`

**Phase:** 5
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
