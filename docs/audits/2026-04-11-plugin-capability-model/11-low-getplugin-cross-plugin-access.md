# [Low] GetPlugin allows cross-plugin state access

**Scope:** Plugin capability model
**Topic:** Security — isolation gap
**Date:** 2026-04-11

## Problem

`Host.GetPlugin(id)` is part of the SDK interface and returns another loaded plugin by ID. The returned `plugin.Plugin` interface exposes `Status()`, `ID()`, `Name()`, `Version()`, `Description()`, and `Dependencies()`. While this is read-only metadata, the caller receives the concrete plugin object and can type-assert it to the known implementation type to access internal state or call unexported methods (if in the same package) or exported non-interface methods.

## Evidence

SDK interface at `framework/libs/go-plugin/plugin.go:L49`:

```go
// GetPlugin retrieves another loaded plugin by ID
GetPlugin(id string) (Plugin, bool)
```

Implementation at `internal/plugin/host.go:L177-182`:

```go
func (h *Host) GetPlugin(id string) (plugin.Plugin, bool) {
    h.mu.RLock()
    defer h.mu.RUnlock()
    p, exists := h.plugins[id]
    return p, exists
}
```

Returns the actual plugin object, not a metadata-only proxy.

## Impact

- Low in practice because the returned `plugin.Plugin` interface is narrow (6 read-only methods + `Load`/`Unload`).
- A plugin could call `Unload()` on another plugin, causing it to be unloaded from under the host's feet (the host wouldn't update its internal maps).
- Type assertion to the concrete type (e.g., `*supportticket.SupportPlugin`) gives access to that plugin's internal state.

## Recommendation

Return a metadata-only wrapper instead of the real plugin object:

```go
type PluginInfo struct {
    id, name, version, description string
    deps []string
    status plugin.PluginStatus
}
```

Or guard `Load`/`Unload` to only be callable from the host.

## References

- `framework/libs/go-plugin/plugin.go:L49` — GetPlugin in SDK interface
- `internal/plugin/host.go:L177-182` — implementation
