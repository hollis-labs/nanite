# [High] OpenTelemetry exporter always initializes with no opt-out mechanism

**Scope:** Tracing / telemetry
**Topic:** Outbound network — OTel
**Date:** 2026-04-11

## Problem

The OpenTelemetry trace exporter initializes unconditionally at startup with no user-facing opt-out. It defaults to exporting traces to `localhost:4318` and will silently export to any remote endpoint if `OTEL_EXPORTER_OTLP_ENDPOINT` is set. There is no `NANITE_OTEL_DISABLED` toggle, no config-file opt-out, and no documentation of this behavior.

## Evidence

`cmd/nanite/main.go:L88-95`:
```go
// Initialise OpenTelemetry tracing (otel).
otelCtx := context.Background()
otelShutdown, otelErr := feotel.Init(otelCtx, feotel.WithServiceName(brand.OTelService))
if otelErr != nil {
    log.Printf("warning: OTel init failed: %v", otelErr)
} else {
    defer otelShutdown(otelCtx)
}
```

The `feotel.Init` function (from `../framework/libs/go-otel/feotel.go:L30-72`) creates an OTLP HTTP exporter with `AlwaysSample()` by default:

```go
exporter, err := otlptracehttp.New(ctx,
    otlptracehttp.WithEndpoint(cfg.otlpEndpoint),
    otlptracehttp.WithInsecure(),
)
```

Default endpoint: `localhost:4318`. Sampler: `AlwaysSample()`.

Spans are created in 9 locations across the codebase (provider calls, tool calls, context assembly, delegation, MCP discovery, chat generation). Each span includes operation name, duration, and span attributes set via `otel/attribute`. For provider calls, this includes the model name; for tool calls, the tool name.

No span in the codebase attaches user message content or conversation text — the span data is limited to operation metadata (names, durations, status codes). However, if a user sets `OTEL_EXPORTER_OTLP_ENDPOINT` to a remote collector, this operational metadata leaves the machine.

## Impact

- **Default (localhost:4318):** If no OTel collector is running locally, the exporter fails silently (HTTP connection refused). No data leaves the machine. In practice this is a no-op for most users.
- **With `OTEL_EXPORTER_OTLP_ENDPOINT` set:** All sampled spans export to that endpoint. Data includes: service name (`nanite`), operation names, tool names, model names, durations, and error status codes. Not conversation content, but operational metadata.
- **No user opt-out:** Even when `OTEL_EXPORTER_OTLP_ENDPOINT` is configured, there is no `NANITE_OTEL_ENABLED=false` toggle to disable tracing. The only way to prevent export is to not have a collector at the endpoint.
- **Insecure transport:** The exporter uses `WithInsecure()` — no TLS. If pointed at a remote endpoint, spans travel in plaintext.

## Recommendation

1. Add a `NANITE_OTEL_ENABLED` env var (default: `false`). Only call `feotel.Init` when explicitly enabled. This makes tracing opt-in.
2. Document in the user config guide that OTel is available, what it exports, and how to enable/disable it.
3. Consider defaulting to TLS when the endpoint is not `localhost`.
4. Alternatively, use `NeverSample()` as the default sampler unless the user opts in, so the exporter exists but produces no spans.

## References

- `cmd/nanite/main.go:L88-95`
- `../framework/libs/go-otel/feotel.go:L30-72`
- OpenTelemetry Go SDK: `otlptracehttp` exporter
