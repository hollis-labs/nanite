# [Low] No resource limits per plugin — goroutines, memory, file descriptors

**Scope:** Plugin capability model
**Topic:** Resource management
**Date:** 2026-04-11

## Problem

Plugins run in-process with no resource limits. A plugin can spawn unlimited goroutines, allocate unbounded memory, open unlimited file descriptors, and consume unlimited CPU. There is no per-plugin accounting or circuit breaker.

## Evidence

The Host provides `Context()` for plugin lifecycle, but plugins are not required to use it for their goroutines. The event hook dispatch uses `context.WithTimeout(h.ctx, 5*time.Second)` for individual hook calls, but there is no aggregate limit on how many hooks a plugin can register or how much work they do.

- `RegisterEventHook` — no limit on number of hooks per plugin or per event type
- `RegisterCRUDHandler` — no request rate limit on the wired routes
- `RegisterUIComponent` — no limit on number of components
- `RegisterConnector` — no limit on number of connectors
- `RegisterFilter` — no limit on filter chain length

## Impact

Low because the in-process model inherently shares resources and this is a documented design choice. However, a buggy plugin (not necessarily malicious) that leaks goroutines or accumulates state will degrade the entire system with no attribution or isolation.

## Recommendation

1. Add per-plugin goroutine tracking (e.g., a `sync.WaitGroup` per plugin for host-spawned goroutines).
2. Add per-plugin registration counts in the host with configurable caps (e.g., max 10 event hooks per plugin, max 50 UI components).
3. Log per-plugin resource usage at INFO level on shutdown for post-mortem analysis.

## References

- `internal/plugin/host.go` — all Register* methods (no caps)
- `internal/plugin/host.go:L1080-1100` — goroutine spawning in EmitEvent
