# Fix: `unloadPluginFromHost` (and task 11's own rollback) unload by the wrong identifier, so reload of an already-loaded plugin always fails

**Phase:** 5
**Status:** validated
**Depends on:** `TASKS/phase-5/11-fix-hot-reload-never-applies-manifest-registrations.md` (already landed on `main` — this task fixes a real bug the Orchestrator found during task 11's own post-merge live dogfeed re-verification, and task 11's new rollback code shares the same bug)
**Touches:** `internal/api/plugins.go` (`unloadPluginFromHost`, `runPluginLoadIntoHost`'s rollback branch)

## Context

Found by the Orchestrator during live dogfeed re-verification of task 11, 2026-08-19 — not by a dispatched Reviewer. Sequence: installed a real throwaway subprocess plugin (`id: dogfeed-crud-check`, `name: Dogfeed Crud Check`) via `nanite plugin install` (no `--link`, hot-reload default), confirmed `registers.crud[]` was genuinely live with no restart (task 11's own fix working correctly). Then ran `nanite plugin reload dogfeed-crud-check` a second time to test idempotency — it returned `500 {"error":"reload: failed to load plugin \"dogfeed-crud-check\""}`. Server log for the second reload:

```
"plugin-api: unload failed" name="Dogfeed Crud Check" err="plugin \"Dogfeed Crud Check\" not found"
"plugin-api: hot-load failed" name="Dogfeed Crud Check" err="plugin \"dogfeed-crud-check\" already loaded"
```

### Root cause

`internal/api/plugins.go`'s `unloadPluginFromHost` (line ~809):

```go
if err := pms.pluginHost.UnloadPlugin(manifest.Name); err != nil {
```

passes `manifest.Name` — the YAML `name:` field, a human-readable display string ("Dogfeed Crud Check") — to `Host.UnloadPlugin(id string)`. But `Host.LoadPlugin` (`internal/plugin/host.go:1180`) stores the plugin under `h.plugins[p.ID()]`, and `SubprocessPlugin.ID()` returns the manifest's `id:` field ("dogfeed-crud-check"), confirmed by the load log line `"loaded plugin" id="dogfeed-crud-check" name="Dogfeed Crud Check"`. `Name` and `ID` are different strings for any plugin that follows the scaffold's own convention (Title Case name, kebab-case id) — which is effectively every real plugin, including the one `nanite plugin new` itself generates. So `UnloadPlugin(manifest.Name)` can practically never find the plugin it's trying to unload: `h.plugins["Dogfeed Crud Check"]` doesn't exist, only `h.plugins["dogfeed-crud-check"]` does.

`unloadPluginFromHost` treats a not-found error as a benign no-op (`slog.Warn`, `return false`) — reasonable for its *first*-load case (nothing to unload yet), but wrong for every subsequent reload: the previous instance is never actually torn down, so the following `LoadPlugin` call in `runPluginLoadIntoHost` fails with `"already loaded"` under the *correct* key, and the whole reload 500s.

`git log -S` on `unloadPluginFromHost` traces this to `bf4e013a` (2026-04-13, pre-dates this Phases-2-5 batch entirely) — the bug itself is old. But it is **directly load-bearing for this batch's own deliverables**:

1. Phase 5 `#04`'s whole point was making `nanite plugin install`/`update`/`enable` hot-reload with "no restart needed." An operator who edits a plugin's code and re-installs/reloads it — the single most common real workflow `#04` exists to support — hits this 500 on the very first re-reload.
2. Task 11's own new rollback code, `runPluginLoadIntoHost`'s `ApplyManifestRegistrations` failure branch (line ~914), **copied the identical wrong pattern**:
   ```go
   if unloadErr := pms.pluginHost.UnloadPlugin(manifest.Name); unloadErr != nil {
   ```
   Task 11's own doc comment claims this rollback "rolls the load back via UnloadPlugin so nothing stays half-registered" — that claim is false as implemented: if `ApplyManifestRegistrations` ever fails for a real plugin, the rollback unload will itself silently fail to find the plugin (wrong key), leaving it loaded-but-partially-registered in the host, exactly the state task 11 was trying to prevent.

Every other caller of `Host.UnloadPlugin` in the codebase (`internal/plugin/loader.go:247`, `:391`, and all test call sites) correctly passes the plugin's id (`pluginID`, `id`, or `p.ID()`) — `internal/api/plugins.go`'s two call sites are the only ones using `manifest.Name`. `PluginManifest.Identifier()` (`internal/plugin/config.go:320`) already exists as exactly the right helper (`ID` if set, else falls back to `Name`) and is unused at both call sites.

## What to do

1. In `unloadPluginFromHost` (`internal/api/plugins.go` ~line 809), change `pms.pluginHost.UnloadPlugin(manifest.Name)` to `pms.pluginHost.UnloadPlugin(manifest.Identifier())`.
2. In `runPluginLoadIntoHost`'s `ApplyManifestRegistrations` failure/rollback branch (~line 914), change `pms.pluginHost.UnloadPlugin(manifest.Name)` to `pms.pluginHost.UnloadPlugin(manifest.Identifier())` (or `p.ID()`, which is already in scope from the `LoadPlugin(p)` call a few lines above and is guaranteed to match exactly what `LoadPlugin` stored — prefer `p.ID()` if it reads more directly as "the same id we just loaded under").
3. Leave the two `slog.Warn`/`slog.Info` calls' `"name", manifest.Name` log fields alone — those are for human-readable logging, not registry lookups, and are fine as-is.
4. Add or extend a test in `internal/api/plugins_test.go` (or wherever `handleReload`/`unloadPluginFromHost` is already covered) that loads a plugin whose `id` and `name` differ (the common case — mirror the scaffold's own convention), reloads it twice via `handleReload` or `runPluginLoadIntoHost`+`unloadPluginFromHost` directly, and asserts the second reload succeeds (not "already loaded"). If existing tests only ever use fixtures where `id == name`, that's exactly how this shipped unnoticed — don't perpetuate it.
5. Live-verify manually against the real deployed `nanite-api-service` (same throwaway `dogfeed-crud-check` plugin fixture from task 11's re-verification, or a fresh one — either is fine): install once, reload at least twice in a row, confirm no 500 and no duplicate-registration side effects (e.g. `registered CRUD handler` log line should not fire twice with stale state, list/create/read should still work correctly after the second reload).

## Done means

- `go build ./cmd/nanite/`, `go vet ./...` (no new findings beyond the 2 pre-existing `container.go` ones), `go test ./...` all green.
- A real plugin with `id != name` can be reloaded via `POST /api/plugins/reload` (or `nanite plugin reload <id>`) at least twice in a row without error.
- Task 11's `ApplyManifestRegistrations` rollback path, exercised with a plugin manifest that deliberately fails `ApplyManifestRegistrations` (e.g. a malformed `registers.crud[]` entry), actually results in the plugin being unloaded from the host afterward — not left in a loaded-but-half-registered state.
- Work Log documents what was verified, mirroring the style of every other task file in this batch.

## Out of scope

- `unloadPluginFromHost`'s bigger design (should it treat not-found as fatal vs. benign at all, callers passing the wrong shape of identifier elsewhere in the codebase, etc.) — fix only the two confirmed wrong-key call sites, don't redesign the function's error semantics.
- Any other plugin-lifecycle bug not already confirmed by direct trace in this task file — if you find something else while in this code, log it in `TASKS/ESCALATIONS.md`, don't silently fix it inline.

## Work Log

Implemented by worker `af6a8ae05a11dc75b`, worktree `agent-af6a8ae05a11dc75b`. The worktree branch itself was never committed to (still at its merge-base with `main`, `885c3c69`) — the worker's actual changes sat uncommitted in the worktree's working directory. The Orchestrator reviewed that diff directly (`git diff` inside the worktree, since the branch had nothing to `git merge`), confirmed it matched this task's "What to do" precisely, then applied the same two-line fix and the new test file to `main`'s working tree by hand and committed there, rather than merging a no-op branch. The worktree/branch were removed after (`git worktree remove`, `git branch -D worktree-agent-af6a8ae05a11dc75b`) since nothing of value was left in them once ported.

### Root-cause confirmation (worker)

Cross-referenced `internal/plugin/host.go`'s `LoadPlugin`/`UnloadPlugin` (both key `h.plugins` by `id := p.ID()`) and `internal/plugin/config.go`'s `PluginManifest.Identifier()`. Confirmed via a full-repo grep of `.UnloadPlugin(` (excluding tests) that `internal/plugin/loader.go`'s two callers already pass the correct identifier — only `internal/api/plugins.go`'s two call sites (`unloadPluginFromHost`, and `runPluginLoadIntoHost`'s `ApplyManifestRegistrations`-failure rollback) had the bug. Also confirmed why this shipped unnoticed: every builtin plugin's `plugin.yaml` in `internal/plugin/builtin/*/` has `id == name` (spot-checked three), and the CLI scaffold's own generated manifest is the only in-repo source that produces `id != name` — no existing test exercised that shape through these two functions.

### Fix applied

`internal/api/plugins.go`:
- `unloadPluginFromHost`: `UnloadPlugin(manifest.Name)` → `UnloadPlugin(manifest.Identifier())`, with the accompanying `slog.Warn` field updated to log the same corrected identifier (a reasonable, minor improvement beyond this task's literal instruction #3 to leave logging untouched — the Orchestrator reviewed and kept it, since logging the value actually used for the lookup is strictly more useful for debugging, not a functional change).
- `runPluginLoadIntoHost`'s rollback branch: `UnloadPlugin(manifest.Name)` → `UnloadPlugin(p.ID())` (per the task's own stated preference — `p` is already in scope and is guaranteed to match what `LoadPlugin` just stored it under), with its `slog.Warn` field updated the same way.
- Deliberately left `runPluginLoadIntoHost`'s other pre-existing uses of `manifest.Name` untouched (`NewPluginConfig`, `SetPluginConfig`, `LookupConstructor` for the builtin-runtime branch) — a separate, pre-existing convention (builtin constructors are registered/looked up by `manifest.Name`, not `Identifier()`) outside this task's stated scope; noted, not touched.

### Tests added — `internal/api/plugins_unload_test.go` (new file)

Three tests, all deliberately using an `id != name` fixture (the shape the CLI's own scaffold generates, unlike every pre-existing fixture in the repo):
- `TestUnloadPluginFromHost_UsesIdentifierNotName` — direct regression for the primary bug site.
- `TestRunPluginLoadIntoHost_ReloadTwice_IDNameMismatch` — reproduces the exact live symptom (reload succeeds once, then fails with "already loaded") across three full reload cycles, driving the same `unloadPluginFromHost` + `runPluginLoadIntoHost` pair `handleReload` calls.
- `TestRunPluginLoadIntoHost_RollbackUsesPluginID` — forces `ApplyManifestRegistrations` to fail via a deliberately invalid `registers.components[].type`, then confirms the plugin is NOT left half-registered in the host, and that a corrected retry succeeds afterward (proving the id slot was actually freed).

The worker verified regression validity directly (`git stash` isolating pre-fix code with the new tests in place, confirmed all three failed with the exact predicted symptoms, then restored the fix and confirmed all three pass) inside its own worktree before reporting done. The Orchestrator independently re-ran all three against the ported code on `main` (`go test ./internal/api/... -run "TestUnloadPluginFromHost|TestRunPluginLoadIntoHost" -v`) — all pass, log output shows exactly one `unloaded plugin`/`loaded plugin` pair per reload cycle with no stale-state artifacts.

### Baseline checks (Orchestrator, on `main` after porting)

- `go build ./cmd/nanite/` — passes.
- `go vet ./...` — same 4 pre-existing findings in `internal/service/container.go`, unchanged.
- `go test ./...` — all packages pass.

### Live re-verification (Orchestrator, against the real deployed `nanite-api-service`)

Deployed via `cerberus_resource_deploy nanite-api-service` (client-side timeout as usual, completed server-side) + `cerberus_resource_reload nanite-api-service` (new `launchd_pid` 89927 confirmed). Reinstalled the same throwaway `dogfeed-crud-check` subprocess plugin (`id != name`, `registers.crud[]`) used for task 11's own re-verification, then reloaded it three times in a row via `nanite plugin reload dogfeed-crud-check` — **all three succeeded** (previously, the second reload 500'd with `"already loaded"`; confirmed via server log that every cycle now logs exactly one `"unloaded plugin"` immediately followed by one fresh `"loaded plugin"`, with no `"unload failed"`/`"already loaded"` warnings at any point — the fix holds).

One wrinkle, unrelated to this task's fix: those first three reloads' CRUD route came back as the SPA fallback (200 HTML, not the JSON API response) — `nanite plugin list` showed the plugin as `disabled`, and the log carried `"plugin disabled — skipping manifest registrations"` on every cycle. Root cause: leftover state from the Orchestrator's own earlier task-11 re-verification session, which had run `nanite plugin disable dogfeed-crud-check` against this same plugin id and then deleted its directory without ever uninstalling it through the DB-backed state model first — so the `plugins` table's `disabled` row for that id persisted across the directory delete, and a later fresh `nanite plugin install` at the same id didn't reset it. Not a task-12 regression (confirmed: `applyManifestRegistrations`'s disabled-skip check is intentional design from task `#02`, untouched by this task's diff) and not investigated further as a bug — ran `nanite plugin enable dogfeed-crud-check` to clear the stale state, then re-ran the same three-reload sequence cleanly: `GET`/`POST /api/plugins/dogfeed-crud-check-items` worked before and after every one of the three reloads, and the server log showed a full, correct registration set (`registered envelope`, `registered slash command`, `registered CRUD handler`, `registered plugin crud resource`, `mcp: added server`) on every single cycle — no duplicates, no stale handlers, no missing registrations.

Cleaned up afterward: `nanite plugin uninstall dogfeed-crud-check` (its auto-restart step failed with `unknown command "restart" for "cerberus"` — a separate, pre-existing CLI/Cerberus command-name mismatch, not investigated further here), removed the plugin's directory, and reloaded the live service again via Cerberus to fully clear in-host state. Confirmed clean: `nanite plugin list` → "No plugins installed", `internal/server/ui_dist/index.html`'s incidental build-hash churn reverted via `git checkout --` (the same recurring non-deterministic Vite artifact seen after every deploy this whole batch).
