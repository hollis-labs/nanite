# [Critical] Slot system exists as data structures but is not wired into the generate path

**Scope:** context-management
**Topic:** Correctness / Claimed-vs-actual
**Date:** 2026-04-11

## Problem

The slot system (`internal/context/slot.go`, `window.go`) defines 8 named slots (system, memory, agent, rules, tools, session, context, conversation) with token budgets, cache keys, and assembly ordering. A `ContextService.AssembleSlots()` method wraps the slot system for use by the chat engine. **None of this is wired into the actual generate path.** The generate path at `internal/service/chat_generate.go:109` calls `s.context.AssembleContext()` — the legacy flat-string path — exclusively.

## Evidence

The generate path:

```go
// internal/service/chat_generate.go:109
systemPrompt, chatMessages, err := s.context.AssembleContext(ctx, session, agent, mode, workspace)
```

`AssembleSlots` callers outside tests:

```
$ rg "AssembleSlots" --type go
internal/service/context.go:23:   AssembleSlots(...)  // interface definition
internal/service/context.go:68:   func (s *contextServiceImpl) AssembleSlots(...)  // implementation
internal/service/context_test.go:86:   func TestContextService_AssembleSlots(...)  // test
internal/service/context_test.go:116:  result, err := svc.AssembleSlots(...)  // test call
```

No non-test caller exists. The entire `internal/context/` package — `slot.go` (84 lines), `window.go` (197 lines), `compaction.go` (268 lines), `tokens.go` (20 lines) — is reachable only from `service/context.go:AssembleSlots`, which is itself never called from production code.

Additionally, `AssembleSlots` only populates 2 of 8 defined slots:

```go
// internal/service/context.go:77-85
// For now, the system prompt goes into the System slot. As we add
// memory, agent, and rules slots in later phases, we'll split the
// system prompt into its constituent parts.
cw.SetContent(ctxpkg.SlotSystem, systemPrompt)
convContent := serializeMessagesForSlot(messages)
cw.SetContent(ctxpkg.SlotConversation, convContent)
```

The memory, agent, rules, tools, session, and context slots are defined but never populated.

## Impact

The slot system is a claimed feature that does not function. Callers cannot allocate a slot, fill it, swap it, and see the change reflected in context assembly — the infrastructure exists in `internal/context/` but the integration layer (`AssembleSlots`) is never invoked, and even if it were, only 2 of 8 slots are populated. Any documentation, UI, or user expectation around slot-based context management is inaccurate.

The `ContextWindow` struct is ephemeral — created fresh in each `AssembleSlots` call — so `PrevHashes` (which tracks cache state across turns for cache-hit detection) resets every call and can never actually detect a cache hit in production, even if `AssembleSlots` were wired in.

## Recommendation

Two paths:

1. **Wire `AssembleSlots` into the generate path** (recommended). Replace the `AssembleContext` call in `chat_generate.go` with `AssembleSlots`, translate `SlotBlock` output into provider-specific payloads, and persist the `ContextWindow` across turns (likely on the `chatServiceImpl` struct, keyed by session ID) so cache-hit detection works. Then incrementally populate the remaining 6 slots.

2. **Remove the slot system** if the feature is not planned for near-term. The dead code adds ~570 lines of test surface and maintenance burden without behavioral value.

Either way, the current state should not be presented as functional.

## References

- `internal/context/slot.go` — slot definitions
- `internal/context/window.go` — ContextWindow with budget, cache, assembly
- `internal/service/context.go:68` — AssembleSlots implementation (never called)
- `internal/service/chat_generate.go:109` — actual generate path uses AssembleContext
- Related: [02-critical-compact-api-is-mvp-stub.md](02-critical-compact-api-is-mvp-stub.md) — CompactionPipeline also unwired
