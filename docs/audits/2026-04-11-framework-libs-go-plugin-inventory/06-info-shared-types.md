# [Info] Shared types: SDK is the source of truth

**Scope:** `framework/libs/go-plugin/plugin.go` + `nanite/internal/plugin/types.go`
**Topic:** Inventory
**Date:** 2026-04-11

## Problem

The task asks whether types/interfaces are shared between the SDK module and nanite's internal plugin package. This finding documents the sharing mechanism.

## Evidence

### Sharing mechanism: type aliases in `types.go`

`nanite/internal/plugin/types.go:L1-14`:
```go
package plugin

import (
	goplugin "github.com/hollis-labs/go-plugin"
)

type UISlotName = goplugin.UISlotName
type UISlotEntry = goplugin.UISlotEntry
type CommandArg = goplugin.CommandArg
type SlashCommandDef = goplugin.SlashCommandDef
type KeybindingDef = goplugin.KeybindingDef
```

These are Go **type aliases** (`=`), not type definitions. This means:
- A `goplugin.SlashCommandDef` and a `plugin.SlashCommandDef` (from nanite's internal package) are the **same type** at compile time.
- No conversion is needed when passing values between the SDK and nanite's internal code.
- The SDK is the single source of truth for the type definition.

### Constants re-exported

`nanite/internal/plugin/types.go:L16-24` re-exports all 8 SDK slot constants and adds 6 nanite-specific ones.

### Direct SDK type usage (no alias)

Most SDK types are used directly via the `plugin` import alias without re-export. For example, in `host.go`:

```go
plugins       map[string]plugin.Plugin
eventHooks    map[string][]plugin.EventHook
crudHandlers  map[string]plugin.CRUDHandler
uiComponents  []plugin.UIComponent
connectors    map[string]plugin.Connector
```

These types exist only in the SDK. There is no duplicate definition in nanite.

### Subprocess protocol: wire-type duplication

`nanite/internal/plugin/subprocess/protocol.go:L140-175` defines its own `UISlotEntry`, `KeybindingDef`, and `CommandArg` structs that are **separate types** from the SDK types. This is intentional to avoid import cycles (subprocess is a sub-package of internal/plugin). The parent package translates between these wire types and the SDK types during subprocess plugin loading.

## Impact

The SDK module is structurally embedded in nanite's type system. Deletion without migration would require:
- Replacing all 5 type aliases in `types.go`
- Replacing all direct `plugin.X` type references across 41 files
- Updating the subprocess protocol's `plugin.ConfigFieldDef` and `plugin.UIComponentType` references

This is a straightforward but wide-reaching refactor (41 files touched).

## Recommendation

The type alias approach in `types.go` is a good migration seam. If the SDK is inlined:
1. Replace the aliases with full type definitions copied from the SDK.
2. The 6 nanite-specific slot constants stay as-is.
3. Update all `plugin.X` references in `host.go` and other files to use the local definitions.
4. The subprocess protocol's `plugin.ConfigFieldDef` / `plugin.UIComponentType` references need updating.

Estimated scope: 41 files, mostly mechanical import-path changes. No logic changes required.

## References

- `nanite/internal/plugin/types.go:L1-31`
- `nanite/internal/plugin/host.go:L56-80`
- `nanite/internal/plugin/subprocess/protocol.go:L140-175`
- `framework/libs/go-plugin/plugin.go` (full file, type definitions)
