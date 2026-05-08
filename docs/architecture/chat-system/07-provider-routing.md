# 07 · Provider Routing

> **Scope:** how nanite chooses an LLM provider for a model, what the provider abstraction guarantees, where token / context-window / cost numbers come from, and how prompt caching is wired.
>
> **Related:** [04 Harness](04-chat-harness-and-loop-orchestration.md) (calls `provider.StreamChat`), [10 Context Window](10-context-window-management.md) (cache hints + race), [05 External Agent Execution](05-external-agent-execution.md) (PTY adapters).

## Purpose

Decouple the chat harness from any specific LLM API. Resolve `model -> provider`, normalize streaming events, expose capabilities, surface usage and cost.

## Key files

- `pkg/models/registry.go` — model catalog + `ProviderFor(model)` mapping
- `internal/chat/engine.go:175-200` — `InferProvider`
- `internal/service/chat_generate.go:230` — `resolveProvider`
- `internal/service/chat_generate.go:2037-2050` — `contextWindowSize` (the source-of-truth lookup)
- `internal/store/usage.go` — `RecordUsage`, `EstimateCost`
- `~/Projects-apps/go-providers/provider/*.go` — adapter implementations

## Resolution

`models.ProviderFor(model)` priority:

1. Registry exact match (most explicit).
2. `models.ProviderHasPrefix(model)` — `gpt-`, `o1-`/`o3-`/`o4-` → openai; `llama`/`gemma`/`mistral-7b` → ollama; `model:tag` form → ollama.
3. `DefaultProvider()` fallback.

## Adapters

Native HTTP: `Anthropic`, `OpenAI`, `Gemini`, `Mistral`, `Ollama`, `Openrouter`, `Openzen`, `Azure_OpenAI`.

PTY adapters (subprocess-bridged, see [05](05-external-agent-execution.md)): `pty-claude`, `pty-codex`, `pty-gemini`, `pty-aider`, `pty-junie`, `pty-copilot`, `pty-opencode`. Subprocess bridge in `~/Projects-apps/go-providers/provider/subprocess.go`.

## Capabilities

`ProviderCapabilities` per adapter:

```go
type ProviderCapabilities struct {
    SupportsStreamJSON          bool
    SupportsPreToolHooks        bool
    SupportsSystemPromptCaching bool
    SupportsToolCalling         bool
    SupportsBatch               bool
    SupportsImageInput          bool
    MaxTokens                   int
}
```

PTY bridges report `SupportsSystemPromptCaching=false` (CLI manages its own caching) and `SupportsToolCalling=true` (CLI handles its own tool loop internally — but tool calls are *not* surfaced as nanite SSE events; see [05 gaps](05-external-agent-execution.md#current-gaps)).

## Source-of-truth — token / window / cost

This is the answer to the question "are we calculating from models.dev or provider envelopes?" — **both, with clear roles.**

### Context window

`chatServiceImpl.contextWindowSize(provider, model)` (`chat_generate.go:2037-2050`) priority:

1. **`user_settings.ContextWindowTokens`** — explicit user override.
2. **`s.modelCatalog.Get(provider, model).Limit.ContextWindow`** — **this is the models.dev catalog** (`container.go:567` — "fetches pricing and context-window data from models.dev").
3. **`0`** — caller falls back to `DefaultContextWindowTokens` (~200k).

### Actual token usage

Comes from the **provider response envelope**:

```go
provider.StreamEvent.Usage{
    InputTokens, OutputTokens,
    CacheCreationTokens, CacheReadTokens,
    StopReason,
}
```

Accumulated into `finalUsage` in the loop (`chat_generate.go:1095-1116`). Persisted via `s.store.RecordUsage` and `RecordExecutionMetrics`.

### Cost

`s.store.RecordUsage` writes the token rows; `internal/store/usage.go:EstimateCost` computes cost using **model catalog pricing rows** (also from models.dev).

### Summary table

| Metric | Source | Notes |
|---|---|---|
| Context window | models.dev catalog (override: user_settings) | Static per (provider, model) |
| Input/output tokens | provider envelope | Authoritative for that turn |
| Cache create/read tokens | provider envelope | Used for cache effectiveness telemetry |
| Cost | computed: tokens × pricing from models.dev | `EstimateCost` |
| `cacheable_prefix_tokens` | `provider.Cacheable.EstimateCacheablePrefix` | Pre-flight estimate; subject to G-CACHE-RACE |

## Prompt caching

Two layers, complementary:

- **Provider-side** — `provider.CacheableProvider.SetCacheHints(provider.DefaultCacheStrategy())` at `chat_generate.go:252-254`. Anthropic adapter places `cache_control` markers via slot boundaries.
- **Nanite-side** — per-slot `cache_key = SHA-256(content)` in `internal/context/slot.go:67`. `cacheable_prefix_tokens` reported in `request_build` telemetry via `provider.Cacheable.EstimateCacheablePrefix` (`chat_generate.go:836-844`).

**The hand-off:** nanite ships `SlotBlocks []SlotBlock{Name, Content, CacheKey}` to the provider; the provider places its native cache markers at the *longest common prefix* that didn't change since last turn. The cache_key is the changed-vs-unchanged signal.

## Logic gates

- **Provider singleton** `prov` is fetched from a registry per call but is the *same instance* across concurrent sessions for the same provider+model. State-mutation methods (`SetCacheHints`, `EstimateCacheablePrefix`) operate on this shared instance — see G-CACHE-RACE.
- **`SystemPrompt` vs `SlotBlocks`** — `SystemPrompt` is the *per-turn dynamic prefix only* (no-tools warning + progressive catalog + nativeToolGuide + override block). Persistent system content lives in `SlotBlocks` so it can be cached.
- **PTY adapter quirks** — `EffectiveSystemPrompt()` flattens `SystemPrompt + SlotBlocks` into a single string; PTY bridge then drops the lot on resume turns. See [05](05-external-agent-execution.md).

## Current gaps

- **G-CACHE-RACE** — `prov` is shared across concurrent sessions; `SetCacheHints` (line 252-254) and rate-budget `EstimateCacheablePrefix` (line 836-844) mutate state on the shared instance rather than per-call `ChatRequest`. Concurrent session B's `SetCacheHints` can overwrite hints between session A's `SetCacheHints` and `StreamChat`. Effect: `cacheable_prefix_tokens` telemetry and rate-budget gating estimates can be wrong under concurrency. Documented in code at lines 814-835. Structural fix: move cache hints to `provider.ChatRequest` per-call (touches go-providers interface + every caller). See [10](10-context-window-management.md), [gaps.md](gaps.md#g-cache-race).

## Test surface

- Mock `provider.Provider` for happy path + each Usage shape.
- Mock `models.Catalog` to assert source-of-truth priority (user override > catalog > default fallback).
- Cost regression: known-token fixture × known pricing → expected cost. Include `TestEstimateCostUnknownModel` (`usage_test.go:148`) which exercises the unknown-model fallback.
- Cache hand-off: two consecutive turns with identical SlotBlocks → assert provider received same `CacheKey` + `cache_control` placement implies cache hit reported in next `Usage.CacheReadTokens`.
- Concurrency stress test (G-CACHE-RACE): two sessions in flight on the same model, assert telemetry mismatches the per-call ChatRequest — this *should* fail today, documenting the gap.
