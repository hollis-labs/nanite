# [High] Proxy CONNECT handler leaks goroutines and blocks `Stop()` indefinitely; HTTP path leaks response body on errors

**Scope:** sandbox / network proxy / lifecycle
**Topic:** Concurrency correctness, goroutine leaks, shutdown order
**Date:** 2026-04-10

## Problem

Three related lifecycle bugs in `internal/sandbox/proxy.go`, each of which independently produces a goroutine leak or a blocked shutdown:

1. **CONNECT bidirectional `io.Copy` has no deadline and no cancellation.** If either side stops reading/writing (e.g. a TCP half-close, a TLS handshake stall, or a sandboxed process that opened a connection and went idle), the two copy goroutines block forever. `copyWg.Wait()` then also blocks forever, leaving the handler goroutine stuck. On top of that, `p.server.Shutdown(ctx)` does **not** terminate hijacked connections (Go's `http.Server.Shutdown` explicitly documents that), so `p.wg.Wait()` in `Proxy.Stop` never returns.

2. **`Proxy.Stop` is deferred from `AgentExec`** via `defer proxy.Stop()`. If a long-running sandboxed command opens a CONNECT tunnel and returns successfully from its work, `AgentExec`'s defer calls `Stop`, `Stop` times out its Shutdown after 5s, then blocks forever on `wg.Wait()` because the hijacked CONNECT is still open. The net result: **every `AgentExec` with proxy that used CONNECT and didn't fully drain leaks a proxy goroutine group permanently.**

3. **`handleHTTP` does not set a deadline on the outgoing request context** beyond the 60-second client timeout, and uses `r.Context()` which is the inbound-request context. That's fine, but: on any early `http.Error` after `client.Do` succeeds, there is no `defer resp.Body.Close()` before the early returns because the current code only defers after the success path. Actually re-reading — `defer resp.Body.Close()` IS present on line 176, so (3) is not a leak. Strike this item; included here for transparency so the reviewer doesn't re-flag it.

4. **`handleConnect` closes `targetConn` and `clientConn` via `defer` before `copyWg.Wait()`.** Closing an `io.Copy` source/sink mid-copy is the correct way to unblock it, but the defers run in LIFO order — so the close is only issued after the goroutines are already blocked waiting for bytes. The `defer targetConn.Close()` and `defer clientConn.Close()` and `copyWg.Wait()` are sequenced as: Wait, then Close. So Wait blocks forever and Close never runs. This is the primary leak.

## Evidence

```go
// internal/sandbox/proxy.go:82-131 (abridged, with line numbers)
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
    host, _, err := splitHostPort(r.Host)
    ...
    // Dial the target.
    targetConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
    if err != nil {
        http.Error(w, fmt.Sprintf("dial target: %v", err), http.StatusBadGateway)
        return
    }
    defer targetConn.Close()                     // runs AFTER copyWg.Wait

    hijacker, ok := w.(http.Hijacker)
    ...
    clientConn, _, err := hijacker.Hijack()
    ...
    defer clientConn.Close()                     // runs AFTER copyWg.Wait

    _, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

    // Bidirectional copy.
    var copyWg sync.WaitGroup
    copyWg.Add(2)
    go func() {
        defer copyWg.Done()
        _, _ = io.Copy(targetConn, clientConn)   // no deadline, no ctx
    }()
    go func() {
        defer copyWg.Done()
        _, _ = io.Copy(clientConn, targetConn)   // no deadline, no ctx
    }()
    copyWg.Wait()                                // blocks until BOTH copies return
}
```

The handler blocks on `copyWg.Wait()` forever if one half of the TCP pair stalls. The defers to close the conns are registered but only execute after the function returns — which it never will. So the conns stay open, the copies stay blocked, and the handler goroutine leaks. Every CONNECT that stalls = one permanently-pinned goroutine plus two inner copy goroutines.

`Proxy.Stop` depends on the handler goroutines draining so the server's Serve can return:

```go
// internal/sandbox/proxy.go:61-70
func (p *Proxy) Stop() error {
    if p.server == nil {
        return nil
    }
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    err := p.server.Shutdown(ctx)
    p.wg.Wait()              // never returns if a CONNECT handler is leaked
    return err
}
```

`http.Server.Shutdown` explicitly: "Shutdown does not attempt to close nor wait for hijacked connections." So the 5-second context just causes Shutdown to return an error, then `p.wg.Wait()` hangs.

`AgentExec` then leaks one proxy goroutine group per call with a stalled CONNECT:

```go
// internal/sandbox/exec.go:104-121
if len(opts.NetworkAllow) > 0 {
    proxy = NewProxy(opts.NetworkAllow)
    if err := proxy.Start(); err != nil {
        return nil, fmt.Errorf("sandbox: start proxy: %w", err)
    }
    defer proxy.Stop()       // blocks forever if CONNECT leaked
    ...
```

Since `AgentExec` is synchronous, the calling goroutine (MCP tool handler, workflow step) is the one that blocks. A single stuck MCP tool call wedges its handler goroutine forever and leaks all child goroutines.

## Impact

- **Who:** any agent execution that uses the network proxy and opens a CONNECT tunnel that subsequently stalls. Real-world trigger: fetch an HTTPS URL whose server accepts the TLS handshake and then stops responding (common anti-scraping behavior, or a slow API).
- **What:** unbounded goroutine growth in the Nanite process, plus blocked MCP tool handlers and blocked workflow runs. The process keeps running but gradually accumulates FDs and goroutines until OOM or FD exhaustion.
- **Blast radius:** process-wide. First-session-to-hit-it brings down subsequent sessions. Hard to diagnose from user-facing symptoms.
- **Reproducibility:** requires the target to stall, so flaky in tests, but a deterministic hostile server can trigger it on demand.

## Recommendation

1. **Kill the copy by closing the conns on first-EOF, not waiting for both.** The standard Go idiom:

   ```go
   errCh := make(chan error, 2)
   go func() { _, err := io.Copy(targetConn, clientConn); errCh <- err }()
   go func() { _, err := io.Copy(clientConn, targetConn); errCh <- err }()
   <-errCh                        // first side done
   targetConn.Close()
   clientConn.Close()
   <-errCh                        // drain the second (now unblocked)
   ```

   This guarantees both goroutines terminate within the TCP close latency of the first side.

2. **Add a per-CONNECT context with deadline.** 5-minute absolute cap, configurable. Use `net.Dialer{Timeout: 10*time.Second}.DialContext(ctx, ...)` for the dial, and wrap the conns with `SetDeadline`:

   ```go
   deadline := time.Now().Add(5 * time.Minute)
   _ = targetConn.SetDeadline(deadline)
   _ = clientConn.SetDeadline(deadline)
   ```

   Either side exceeding the deadline returns from `io.Copy` with an i/o timeout error, and the unblock-by-close pattern above closes the other side.

3. **Register the proxy's in-flight CONNECTs and tear them down on `Stop()`.** Keep a `map[net.Conn]struct{}` of active hijacked pairs guarded by a mutex; `Stop` walks it and closes both ends before calling `wg.Wait()`. This is what https servers that want clean shutdown with hijacked conns do.

4. **Do NOT rely on `http.Server.Shutdown` for hijacked conn cleanup** — it's documented not to handle them. Use `Close` if you need a hard stop (accepts lost bytes in exchange for guaranteed return).

5. **Stop timeout in `Proxy.Stop`** should be larger than the CONNECT deadline in (2), or the conn registry (3) makes it unnecessary. Either is fine.

6. **Add a test** that opens a CONNECT tunnel to a server that accepts but never responds, then calls `Proxy.Stop` with a 2s timeout and asserts Stop returns within 3s. Without the fix, the test hangs; CI should catch it.

## References

- `internal/sandbox/proxy.go:82-131, 61-70`
- `internal/sandbox/exec.go:104-121`
- `net/http.Server.Shutdown` docs — hijacked connection exclusion
- Go stdlib `io.Copy` documentation
- Related: `02-critical-proxy-ssrf-rfc1918-and-port.md` (same file, separate issues)
