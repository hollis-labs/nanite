# [Critical] Stdio transport leaks a subprocess, a goroutine, and a pipe pair on every timeout or cancellation

**Scope:** MCP client transport — stdio subprocess lifecycle
**Topic:** Memory & Resources / Concurrency
**Date:** 2026-04-10

## Problem

When `StdioTransport.call` hits its 30 s timeout (line 127) or a context cancellation (line 130), it sets `t.started = false` and returns. It does **not**: kill the subprocess, close `t.stdin`, wait on the process, or close the stdout pipe. The next call to `start()` sees `t.started == false` and constructs a brand-new `exec.Cmd`, reassigning `t.cmd`, `t.stdin`, and `t.stdout` in place. The previous subprocess keeps running, its reader goroutine keeps blocking on `stdout.ReadBytes`, and the pipe pair keeps holding file descriptors. Each subsequent timeout adds another tripled leak.

## Evidence

`internal/mcp/stdio_transport.go:L75-L133`:

```go
func (t *StdioTransport) call(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
    t.mu.Lock()
    defer t.mu.Unlock()

    if err := t.start(); err != nil { return nil, err }

    // ... write request ...

    readCh := make(chan readResult, 1)
    go func() {
        line, err := t.stdout.ReadBytes('\n')  // blocks here forever after timeout
        readCh <- readResult{line, err}
    }()

    timeout := 30 * time.Second
    if deadline, ok := ctx.Deadline(); ok { timeout = time.Until(deadline) }

    select {
    case res := <-readCh:
        // ...
    case <-time.After(timeout):
        t.started = false  // <-- no kill, no close, no wait
        return nil, fmt.Errorf("timeout waiting for response from %s after %s", t.command, timeout)
    case <-ctx.Done():
        t.started = false  // <-- same
        return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
    }
    // ...
}
```

`start()` at `internal/mcp/stdio_transport.go:L41-L72` unconditionally rebuilds the cmd when `t.started == false`:

```go
func (t *StdioTransport) start() error {
    if t.started { return nil }

    t.cmd = exec.Command(t.command, t.args...)  // <-- overwrites old cmd; old process is now unreferenced
    // ...
    t.stdin, err = t.cmd.StdinPipe()
    stdoutPipe, err := t.cmd.StdoutPipe()
    t.stdout = bufio.NewReader(stdoutPipe)
    t.cmd.Stderr = nil

    if err := t.cmd.Start(); err != nil { return fmt.Errorf("start %s: %w", t.command, err) }
    t.started = true
    return nil
}
```

Nothing in the package kills or waits on the old process. `Close()` only touches `t.cmd`, which now points at the *new* cmd; the previous cmd has no live reference and cannot be reaped.

## Impact

For every timeout or context cancel on a stdio MCP server, Nanite leaks (permanently, for the lifetime of the host process):

1. **One subprocess.** Still running, writing to a pipe whose read end nobody will drain. When that pipe fills (default 64 KB on macOS, 64 KB on Linux), the subprocess's writer blocks indefinitely. It becomes a zombie in the operational sense — alive, unreachable, holding memory.
2. **One goroutine.** Blocked on `t.stdout.ReadBytes('\n')`. The goroutine holds a reference to the old `bufio.Reader` and will never return. Minor per-occurrence, but unbounded under repeated failures.
3. **One stdin write FD + one stdout read FD.** The OS holds them open until the host process exits.

Realistic trigger paths:
- A user Ctrl-Cs a chat turn while an `mcp/call_tool` is mid-flight. The chat context cancels, flows to `StdioTransport.call`, hits the `<-ctx.Done()` branch.
- An MCP server's upstream (the typical "wrap a cloud API in MCP" pattern) exceeds the 30 s default timeout.
- A server with a slow `tools/list` handshake (`Manager.DiscoverTools` at `internal/mcp/manager.go:L116-L164` uses whatever context the caller supplies — often with a modest deadline).

The first leak is invisible. The twentieth is a laptop fan. The hundredth is a beta user filing an issue that "nanite gets slow after a while" and rebooting. File descriptor exhaustion is the ultimate endpoint — on macOS the default soft limit is 256, so ~120 leaked timeouts plus all of Nanite's own FDs will start failing `open()` calls.

There is no automatic restart logic, no crash detection, and no supervisor that can observe or remediate the leak.

## Recommendation

Fold a cleanup step into the timeout and cancel branches. At minimum:

```go
case <-time.After(timeout):
    t.killAndReapLocked()
    return nil, fmt.Errorf("timeout waiting for response from %s after %s", t.command, timeout)
case <-ctx.Done():
    t.killAndReapLocked()
    return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
```

```go
// killAndReapLocked kills the current subprocess, closes pipes, waits for
// exit, and marks the transport as not started. Caller must hold t.mu.
// Idempotent.
func (t *StdioTransport) killAndReapLocked() {
    if !t.started { return }
    if t.stdin != nil { _ = t.stdin.Close() }
    if t.cmd != nil && t.cmd.Process != nil { _ = t.cmd.Process.Kill() }
    if t.cmd != nil { _ = t.cmd.Wait() }
    t.started = false
}
```

This reaps the subprocess, unblocks the orphan reader goroutine (because `Wait()` closes the stdout pipe, so `ReadBytes` returns `io.EOF`), and releases FDs.

The same helper should be invoked from the write-error branch at line 99 — currently that branch also only sets `t.started = false`.

Two broader follow-ups that subsume this finding:

1. **Reader-goroutine-per-transport model.** Spawn one reader goroutine when `start()` succeeds. Let it own the pipe for the transport's lifetime. `call()` writes and waits on a per-ID channel. On transport close, the reader exits via EOF. This matches plugin audit finding 02's recommendation and should be shared between the two transports.
2. **Transport-level crash detection.** Run `cmd.Wait()` in its own supervisor goroutine so that if the subprocess exits unexpectedly the Manager learns about it and can either restart or surface the crash to the user. Currently a crashed MCP server is invisible until the next RPC, which returns a `read: file already closed` error with no context.

## References

- `internal/mcp/stdio_transport.go:L41-L133` — `start`, `call`, timeout branches
- `internal/mcp/stdio_transport.go:L185-L199` — `Close` (the only reap path, manual)
- `internal/mcp/manager.go:L116-L164` — `DiscoverTools` uses whatever context the caller supplies
- Related: finding 04 (transport marked dead but subprocess alive), finding 03 (single-mutex serialization), plugin audit `03-high-transport-timeout-kills-connection-permanently.md`
