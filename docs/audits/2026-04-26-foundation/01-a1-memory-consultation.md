# A1 Audit: Memory consultation verification (Vanta → chat Memory slot)

**Ticket:** CW-20260426-0001
**Date:** 2026-04-26
**Verdict:** WIRED

## Trace

**Entry point:** `internal/service/chat_generate.go:1369`
`generateResponse` calls `s.context.AssembleSlots(...)`.

**AssembleSlots** (`internal/service/context.go:171`) delegates to:
`s.client.AssembleSlotSources(ctx, session, agent, mode, workspace)` → `internal/chat/context_client.go:138`.

**AssembleSlotSources** (`internal/chat/context_client.go:173–185`):
```go
if cb.ContextBroker != nil {
    intent := cb.deriveIntent(session, agent)
    packet, err := cb.ContextBroker.Fetch(ctx, intent)
    // ...
    memoryContent = formatPacketItemsBySource(packet, true)   // Source == "memory"
    contextContent = formatPacketItemsBySource(packet, false) // Source != "memory"
}
```

**ContextBroker.Fetch** (`internal/contextbroker/broker.go:142`) fans out to all registered sources,
including **MemorySource** (`internal/contextbroker/source_memory.go:33`), which calls
`s.Memory.Recall(ctx, opts)` — a real Vanta Conduit `store.Recall` call with
`Ranking("relevance")`, session/project/user namespace cascade, and a `MinConfidence: 0.4` filter.

**Slot set:** Back in AssembleSlots (`internal/service/context.go:180`):
```go
cw.SetContent(ctxpkg.SlotMemory, sources.Memory)
```
Also included in the legacy `composeLegacySystemPrompt` at `context.go:556`.

**Wiring in container:** `internal/service/container.go:371–409`:
```go
if memorySvc != nil {
    sources = append(sources, contextbroker.NewMemorySource(memorySvc))
}
// ...
broker := contextbroker.New(contextbroker.DefaultBudget(), sources...)
contextClient.ContextBroker = broker
```
`memorySvc` is non-nil when Conduit opens successfully (line 344). The broker is assigned to
`contextClient.ContextBroker` unconditionally when at least one source exists (lines 406–408).

## Evidence

| File | Lines | Signal |
|------|-------|--------|
| `internal/chat/context_client.go` | 173–185 | `ContextBroker.Fetch` fires; split by `Source == "memory"` |
| `internal/chat/context_client.go` | 201 | `Memory: memoryContent` returned in `SlotSources` |
| `internal/service/context.go` | 180 | `cw.SetContent(ctxpkg.SlotMemory, sources.Memory)` |
| `internal/service/context.go` | 556 | `sources.Memory` included in legacy system-prompt concat |
| `internal/contextbroker/source_memory.go` | 33–122 | `MemorySource.Fetch` calls `s.Memory.Recall` with real Conduit opts |
| `internal/memory/service.go` | 97–175 | `Service.Recall` calls `s.store.Recall` (embedded Conduit) |
| `internal/service/container.go` | 376–408 | `MemorySource` registered; broker assigned to `contextClient.ContextBroker` |
| `internal/service/chat_generate.go` | 1369 | Hot path calls `AssembleSlots` |

## Findings

- The Memory slot is fully wired: Conduit `Recall` fires on every `AssembleSlots` call when `memorySvc != nil` (i.e., Conduit opened successfully at startup).
- Namespace cascade is session → project → user, with `Ranking("relevance")` and `MinConfidence: 0.4`. If `Intent.QueryText` is empty, Conduit degrades to activation ranking (logged as a warning in `source_memory.go:77` comment and `memory/service.go:151`).
- One minor unresolved item (`RankingRelevance` re-export from Vanta): the string literal `"relevance"` is used as a workaround (`memory/service.go:118`). Conduit accepts the raw string per its internal switch, so this is functional but fragile; flagged in a code comment as a BLG-worthy Vanta patch.

## Follow-up

No follow-up needed — wiring is correct. CW-20260419-0028 (memory-as-grounding) may proceed on the assumption that the Memory slot is live.

Minor: the `RankingRelevance` re-export gap should be a BLG ticket against the Vanta Conduit package (no blocking impact on this arc).
