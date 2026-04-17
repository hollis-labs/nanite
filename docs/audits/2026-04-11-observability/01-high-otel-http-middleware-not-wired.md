# [High] OTel HTTP propagation middleware defined but not wired into server

**Scope:** Observability — OTel wrapper
**Topic:** OTel wrapper correctness
**Date:** 2026-04-11

## Problem

The `go-otel` library provides a fully functional `HTTPMiddleware` that creates per-request server spans with W3C traceparent extraction, status code recording, and error flagging. It is never wired into nanite's middleware chain. Every HTTP request to nanite is invisible to the trace backend.

## Evidence

The middleware exists in the library:

```go
// ../framework/libs/go-otel/propagation/propagation.go:18-41
func HTTPMiddleware(next http.Handler) http.Handler {
    tracer := otel.Tracer("fe.http")
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        prop := otel.GetTextMapPropagator()
        ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
        spanName := r.Method + " " + r.URL.Path
        ctx, span := tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindServer))
        defer span.End()
        // ... records http.method, http.target, http.status_code, sets error on 5xx
    })
}
```

The nanite server middleware chain (`internal/server/server.go:60`):

```go
handler := s.recoverMiddleware(s.loggingMiddleware(basicAuthMiddleware(s.corsMiddleware(s.mux))))
```

No reference to `HTTPMiddleware` or `propagation.HTTPMiddleware` anywhere in the nanite codebase. Grep for `HTTPMiddleware` across `~/Projects-apps/nanite` returns zero results.

This means:
- No per-request spans are created at the HTTP boundary
- W3C `traceparent` headers from upstream callers are not extracted
- The child spans created inside handlers (e.g., `nanite.service.generateResponse`, `nanite.mcp.discoverTools`) have no parent span linking them to the HTTP request
- Status code recording and 5xx error flagging do not happen

## Impact

Distributed tracing across service boundaries is broken. Any OTel collector receiving nanite spans sees orphaned spans with no HTTP request parent. Operators cannot correlate a slow API response to the internal spans that caused it. The `statusWriter` wrapper in the middleware also implements `http.Flusher` delegation — without it, SSE endpoints through a future OTel middleware would break.

## Recommendation

Insert `propagation.HTTPMiddleware` into the middleware chain, outermost after recover:

```go
handler := s.recoverMiddleware(propagation.HTTPMiddleware(s.loggingMiddleware(basicAuthMiddleware(s.corsMiddleware(s.mux)))))
```

This single change connects all existing child spans to HTTP request parents and enables distributed trace propagation.

## References

- `../framework/libs/go-otel/propagation/propagation.go:L18-41` — the middleware
- `internal/server/server.go:L60` — the chain where it should be inserted
- `telemetry-privacy-posture` audit finding 01 — OTel always exports (this finding makes the unused middleware doubly wasteful)
