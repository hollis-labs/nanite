# Build a real installed/enabled state model for plugins (WordPress-style, DB-backed, builtin+subprocess uniform)

**Phase:** 5
**Status:** not-started
**Depends on:** none
**Touches:** `internal/plugin/manage.go` (`DisablePlugin`/`EnablePlugin`/`IsDisabled`/`PluginStatus` — replace the file-rename mechanism), new migration (`plugins` state table), `internal/plugin/registrations.go` (`applyManifestRegistrations` — the shared entrypoint where the new state model gates registration), `internal/plugin/builtin/*/plugin.yaml` (12 builtin manifests, read-only reference)

## Context

Architecture doc `09-plugin-system.md`: *"Builtin plugin enable/disable — needs a real installed/enabled state model, not the current file-rename mechanism... Explicitly modeled on WordPress's plugin state (code present = installed, a separate flag = active/enabled), working uniformly for both builtin and subprocess plugins via GUI/CLI/API — not the current subprocess-only, disk-manifest-rename approach that doesn't even apply to builtins... this state belongs in the DB, not encoded as a file's presence/absence."*

### Current mechanism, verified — file-rename, and a real nuance about builtins worth resolving before assuming it's a no-op for them

`internal/plugin/manage.go` — `DisablePlugin`/`EnablePlugin`/`IsDisabled`/`PluginStatus` all operate by renaming `plugin.yaml` ↔ `plugin.yaml.disabled` inside `pluginsDir`. **12 builtins confirmed, each with its own real on-disk `plugin.yaml`** (not just compiled Go — they genuinely have manifest files too): `adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-nanite-native`, `adapter-opencode`, `agentwidgets`, `bookmarks`, `card-rules-demo`, `contextwidgets`, `debugwidgets`, `observabilitywidgets`, `sessionstats` — all under `internal/plugin/builtin/*/plugin.yaml`. No DB table exists for plugin state today — fully greenfield.

**A nuance a worker must resolve, not assume**: because builtins do have on-disk `plugin.yaml` files, the file-rename mechanic would technically *execute* against them — but disabling a builtin this way only removes its manifest from disk; the Go code is still compiled into the binary, and whether its `init()`/registration wiring still runs depends on the actual builtin-loading path (`LookupConstructor`). **Confirm this behavior directly during implementation** before assuming the current mechanism is a clean no-op for builtins — if it's actually silently broken (manifest gone, code still registers), that's a real bug this task's replacement should also fix, not just a "doesn't apply" framing to note and move past.

`applyManifestRegistrations` (`internal/plugin/registrations.go:220`) is the single shared registration entrypoint for both builtin and subprocess plugins — this is where the new DB-backed state model gates registration (skip calling it for a disabled plugin, builtin or subprocess alike, instead of the current disk-presence check).

## What to do

1. Create a `plugins` table: `id`/`slug`, `kind` (`builtin`/`subprocess`), `installed` (bool — code present, or manifest/package staged), `enabled` (bool — the WordPress-style active flag), timestamps.
2. Seed the table for all 12 current builtins as installed+enabled (matching today's default-on behavior) and for any currently-installed subprocess plugins per their current file-presence state.
3. Replace `DisablePlugin`/`EnablePlugin`/`IsDisabled`/`PluginStatus`'s file-rename implementation with DB reads/writes against the new table — same function signatures where possible, so GUI/CLI/API callers don't need to change beyond the implementation swap.
4. Gate `applyManifestRegistrations` (or its caller) on the new `enabled` flag, uniformly for builtin and subprocess — confirm this actually stops a disabled builtin's registration wiring from running (the real bug flagged in Context, if confirmed present).
5. Resolve the builtin-disable behavior nuance from Context as part of this task, not deferred — a disabled builtin must genuinely not register, not just lose its manifest file.

## Done means

- `plugins` table exists, correctly seeded for all current builtins and subprocess plugins.
- Disabling a plugin (builtin or subprocess) via GUI/CLI/API genuinely stops its registration wiring from running on the next reload/restart — verified for at least one builtin and one subprocess plugin.
- The file-rename mechanism is fully retired.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
