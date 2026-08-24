# 04 · Chat Harness & Loop Orchestration

> **Scope:** the loop itself. Turn lifecycle, terminators, soft `max_turns` semantics, recoverable error handling, telemetry emission points. The harness sits between [01 messaging](01-chat-stream-messaging.md), [07 provider](07-provider-routing.md), [02 tools](02-tool-invocation-and-authority.md), and [03 SSE](03-sse-envelope-and-interaction-protocol.md).

## Purpose

Drive a single chat session's tool-use loop from request → final response. Decide when to stop (clean end vs runaway), when to compact (mid-stream overflow), when to retry (provider error vs rate-budget refusal), when to circuit-break, when to surface user-facing warnings.

## Key files

- `internal/service/chat_generate.go:121-1696` — `generateResponse`, the loop
- `internal/service/chat_loop_state.go` — loop state + termination logic
- `internal/service/chat_loop_state.go:30-58` — `defaultMaxTurns=75`, soft-budget semantics
- `internal/service/chat_loop_state.go:375-400` — `shouldStop` (the only real terminators)
- `internal/service/chat_loop_state.go:410-423` — `checkSoftMaxTurnsWarning`
- `internal/service/chat_loop_state.go:462` — `recordPermissionDenial`
- `internal/chat/engine.go:13-31` — `AgentConstraints`
- `internal/chat/budget.go` — `EnforceTokenBudget`
- `internal/chat/loop_circuit.go` — circuit breaker integration

## Entry points

- `chatServiceImpl.HandleMessage` → `launchGeneration` → `generateResponse`. Goroutine per session.
- Cancellation: `inFlightGen` registry per session. New message cancels prior generation (CW-20260418-0043).

## Happy path

1. Resolve agent / model / provider; build `AgentConstraints` (max_turns, idle timeout, etc.).
2. Pre-loop: classifier + effort + strategy planner run once. Reflex hints generated.
3. `enforceBudgetOrCompact` ([10](10-context-window-management.md)) — drop enrichment → summarize oldest → strip tool blocks if needed.
4. `newLoopState(constraints, strategy)` initializes the loop counters.
5. Per iteration:
   a. Soft-warning check: `iteration >= resolvedMaxTurns` → emit `chat-loop-budget-soft-warning` (devmode SSE) + always-log telemetry. **Loop continues.**
   b. Budget enforcement: `EnforceTokenBudget(systemPrompt, msgs, tools, ceiling)`.
   c. `provider.StreamChat` → consume `provCh` channel: `delta`, `tool_use`, `usage`, `thinking`, `error`.
   d. Stream end with `stop_reason`:
      - `tool_use` → preCheck + executeToolBatch + postProcess → loop back.
      - `end_turn` (or anything not tool_use) → break.
6. Post-loop persist + finalize ([01](01-chat-stream-messaging.md)).

## Loop terminators (the only ones that actually stop)

Defined at `chat_loop_state.go:375-400`:

| Terminator | Default | Behavior |
|---|---|---|
| `runawayFailCap` | 10 | consecutive errors / permission denials |
| `idleTimeout` | 900s | no provider activity |
| `hardCeiling` | 200 | absolute iteration cap |
| `retryBudget` exhausted | (per provider) | provider retries + rate-budget retries |
| `stop_reason == "end_turn"` | n/a | natural completion |

`max_turns` (default 75) is **soft only** — it triggers a warning, not a stop. `consecutive_fail_cap=3` is also **soft** (CW-20260417-0485) — drives a critical `tool_warning` SSE but doesn't terminate. `runawayFailCap` is clamped to ≥ `consecutiveFailCap` so the soft cap can never exceed the hard cap.

## Soft `max_turns` (CW-20260504-0001)

