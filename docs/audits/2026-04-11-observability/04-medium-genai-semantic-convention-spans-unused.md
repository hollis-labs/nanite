# [Medium] GenAI semantic convention spans and attributes defined but never used

**Scope:** Observability — OTel wrapper
**Topic:** Span discipline
**Date:** 2026-04-11

## Problem

The `go-otel/genai` package defines `ModelCallSpan`, `RecordTokenUsage`, `RecordModelLatency`, and six standard GenAI semantic convention attribute keys (`gen_ai.system`, `gen_ai.request.model`, `gen_ai.usage.input_tokens`, etc.). None are called by any provider adapter in nanite.

Instead, providers use ad-hoc `feotel.StartSpan` with custom attribute names (`nanite.provider`, `nanite.model`, `nanite.provider.input_tokens`, `nanite.provider.output_tokens`). These custom names are functional but miss the OTel GenAI semantic conventions that would make nanite traces interoperable with standard GenAI observability tooling.

## Evidence

GenAI package defined at `../framework/libs/go-otel/genai/`:

```go
// genai/genai.go:17-24
func ModelCallSpan(ctx context.Context, model, operation string) (context.Context, trace.Span) {
    return otel.Tracer(tracerName).Start(ctx, "gen_ai."+operation,
        trace.WithAttributes(
            attribute.String(string(GenAIRequestModelKey), model),
            attribute.String(string(GenAIOperationNameKey), operation),
        ),
    )
}
```

Grep for `genai.ModelCallSpan`, `genai.RecordTokenUsage`, `genai.RecordModelLatency` across nanite: **zero matches**.

What nanite actually does (`pkg/provider/anthropic.go:L276-282`):

```go
ctx, span := feotel.StartSpan(ctx, "nanite.provider.anthropic.stream")
span.SetAttributes(
    attribute.String("nanite.provider", "anthropic"),
    attribute.String("nanite.model", model),
    attribute.Int("nanite.messages.count", len(messages)),
    attribute.Int("nanite.tools.count", len(tools)),
)
```

And in the SSE reader (`pkg/provider/anthropic.go:L454-458`):

```go
span.SetAttributes(
    attribute.Int("nanite.provider.input_tokens", totalInput),
    attribute.Int("nanite.provider.output_tokens", totalOutput),
)
```

These custom attribute names (`nanite.*`) are not recognized by standard GenAI observability dashboards that expect `gen_ai.*` attributes.

## Impact

Nanite traces are not interoperable with the emerging OTel GenAI semantic conventions. Tools like Langfuse, Traceloop, and Grafana GenAI dashboards that filter on `gen_ai.*` attributes will not find nanite spans. The custom `nanite.*` namespace works for internal use but limits ecosystem integration.

Additionally, only the Anthropic provider has any tracing at all. The other 7 HTTP providers (OpenAI, Ollama, Gemini, Mistral, Azure, OpenRouter, OpenZen) and all 8 PTY adapters create no spans whatsoever.

## Recommendation

1. Replace `feotel.StartSpan(ctx, "nanite.provider.anthropic.stream")` with `genai.ModelCallSpan(ctx, model, "chat")` in the Anthropic adapter.
2. Call `genai.RecordTokenUsage(span, totalInput, totalOutput)` instead of raw attribute setting.
3. Call `genai.RecordModelLatency(ctx, model, elapsed)` at span end.
4. Add the same tracing to the other 7 HTTP providers and 8 PTY adapters.
5. Keep `nanite.*` attributes as supplementary (e.g., `nanite.messages.count`, `nanite.tools.count`) — these are useful nanite-specific context that the GenAI conventions don't cover.

## References

- `../framework/libs/go-otel/genai/genai.go:L17-47`
- `../framework/libs/go-otel/genai/attributes.go:L7-25`
- `pkg/provider/anthropic.go:L276-282` — current custom span
- `pkg/provider/anthropic.go:L454-458` — current custom token attributes
- `provider-abstractions` audit — confirms only Anthropic has spans
