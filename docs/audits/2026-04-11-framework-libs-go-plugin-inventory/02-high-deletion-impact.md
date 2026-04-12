# [High] Deletion breaks 4+ consumers and all external plugins

**Scope:** `framework/libs/go-plugin/` (all files)
**Topic:** Deletion impact
**Date:** 2026-04-11

## Problem

Track I.1 proposes deleting `framework/libs/go-plugin/` without a verified inventory. Deletion would break compilation of at least 4 Go modules and 3 external plugins that import `github.com/hollis-labs/go-plugin`.

## Evidence

### Direct `go.mod` consumers (verified via `grep` across `~/Projects-apps/`)

| Consumer | go.mod location | Replace directive |
|----------|----------------|-------------------|
| **nanite** | `nanite/go.mod:9,24` | `=> ../framework/libs/go-plugin` |
| **vanta-conduit** | `vanta-conduit/go.mod:6,21` | `=> ../framework/libs/go-plugin` |
| **fragments-engine** | `fragments-engine/engine/go.mod:6,27` | `=> ../../framework/libs/go-plugin` |
| **nanite-verify-agentrc** | `nanite-verify-agentrc/plugin-alias/go.mod:5,7` | absolute path to framework |
| **.archived/cortex** | `.archived/cortex/go.mod:6,20` | `=> ../../framework/libs/go-plugin` |
| **framework verify worktree** | `framework/.verify-sessionstats.buBu4U/go.mod:9,24` | relative path |

### Nanite import sites (41 files)

Every file in `nanite/internal/plugin/` imports the SDK. Key import patterns:

- `nanite/internal/plugin/host.go` — imports `plugin` (the SDK) for `Plugin`, `EventHook`, `CRUDHandler`, `UIComponent`, `Connector`, `Event`, `Logger`, `ConfigFieldDef`, `UIComponentType` constants.
- `nanite/internal/plugin/types.go` — re-exports 5 type aliases and 8 slot constants from the SDK.
- `nanite/internal/plugin/registry.go` — `PluginConstructor` returns `fplugin.Plugin`.
- `nanite/internal/plugin/loader.go` — `LoadDiscovered` and `LoadRegisteredBuiltins` return `[]fplugin.Plugin`.
- `nanite/internal/plugin/logger.go` — implements `plugin.Logger`.
- `nanite/internal/plugin/subprocess/protocol.go` — uses `plugin.ConfigFieldDef`, `plugin.UIComponentType`.
- All 13 builtin plugins (`adapter-*`, `sessionstats`, `oembed`, `giphy`, etc.) import the SDK.
- `nanite/internal/api/plugins.go` and `nanite/internal/memory/extraction.go` import the SDK.
- `nanite/cmd/nanite/plugin_cmd.go` imports the SDK.

### External plugins in framework repo

| Plugin | File | Import |
|--------|------|--------|
| `support-ticket` | `framework/plugins/nanite/support-ticket/plugin.go` | `github.com/hollis-labs/go-plugin` |
| `session-stats` | `framework/plugins/nanite/session-stats/plugin.go` | `github.com/hollis-labs/go-plugin` |
| `observability-widgets` | `framework/plugins/nanite/observability-widgets/plugin.go` | `plugin "github.com/hollis-labs/go-plugin"` |

### vanta-conduit import sites (7 files)

`vanta-conduit/internal/plugin/host.go`, `events.go`, `loader.go`, `registry.go`, `logger.go`, plus `internal/contextcli/plugin_cmd.go`.

### fragments-engine import sites (5 files)

`fragments-engine/engine/internal/plugin/host.go`, `events.go`, `loader.go`, `registry.go`, `logger.go`.

## Impact

Deleting the module without migration causes:

1. **Immediate compilation failure** of nanite, vanta-conduit, and fragments-engine. All three have `replace` directives pointing to the local path.
2. **External plugin compilation failure** for all three framework plugins.
3. **Test verification failure** for `nanite-verify-agentrc/plugin-alias/`.
4. **CI breakage** for any pipeline that builds any of the above.

The module has zero external dependencies (`go.mod` contains only the module declaration and Go version). It is purely an interface/type definition package. This makes it a good candidate for inlining, but the migration must happen before deletion.

## Recommendation

**Before deletion, execute this migration sequence:**

1. Copy all types and interfaces from `go-plugin/plugin.go` into a new `nanite/pkg/plugin/` (or `nanite/internal/plugin/sdk.go`).
2. Update all nanite import paths from `github.com/hollis-labs/go-plugin` to the new location.
3. Remove the `replace` directive from `nanite/go.mod`.
4. For vanta-conduit and fragments-engine: either (a) inline the same types, or (b) point their `replace` at nanite's new location, or (c) publish the types as a Go module if external plugins must remain separately compiled.
5. Update or archive the three framework external plugins.
6. Only then delete `framework/libs/go-plugin/`.

**Alternative (recommended if external plugins are supported):** Move the module to `nanite/pkg/plugin/` as a stable public API package. External plugins change their import path but keep compiling against a published contract.

## References

- All `go.mod` files cited in the evidence table.
- `nanite/internal/plugin/types.go:L1-31` — the re-export shim that would need updating.
- Track I.1 plan (referenced in scope instructions).
