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

## Update — 2026-08-15 (CW-20260815-0011)

D1's "the broker rejects wildcard intents and falls back to a minimal set"
claim was only half true in practice. `isWildcardIntent` did correctly
reject a literal `""`/`"*"` intent, but it never covered the far more
common real case: Nanite ran with the go-toolbroker library's own bundled
example ruleset (`hadron_*`/`volon_*`/`cortex_*` patterns, a different
app's tool names), which never matched anything in Nanite's real registry
(`torque_*`, `dev_*`, `memory_*`, the unprefixed self-tool names, ...). A
well-formed, non-wildcard extracted intent that simply matched zero real
rules fell straight through to go-toolbroker's *own* fallback —
`SelectResult.Rationale == "no rules matched intent; returning all tools"`
— returning the entire unranked catalog. That result was then further cut
by a hard `MaxSelectedTools = 15` slice applied *before* permission and
allowlist filtering ever ran, so a correctly-declared, correctly-permitted
tool sitting past index 15 in registration order (confirmed for
`torque_task_get` among Torque's ~90+ tools) never survived to reach its
own allowlist check. This was root-caused as the reason a live Orchestrator
durable-agent session could not find tools it had explicitly declared.

Fixed in CW-20260815-0011:

- `internal/toolclient/config.go` — `DefaultConfig()` now uses
  `NaniteDefaultRules()`, a real (if minimal) Nanite-specific ruleset with
  an explicit `Intent: "*"` catch-all `include` rule, instead of the
  library's example config. This also closes the "no rules matched" gap
  structurally: a rule now always applies, so go-toolbroker's own
  zero-match fallback is no longer how Nanite gets its base candidate set
  (a defense-in-depth guard against that fallback remains in
  `selectToolsUncapped` for the degenerate case of an empty ruleset).
- `internal/toolclient/broker.go` — `MaxSelectedTools` capping and
  token-budget pruning were moved out of the broker-selection step
  (`SelectToolsAsProvider`) and into a new `FinalizeToolSelection`, called
  by `service/tool.go SelectForAgent` only after BOTH the
  `tool_permissions` check and the schema-v2 `tools` allowlist have run.
- `cmd/nanite/main.go` — the MCPManager's shared broker and the
  `ToolClient`'s `Config` are now constructed from the same
  `toolclient.DefaultConfig()` call, closing a second gap where the two
  were built independently and neither actually went through Nanite's own
  config abstraction.

See `CW-20260815-0011` for the full investigation and
`internal/service/tool_test.go`'s
`TestSelectForAgent_LateAlphabetAllowlistedToolSurvivesCap` for the
regression test.
