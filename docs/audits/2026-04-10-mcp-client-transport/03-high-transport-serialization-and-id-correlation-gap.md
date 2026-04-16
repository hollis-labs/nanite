# [High] Stdio transport serializes every RPC via a single mutex and never checks response IDs — same pattern as plugin transport finding 02

**Scope:** MCP client transport — stdio concurrency + JSON-RPC correlation
**Topic:** Concurrency / Protocol correctness
**Date:** 2026-04-10

## Problem

`StdioTransport.call` holds a single `t.mu` mutex across the entire write-then-read cycle. All RPCs on a single MCP server serialize, no matter how many goroutines are trying to call tools concurrently. Layered on top: the transport never matches the response's `id` field against the request's `id`, so the protocol correlation is "whatever line comes out next is the answer to whatever we last sent." That works under serialization, but it means a malicious or buggy MCP server can poison the next legitimate response with an unsolicited stdout line, and it means any future attempt to break the serialization will break correlation silently.

This is the **same pattern** the plugin-system audit flagged for the plugin subprocess transport (`docs/audits/2026-04-10-plugin-system-plan-eval/02-high-transport-serialization-blocks-rpc-proliferation.md`). The MCP client transport is independent code but shares the anti-pattern almost line-for-line, including the suggestive `nextID atomic.Int64` field whose value is never used for correlation.

## Evidence

`internal/mcp/stdio_transport.go:L17-L29`:

```go
type StdioTransport struct {
    command string
    args    []string
    env     []string
    cmd     *exec.Cmd
    stdin   io.WriteCloser
    stdout  *bufio.Reader
    nextID  atomic.Int64      // atomic as if for correlation...
    mu      sync.Mutex        // "serializes requests"
    started bool
}
```

`internal/mcp/stdio_transport.go:L75-L145`:

```go
func (t *StdioTransport) call(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
    t.mu.Lock()
    defer t.mu.Unlock()                  // held across write + read

    if err := t.start(); err != nil { return nil, err }

    id := t.nextID.Add(1)                // ID allocated...
    rpcReq := JSONRPCRequest{ JSONRPC: "2.0", ID: id, Method: method, Params: params }
    payload, _ := json.Marshal(rpcReq)
    payload = append(payload, '\n')
    if _, err := t.stdin.Write(payload); err != nil { ... }

    // ... read a line under the same held mutex ...

    var rpcResp JSONRPCResponse
    if err := json.Unmarshal(line, &rpcResp); err != nil { ... }
    // rpcResp.ID is never compared to id — correlation is implicit
    return &rpcResp, nil
}
```

`JSONRPCResponse.ID` is decoded but never checked at the call site or anywhere else in `internal/mcp/`:

```bash
$ rg 'rpcResp\.ID|JSONRPCResponse.*\.ID' internal/mcp/
internal/mcp/http_transport.go:26:    ID      int64           `json:"id"`
(no read sites; only the struct field)
```

## Impact

**Serialization.** The chat engine routes every tool call through `Manager.ExecuteTool` → `StdioTransport.CallTool` → `StdioTransport.call`, one transport per configured server. A single chat session that wants to call `mcp__engine__engine_tasks_list` and `mcp__vanta__context_search` on the same server in parallel (or a sub-agent doing delegation) will serialize them. The chat engine's delegation / orchestrator features spawn child sessions that share the Manager — their RPCs also queue.

This is not hypothetical for Nanite. `internal/chat/orchestrator.go:L152-L190` calls `ExecuteTool` multiple times against `mcp__engine__engine_sprint_create` and `mcp__engine__engine_task_create` in sequence; a worker pool that did any of this concurrently would slow to one-at-a-time per server. With the context-broker's five-source parallel query model, any future feature that issues parallel tool calls will hit this wall.

