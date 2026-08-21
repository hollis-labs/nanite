# Add registration pre-flight validation pass + NaniteCompat enforcement

**Phase:** 1 — Registration hardening (`TASKS/plugin-system`)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/plugin/loader.go` (`LoadDiscovered`, `LoadRegisteredBuiltins`),
`internal/plugin/registrations.go` (`applyManifestRegistrations`/`ApplyManifestRegistrations`,
the per-category conflict checks), `internal/plugin/config.go` (`NaniteCompat` — read, not
changed), new file(s) for the pre-flight checker itself. Reference-only, not modified:
`internal/plugin/shadcn_version.go`, `internal/version/version.go`.

## Context

`docs/engineering/architecture/09-plugin-system.md`'s "Target design: registration atomicity"
section: today, `LoadDiscovered` spawns a subprocess plugin's process and runs the
`plugin/init`/`plugin/load` handshake *before* `applyManifestRegistrations` runs its conflict
checks — not matching the intended pipeline order (discover → parse → validate → resolve deps
→ **build plan → conflict/policy validation** → start/handshake → atomic commit). A plugin
guaranteed to fail registration still pays for a full process spawn before finding out.
**Decision: add a pre-flight validation pass before handshake.** Folded into the same pass:
`NaniteCompat{Min,Max}` enforcement — parsed from every manifest today, never checked against
the running host version anywhere.

This planning session's own research independently re-verified every claim above against
current code (not just the doc's prose):

- **Ordering, confirmed exactly as described.** `LoadDiscovered` (`internal/plugin/loader.go:158`)
  calls `host.LoadPlugin(p)` at `loader.go:217`, which — for a subprocess plugin — transitively
  spawns the OS process (`SubprocessPlugin.Load`, `internal/plugin/subprocess/plugin.go:182`,
  calling `sp.mgr.Start(...)` at `:186`, which does `cmd.Start()` at
  `internal/plugin/subprocess/manager.go:162`) and runs the `plugin/init`/`plugin/load`
  handshake (`plugin.go:201`, `:223`) — all of this completes *before*
  `applyManifestRegistrations(host, dp.Manifest, p, dp.Dir)` is called at `loader.go:239`. Same
  pattern in `LoadRegisteredBuiltins` (`host.LoadPlugin` at `loader.go:371` before
  `applyManifestRegistrations` at `:386`), and — post-Phase-5 — on the API-driven hot-reload
  path too (`internal/api/plugins.go:902` before `:915`). Failure rolls back cleanly via
  `host.UnloadPlugin(pluginID)` (`loader.go:247`, and the equivalent in `internal/api/plugins.go:924`)
  — this is real, but doesn't change the "pays for a spawn it was always going to fail" cost.
- **Correction to the doc's "envelope/command/route/CRUD collisions, checked inline
  per-category" claim — only partially true.** Direct trace of `applyManifestRegistrations`
  (`internal/plugin/registrations.go:248`) found real, current collision *rejection* for
  **envelopes** (`registrations.go:119-122`, `Host.RegisterEnvelope`, returns an error naming
  the owning plugin), **UI components** (`internal/plugin/host.go:455`), and **keybindings**
  (`host.go:1133`) — but **no collision check at all** for **commands** (`Host.RegisterCommand`
  → `CommandRegistry.RegisterPluginCommand`, `internal/chat/commands.go:160-164`, unconditional
  overwrite), **CRUD resources** (`Host.RegisterCRUDHandler`, `host.go:348-359`, unconditional
  map write), or **HTTP routes** (`MutablePluginMux.Handle`, `internal/plugin/httpmux.go:44-57`
  — deliberately silent-replace, by design, "so a plugin reload with the same routes doesn't
  fail"). **This task's pre-flight pass needs genuinely new detection logic for commands,
  CRUD, and routes — it cannot just relocate an existing check for those three categories**,
  since none exists to relocate. Envelopes/components/keybindings already reject on conflict
  live (during registration); moving *those* three into a true pre-flight dry-run is a
  relocation, not new logic.
- **A real manifest-only, no-process-needed validation pipeline already exists, but isn't
  wired into the load path.** `internal/plugin/install/validate.go`'s `validator.run`
  (`validate.go:185-194`) does schema, cross-ref (uniqueness of envelope/command/slot/
  keybinding/route/MCP-server/agent-profile/card-rule identifiers — `validateCrossRefs`,
  `validate.go:255-381`), bundle-asset, platform, and `ui.shadcn_version` compatibility
  checks — entirely against the manifest, no subprocess needed. But it's called only from the
  CLI's one-time `nanite plugin install` flow (`cmd/nanite/plugin_install_flow.go:126`), never
  from `LoadDiscovered`/`LoadRegisteredBuiltins` (boot time) or `runPluginLoadIntoHost`
  (hot-reload/enable). This is the closest existing precedent for "dry-run against the
  manifest only" — reuse its shape/conventions rather than inventing a new validation style,
  but it needs to actually run on the load path, not just the one-time install CLI path.
- **`NaniteCompat` — confirmed zero enforcement, exactly as the doc claims.** Struct at
  `internal/plugin/config.go:112-116` (`Min`/`Max string`), field on `PluginManifest` at
  `config.go:70-71` (`yaml:"nanite_compat"`). Full-repo grep found no reference outside
  `config.go` itself and a parse-only test assertion
  (`internal/plugin/config_manifest_v1_test.go:136-137`). Zero comparison-against-host-version
  call sites anywhere.
- **A ready-made template for the compat check already exists in-repo, for a different field.**
  `internal/plugin/shadcn_version.go` implements the identical shape of check for
  `ui.shadcn_version`: `HostShadcnVersion = "1.0.0"` (line 19), `CheckShadcnCompat(pluginRange string) error`
  (line 34, with `^`/`~`/exact semver-range parsing, lines 38-127), invoked from
  `validateShadcnVersion` (`internal/plugin/install/validate.go:199-206`) purely against the
  manifest. The running host version is `internal/version.Version` (`internal/version/version.go:4`,
  currently `"0.3.0-beta"`), already the source of truth surfaced at `/api/server`'s
  `"version"` field and the subprocess `plugin/init` payload's `InitParams.Version`.
  `NaniteCompat{Min,Max}` enforcement should follow `CheckShadcnCompat`'s exact pattern against
  `internal/version.Version`, not invent a new range-parsing scheme.

## What to do

1. Build a pre-flight validation function (new, e.g. `internal/plugin/preflight.go`) that
   accepts a `*PluginManifest` (and whatever minimal host-state snapshot it needs — the
   currently-registered envelope types, command names, CRUD resource types, keybinding keys,
   HTTP route patterns) and returns every conflict it finds, with **no subprocess spawn and no
   builtin constructor call required to run it**. For envelopes/components/keybindings, this
   can reuse the exact rejection logic that `applyManifestRegistrations` already runs live
   (extract it into a form callable both pre-flight and at real-registration time, don't
   duplicate the logic in two places that can drift). For commands/CRUD/routes, add real
   detection — reading the current registered-name sets is enough; you don't need to change
   the intentional silent-replace behavior of live registration itself, just detect and report
   the conflict before a process gets spawned over it.
2. Fold `NaniteCompat{Min,Max}` enforcement into the same pass, following
   `CheckShadcnCompat`/`validateShadcnVersion`'s exact pattern (`internal/plugin/shadcn_version.go`,
   `internal/plugin/install/validate.go:199-206`) against `internal/version.Version`. A plugin
   whose declared range excludes the running host version fails pre-flight with a clear error
   naming both the plugin's declared range and the actual host version.
3. Wire this pre-flight pass into `LoadDiscovered` and `LoadRegisteredBuiltins`
   (`internal/plugin/loader.go`) *before* `host.LoadPlugin(p)` is called (i.e. before subprocess
   spawn/handshake, before a builtin constructor runs) — not just before
   `applyManifestRegistrations`, since the whole point is avoiding the spawn cost for a
   plugin that's going to fail anyway. Also wire it into the API-driven install/hot-reload path
   (`internal/api/plugins.go`'s `runPluginLoadIntoHost` or wherever it resolves to after
   `TASKS/phase-5/11`'s fix), in the same relative position.
4. On pre-flight failure: the plugin never loads at all (no spawn, no constructor call, no
   partial registration) — this is a stronger guarantee than today's post-hoc `UnloadPlugin`
   rollback, not just an earlier version of it. Surface a clear, specific error (which
   conflict, which category, which existing owner) to both the CLI and API install paths.
5. Do not change the intentional silent-replace behavior of live command/CRUD/route
   registration (`internal/plugin/httpmux.go:44-57`'s documented reload-must-not-fail
   rationale still applies for a genuine reload of the *same* plugin) — pre-flight detection
   should distinguish "this exact plugin is re-registering its own prior resource" (allowed,
   matches today's reload semantics) from "a different plugin already owns this name"
   (rejected). Check `dp.Manifest.Identifier()`/`p.ID()` against the existing owner before
   flagging a conflict.

## Done means

- A subprocess plugin whose manifest collides with an already-loaded plugin's envelope type,
  UI component ID, or keybinding is rejected at pre-flight, with **zero process spawn** —
  verified by a test asserting no subprocess is started (e.g. assert on the `Manager`'s
  process count/PID list, not just the returned error).
- A subprocess plugin whose manifest collides with an already-loaded (different) plugin's
  command name, CRUD resource type, or HTTP route pattern is likewise rejected at pre-flight
  with zero spawn — new coverage, since no such rejection exists anywhere today.
- A plugin re-registering its own prior resources (a genuine reload of the same plugin) is NOT
  rejected as a false-positive conflict.
- A plugin whose `nanite_compat` range excludes the running `internal/version.Version` is
  rejected at pre-flight with an error naming both values — verified by a test analogous to
  `internal/plugin/shadcn_version_test.go`'s shape.
- Pre-flight runs on all three load paths: boot-time discovery, boot-time builtin registration,
  and the API-driven install/hot-reload path.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
