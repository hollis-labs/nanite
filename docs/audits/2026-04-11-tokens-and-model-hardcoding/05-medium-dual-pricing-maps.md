# [Medium] Two independent pricing maps with different schemas and partial overlap

**Scope:** cost estimation
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

Token pricing data is defined in two separate locations with incompatible schemas:

1. `internal/store/usage.go:6-31` — `modelPricing` map, keyed by model ID, values are `[2]float64{inputPerMillion, outputPerMillion}` in USD.
2. `pkg/provider/cost_monitor.go:58-76` — `getDefaultCostRates()` map, keyed by provider name, values are `CostRate{InputTokensPerDollar, OutputTokensPerDollar}` (inverse relationship).

These maps serve different purposes (usage recording vs. budget enforcement) but both estimate cost from the same raw data (token counts). Having them separate guarantees drift.

## Evidence

`internal/store/usage.go:6-31`:
```go
var modelPricing = map[string][2]float64{
    "claude-sonnet-4-20250514":    {3.0, 15.0},
    "gpt-4o":                      {2.5, 10.0},
    // ... 15 models total
}
```

`pkg/provider/cost_monitor.go:58-76`:
```go
func getDefaultCostRates() map[string]CostRate {
    return map[string]CostRate{
        "anthropic": {InputTokensPerDollar: 333333, OutputTokensPerDollar: 66667},
        "openai":    {InputTokensPerDollar: 200000, OutputTokensPerDollar: 66667},
        "ollama":    {InputTokensPerDollar: 1000000000, OutputTokensPerDollar: 1000000000},
    }
}
```

**Pricing disagreements:**

| Provider/Model | usage.go | cost_monitor.go | Match? |
|---|---|---|---|
| Anthropic Sonnet (input) | $3.00/1M | 333333 tokens/$ = $3.00/1M | Yes |
| Anthropic Sonnet (output) | $15.00/1M | 66667 tokens/$ = $15.00/1M | Yes |
| OpenAI GPT-4o (input) | $2.50/1M | 200000 tokens/$ = $5.00/1M | **No** ($2.50 vs $5.00) |
| OpenAI GPT-4o (output) | $10.00/1M | 66667 tokens/$ = $15.00/1M | **No** ($10.00 vs $15.00) |
| Gemini, Mistral, Azure | Present in usage.go | **Absent** from cost_monitor.go | N/A |

The `usage.go` unknown-model fallback at line 38 uses Sonnet pricing `{3.0, 15.0}`, while `cost_monitor.go:137` falls back to the `"anthropic"` CostRate.

## Impact

Budget enforcement (CostMonitor) and usage reporting (usage.go) give different cost figures for the same tokens on OpenAI models. A user checking their usage dashboard sees one cost; the budget monitor enforces a different threshold. For Gemini/Mistral, the CostMonitor has no rate data at all and falls back to Anthropic rates.

## Recommendation

Consolidate to a single pricing data source. `usage.go:modelPricing` is the more complete and per-model-accurate source. CostMonitor should consume the same map (or a derivative) rather than maintaining a parallel provider-level map.

## References

- `internal/store/usage.go:L6-31` (model pricing map)
- `internal/store/usage.go:L38` (unknown-model fallback)
- `pkg/provider/cost_monitor.go:L58-76` (provider cost rates)
- `pkg/provider/cost_monitor.go:L104` (hardcoded "anthropic" provider)