**Correlation poisoning.** A malicious or buggy MCP server writes one unsolicited JSON object to stdout (e.g., an unhandled "notification" it thinks it should emit, or a debug log formatted as JSON). The next legitimate `call` reads that object, unmarshals it, and returns it as the answer to the caller's request. The caller sees stale or wrong data with no way to detect the mismatch. For user-visible tool outputs (LLM tool calls), the wrong result is interpreted as a real tool result.

The correlation gap is silent: if the next call's ID is 42 and the injected stale response has ID 1, `json.Unmarshal` succeeds and the caller is never told the IDs differ. JSON-RPC 2.0 §5 requires that the client SHOULD match `id` on response; Nanite's transport ignores it.

**Cross-audit pattern.** The plugin system audit's finding 02 recommended a full concurrent-transport rework. If that rework lands in `internal/plugin/subprocess/transport.go`, it SHOULD land in `internal/mcp/stdio_transport.go` too — the two files can share a `transport-core` package. Otherwise, the same bug is fixed twice, inconsistently.

## Recommendation

The fix is the same as the plugin audit's: rework the transport to a reader-goroutine-per-transport model with a pending-request map keyed by ID.

1. Spawn one reader goroutine in `start()`. It owns `t.stdout` for the lifetime of the transport. It loops on `ReadBytes('\n')`, unmarshals each line, and dispatches by `id` to a channel in `pending map[int64]chan *JSONRPCResponse`.
2. `call()` allocates an ID, registers a channel under `pending[id]`, writes the request under a short-lived `writeMu`, then `select` on the channel vs `ctx.Done()`.
3. On unknown IDs, the reader logs a warning and discards the line rather than returning it as the next response. This closes the correlation gap.
4. On `ctx.Done()` or timeout, remove the ID entry. The reader will silently drop a late response.
5. On transport close, fail all pending channels with a transport error so goroutines don't hang.

Sketch mirrors the plugin audit's finding-02 recommendation:

```go
type StdioTransport struct {
    // ...
    writeMu  sync.Mutex
    pending  map[int64]chan *JSONRPCResponse
    pendMu   sync.Mutex
    closed   atomic.Bool
}

// In start(), after cmd.Start():
go t.readLoop()

func (t *StdioTransport) readLoop() {
    for {
        line, err := t.stdout.ReadBytes('\n')
        if err != nil { t.failPending(err); return }
        var resp JSONRPCResponse
        if err := json.Unmarshal(line, &resp); err != nil { continue } // or log
        t.pendMu.Lock()
        ch, ok := t.pending[resp.ID]
        if ok { delete(t.pending, resp.ID) }
        t.pendMu.Unlock()
        if ok { ch <- &resp } else { log.Printf("mcp: unsolicited response id=%d method=?", resp.ID) }
    }
}
```

**Add a cross-file contract.** Consider extracting the two subprocess transports into a shared module under `internal/transport/jsonrpcstdio/` or similar, so the plugin and MCP clients share one well-tested implementation. This is the kind of work the index's "cross-cutting themes" calls for.

**Tests to add (noted for the follow-up `mcp-client-tooling-and-tests` scope):**

- Concurrent callers against a fake server that sleeps 100 ms per request — 10 parallel calls should total < 200 ms, not > 1 s.
- A fake server that emits an unsolicited line, then responds to a real request — the unsolicited line should be logged and dropped.
- A fake server that emits a response with the wrong `id` — should not be returned to the caller.

## References

- `internal/mcp/stdio_transport.go:L17-L145` — serialized call + implicit correlation
- `internal/mcp/http_transport.go:L21-L29` — `JSONRPCResponse.ID` is declared but never read
- JSON-RPC 2.0 §5 — "the Server MUST respond with the same id that was sent in the Request Object."
- Cross-audit: `docs/audits/2026-04-10-plugin-system-plan-eval/02-high-transport-serialization-blocks-rpc-proliferation.md` — same pattern in plugin subprocess transport
- Related: finding 04 (timeout kills transport), finding 01 (subprocess leak)
