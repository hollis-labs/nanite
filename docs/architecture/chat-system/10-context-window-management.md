# 10 · Context Window Management

> **Scope:** how nanite keeps a turn within the model's context window. Slot trim, cache hints + the singleton race, prompt-cache boundary, `EnforceTokenBudget`, mid-stream overflow recovery.
>
> **Related:** [09 Session/Slot](09-session-and-slot-management.md) (the slots being managed), [07 Provider](07-provider-routing.md) (cache markers + envelope tokens), [04 Harness](04-chat-harness-and-loop-orchestration.md) (when budget enforcement runs).

## Purpose

Stay under the per-(provider, model) context ceiling per turn, with deterministic compaction when needed and provider-friendly cache placement so successive turns are cheap.

## Key files

- `internal/context/window.go` — slot Window assembly
- `internal/context/compaction.go` — compaction pipeline
- `internal/context/handoff_stash.go` — legacy P7 stash (per-turn / ephemeral sessions)
- `internal/context/tokens.go` — token estimation
- `internal/chat/budget.go` — `EnforceTokenBudget`
- `internal/service/chat_generate.go:684-722` — per-turn enforcement
- `internal/service/chat_generate.go:814-835` — `cache_hints_shared_singleton_race` documented in code

## Slot trim (Glass-7, CW-20260502-0016)

From telemetry: ~12.6% reduction in baseline slot tokens. `SlotAgent` dedup of relocated capability bullets (commit 499160c). Capability content moved out of the agent prompt into the Tools slot, then deduped in the agent prompt. Migration `053_chat_harness_capability_dedup`.

## Cache hints — and the singleton race

`prov` is a singleton fetched from the provider registry. State-mutation methods on the provider (`SetCacheHints`, `EstimateCacheablePrefix`) operate on the *shared instance* rather than per-call ChatRequest.

Two callsites mutate `prov` per-turn:

- `chat_generate.go:252-254` — `SetCacheHints(provider.DefaultCacheStrategy())`
- `chat_generate.go:836-844` — rate-budget pre-flight `EstimateCacheablePrefix`

Under concurrency: session B's `SetCacheHints` can land between session A's `SetCacheHints` and `StreamChat`, overwriting hints. Effect: **`cacheable_prefix_tokens` telemetry and the rate-budget gate's pre-flight estimate can be wrong** under concurrent sessions.

The race is **documented at `chat_generate.go:814-835`** and tracked as `limitations.nanite.cache_hints_shared_singleton_race` in Vanta.

**Severity:** affects gating + telemetry, not turn correctness — provider-side cache placement still works because the provider gets `SlotBlocks{CacheKey}` per call. The race is in nanite's pre-flight estimate, not the provider's actual cache decision.

**Structural fix:** move cache hints to `provider.ChatRequest` per-call. Touches the go-providers interface + every caller. Out of recent cleanup scope.

## Prompt-cache boundary

Each slot's `cache_key = SHA-256(content)`. `SlotBlocks []SlotBlock{Name, Content, CacheKey}` ships to the provider. Provider places `cache_control` markers at the *longest common prefix* of unchanged slots.

Practical implication: **slot churn breaks cache.** If a normally-stable slot (Tools, Agent) changes content, every slot after it is uncached. Slot order is chosen so volatile content (Conversation, UserContext-reminders) lives at the end.

## `EnforceTokenBudget`

`chat.EnforceTokenBudget(systemPrompt, msgs, tools, ceiling)`:

- `ceiling = HardCeilingPct × per-model context window`. The per-model window comes from the models.dev catalog or user override ([07](07-provider-routing.md)).
- Effort scalar (`F1`) multiplies the ceiling — high-effort agents get more headroom.
- **Pre-loop** runs `enforceBudgetOrCompact`: drop enrichment slot → summarize oldest history → strip tool blocks.
- **Per-iteration** runs `EnforceTokenBudget` to catch mid-loop drift (tool result accumulation).

## Overflow recovery

Two paths:

### Pre-call (deterministic)

Before `provider.StreamChat`, `EnforceTokenBudget` confirms we're under ceiling. If not: pre-loop `enforceBudgetOrCompact` ran already; this is the safety net.

### Mid-stream (recoverable)

If the provider returns an error matched by `ctxpkg.IsCompactRecoverable`:

1. Drop the enrichment slot (Memory, Session, Context where present).
2. Summarize oldest history into a compact summary message.
3. Strip tool_use / tool_result blocks (keep text only).
4. Retry. Up to `maxCompactRecoverableAttempts=2` attempts.

If still failing → persist partial assistant + emit `chat-loop-terminated` envelope ([04](04-chat-harness-and-loop-orchestration.md)).

The same recovery path applies to rate-budget refusals (different error, same recovery shape).

## Logic gates

- **Compactable=false slots are immutable to compaction** ([09](09-session-and-slot-management.md)). System, Agent, Mode, Rules, UserContext, Handoff survive.
- **Long-running session uses Handoff slot, not P7 stash.** `ensureGlass4HandoffPreCompact` runs only when `IsLongRunning(sess)` ([09](09-session-and-slot-management.md)).
- **Per-call `cache_key` is reliable; pre-flight `cacheable_prefix_tokens` is not** under concurrency. Use the former for cache decisions, treat the latter as best-effort telemetry.

## Current gaps

- **G-CACHE-RACE** — singleton-shared `SetCacheHints` / `EstimateCacheablePrefix`. Affects telemetry + rate-budget pre-flight, not turn correctness. See [gaps.md](gaps.md#g-cache-race).
- **G-HOT-SWAP-DEAD** — LazyLoad / LoadHint primitive in `window.go` is unused; cache invalidation logic is plumbed but no caller toggles flags. See [09](09-session-and-slot-management.md) and [gaps.md](gaps.md#g-hot-swap-dead).

## Test surface

- Token-count fixture for each slot + assembled Window; assert `EnforceTokenBudget` triggers at exact ceiling.
- Compaction order: confirm enrichment-drop → history-summarize → tool-strip ordering.
- Mid-stream recovery: provider mock returns `compact-recoverable` on first call; assert recovery path runs and second call succeeds.
- Cache-key stability: identical (system + slot blocks) → identical cache_keys; mutate one slot → its cache_key changes, others stable, all-after-it cache markers shift.
- G-CACHE-RACE repro: two concurrent goroutines call `SetCacheHints` with different strategies before a third calls `StreamChat`; assert observable mismatch in telemetry.
