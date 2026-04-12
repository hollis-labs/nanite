# [Medium] SSE endpoints have no connection limits or keepalive mechanism

**Scope:** SSE streaming endpoints
**Topic:** Security / Resources
**Date:** 2026-04-11

## Problem

Four SSE endpoints (`/api/stream/{messageID}`, `/api/presence`, `/api/plugins/events/stream`, `/api/workflows/events`) accept connections without any limit on concurrent connections. Each connection holds a goroutine and an OS-level file descriptor for the duration of the connection.

## Evidence

All four SSE handlers follow the same pattern — enter a `for/select` loop that only exits on `ctx.Done()` (client disconnect):

`internal/api/messages.go:L142-193` — message stream
`internal/api/presence.go:L9-48` — presence stream
`internal/api/event_stream.go:L14-71` — plugin event stream
`internal/api/workflows.go:L141-180` — workflow event stream

The message stream has session-level dedup (one active stream per session, L163-169), which is good. But the other three SSE endpoints have no dedup or connection limits at all.

Additionally, none of the SSE endpoints implement a keepalive/heartbeat mechanism. If the underlying TCP connection is silently broken (e.g., NAT timeout, proxy timeout), the server-side goroutine blocks indefinitely on channel receive. The `ctx.Done()` signal depends on the Go HTTP server detecting the broken connection, which requires a failed write. Without periodic writes, broken connections are never detected.

## Impact

- **Resource exhaustion.** An attacker can open thousands of SSE connections (especially to `/api/presence` and `/api/plugins/events/stream` which have no dedup), exhausting goroutines and file descriptors.
- **Goroutine leaks.** Silent TCP disconnections leave goroutines blocked on channel receive indefinitely.
- **No server-side timeout.** Combined with finding 04 (no HTTP server timeouts), SSE connections persist forever.

## Recommendation

1. Add a server-side per-IP or global connection limit for SSE endpoints. A simple atomic counter per endpoint type with a configurable max (e.g., 100) is sufficient.
2. Add a periodic keepalive comment (`:keepalive\n\n`) every 30 seconds to detect dead connections.
3. Add a maximum connection lifetime (e.g., 4 hours) after which the server sends a reconnect hint and closes.

```go
// Keepalive ticker
keepalive := time.NewTicker(30 * time.Second)
defer keepalive.Stop()

for {
    select {
    case <-ctx.Done():
        return
    case <-keepalive.C:
        fmt.Fprintf(w, ":keepalive\n\n")
        flusher.Flush()
    case evt, ok := <-ch:
        // ...
    }
}
```

## References

- `reviewer-backend.md` priority 6: "SSE endpoint lifecycle. Connection lifetime, cleanup on disconnect."
- The message stream's session-level dedup (L163-169) is well-implemented and should be used as the model.
- Cross-ref: `04-high-no-http-server-timeouts.md` (no idle timeout compounds this issue).
