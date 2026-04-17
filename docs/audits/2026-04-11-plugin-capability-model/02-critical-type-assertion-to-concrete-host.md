# [Critical] Type assertion to concrete Host bypasses SDK interface boundary

**Scope:** Plugin capability model
**Topic:** Security — isolation boundary defeat
**Date:** 2026-04-11

## Problem

The plugin SDK defines a narrow `plugin.Host` interface (13 methods). However, because all plugins — including those in `plugins/` — run in the same Go process and import `internal/plugin`, they can type-assert the interface back to `*hostplugin.Host` and access every method on the concrete struct, including methods deliberately excluded from the SDK: `RegisterHTTPHandler`, `RegisterFilter`, `RegisterSlot`, `RegisterKeybinding`, `PlaceArtifact`, `RegisterService`, `SetStore`, `SetRouter`, `SetCommandRegistry`, `EmitEvent`, `EmitPreHook`, `SubscribeEvents`, `Shutdown`, and all internal state.

## Evidence

**fragments-engine plugin** — `plugins/fragments-engine/plugin.go:L76`:

```go
if nh, ok := host.(*hostplugin.Host); ok {
    if err := nh.RegisterSlot(hostplugin.UISlotEntry{
```

**fragments-engine plugin** — `plugins/fragments-engine/plugin.go:L121`:

```go
h, ok := host.(*hostplugin.Host)
if !ok {
    host.Logger().Warn("fragments-engine: cannot register HTTP routes — host type assertion failed")
    return
}

h.RegisterHTTPHandler("POST /api/plugins/engine/backlog", http.HandlerFunc(p.handleCreateBacklog))
```

The pattern works because `*Host` satisfies `plugin.Host`, and the concrete type is always `*Host` for in-process plugins. Any plugin author reading the fragments-engine example learns this escape hatch.

## Impact

A malicious plugin can:

1. **Call `h.Shutdown()`** — kill the entire plugin host and all loaded plugins.
2. **Call `h.RegisterService("store", maliciousStore)`** — replace the store service for all other plugins.
3. **Call `h.SetRouter(maliciousRouter)`** — replace the HTTP router.
4. **Call `h.EmitEvent(crafted)`** — emit arbitrary events that trigger other plugins' hooks.
5. **Call `h.SubscribeEvents()`** — eavesdrop on all system events (session data, tool calls, message content).
6. **Call `h.RegisterHTTPHandler()`** — mount routes on the server's mux (no auth check by default if the middleware chain is applied at a higher level, which it is — CRUD routes go through the same mux as authenticated routes).
7. **Access `h.store` (unexported but same package won't work) — however, via `h.GetService("store")` the same result is achieved.**

The SDK interface boundary is a documentation convention, not an enforcement mechanism.

## Recommendation

This is an inherent limitation of in-process Go plugins. True isolation requires one of:

1. **Subprocess plugins only** — the subprocess runtime already enforces the SDK boundary because the subprocess communicates via JSON-RPC and cannot type-assert.
2. **Separate binary for plugin host** — plugins load into a different process.
3. **Accept and document** — if in-process plugins are a deliberate design choice (they are), then the SDK interface is advisory. Document that in-process plugins have full host access and that the SDK interface is the *supported* API, not a security boundary. Treat `plugins/` as trusted code (same trust level as `internal/`).

For the current architecture, the recommended approach is option 3 plus making `RegisterSlot`, `RegisterKeybinding`, and `RegisterHTTPHandler` part of the official SDK interface (they're needed by real plugins and shouldn't require a type assertion).

## References

- `plugins/fragments-engine/plugin.go:L76,L121` — type assertion examples
- `internal/plugin/host.go` — concrete Host struct with all methods
- SDK interface: `framework/libs/go-plugin/plugin.go:L47-104`
