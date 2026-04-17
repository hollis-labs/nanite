# [High] Metrics infrastructure defined but zero metrics emitted by nanite

**Scope:** Observability — metric coverage
**Topic:** Metric coverage
**Date:** 2026-04-11

## Problem

The `go-otel` library defines three standard metric instruments (`fe.request.count`, `fe.request.latency`, `fe.error.count`) via `RegisterMetrics()`, plus a GenAI-specific `RecordModelLatency()` histogram. None are called anywhere in the nanite codebase. Nanite emits zero metrics. There is no metric exporter configured and no `otel.Meter` call anywhere in nanite code.

## Evidence

Metrics are defined in the library:

```go
// ../framework/libs/go-otel/metrics.go:15-43
func RegisterMetrics(meter metric.Meter) (*Metrics, error) {
    reqCount, _ := meter.Int64Counter("fe.request.count", ...)
    reqLatency, _ := meter.Float64Histogram("fe.request.latency", ...)
    errCount, _ := meter.Int64Counter("fe.error.count", ...)
    return &Metrics{RequestCount: reqCount, RequestLatency: reqLatency, ErrorCount: errCount}, nil
}
```

GenAI model latency recording:

```go
// ../framework/libs/go-otel/genai/genai.go:35-47
func RecordModelLatency(ctx context.Context, model string, duration time.Duration) {
    histogram, _ := otel.Meter(meterName).Float64Histogram("gen_ai.client.operation.duration", ...)
    histogram.Record(ctx, float64(duration.Milliseconds()), ...)
}
```

Grep results for `RegisterMetrics`, `fe.request.count`, `fe.error.count`, `metric.Meter`, `RecordModelLatency` across nanite: **zero matches**.

The OTel init (`feotel.Init`) only sets up a trace provider — no meter provider is configured. Even if `RegisterMetrics` were called, there is no exporter to receive the data.

## Impact

Operators have no metrics visibility into nanite. No request counts, no latency histograms, no error rate counters. This is the most basic observability gap: you cannot dashboard or alert on a service that emits no metrics.

The library provides everything needed — counters, histograms, GenAI-specific instruments — but the application layer never wires them.

## Recommendation

1. Add a meter provider to `feotel.Init` (or a separate `InitMetrics` function) with an OTLP metric exporter.
2. In `cmd/nanite/main.go`, after `feotel.Init`, call `feotel.RegisterMetrics(otel.Meter("nanite"))` and pass the `*Metrics` struct to the server and service container.
3. In `loggingMiddleware` or `HTTPMiddleware`, increment `RequestCount` and record `RequestLatency` per request.
4. In error paths, increment `ErrorCount`.
5. In `generateResponse`, call `genai.RecordModelLatency(ctx, model, time.Since(startTime))` at the end of each provider call.
6. Consider the privacy posture: metrics are generally safe (no user content), but model names and tool names appear as attributes. This is lower risk than trace spans but should be documented.

## References

- `../framework/libs/go-otel/metrics.go:L15-43`
- `../framework/libs/go-otel/genai/genai.go:L35-47`
- `cmd/nanite/main.go:L88-95` — OTel init (trace only, no metrics)
