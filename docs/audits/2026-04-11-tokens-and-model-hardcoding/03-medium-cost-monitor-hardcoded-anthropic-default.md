# [Medium] CostMonitor always estimates cost using Anthropic rates regardless of actual provider

**Scope:** cost tracking
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

`CostMonitor.updateUsageAndCheck()` hardcodes `"anthropic"` as the provider for all cost estimates, regardless of which provider actually generated the tokens. The `getDefaultCostRates()` map also only covers three providers (anthropic, openai, ollama), missing gemini, mistral, azure, openrouter, and openzen.

## Evidence

```go
// pkg/provider/cost_monitor.go:104
estimatedCost := cm.estimateCost(usage.InputTokens, usage.OutputTokens, "anthropic")
```

The cost rates map at `cost_monitor.go:58-76`:

```go
func getDefaultCostRates() map[string]CostRate {
    return map[string]CostRate{
        "anthropic": {
            InputTokensPerDollar:  333333, // ~$3.00 per 1M input tokens
            OutputTokensPerDollar: 66667,  // ~$15.00 per 1M output tokens
        },
        "openai": {
            InputTokensPerDollar:  200000, // ~$5.00 per 1M input tokens
            OutputTokensPerDollar: 66667,  // ~$15.00 per 1M output tokens
        },
        "ollama": {
            InputTokensPerDollar:  1000000000,
            OutputTokensPerDollar: 1000000000,
        },
    }
}
```

Note the OpenAI rate is wrong: `200000` tokens/dollar = `$5.00/1M` input tokens, but GPT-4o is `$2.50/1M` (see `usage.go:17`). The CostMonitor and `usage.go` use different pricing sources that disagree.

## Impact

1. **Budget enforcement is inaccurate for all non-Anthropic providers.** A Gemini session using `gemini-2.5-flash` ($0.15/1M input) would be cost-tracked at Anthropic Sonnet rates ($3.00/1M input) — **20x overestimate**. Budget kill/log thresholds fire prematurely.

2. **Two independent pricing sources** — `usage.go:modelPricing` (per-model, used for usage records) and `cost_monitor.go:getDefaultCostRates` (per-provider, used for budget enforcement) — will drift independently.

3. The `SetCostRate()` method exists for runtime updates but is never called in the codebase.

## Recommendation

Pass the provider name (or model name) through the event pipeline so `CostMonitor` can select the correct rate. Unify with `usage.go:modelPricing` or at minimum keep both maps in the same file so updates are co-located.

## References

- `pkg/provider/cost_monitor.go:L58-76` (rate definitions)
- `pkg/provider/cost_monitor.go:L104` (hardcoded "anthropic")
- `internal/store/usage.go:L6-31` (separate pricing map)
- `pkg/provider/event_pipeline.go:L42` (100k token budget default)
