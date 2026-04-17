# [Medium] GetService returns untyped interface{} — no capability boundary

**Scope:** Plugin capability model
**Topic:** Security — implicit access surface
**Date:** 2026-04-11

## Problem

`Host.GetService(name string) (interface{}, error)` returns a raw `interface{}`. The caller type-asserts to whatever concrete type they want. There is no registry of what services exist, no documentation of what each service exposes, and no enforcement of which plugins may access which services.

Five services are registered at startup, each granting significant capabilities:

| Service name | Concrete type | What it grants |
|---|---|---|
| `"store"` | `*store.Store` | Full database access (Critical — finding 01) |
| `"mcp"` | `*mcp.Manager` | Full MCP tool execution and server lifecycle (Critical — finding 03) |
| `"toolclient"` | `*toolclient.ToolClient` | Tool broker access — tool selection, permission checks |
| `"container"` | Container struct | Service container with references to all core services |
| `"tasks"` | Task service | Task backend registration and management |

## Evidence

`cmd/nanite/main.go:L175-177,L249-251`:

```go
pluginHost.RegisterService("store", s)
pluginHost.RegisterService("mcp", mcpManager)
pluginHost.RegisterService("toolclient", tb)
// ...
pluginHost.RegisterService("container", container)
pluginHost.RegisterService("tasks", container.Tasks)
```

`internal/plugin/host.go:L374-384`:

```go
func (h *Host) GetService(name string) (interface{}, error) {
    h.mu.RLock()
    defer h.mu.RUnlock()
    service, exists := h.services[name]
    if !exists {
        return nil, fmt.Errorf("service %q not found", name)
    }
    return service, nil
}
```

## Impact

- The `"container"` service is particularly dangerous — it wraps references to ALL core services. A plugin that gets the container gets everything.
- New services added in the future are automatically accessible to all plugins with no review gate.
- There is no way to audit which plugins access which services without grep-ing the source.

## Recommendation

1. Replace `GetService(string)` with typed accessors (e.g., `GetPluginStore() PluginStore`, `GetLogger() Logger`). Each accessor returns a scoped interface, not the concrete type.
2. If the generic service registry must be kept for extensibility, add an ACL: each plugin's manifest declares which services it requires, and `GetService` checks the manifest before returning.
3. At minimum, remove `"container"` from the service registry — it grants transitive access to everything.

## References

- `internal/plugin/host.go:L374-392` — GetService / RegisterService
- `cmd/nanite/main.go:L175-251` — service registrations
- SDK interface: `framework/libs/go-plugin/plugin.go:L61` — `GetService` in interface
