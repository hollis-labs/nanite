# [Medium] AssembleContext budget hardcodes 200k regardless of provider window size

**Scope:** context-management
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

`ContextClient.AssembleContext()` computes the token budget as `DefaultContextWindow * BudgetPct` where `DefaultContextWindow` is hardcoded at 200,000. This budget is used to drop oldest messages when context is too large. The actual provider's context window size is never consulted.

The downstream `EnforceTokenBudget()` has the same issue — it computes `ceiling = DefaultContextWindow * HardCeilingPct` (200k * 0.80 = 160k).

For providers with smaller context windows (Ollama models with 8k-32k, Mistral models, some OpenAI models), the system assembles up to 150k tokens of context and sends it to a provider that may reject it or silently truncate it.

## Evidence

```go
// internal/chat/context_client.go:91
budget := int(float64(DefaultContextWindow) * budgetPct)
```

```go
// internal/chat/context_client.go:229
ceiling := int(float64(DefaultContextWindow) * HardCeilingPct)
```

`DefaultContextWindow` is 200,000:

```go
// internal/chat/context_client.go:22
const DefaultContextWindow = 200000
```

Compare with the slot system which accepts `providerWindowSize` as a parameter:

```go
// internal/service/context.go:68
func (s *contextServiceImpl) AssembleSlots(ctx context.Context, ..., providerWindowSize int) (*SlotAssemblyResult, error) {
```

The slot system would solve this — it takes the provider window as input. But since the slot system is not wired (finding 01), the hardcoded value is what runs.

## Impact

Models with context windows smaller than 200k will receive over-budget requests. Provider-level errors or silent truncation will occur for Ollama (typically 8k-32k), some Mistral models, and potentially Azure OpenAI deployments with custom limits. The error recovery path (`EnforceTokenBudget` step 4) returns an error to the user only when the hardcoded 160k ceiling is exceeded, which is unreachable for small-window providers.

## Recommendation

Pass the provider's reported context window size through to `AssembleContext` and `EnforceTokenBudget`. The provider `Capabilities()` method likely reports the window size. If not, add it — the model registry in `seed.go` already has per-model metadata.

Short-term fix: add a `ceilingOverride` parameter to `AssembleContext` (similar to `EnforceTokenBudget`'s existing parameter) and pass the provider window from the generate path.

## References

- `internal/chat/context_client.go:L22` — DefaultContextWindow constant
- `internal/chat/context_client.go:L91` — budget computation
- `internal/chat/context_client.go:L229` — ceiling computation
- `internal/service/context.go:L68` — AssembleSlots takes providerWindowSize (but is never called)
