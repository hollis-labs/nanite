# [High] Hot-swap does not exist as a feature

**Scope:** context-management
**Topic:** Correctness / Claimed-vs-actual
**Date:** 2026-04-11

## Problem

There is no code path that allows a running session's context slots to be swapped at runtime without restarting the session. The `ContextWindow.SetContent()` method exists and correctly updates slot content, token counts, and cache keys. However, `ContextWindow` is ephemeral — created fresh in every `AssembleSlots` call (`service/context.go:75`) — so there is no persistent window object whose slots could be "swapped" mid-session.

## Evidence

`ContextWindow` creation:

```go
// internal/service/context.go:75
cw := ctxpkg.NewContextWindow(providerWindowSize, s.estimator)
```

A new window is created every time `AssembleSlots` is called. There is no session-keyed storage of `ContextWindow` instances:

```
$ rg "ContextWindow" --type go (excluding internal/context/)
internal/service/context.go:    cw := ctxpkg.NewContextWindow(...)  // created, used, discarded
```

The `PrevHashes` field on `ContextWindow` (which tracks cache state across turns for delta detection) is initialized empty every call and populated during `Assemble()`, then discarded when the function returns. Cache-hit detection across turns is structurally impossible.

Search for "swap" in context-related code:

```
$ rg -i "hot.?swap|hotswap|HotSwap|SwapSlot" --type go
internal/service/stream.go:82:  if prev, loaded := sm.sessionSSE.Swap(...)  // SSE connection swap, unrelated
internal/plugin/subprocess/transport.go:16:  // designed to be swappable  // transport swap, unrelated
```

No context-management swap functionality exists.

## Impact

If hot-swap was an expected feature (e.g., switching an agent's rules or memory mid-conversation without losing message history), it does not work. The infrastructure for it exists in `Slot`, `SlotBlock`, and `ContextWindow` data structures, but the runtime lifecycle — persisting a window across turns, exposing a swap API, and reflecting the swap in the next provider call — is absent.

## Recommendation

If hot-swap is a planned feature:

1. Persist `ContextWindow` per-session on the `chatServiceImpl` (e.g., `map[string]*ctxpkg.ContextWindow` protected by a mutex, or stored in the session row).
2. Expose a swap API (e.g., `POST /api/sessions/{id}/context/slots/{name}`) that calls `SetContent` on the persisted window.
3. Have the generate path use the persisted window instead of creating a fresh one.
4. Add cache-hit tracking to avoid re-sending unchanged slots to providers that support caching (Anthropic `cache_control`).

If hot-swap is not planned, document the slot system as "budget accounting and assembly ordering only" rather than implying runtime mutability.

## References

- `internal/context/window.go:35-61` — NewContextWindow (ephemeral creation)
- `internal/context/window.go:64-74` — SetContent (functional but ephemeral)
- `internal/context/window.go:131-171` — Assemble with PrevHashes (cache hit detection that can never work)
- `internal/service/context.go:68-99` — AssembleSlots creates and discards ContextWindow