- Configured per-agent via `AgentConstraints.MaxTurns`; default 75.
- `checkSoftMaxTurnsWarning` (`chat_loop_state.go:410-423`) fires **once** when `iteration >= maxTurns`.
- Emits SSE `chat-loop-budget-soft-warning` when `developer_mode || NANITE_DEVMODE=1`. Always logs telemetry.
- **The loop keeps running.** Only `runawayFailCap` / `idleTimeout` / `hardCeiling` / `retryBudget` actually stop it.
- Strategy planner can produce a soft hint via `strategy.Strategy.MaxTurns` ([08](08-classification-and-intent.md)).

## Sad path

- **Provider stream error** classified by `ctxpkg.IsCompactRecoverable`:
  - **Recoverable (context overflow / rate-budget refused)** → synchronous compaction with `maxCompactRecoverableAttempts=2`. If still failing, harness emits `error` SSE + `chat-loop-terminated` envelope.
  - **Non-recoverable** → propagate as `error` SSE + persist partial assistant.
- **Rate-budget refused** (`provider.ErrRequestExceedsRateBudget`) → `pauseAndMaybeRetryRateBudget` (`chat_rate_budget_pause.go:104`):
  - First refuse: emit `rate_budget_pause` event with `auto_retry`.
  - Second refuse: emit `rate_budget_pause` with `user_action_needed`.
- **Circuit breaker open** (`Anthropic.CircuitBreaker.IsOpen`) → emit `circuit_open` SSE + break.
- **Runaway** → `earlyStopSynthesis` (one final no-tools LLM call to summarize) → `emitChatLoopTerminated` envelope + `status` event + break.
- **Mid-stream context overflow** → recovery = drop enrichment slot → summarize oldest history → strip tool blocks → retry. Up to 2 attempts. If still over budget, persist partial + emit `chat-loop-terminated`.
- **Timeout** (`idleTimeout`, default 900s no provider activity) → emit `error` SSE + persist partial.

## Logic gates

- **Cancellation** the `inFlightGen` registry tracks `(sessionID, ctx, cancel)`. New `HandleMessage` for the same session calls cancel and removes the entry. Spawned subagents have their own entries (CW-20260418-0043 lineage).
- **Recovery vs propagate** `ctxpkg.IsCompactRecoverable` is the gatekeeper. Anthropic-specific overflow errors + rate-budget = recoverable.
- **`runawayFailCap` clamping** the resolved value is `max(runawayFailCap, consecutiveFailCap)`. This guarantees the soft cap can warn before the hard cap stops.
- **Devmode gating for soft warning** SSE only fires under devmode; telemetry always fires. Same pattern as other unsubscribed signals.

## Telemetry emission points

- `request_build` per provider call (Glass-2 telemetry, ~`chat_generate.go:857`) — captures `cacheable_prefix_tokens`, slot sizes, model, provider, mode.
- `tool_call` / `tool_result` per tool exec.
- `chat-loop-terminated` envelope on runaway/idle/hardCeiling/retry-exhausted.
- `pty_turn_start` / `pty_turn_complete` / `pty_turn_failed` for PTY-provider sessions (`messaging.EventPTYTurn*`).
- `usage` aggregation persisted via `s.store.RecordUsage` and `RecordExecutionMetrics` ([07](07-provider-routing.md)).

## Current gaps

- **G-SSE-UNSUBSCRIBED** — `chat-loop-budget-soft-warning` and `rate_budget_pause` emit cleanly but FE doesn't subscribe. Operators only see them in devmode + logs. See [gaps.md](gaps.md#g-sse-unsubscribed).
- **G-RECOVERY-COVERAGE** — `recoverFromContextOverflow` covers the synchronous case; mid-stream is documented but the error matrix (which provider error → recoverable?) is centralized only in `ctxpkg.IsCompactRecoverable`. Adding a new provider needs care.

## Test surface

- `loopState.shouldStop` table-test all 4 terminators + 2 soft caps.
- `provider.Provider` channel mock; assert recovery path on `IsCompactRecoverable` errors.
- Synthesis-on-runaway: trigger 10 permission denials, assert `earlyStopSynthesis` runs once and `chat-loop-terminated` envelope fires.
- Cancellation: send `HandleMessage` to a session with an in-flight gen, assert prior gen ctx is canceled.
