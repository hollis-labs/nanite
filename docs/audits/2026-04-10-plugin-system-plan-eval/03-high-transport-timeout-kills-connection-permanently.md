# [High] Any transport read timeout permanently kills the plugin connection

**Scope:** plugin system — subprocess transport
**Topic:** collateral / reliability
**Date:** 2026-04-10

## Problem

When `Transport.read()` hits its 30-second default timeout (or its context deadline), it calls `t.Close()` on the underlying reader to unblock the goroutine. After that, the transport is dead: every subsequent `Call` fails at `ReadBytes` because the stdout reader is closed. There is no recovery and no alternative transport. The subprocess itself is still running — only the host-side view is broken. The plan never addresses this and Track G's install flow will hit it the first time a plugin's `init` handshake stalls.

## Evidence

`internal/plugin/subprocess/transport.go:L101-139`:

```go
func (t *Transport) read(ctx context.Context) (*RPCResponse, error) {
    type readResult struct {
        line []byte
        err  error
    }
    ch := make(chan readResult, 1)
    go func() {
        line, err := t.r.ReadBytes('\n')
        ch <- readResult{line, err}
    }()

    // Default 30s timeout, respect context deadline if shorter.
    timeout := 30 * time.Second
    if deadline, ok := ctx.Deadline(); ok {
        if d := time.Until(deadline); d < timeout {
            timeout = d
        }
    }

    select {
    case res := <-ch:
        // ...
    case <-time.After(timeout):
        // Close the underlying reader so the goroutine blocked on
        // ReadBytes unblocks and does not leak or corrupt future reads.
        t.Close()
        return nil, fmt.Errorf("timeout after %s", timeout)
    case <-ctx.Done():
        t.Close()
        return nil, fmt.Errorf("context: %w", ctx.Err())
    }
}
```

`Close` closes `t.rClose`, which is the subprocess's stdout pipe:

```go
// transport.go:L41
func (t *Transport) Close() error {
    if t.rClose != nil {
        return t.rClose.Close()
    }
    return nil
}
```

From `manager.go:L139-157`, `stdout` is the `cmd.StdoutPipe()` wrapper, and after closing, any future `bufio.Reader.ReadBytes` on it returns `io.EOF` or `file already closed`. The Manager doesn't detect this — it still thinks the plugin is "Running" because the process itself hasn't exited. `waitForExit` only fires when `cmd.Wait()` returns, and the subprocess is still alive writing to a now-closed read end.

What's worse: the plan's concurrency adds attack surface here. Track E's giphy integration test will issue `command/execute` with a 30s context; on first API call, the Giphy upstream may exceed that. The context's deadline triggers the second `case`, closes the pipe, and the plugin looks dead forever.

Once the read-side is closed, the Manager's state is `StateRunning`, and on the next `Call`, `Start` returns the already-started transport:

```go
// manager.go:L116
if m.state == StateRunning {
    return m.transport, nil
}
```

The next call re-uses the broken transport and fails immediately. The Manager's restart logic in `waitForExit` is the only way to recover, and that only fires if `cmd.Wait()` returns — which it won't, because the process is happy.

## Impact

Triggered on:
- A plugin whose `init` handshake exceeds `StartupTimeout` (default 10s per `DefaultManagerConfig` at `manager.go:L78`)
- A plugin's command that exceeds 30s (the transport default)
- Any context cancellation mid-request — e.g., a user Ctrl-C during `/giphy` cancels the chat context, which propagates to the transport, which closes the pipe

Once hit, the plugin is zombie-like: the subprocess is alive, the host considers it Running, and every subsequent RPC fails with "read: file already closed." No automatic restart. The user-visible symptom is "plugin installed, but every command silently errors." Restart nanite to recover.

For a beta with developer friends, this is almost guaranteed to happen — developers routinely hit Ctrl-C on slow commands, and any non-trivial MCP tool call will exceed 30s eventually.

## Recommendation

Three changes, in order:

1. **Don't close the pipe on timeout.** On timeout, leak the read goroutine (it will return on EOF when the subprocess exits) or signal it via a cancellation channel rather than closing the underlying fd. The concurrent transport rework from finding 02 makes this natural: a single reader goroutine owns the read end permanently, and per-call timeouts just abandon the response channel.

2. **Make transport errors recoverable.** When any RPC fails with a transport-level error (not a protocol-level one), Manager should transition to `StateCrashed`, invoke `onCrash`, and let the restart logic kick in. Currently the error bubbles up to the caller as a plain error and nothing restarts.

3. **Startup timeout should kill the process, not just the transport.** If plugin/init takes > `StartupTimeout`, the current code in `plugin.go:L110-117` cancels the context, which flows into `transport.read` and closes the pipe. Instead: cancel the context AND call `mgr.Stop()` to kill the subprocess so restart logic can run.

This should land as part of B.0 (the new transport section) alongside finding 02's concurrent rework, or as a standalone gate in Track A.5 if the team decides to defer concurrency.

## References

- `internal/plugin/subprocess/transport.go:L101-139` — the timeout branch that kills the transport
- `internal/plugin/subprocess/manager.go:L112-180` — Start logic that re-uses a dead transport
- `internal/plugin/subprocess/plugin.go:L110-145` — Load uses `WithTimeout` context that triggers the bug
- Related: finding 02 (concurrent transport rework subsumes the fix)
