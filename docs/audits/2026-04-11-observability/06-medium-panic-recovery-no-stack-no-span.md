# [Medium] Panic recovery middleware logs only %v with no stack trace and no OTel span event

**Scope:** Observability — error reporting
**Topic:** Error reporting
**Date:** 2026-04-11

## Problem

The HTTP recovery middleware catches panics and logs them with `log.Printf("PANIC: %v", err)`. This loses the goroutine stack trace, does not record the panic as an OTel span event, and does not include any request context (method, path, trace_id) in the log line.

## Evidence

`internal/server/server.go:L123-132`:

```go
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                log.Printf("PANIC: %v", err)
                http.Error(w, "internal server error", http.StatusInternalServerError)
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

Three deficiencies:

1. **No stack trace.** `%v` on a panic value gives the panic message but not `debug.Stack()`. A `nil pointer dereference` panic becomes `PANIC: runtime error: invalid memory address or nil pointer dereference` — no indication of where in the code it happened.

2. **No OTel span event.** If the HTTP middleware from finding 01 were wired, there would be a span available in the request context. The recover handler should record the panic as a span event with `span.RecordError` and `span.SetStatus(codes.Error, ...)` so it appears in the trace.

3. **No request context.** The log line does not include `r.Method`, `r.URL.Path`, or any identifier. In a multi-request log stream, there is no way to associate the panic with the request that caused it.

## Impact

When a panic occurs in an HTTP handler (the only path with any recovery), operators get a one-line log message with no actionable information. Debugging requires reproducing the panic with a debugger. The `panic-recovery-sweep` audit confirms this is the only `recover()` in the entire codebase — making this single recovery point the entirety of nanite's panic observability.

## Recommendation

```go
defer func() {
    if err := recover(); err != nil {
        stack := debug.Stack()
        slog.ErrorContext(r.Context(), "panic recovered",
            "error", err,
            "method", r.Method,
            "path", r.URL.Path,
            "stack", string(stack),
        )
        // Record on OTel span if available.
        if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
            span.RecordError(fmt.Errorf("panic: %v", err))
            span.SetStatus(codes.Error, fmt.Sprintf("panic: %v", err))
        }
        http.Error(w, "internal server error", http.StatusInternalServerError)
    }
}()
```

## References

- `internal/server/server.go:L123-132`
- `panic-recovery-sweep` audit — confirms this is the only recover() in production code
- Finding 01 in this audit — OTel HTTP middleware prerequisite for span recording
- Finding 03 in this audit — slog prerequisite for structured panic logging
