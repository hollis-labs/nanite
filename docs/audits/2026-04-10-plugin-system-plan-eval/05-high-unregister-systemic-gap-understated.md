# [High] Plan's "Unregister paths" section understates how many registration types leak on unload

**Scope:** plugin system — host lifecycle
**Topic:** plan-completeness / plan-accuracy
**Date:** 2026-04-10

## Problem

The plan's B.6 says `host.go` needs `Unregister*` for "commands, slots, keybindings, CRUD handlers, HTTP routes, MCP servers, event hooks, envelope type registrations, config schemas, UI components." That list is incomplete. The plan also calls the sharp-edge "event hook unregistration" as if it is uniquely hard — but the same plugin-id ownership gap exists for commands, slots, CRUD handlers, HTTP routes, connectors, filters, services, task backends, and config schemas. Only UI components, filters, and keybindings currently track plugin ownership. The plan reads like "add Unregister next to each Register," which understates the work: every Register* call site needs an ownership entry first, then Unregister, then the audit of every `Load()` method in every existing plugin to make sure it's registering things that the new cleanup can find.

## Evidence

Current ownership tracking fields in `Host` (`host.go:L56-82`):

```go
type Host struct {
    mu            sync.RWMutex
    plugins       map[string]plugin.Plugin
    eventHooks    map[string][]plugin.EventHook      // no ownership
    crudHandlers  map[string]plugin.CRUDHandler      // no ownership
    uiComponents  []plugin.UIComponent
    uiOwners      map[string]string // component ID → plugin ID  ✓
    connectors    map[string]plugin.Connector
    connectorOwners  map[string]string               // ✓
    connectorHealth  map[string]*ConnectorStatus
    commands      CommandRegistrar                    // no ownership (delegated to chat.CommandRegistry)
    keybindings   map[string]KeybindingDef
    kbOwners      map[string]string                  // ✓
    slots         map[UISlotName][]UISlotEntry       // no ownership
    services      map[string]interface{}             // no ownership
    configs       map[string]*PluginConfig           // keyed by pluginID but never unregistered
    // ...
    pendingRoutes []pendingRoute                     // no ownership
    triggers      *TriggerDispatcher
    filters       *FilterRegistry                    // ✓ (via filters.RemoveByPlugin)
}
```

