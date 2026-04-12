# [Medium] MCP StdioTransport leaks subprocess on read timeout or context cancel

**Scope:** MCP subprocess lifecycle
**Topic:** Memory & Resources — zombie processes
**Date:** 2026-04-11

## Problem

When `StdioTransport.call()` times out or the context is cancelled while waiting for a response, the function sets `t.started = false` and returns an error, but does not kill or wait for the subprocess. The process continues running as an orphan.

## Evidence

`internal/mcp/stdio_transport.go:L120-133`:
```go
select {
case res := <-readCh:
    if res.err != nil {
        t.started = false        // marks as not started
        return nil, fmt.Errorf(...)
    }
    line = res.line
case <-time.After(timeout):
    t.started = false            // marks as not started, process still running
    return nil, fmt.Errorf("timeout waiting for response from %s after %s", t.command, timeout)
case <-ctx.Done():
    t.started = false            // marks as not started, process still running
    return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
}
```

After `t.started = false`, the next call to `call()` will invoke `t.start()` which creates a NEW subprocess (`exec.Command` at `:46`), while the old one is still alive with no reference held. The old process becomes a zombie/orphan.

Additionally, the goroutine spawned at `:109` to read stdout remains blocked on `t.stdout.ReadBytes('\n')` indefinitely — it is never cancelled and holds a reference to the old subprocess's stdout pipe.

The `Close()` method at `:185-198` only kills the last-known `t.cmd`:
```go
func (t *StdioTransport) Close() error {
    // ...
    t.stdin.Close()
    err := t.cmd.Process.Kill()
    t.cmd.Wait()
    // ...
}
```

Any previously leaked processes are not tracked and cannot be cleaned up.

## Impact

- Each timeout or context cancellation leaks one MCP server process and one goroutine.
- Under load or with flaky MCP servers, this causes unbounded process accumulation.
- Zombie processes hold file descriptors, memory, and potentially database locks.
- The goroutine leak is bounded per call, but the process leak is unbounded.

## Recommendation

On timeout/cancel, kill the current subprocess before returning:

```go
case <-time.After(timeout):
    t.killAndReset()
    return nil, fmt.Errorf("timeout...")
case <-ctx.Done():
    t.killAndReset()
    return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
```

```go
func (t *StdioTransport) killAndReset() {
    if t.cmd != nil && t.cmd.Process != nil {
        t.stdin.Close()
        t.cmd.Process.Kill()
        t.cmd.Wait()
    }
    t.started = false
}
```

Additionally, consider using `cmd.Cancel` / `cmd.WaitDelay` (Go 1.20+) for the initial `exec.Command` setup, or store a cancel function alongside the command.

## References

- `internal/mcp/stdio_transport.go:L40-72` — start()
- `internal/mcp/stdio_transport.go:L184-198` — Close()
- `2026-04-10-mcp-client-transport` audit — may have adjacent findings
