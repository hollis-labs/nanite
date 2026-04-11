# [High] Transport marks itself dead on any read error, but the subprocess keeps running and nothing observes it — same class as plugin transport finding 03

**Scope:** MCP client transport — failure recovery
**Topic:** Concurrency / Error handling / Memory & Resources
**Date:** 2026-04-10

## Problem

Every failure branch in `StdioTransport.call` sets `t.started = false` and returns. The next call constructs a brand-new cmd with no awareness that the previous one is still running. There is no transport-level crash detection, no Manager-level restart, no goroutine watching `cmd.Wait()`. Once a read fails for any reason (timeout, context cancel, malformed JSON, upstream server crash with EOF), the failure is reported to the immediate caller and then forgotten. The subprocess may be alive, dead, or hung — the host never learns.

This is the MCP-client-side counterpart of the plugin audit's finding 03 (`docs/audits/2026-04-10-plugin-system-plan-eval/03-high-transport-timeout-kills-connection-permanently.md`). The symptoms are slightly different (plugin transport closes the pipe, mcp transport just drops the reference), but the underlying design gap is the same: **no supervisor distinguishes between "RPC failed" and "transport crashed" and nothing owns recovery.**

## Evidence

`internal/mcp/stdio_transport.go:L98-L133`:

```go
if _, err := t.stdin.Write(payload); err != nil {
    t.started = false
    return nil, fmt.Errorf("write to stdin: %w", err)
}

// ...
select {
case res := <-readCh:
    if res.err != nil {
        t.started = false
        return nil, fmt.Errorf("read from stdout: %w", res.err)
    }
    line = res.line
case <-time.After(timeout):
    t.started = false
    return nil, fmt.Errorf("timeout waiting for response from %s after %s", t.command, timeout)
case <-ctx.Done():
    t.started = false
    return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
}
```

Each branch sets `started = false` and does nothing else. There is no `go cmd.Wait()` anywhere in the transport, so nobody reaps the subprocess or learns when it exits.

Compare to `Close()` at `internal/mcp/stdio_transport.go:L185-L199`, the only place that kills the process:

```go
func (t *StdioTransport) Close() error {
    t.mu.Lock()
    defer t.mu.Unlock()
    if !t.started { return nil }
    t.stdin.Close()
    err := t.cmd.Process.Kill()
    t.cmd.Wait()
    t.started = false
    log.Printf("mcp: stopped stdio transport for %s", t.command)
    return err
}
```

`Close()` is only called from:
- `Manager.RemoveServer` (`manager.go:L85-L87`) — explicit user removal via API
- `Manager.Close` (`manager.go:L451-L460`) — shutdown

So during normal operation, the only path that reaps a subprocess is shutdown. In between, any number of stdio transports can fail a read, mark themselves dead, re-start new subprocesses, and leave the old ones running (see finding 01 for the resource-leak angle).

There is also no Manager-level state tracking: `Manager.servers` is a flat `map[string]MCPTransport` with no per-server health or status. The `ListServers()` helper at `manager.go:L331-L350` returns `Connected: true` for every registered server regardless of actual state:

```go
infos = append(infos, ServerInfo{
    Name:      name,
    ToolCount: toolCounts[name],
    Connected: true, // registered means connected
})
```

## Impact

Triggered on any of: a slow MCP server exceeding 30 s (the default timeout), a user Ctrl-C propagating through the chat context to the transport, an MCP server that crashes while processing a request (EOF on stdout), a read error from a closed pipe, a JSON-unparseable line from a buggy server.

The user-visible symptom is any of:

- "My MCP tool works the first time and then every call errors with `read: file already closed`." (If the OS closes the pipe after the parent stops reading.)
- "MCP tools are slow — every call takes 30 s and then times out." (If the subprocess is stuck in a state where it's no longer writing to stdout.)
- "Nanite gets laggy after an hour." (The zombie subprocesses from finding 01 are holding memory; this finding is the reason they never get restarted.)
- "The UI shows my MCP server as Connected but every call fails." (`ListServers` always reports Connected=true.)

For a beta, the failure mode is opaque: there is no log saying "server X crashed, restarting" because the transport never detected a crash, only a failed RPC. The user has no feedback loop; restarting Nanite is the only remediation.

The chat-engine layer also has no visibility. When `Manager.ExecuteTool` returns an error, it flows back to `internal/service/tool.go:L194-L199` which wraps it into a `ToolResult{IsError: true}`, then to the chat engine which surfaces the error to the LLM as tool output. The LLM sees "error" but has no way to distinguish "tool error" from "transport dead — please give up on this server."

## Recommendation

Three changes, in order:

1. **Reap on failure.** As in finding 01, every failure branch should call a `killAndReapLocked` helper that closes stdin, kills the process, and waits. This closes the subprocess leak and guarantees `t.started = false` actually means "subprocess is gone."

2. **Run a supervisor goroutine per transport.** On successful `start()`, spawn a goroutine that calls `cmd.Wait()` and notifies the Manager (or a transport-level callback) on exit. The supervisor should:
   - Distinguish graceful exit (`Wait()` returns nil) from crash (non-zero exit or signal)
   - Update a per-transport state field (`StateRunning / StateCrashed / StateStopped`)
   - Invoke an `onCrash` callback that can trigger a bounded restart with backoff

   Sketch:

   ```go
   func (t *StdioTransport) supervise() {
       err := t.cmd.Wait()
       t.mu.Lock()
       t.started = false
       state := StateStopped
       if err != nil { state = StateCrashed }
       t.mu.Unlock()
       if t.onExit != nil { t.onExit(state, err) }
   }
   ```

3. **Manager-level health.** `Manager.ListServers()` should reflect real state. Replace the hardcoded `Connected: true` with the transport's observed state. Add a method like `Manager.ServerState(name) string` that proxies to the transport.

4. **Surface crashes to the user.** When a transport crashes, emit a structured log or an SSE event so the UI can show "MCP server X crashed" rather than pretending it's healthy. This pairs with the reviewer-backend context's "SSE dedup" theme.

5. **Bounded reconnect.** On crash, the Manager should either restart the subprocess with exponential backoff (bounded to, e.g., 5 attempts per hour) or mark the server as disabled and require manual re-enable. Do not retry unboundedly on startup failure — a command that doesn't exist will loop forever otherwise.

All of this is subsumed by the reader-goroutine-per-transport refactor in finding 03, which is the natural home for the supervisor and the pending-request failure semantics.

## References

- `internal/mcp/stdio_transport.go:L75-L133` — every failure branch
- `internal/mcp/stdio_transport.go:L185-L199` — `Close` is the only path that calls `Wait()`
- `internal/mcp/manager.go:L331-L350` — `ListServers` always reports Connected
- Related: finding 01 (subprocess leak), finding 03 (serialization + correlation), cross-audit: plugin audit finding 03
