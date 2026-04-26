> **SUPERSEDED 2026-04-26 (CW-20260426-0002).** This finding is no longer accurate.
> See `docs/audits/2026-04-26-foundation/02-a2-compaction-fires.md` for the
> current state — `CompactionPipeline` was wired into the chat-generate hot
> path through three call sites between 2026-04-11 and HEAD.

# [High] Auto-compaction never fires — NeedsCompaction is computed but never acted on

**Scope:** context-management
**Topic:** Correctness / Claimed-vs-actual
**Date:** 2026-04-11

## Problem

`ContextWindow.NeedsCompaction()` correctly detects when the conversation slot exceeds its budget. `AssembleSlots` computes this flag and returns it as `SlotAssemblyResult.NeedsCompaction`. But:

1. `AssembleSlots` is never called from the generate path (finding 01).
2. Even if it were called, no code inspects `NeedsCompaction` to trigger the `CompactionPipeline`.
3. The `CompactionPipeline` is never instantiated in production code (finding 02).

The actual budget enforcement in the generate path is `chat.EnforceTokenBudget()` at `chat_generate.go:265`, which performs a different, simpler cascade: prune tool results in-memory, drop tools from end, drop oldest messages, then refuse to send. This is a hard-coded reduction, not a summarization-based compaction. It does not invoke the LLM summarizer, does not fire compaction events, and does not update the database.

## Evidence

The generate path's budget enforcement:

```go
// internal/service/chat_generate.go:265
chatMessages, tools, breakdown, budgetErr = chat.EnforceTokenBudget(systemPrompt, chatMessages, tools, 0)
```

`EnforceTokenBudget` cascade (from `context_client.go:L223-L301`):
1. `pruneToolResultsInMemory` — replace old tool_result content blocks with `[pruned: ...]` markers
2. Drop tools from end until budget fits or 1 tool remains
3. Drop oldest messages until budget fits or 1 message remains
4. Refuse to send if still over budget

This is functional and works. But it is not "auto-compaction" in the sense of LLM-summarized context reduction. It is brute-force trimming that loses information permanently (within the turn — DB content is unchanged).

Meanwhile, the `CompactionPipeline` stages:
1. Drop Context slot enrichment
2. Summarize oldest messages via LLM (`Summarizer` interface)
3. Strip tool blocks from non-tool-use spans

These stages are tested in `compaction_test.go` but never run in production.

## Impact

When context exceeds budget, the system silently drops messages and tools rather than summarizing them. Users lose conversation history from the LLM's perspective without any notification that a summarization could have preserved key information. The `status` event at `chat_generate.go:283` logs "budget reduced" but does not indicate what was lost.

## Recommendation

The `EnforceTokenBudget` cascade is a reasonable fallback. The gap is that it should attempt LLM summarization before resorting to message dropping. Integration path:

1. Wire `AssembleSlots` into the generate path (finding 01).
2. When `NeedsCompaction` is true, run `CompactionPipeline` with the utility model as `Summarizer`.
3. If the pipeline fails or doesn't free enough, fall back to the existing `EnforceTokenBudget` cascade.
4. Fire `EmitPreCompact`/`EmitPostCompact` around the pipeline run.

## References

- `internal/context/window.go:L116-L122` — NeedsCompaction (computed, never consumed)
- `internal/service/context.go:L97` — NeedsCompaction returned in SlotAssemblyResult (result never read)
- `internal/chat/context_client.go:L223-L301` — EnforceTokenBudget (actual budget enforcement)
- `internal/service/chat_generate.go:L265` — EnforceTokenBudget call site
- `internal/context/compaction.go:L60-L98` — CompactionPipeline.Run (never called)
- Related: [01-critical-slot-system-unwired.md](01-critical-slot-system-unwired.md), [02-critical-compact-api-is-mvp-stub.md](02-critical-compact-api-is-mvp-stub.md)
