# [Medium] Redaction span processor defined but not wired into trace pipeline

**Scope:** Observability — OTel wrapper correctness
**Topic:** OTel wrapper correctness
**Date:** 2026-04-11

## Problem

The `go-otel/redaction` package defines a span processor and `ShouldRedact` function that checks a denylist of sensitive GenAI attribute keys (`gen_ai.content.prompt`, `gen_ai.content.completion`). This processor is never registered with the trace provider. The `FE_OTEL_REDACT_PROMPTS` env var that controls it has no effect because the processor is not in the pipeline.

Currently this is a latent issue — no span in the nanite codebase attaches prompt content to attributes. But if a future change uses the GenAI semantic conventions (finding 04 recommends this) and records prompt/completion content in span attributes, the redaction layer will silently not fire.

## Evidence

Redaction processor defined at `../framework/libs/go-otel/redaction/redaction.go:L30-35`:

```go
func SpanProcessor() sdktrace.SpanProcessor {
    return &redactProcessor{
        enabled: shouldRedactEnabled(os.Getenv("FE_OTEL_REDACT_PROMPTS")),
        deny:    denylistSet(),
    }
}
```

The denylist (`redaction.go:L14-19`):

```go
func Denylist() []string {
    return []string{
        "gen_ai.content.prompt",
        "gen_ai.content.completion",
    }
}
```

The `feotel.Init` function (`feotel.go:L59-63`):

```go
tp := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(exporter),
    sdktrace.WithResource(res),
    sdktrace.WithSampler(cfg.sampler),
)
```

No `sdktrace.WithSpanProcessor(redaction.SpanProcessor())` call. The processor is defined but never instantiated in the pipeline.

Additionally, the processor's `OnEnd` method is a no-op (`redaction.go:L44-46`):

```go
func (r *redactProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
    // ReadOnlySpan is immutable; enforcement must happen in a wrapping exporter.
    _ = s
}
```

The code acknowledges that `sdktrace.ReadOnlySpan` is immutable and that enforcement must happen in a wrapping exporter, but no such wrapping exporter exists. The `ShouldRedact` method exists but nothing calls it.

## Impact

If prompt content is ever added to span attributes (which the GenAI semantic conventions recommend), it would export unredacted to whatever OTel collector is configured. The redaction layer exists to prevent this but is not wired, creating a false sense of safety.

## Recommendation

1. Register the processor in `feotel.Init`:
   ```go
   tp := sdktrace.NewTracerProvider(
       sdktrace.WithBatcher(exporter),
       sdktrace.WithSpanProcessor(redaction.SpanProcessor()),
       sdktrace.WithResource(res),
       sdktrace.WithSampler(cfg.sampler),
   )
   ```
2. Since `ReadOnlySpan` is immutable, implement a wrapping exporter that checks `ShouldRedact` and strips denylist attributes before forwarding to the real OTLP exporter. The current no-op `OnEnd` is architecturally honest about this limitation — the wrapping exporter is the actual fix.
3. Alternatively, document that prompt content should never be placed in span attributes, and use span events with explicit opt-in for prompt recording. This sidesteps the redaction gap.

## References

- `../framework/libs/go-otel/redaction/redaction.go:L14-81`
- `../framework/libs/go-otel/feotel.go:L59-63` — trace provider without processor
- `telemetry-privacy-posture` audit finding 01 — OTel always exports
