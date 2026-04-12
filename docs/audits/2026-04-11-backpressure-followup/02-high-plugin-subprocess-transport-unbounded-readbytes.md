# [High] Plugin subprocess transport unbounded ReadBytes

**Scope:** Plugin subprocess transport
**Topic:** Memory & Resources
**Date:** 2026-04-11

## Problem

`Transport.read()` in the plugin subprocess transport spawns a goroutine that calls `bufio.ReadBytes('\n')` with no size bound. A plugin subprocess can send a single unbounded line, causing OOM in the nanite host.

## Evidence

`internal/plugin/subprocess/transport.go:L101-L139`:

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
    // ...
}
```

`t.r` is a `bufio.NewReader(r)` (default 4KB buffer, grows without limit). No `io.LimitReader`, no max buffer.

The goroutine leak mitigation is better than the MCP stdio transport: on timeout and context cancel, `t.Close()` is called, which closes the underlying reader and unblocks the goroutine. However, the read-size issue remains.

## Impact

A malicious or buggy plugin subprocess that writes a multi-GB line without a newline will cause unbounded memory allocation. Plugins run with host trust today (design choice), but the subprocess transport is the proposed isolation path for future out-of-process plugins. The lack of a read bound means the isolation boundary has no memory protection.

Practical risk today: low (plugin code is in-process anyway). Architectural risk: high (this is the transport that would be used for sandboxed plugins).

## Recommendation

Switch to a `bufio.Scanner` with an explicit max buffer, or wrap the reader in `io.LimitReader`:

```go
t.r = bufio.NewReaderSize(io.LimitReader(r, 4<<20), 64*1024)
```

Or use a scanner-based approach with `scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)`.

## References

- `internal/plugin/subprocess/transport.go:L101-L139`
- Finding 01 in this audit (MCP stdio transport, same pattern)
- `docs/audits/2026-04-11-provider-abstractions/02-high-unbounded-response-bodies.md`
