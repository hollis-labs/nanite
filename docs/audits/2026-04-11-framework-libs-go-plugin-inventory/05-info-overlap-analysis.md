# [Info] Overlap analysis: go-plugin vs nanite internal/plugin

**Scope:** `framework/libs/go-plugin/` vs `nanite/internal/plugin/`
**Topic:** Inventory
**Date:** 2026-04-11

## Problem

Understanding the relationship between the SDK module and nanite's internal plugin package is required before any deletion or migration decision.

## Evidence

### Architecture: SDK defines contracts, nanite implements them

The two packages have a clear layered relationship, not redundant overlap:

| Layer | Package | Role |
|-------|---------|------|
| **SDK (contract)** | `github.com/hollis-labs/go-plugin` | Defines `Plugin`, `Host`, `Logger`, `CRUDHandler`, `EventHook`, `Connector` interfaces plus all shared types (`Event`, `UIComponent`, `SlashCommandDef`, etc.). Zero dependencies. |
| **Host (implementation)** | `nanite/internal/plugin/` | Implements the `Host` interface with nanite-specific logic: store-backed config, router-backed routes, filter chains, trigger dispatch, subprocess bridge, etc. Imports the SDK. |

### What nanite's internal/plugin adds beyond the SDK

Nanite's internal package contains ~30 files with substantial additional functionality:

- **`host.go` (~1100 lines)** — Full `Host` implementation with store integration, router mounting, connector health tracking, filter chains, trigger dispatch, pending route queue, keybinding registry, slot registry, artifact placement, event streaming (SSE).
- **`types.go`** — Re-exports 5 SDK types as aliases + defines 6 additional slot constants (`SlotComposerAbove`, `SlotComposerBelow`, `SlotMessageActions`, `SlotMessageHeader`, `SlotSessionSidebar`, `SlotModal`).
- **`loader.go`** — Plugin discovery (`plugin.yaml` parsing), topological dependency sort, subprocess plugin instantiation.
- **`events.go`** — 35+ typed event emission helpers (`EmitSessionCreated`, `EmitMessageSent`, etc.) plus `EmitPreHook` with cancellation.
- **`filter.go`** — `FilterRegistry` with priority-ordered synchronous filter chains at 6 named filter points.
- **`triggers.go`** — `TriggerDispatcher` for event-to-connector routing with CEL filter expressions.
- **`crud.go`** — CRUD handler wiring with HTTP route auto-registration.
- **`config.go`** — `PluginConfig` with env var / DB / file / default resolution cascade.
- **`subprocess/`** — Full JSON-RPC bridge for out-of-process plugins (protocol.go, plugin.go, plugin_test.go).
- **`registry.go`** — Global `PluginConstructor` registry for compiled-in plugins (different from SDK's `Registry`).
- **`logger.go`** — Logger implementation (reimplements the SDK's `defaultLogger` with slightly different formatting).
- **`manage.go`** — Plugin install/uninstall/enable/disable operations.
- **`scaffold/`** — Plugin template generation.
- **`builtin/`** — 13 builtin plugin implementations.

### Types that exist in BOTH packages

| Type | SDK definition | Nanite usage |
|------|---------------|--------------|
| `Plugin` interface | `plugin.go:12` | Used everywhere as `plugin.Plugin` |
| `Host` interface | `plugin.go:47` | Implemented by `Host` struct in `host.go:56` |
| `Logger` interface | `plugin.go:282` | Implemented by `Logger` struct in `logger.go:12` |
| `Event` struct | `plugin.go:134` | Used as `plugin.Event` in host, events, triggers |
| `UIComponent` struct | `plugin.go:143` | Used as `plugin.UIComponent` in host |
| `UIComponentType` | `plugin.go:153` | Used for validation in `host.go:22-28` |
| `CRUDHandler` interface | `plugin.go:107` | Used as `plugin.CRUDHandler` in host, crud |
| `EventHook` interface | `plugin.go:125` | Used as `plugin.EventHook` in host |
| `Connector` interface | `plugin.go:205` | Used as `plugin.Connector` in host |
| `ConfigFieldDef` struct | `plugin.go:215` | Used as `plugin.ConfigFieldDef` in host |
| `SlashCommandDef` struct | `plugin.go:237` | Re-exported as type alias in types.go |
| `UISlotEntry` struct | `plugin.go:178` | Re-exported as type alias in types.go |
| `CommandArg` struct | `plugin.go:228` | Re-exported as type alias in types.go |
| `KeybindingDef` struct | `plugin.go:251` | Re-exported as type alias in types.go |
| `UISlotName` type | `plugin.go:164` | Re-exported as type alias in types.go |
| `PluginError` struct | `plugin.go:266` | Used in CRUD error mapping |
| `ErrCancelled` var | `plugin.go:263` | Used in pre-hook cancellation |

### What would NOT need migration (SDK-only)

- `Registry` struct and all its methods — nanite does not use the SDK's Registry. Nanite has its own `Host` and its own global constructor registry.
- `UIRegistry` interface and `uiRegistryImpl` — nanite manages UI components directly in `Host`.
- `NewDefaultLogger` — nanite has its own logger implementation.
- `ExamplePlugin` — reference code only.
- `NewRegistry`, `NewUIRegistry` — factory functions for SDK types nanite doesn't use.

## Impact

The SDK is the canonical source of truth for the plugin contract. Nanite's internal package is purely an implementor and consumer. There is no redundant logic -- the relationship is interface-vs-implementation.

## Recommendation

If inlining the SDK into nanite:
1. Move all interface and type definitions from `plugin.go` into a new file (e.g., `nanite/internal/plugin/sdk_types.go` or `nanite/pkg/plugin/types.go`).
2. Delete the type aliases in `types.go` and update imports.
3. The `Registry`, `UIRegistry`, `defaultLogger`, and `ExamplePlugin` can be dropped -- nanite doesn't use them.
4. The `subprocess/protocol.go` types that reference `plugin.ConfigFieldDef` and `plugin.UIComponentType` must be updated to the new import path.

## References

- `framework/libs/go-plugin/plugin.go` (full file)
- `nanite/internal/plugin/types.go:L1-31`
- `nanite/internal/plugin/host.go:L1-80`
