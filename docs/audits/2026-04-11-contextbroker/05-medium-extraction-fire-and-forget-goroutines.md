# [Medium] Memory extraction fire-and-forget goroutines have no cancellation or PII gate

**Scope:** contextbroker / memory
**Topic:** Concurrency / Security (PII)
**Date:** 2026-04-11

## Problem

Two findings in one file, both in `internal/memory/extraction.go`:

### 5a. Fire-and-forget goroutines with no cancellation

Both `perTurnHook.Handle` and `postCompactHook.Handle` spawn goroutines with `go h.extractor.extractPerTurn(...)` and `go h.extractor.extractPostCompact(...)`. These goroutines:

- Create their own `context.Background()` contexts (not derived from any parent)
- Have no stop channel, no WaitGroup, no tracking
- Cannot be cancelled on shutdown

This was noted by the concurrency-cancellation-sweep (`2026-04-11-concurrency-cancellation-sweep/index.md:L161`) as a known pattern, and the goroutines are listed in the goroutine map. Not re-flagging the concurrency aspect. What is new here (and in the contextbroker scope) is the *consequence*: these goroutines call the utility LLM and store memories. On shutdown, a partially-completed extraction can store a corrupted or incomplete memory that will then be recalled by `MemorySource` in future sessions.

### 5b. No PII filtering on memory extraction

The extraction prompt sends the raw user message content to the utility LLM for extraction:

```go
// internal/memory/extraction.go:L159-L172
prompt := fmt.Sprintf(`Extract a memory from this user message...

User message:
%s

Return a JSON object...`, truncateForPrompt(content, 1500))
```

If the user message contains secrets (API keys, passwords, PII), the extraction LLM processes them and may store them as a memory with fields like `summary`, `body`, or `tags`. The stored memory persists across sessions and is recalled by `MemorySource` into future system prompts.

There is no sanitization between user input and memory storage. The `FilterUserMessage` filter chain (if configured) runs only in the chat generation path, not in the extraction path.

## Evidence

Fire-and-forget spawn:

```go
// internal/memory/extraction.go:L105-L106
// Fire-and-forget: don't block the message flow.
go h.extractor.extractPerTurn(sessionID, content)
```

```go
// internal/memory/extraction.go:L135-L136
// Fire-and-forget: don't block compaction flow.
go h.extractor.extractPostCompact(sessionID, tokensSaved)
```

Background context in extraction:

```go
// internal/memory/extraction.go:L156-L157
ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
defer cancel()
```

Memory storage with raw content:

```go
// internal/memory/extraction.go:L223
if err := e.service.Store(context.Background(), m); err != nil {
```

## Impact

**PII persistence:** A user who pastes an API key or password into chat has it extracted into the memory store, where it persists indefinitely and is recalled into future sessions. The memory source has no PII scrubbing. This was called out as a priority in the reviewer-backend context (`.nanite/agents/reviewer-backend.md:L153`).

**Partial writes on shutdown:** A kill during extraction can leave an LLM-generated memory in a partially-valid state. Low practical impact since the LLM response is atomic (it either parses or doesn't), but the `Store` call could be interrupted by process death between write and log.

## Recommendation

1. **PII gate:** Before sending content to the extraction LLM, run the user-message filter chain (or a dedicated PII scrubber) on the content. Alternatively, add an explicit instruction to the extraction prompt: "Do NOT include API keys, passwords, tokens, or other secrets in any field."

2. **Shutdown awareness:** Wire the extraction goroutines to a shared context derived from the host context so they observe shutdown signals. The existing `pluginsdk.Event` includes a context, but both hooks use `context.Background()` instead.

## References

- `internal/memory/extraction.go:L105-L106` — fire-and-forget per-turn
- `internal/memory/extraction.go:L135-L136` — fire-and-forget post-compact
- `internal/memory/extraction.go:L156-L172` — raw user content in LLM prompt
- `internal/contextbroker/source_memory.go:L33-L121` — memory recall into context
- `.nanite/agents/reviewer-backend.md:L153` — "PII leakage in memory extraction"
- Cross-ref: `2026-04-11-concurrency-cancellation-sweep` — goroutine lifecycle catalogued
