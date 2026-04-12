# [Info] Module inventory: complete type and function catalog

**Scope:** `framework/libs/go-plugin/` (all source files)
**Topic:** Inventory
**Date:** 2026-04-11

## Problem

Track I.1 needs a verified inventory of what this module contains before deciding on deletion vs. migration.

## Evidence

### Module metadata

- **Module path:** `github.com/hollis-labs/go-plugin`
- **Go version:** 1.26.1
- **Dependencies:** zero (no `require` block in `go.mod`)
- **Package name:** `plugin`
- **Source files:** 7 (plugin.go, registry.go, uiregistry.go, logger.go, doc.go, example.go, plugin_test.go)
- **Non-source files:** 3 (go.mod, LICENSE, README.md, AUDIT_RESULTS.md)
- **Total lines of Go code:** ~730 (excluding tests)

### Interfaces (3)

| Interface | File | Methods | Purpose |
|-----------|------|---------|---------|
| `Plugin` | plugin.go:12-36 | 8 (ID, Name, Version, Description, Dependencies, Load, Unload, Status) | Core plugin contract |
| `Host` | plugin.go:47-103 | 17 (GetPlugin, RegisterCRUDHandler, RegisterEventHook, RegisterUIComponent, GetService, GetConfig, SetConfig, RegisterConfigSchema, RegisterConnector, RegisterProvider, RegisterCLIAdapter, RegisterCommand, RegisterSlot, RegisterKeybinding, Logger, Context) | Runtime environment for plugins |
| `Logger` | plugin.go:282-288 | 5 (Debug, Info, Warn, Error, With) | Structured logging |

### Additional interfaces (3)

| Interface | File | Methods | Purpose |
|-----------|------|---------|---------|
| `CRUDHandler` | plugin.go:107-122 | 5 (Create, Read, Update, Delete, List) | Plugin resource CRUD |
| `EventHook` | plugin.go:125-131 | 2 (Handle, EventTypes) | Event subscription |
| `UIRegistry` | uiregistry.go:10-25 | 5 (RegisterComponent, GetComponent, GetComponentsByType, ListComponents, UnregisterComponent) | UI component management |

### Optional interfaces (2)

| Interface | File | Purpose |
|-----------|------|---------|
| `Installable` | plugin.go:192-195 | First-time install logic |
| `Uninstallable` | plugin.go:200-202 | Cleanup on uninstall |
| `Connector` | plugin.go:205-212 | Outbound integration (webhook, email, etc.) |

### Structs (11)

| Struct | File | Purpose |
|--------|------|---------|
| `PluginStatus` | plugin.go:39-44 | Plugin state (loaded, enabled, timestamps) |
| `Event` | plugin.go:134-140 | Event payload |
| `UIComponent` | plugin.go:143-150 | UI component definition |
| `UISlotEntry` | plugin.go:178-188 | Slot mount registration |
| `ConfigFieldDef` | plugin.go:215-224 | Config schema for frontend rendering |
| `CommandArg` | plugin.go:228-234 | Slash command argument schema |
| `SlashCommandDef` | plugin.go:237-247 | Full slash command definition |
| `KeybindingDef` | plugin.go:251-258 | Keyboard shortcut definition |
| `PluginError` | plugin.go:266-273 | Typed error with HTTP status code |
| `Registry` | registry.go:28-38 | In-memory plugin registry (implements Host) |
| `ExamplePlugin` | example.go:9-16 | Reference plugin implementation |

### Type definitions and constants

| Type/Const | File | Purpose |
|------------|------|---------|
| `UIComponentType` (string) | plugin.go:153 | Widget, Envelope, Action, Workflow, View |
| `UISlotName` (string) | plugin.go:164 | 8 named mount points (nav-rail, settings-tab, etc.) |
| `ErrCancelled` (var) | plugin.go:263 | Pre-hook cancellation sentinel |

### Exported functions

| Function | File | Purpose |
|----------|------|---------|
| `NewRegistry(Logger) *Registry` | registry.go:41-54 | Create in-memory registry |
| `NewUIRegistry() UIRegistry` | uiregistry.go:16-19 | Create UI component registry |
| `NewDefaultLogger(string) Logger` | logger.go:14-16 | Create stdlib-based logger |
| `ErrNotFound(string) *PluginError` | plugin.go:277 | 404 error constructor |
| `ErrConflict(string) *PluginError` | plugin.go:278 | 409 error constructor |
| `ErrValidation(string) *PluginError` | plugin.go:279 | 422 error constructor |
| `NewExamplePlugin() *ExamplePlugin` | example.go:19-26 | Create reference plugin |

### Registry methods (16)

LoadPlugin, UnloadPlugin, GetPlugin, ListPlugins, RegisterCRUDHandler, GetCRUDHandler, RegisterEventHook, EmitEvent, RegisterUIComponent, GetUIRegistry, RegisterService, GetService, Logger, Context, GetConfig/SetConfig/RegisterConfigSchema/RegisterConnector/RegisterProvider/RegisterCLIAdapter/RegisterCommand/RegisterSlot/RegisterKeybinding (stubs), Shutdown.

### Tests

`plugin_test.go` contains 3 test functions:
- `TestPluginInterface` — load/unload via Registry
- `TestUIRegistry` — register/get/list/unregister components
- `TestEventSystem` — hook registration and event dispatch

## Impact

N/A (informational).

## Recommendation

This inventory should be used as the migration checklist if the module is inlined into nanite. Every interface, type, and function listed above must have a corresponding definition in the target location.

## References

- All files in `framework/libs/go-plugin/`.