And `UnloadPlugin` at `host.go:L1035-1069` only cleans up filters, event hooks (with a no-op loop because there's no ownership), UI components, and keybindings:

```go
delete(h.plugins, id)

// Clean up plugin-owned registrations: filters, event hooks, UI components,
// slots, keybindings. This prevents stale handlers from running after unload.
if removed := h.filters.RemoveByPlugin(id); removed > 0 { ... }

// Remove event hooks owned by this plugin.
for eventType, hooks := range h.eventHooks {
    filtered := hooks[:0]
    for _, hook := range hooks {
        // EventHook interface doesn't expose plugin ID, so we can't selectively
        // remove per-plugin hooks here without extending the interface. This is
        // a known limitation — tracked for future cleanup.
        filtered = append(filtered, hook)
    }
    h.eventHooks[eventType] = filtered
}
// Remove UI components owned by this plugin.
// ...
// Remove keybindings owned by this plugin.
// ...
```

Explicitly missing from UnloadPlugin today:

| Registration | Register site | Cleanup on unload? | Plugin ID tracked? |
|---|---|---|---|
| Slash commands | `host.go:L718` `RegisterCommand` (delegates to chat registry) | **No** | **No** |
| Slots | `host.go:L738` `RegisterSlot` | **No** | **No** |
| HTTP routes | `host.go:L172` `RegisterHTTPHandler` | **No** (also stdlib limit) | **No** |
| CRUD handlers | `host.go:L186` `RegisterCRUDHandler` | **No** | **No** |
| Event hooks | `host.go:L250` `RegisterEventHook` | Loop exists but no-op (comment admits it) | **No** |
| Config schemas | `host.go:L508` `RegisterConfigSchema` | **No** | **No** |
| Connectors | `host.go:L539` `RegisterConnector` | **No** | **Yes** (connectorOwners is tracked but never consulted on unload) |
| Providers | `host.go:L673` `RegisterProvider` | **No** | **No** |
| CLI adapters | `host.go:L698` `RegisterCLIAdapter` | **No** | **No** |
| Services | `host.go:L387` `RegisterService` | **No** | **No** |
| Task backends | `host.go:L403` `RegisterTaskBackend` | **No** | **No** |
| Filters | via `filters.Register(name, pluginID, ...)` | **Yes** (RemoveByPlugin) | **Yes** |
| UI components | `host.go:L265` `RegisterUIComponent` | **Yes** | **Yes** |
| Keybindings | `host.go:L821` `RegisterKeybinding` | **Yes** | **Yes** |
| Envelope types | `chat.RegisterEnvelopeType` (outside host) | **No** | **No** |

Eleven of fourteen categories have no plugin-ID ownership and/or no unload cleanup. The plan's framing makes it sound like event hooks are the one sharp edge (`"Sharp edge: event hook unregistration"`); in fact nine other categories need the same interface extension.

Also: `connectorOwners` exists but is never read anywhere — dead bookkeeping.

## Impact

Track G (install/uninstall flow) will ship with leaks in all of the un-cleaned categories. The user uninstalls a plugin; its slash commands still show in `/help`, its CRUD routes still respond 500, its config schema still appears in settings, its services are still callable by other plugins. On re-install, duplicate registrations either overwrite or error depending on the specific `Register*` path. The "hot uninstall" capability is a lie for anything beyond the categories UnloadPlugin currently handles.

This also means Track H (migrating existing plugins) will reveal hidden reliance on leaked state — a second install of support-ticket will find its old CRUD handlers still wired, config schema duplicated, etc. Track H's integration tests will find these as "flaky reinstall" failures that look like unrelated bugs.

## Recommendation

Split Track B.6 into two sub-phases:

**B.6a — Ownership audit and backfill.** Before writing any Unregister method, add plugin-ID tracking to every Register* that doesn't have it. Pattern: mirror `uiOwners` for each category. Touch points:

- `cmdOwners map[string]string` in Host struct; set in RegisterCommand
- `slotOwners map[string]string` (slot entry ID → plugin ID) in Host struct; set in RegisterSlot
- `routeOwners map[string][]string` (plugin ID → list of registered patterns)
- `crudOwners map[string]string` (resource type → plugin ID)
- `eventHookOwners` — either add `PluginID() string` to the EventHook interface (plan's current approach) OR wrap each registered hook in an internal struct that records the owner. The wrapper approach is less invasive (doesn't break the SDK interface).
- `configSchemaOwners map[string]string` (config key → plugin ID)
- `serviceOwners`, `providerOwners`, `adapterOwners`, `taskBackendOwners` — same pattern
- Delete dead `connectorOwners` or actually use it on unload

**B.6b — Unregister methods.** Only after ownership is in place, add `Unregister*` methods and update `UnloadPlugin` to call them.

**Prefer wrapping to interface change.** The plan's "add `PluginID()` to EventHook" is a breaking change to the SDK interface. Wrapping is equivalent semantics without the breaking change:

```go
type ownedEventHook struct {
    plugin.EventHook
    pluginID string
}

func (h *Host) RegisterEventHook(eventTypes []string, hook plugin.EventHook) error {
    // ...
    owned := &ownedEventHook{EventHook: hook, pluginID: h.activePlugin}
    for _, t := range normalized {
        h.eventHooks[t] = append(h.eventHooks[t], owned)
    }
}
```

Then the unload loop can type-assert to `*ownedEventHook` to check ownership. The SDK interface stays stable.

**Also:** add a gate to Track B.13: "unload plugin X and verify `host.ListCommands()`, `host.GetSlotEntries()`, `host.GetCRUDHandlers()`, `host.GetConfigSchema()`, `host.GetConnectors()` return no entries that reference plugin X." Currently the plan's gate only checks "the unload removes all registrations" generically.

## References

- `internal/plugin/host.go:L56-82` — Host struct fields
- `internal/plugin/host.go:L1010-1069` — UnloadPlugin with partial cleanup
- `internal/plugin/host.go:L247-262` — RegisterEventHook (no ownership)
- `internal/plugin/host.go:L186-245` — RegisterCRUDHandler (no ownership)
- `internal/plugin/host.go:L718-820` — RegisterCommand, RegisterSlot (no ownership)
- `internal/plugin/host.go:L1046-1050` — the self-admitted no-op loop for event hooks
- Plan §B.6 — current understated framing
- Related: finding 01 (UnloadPlugin deadlock) — this work all happens under `h.mu`, which makes the deadlock fix prerequisite
