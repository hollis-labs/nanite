# ADR-001: Tool Scoping and Rate Limit Resilience

**Date:** 2026-03-09
**Status:** Accepted
**Sprint:** SPR-20260309-tool-scoping-resilience

## Context

Adding new MCP servers increased the total tool count beyond what fits in LLM context windows. Sending all tool definitions (with full JSON schemas) to every LLM request caused token limit errors. Additionally, rate limit (429) errors from providers crashed the chat with no graceful recovery.

## Problem

1. `getToolsForAgent()` passed `intent="*"` to the ToolBroker, returning all matching tools regardless of relevance.
2. Tool definitions were not token-budgeted — they consumed unbounded context window space.
3. No retry logic for provider rate limits (429 responses).
4. Frontend displayed raw error strings inline with no debug capability.

## Decisions

### D1: Intent-Based Tool Selection

**Replace wildcard intent with extracted intent.** Before selecting tools, extract keywords from the user message and pass them as intent + hints to the broker. The broker rejects wildcard (`"*"`) intents and falls back to a minimal set.

**Rationale:** Simple keyword extraction is cheap, deterministic, and sufficient for the broker's rule-based matching. No LLM call needed for this phase.

### D2: Token-Aware Tool Budgeting

**Cap tool definitions at 20% of context window tokens.** After the broker selects tools, estimate their total token cost (JSON schema length / 4). If over budget, prune lowest-priority tools until under budget. At least 1 tool always remains.

**Rationale:** 20% balances tool availability vs. leaving room for conversation history and system prompts. The remaining 80% is split between system prompt (~5%) and messages (75%, matching existing `DefaultBudgetPct`).

### D3: Structured Error Events

**Replace raw error strings with typed error payloads.** SSE error events carry `{code, message, details}` with codes: `rate_limit`, `tool_error`, `provider_error`, `internal_error`. Frontend renders error banners with a "View Details" modal for debugging.

**Rationale:** Users need to distinguish transient errors (rate limits) from permanent failures (tool bugs). Debug details must be accessible without requiring server logs.

### D4: Exponential Backoff with Circuit Breaker

**Retry 429 responses with 1s/2s/4s backoff, max 3 retries.** Parse `retry-after` header when available. Emit SSE status events during retry so the frontend shows progress. After exhausting retries (circuit breaker), notify the user and offer a manual retry button.

**Rationale:** Most rate limits are transient and resolve within seconds. Automatic retry prevents unnecessary user frustration. The circuit breaker prevents runaway costs when limits are sustained.

### D5: Progressive Tool Discovery (Future)

**Two-phase tool loading for sessions with 20+ tools.** Phase 1 injects a tool catalog (name + description only) into the system prompt. A meta-tool `request_tools` lets the LLM fetch full schemas on demand. This is planned for a follow-up sprint.

**Rationale:** Deferred because the token budget (D2) addresses the immediate issue. Progressive discovery adds complexity but will be needed as tool counts grow further.

## Consequences

- Tool selection is now message-aware, reducing irrelevant tools in LLM context
- Token budget provides a hard ceiling preventing context overflow from tool definitions
- Errors are user-actionable with debug details accessible via modal
- Transient rate limits are handled automatically with user visibility
- The broker's `intent="*"` path is blocked, forcing all callers to provide meaningful intent
