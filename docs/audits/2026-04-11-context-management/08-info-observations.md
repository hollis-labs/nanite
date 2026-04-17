# [Info] Observations

**Scope:** context-management
**Topic:** Observations
**Date:** 2026-04-11

## What works

### PruneAfterTurn — functional and correctly wired

`ContextClient.PruneAfterTurn()` at `context_client.go:L127-L163` is called after every generate response (`chat_generate.go:L690`). It compacts tool-role messages older than 2 turns by replacing their content with `[compacted: tool output, N chars]` markers in the database. This is the only compaction mechanism that actually runs in production. It is correctly scoped (tool messages only, >500 chars, respects `IsCompacted` flag) and has appropriate logging.

### EnforceTokenBudget — functional fallback

`EnforceTokenBudget()` at `context_client.go:L223-L301` runs before every provider call (`chat_generate.go:L265`). Its 4-stage cascade (prune tool results in-memory, drop tools, drop messages, refuse) works correctly and prevents context overflow. The in-memory pruning at `pruneToolResultsInMemory()` is well-implemented with proper content-block handling.

### Slot system design quality

The `internal/context/` package is well-designed. The data structures are clean, the slot ordering is sensible, the cache-key computation is deterministic, and the compaction pipeline's 3-stage escalation with mode-aware summarization prompts is a solid design. The test coverage in `window_test.go` (168 lines) and `compaction_test.go` (205 lines) is thorough and exercises all stages. The issue is purely one of integration — the code is correct but not wired.

### Context broker enrichment — functional

`enrichWithContextBroker()` at `context_client.go:L305-L348` successfully fetches multi-source context and appends it to the system prompt. This enrichment path works end-to-end (modulo the broker findings in the separate `contextbroker` audit).

## Design notes

### Two parallel token budgets

The codebase has two independent token budget systems:
1. **AssembleContext** (legacy, active): `DefaultBudgetPct` (0.75) of `DefaultContextWindow` (200k) = 150k. Controls message loading.
2. **EnforceTokenBudget** (active): `HardCeilingPct` (0.80) of `DefaultContextWindow` = 160k. Controls the pre-send gate.

These are not in conflict (the soft budget catches most cases, the hard ceiling is the safety net), but the dual system adds cognitive load. When the slot system is wired, it should unify these into a single budget derived from the provider's actual context window.

### ContextWindow ephemeral lifecycle

The current `AssembleSlots` creates a fresh `ContextWindow` per call. For the cache-hit tracking feature to work, the window needs to persist across turns (keyed by session). This is a design prerequisite for wiring the slot system, not a bug in the current implementation.
