# Close the CLI-install-vs-hot-reload asymmetry

**Phase:** 5
**Status:** not-started
**Depends on:** none, but coordinate with `11-build-plugin-installed-enabled-state-model.md` if both touch plugin install/lifecycle code in the same window
**Touches:** `cmd/nanite/plugin_cmd.go` (`triggerRestart`, `pluginInstallLocal`/`pluginInstallRemote` and other lifecycle commands, the existing `--no-restart` flag), `internal/api/plugins.go` (`handleReload` — the existing hot-reload endpoint, unchanged, just called from a new place)

## Context

Architecture doc `09-plugin-system.md`: *"Hot-reload confirmed real (`POST /api/plugins/reload`, dev-mode `window.__nanite_reloadPlugin`) — genuinely avoids a restart... One real asymmetry to close: CLI-based plugin install still requires a manual restart while the API-driven install path and the reload endpoint both hot-load live — have the CLI call the reload endpoint automatically after a local install instead of leaving this inconsistent."*

### Exact current state, verified

`cmd/nanite/plugin_cmd.go:185-197`'s `triggerRestart()` shells out to `cerberus restart <service>` (or prints a manual-restart message if cerberus isn't available). Called at the end of `pluginInstallLocal` (`plugin_cmd.go:290`), `pluginInstallRemote` (`:316,360`), and other lifecycle commands (`:485,495,505`) — always a full process restart, never the hot-reload path. A `--no-restart` flag already exists (`:26-28,46-47`) to skip this, confirming restart-by-default is deliberate current behavior, not an oversight to be treated as a bug in itself.

The hot-reload path already exists and is CLI-reachable today, just not wired into `install`: `nanite plugin reload <name>` (`plugin_cmd.go:92-96` → `pluginReload`, `cmd/nanite/plugin_dev_cmd.go:53-70`) POSTs to `/api/plugins/reload` (`internal/api/plugins.go:124` → `handleReload`, full body at `:701-741`: unload-best-effort → `runPluginLoadIntoHost` → emit disabled+enabled event pair).

### A real constraint: this only applies to subprocess plugins

A builtin's Go code is compiled into the binary — installing/updating a builtin genuinely requires a rebuild+restart; hot-reload structurally cannot apply. **This task's fix must branch on plugin kind**, not universally skip `triggerRestart()` — applying the reload-instead-of-restart fix uniformly would silently break builtin installs.

## What to do

1. In `pluginInstallLocal`/`pluginInstallRemote` (and any other lifecycle command currently ending in `triggerRestart()`), branch on plugin kind: for a **subprocess** plugin, call the same reload path `pluginReload` already uses (`POST /api/plugins/reload`) instead of `triggerRestart()`; for a **builtin**, keep the restart requirement (a builtin install/update genuinely needs one).
2. Preserve the existing `--no-restart` flag's meaning — confirm it still does something sensible now that the default subprocess path no longer restarts at all (may become a no-op for subprocess plugins, should remain meaningful for builtins).
3. Verify end to end: a real local subprocess-plugin install via the CLI hot-loads without a restart; a builtin install/update still correctly prompts for or performs a restart.

## Done means

- A subprocess plugin installed via the CLI is live (hot-loaded) without requiring a manual restart.
- A builtin plugin install/update still correctly requires a restart (regression check — this task must not silently break builtin installs by applying the fix too broadly).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Verified in a real local install of a real subprocess plugin.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
