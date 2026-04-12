# [High] MCP stdio transport unbounded ReadBytes with goroutine leak on timeout

**Scope:** MCP client transport
**Topic:** Memory & Resources / Concurrency
**Date:** 2026-04-11

## Problem

`StdioTransport.call()` spawns a goroutine to read a response line via `bufio.ReadBytes('\n')`. This read has no size limit and the goroutine leaks when the timeout or context cancellation path fires.

## Evidence

`internal/mcp/stdio_transport.go:L104-L133`:

```go
readCh := make(chan readResult, 1)
go func() {
    line, err := t.stdout.ReadBytes('\n')
    readCh <- readResult{line, err}
}()

// ...
select {
case res := <-readCh:
    // ...
case <-time.After(timeout):
    t.started = false
    return nil, fmt.Errorf("timeout waiting for response from %s after %s", t.command, timeout)
case <-ctx.Done():
    t.started = false
    return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
}
```

Two issues in one site:

1. **Unbounded read.** `ReadBytes('\n')` uses the default `bufio.Reader` (4KB initial buffer, grows without limit). A malicious or misbehaving MCP server can send a single line that grows until the nanite process runs out of memory. There is no `io.LimitReader` wrapper and no scanner with a max buffer.

2. **Goroutine leak on timeout/cancel.** When the `select` takes the timeout or ctx.Done branch, the goroutine remains blocked on `ReadBytes`. The transport sets `started = false` and will re-start on the next call, but the old goroutine is still alive, holding the old `bufio.Reader` and `stdout` pipe. Repeated timeouts accumulate leaked goroutines and file descriptors. The plugin subprocess transport (`internal/plugin/subprocess/transport.go:L107-L138`) has the same structural pattern but mitigates the leak by calling `t.Close()` on timeout/cancel, which closes the underlying reader and unblocks the goroutine. The MCP stdio transport does NOT close anything on timeout.

## Impact

- **OOM from unbounded line.** An MCP server controlled by an attacker (or simply buggy) that sends a multi-GB line without a newline will cause unbounded memory allocation in the nanite host. This is reachable whenever any MCP server is configured.
- **Goroutine/FD leak.** Each timed-out call leaks one goroutine and keeps the old stdout pipe open. Under sustained timeout conditions (network-stalled MCP server), this accumulates linearly.

Already partially flagged by `mcp-client-transport` audit (findings 01 + 04) but that audit focused on subprocess lifecycle, not the read-size bound. The unbounded `ReadBytes` was noted in the cross-audit notes but not filed as a numbered finding. This audit files it explicitly as a backpressure concern.

## Recommendation

1. Wrap the stdout reader in an `io.LimitReader` before creating the `bufio.Reader`, or switch to a `bufio.Scanner` with an explicit max buffer (e.g., 4MB):

```go
t.stdout = bufio.NewReaderSize(io.LimitReader(stdoutPipe, 4<<20), 64*1024)
```

Note: `LimitReader` on a persistent pipe means the limit applies to cumulative bytes read, not per-line. A scanner with `scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)` is the better fit for newline-delimited protocols.

2. On timeout or context cancellation, close the stdin pipe and kill the subprocess (or at minimum close the stdout pipe) to unblock the leaked goroutine, matching what the plugin subprocess transport already does.

## References

- `internal/mcp/stdio_transport.go:L104-L133` (this finding)
- `internal/plugin/subprocess/transport.go:L107-L138` (same pattern, better mitigation)
- `docs/audits/2026-04-10-mcp-client-transport/` findings 01, 04
- `docs/audits/2026-04-11-provider-abstractions/02-high-unbounded-response-bodies.md` (same class)
