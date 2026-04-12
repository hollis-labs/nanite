# [High] HTTP server has no read/write/idle timeouts

**Scope:** HTTP server setup
**Topic:** Security
**Date:** 2026-04-11

## Problem

The HTTP server is constructed via `http.ListenAndServe(addr, handler)` which creates an `http.Server` with zero-value timeouts — no `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`. This is a Slowloris DoS vector and was already flagged by gosec G114 in the `whole-repo-tooling-and-tests-sweep` audit.

## Evidence

`internal/server/server.go:L63`

```go
func (s *Server) ListenAndServe() error {
    handler := s.recoverMiddleware(s.loggingMiddleware(basicAuthMiddleware(s.corsMiddleware(s.mux))))
    addr := fmt.Sprintf(":%d", s.port)
    log.Printf("nanite listening on %s (dev=%v)", addr, s.dev)
    return http.ListenAndServe(addr, handler)
}
```

No `ReadTimeout`, `WriteTimeout`, or `IdleTimeout` is set. The `time` import exists in the file but is only used by `loggingMiddleware`.

Confirmed no `http.Server` struct is used anywhere in `internal/server/`:

```
rg 'ReadTimeout|WriteTimeout|IdleTimeout|http\.Server' internal/server/
# No matches
```

## Impact

- **Slowloris attack.** An attacker opens many connections, sends partial headers slowly, and exhausts the server's connection limit. With no read timeout, the server waits indefinitely for request completion.
- **Write-side resource exhaustion.** SSE streams (presence, message, event, workflow) have no server-side write timeout. A client that stops reading but keeps the TCP connection alive holds a goroutine and file descriptor indefinitely.
- **Idle connection exhaustion.** With no idle timeout, keep-alive connections accumulate.

Note: SSE endpoints legitimately need long-lived connections. The recommended fix uses `ReadHeaderTimeout` (protects against Slowloris) without `ReadTimeout` (which would kill SSE streams). SSE-specific write deadlines should be managed per-handler.

## Recommendation

Replace `http.ListenAndServe` with an explicit `http.Server`:

```go
func (s *Server) ListenAndServe() error {
    handler := s.recoverMiddleware(s.loggingMiddleware(basicAuthMiddleware(s.corsMiddleware(s.mux))))
    srv := &http.Server{
        Addr:              fmt.Sprintf(":%d", s.port),
        Handler:           handler,
        ReadHeaderTimeout: 10 * time.Second,
        IdleTimeout:       120 * time.Second,
    }
    log.Printf("nanite listening on %s (dev=%v)", srv.Addr, s.dev)
    return srv.ListenAndServe()
}
```

Do NOT set `ReadTimeout` or `WriteTimeout` globally — they would break SSE streams. Instead, use per-handler `http.TimeoutHandler` wrappers for non-streaming endpoints.

## References

- gosec G114 finding in `whole-repo-tooling-and-tests-sweep` audit (`internal/server/server.go:63`)
- CWE-400: Uncontrolled Resource Consumption
- `sandbox-hardening` audit found the same pattern in `internal/sandbox/proxy.go:45` — the sandbox's internal HTTP proxy also has no timeouts.
- Cross-ref: `05-high-no-request-body-limits.md` (related DoS vector)
