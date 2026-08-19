# Fix: `unloadPluginFromHost` (and task 11's own rollback) unload by the wrong identifier, so reload of an already-loaded plugin always fails

**Phase:** 5
**Status:** not-started
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
