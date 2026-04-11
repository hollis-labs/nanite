# [High] Subprocess transport serializes every RPC via a single mutex — plan's RPC proliferation will deadlock and starve

**Scope:** plugin system — subprocess transport
**Topic:** plan-soundness / concurrency
**Date:** 2026-04-10

## Problem

The current `subprocess.Transport` serializes every `Call()` and `Notify()` through a single mutex. One in-flight RPC blocks all others. The plan proposes adding `http/handle`, `mcp/call_tool`, `event/handle` (fire-and-forget), `command/execute`, `crud/*`, `plugin/migrate`, and `plugin/health` as concurrent method classes all going over the same transport — but never addresses the serialization bottleneck. A single slow RPC will hang command latency, event dispatch, HTTP request handling, and health checks simultaneously.

## Evidence

`internal/plugin/subprocess/transport.go:L17-74`:

```go
type Transport struct {
	w      io.Writer
	r      *bufio.Reader
	rClose io.Closer
	nextID atomic.Int64
	mu     sync.Mutex // serializes writes and read correlation
}

func (t *Transport) Call(ctx context.Context, method string, params any) (*RPCResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := t.nextID.Add(1)
	req := RPCRequest{ JSONRPC: "2.0", ID: id, Method: method, Params: params }
	if err := t.write(req); err != nil { ... }

	resp, err := t.read(ctx)   // blocks here, lock still held
	// ...
}
```

The comment even admits it: "serializes writes and read correlation." `nextID` is `atomic.Int64` as if ID-based correlation was planned, but `read()` is called directly after `write()` under the same held mutex, so the ID is never used for correlation — whatever response arrives first is assumed to match the just-sent request. There is no response-router goroutine and no per-ID channel map.

Now look at what the plan adds to this transport:

- B.10: new `mcp/call_tool`, `http/handle`, `plugin/migrate` methods — all go through `transport.Call`
- B.5: `PluginMCPTransport` wraps a subprocess plugin and issues `mcp/list_tools` / `mcp/call_tool` per LLM tool call
- `subprocessEventHook.Handle` in `plugin.go:L270-298` already does `Call` for pre-hook events and `Notify` for regular events
- `subprocessCRUDHandler` in `plugin.go:L318-389` does one `Call` per CRUD operation
- Track E's integration test exercises slash command + MCP tool call + event hook + HTTP route simultaneously through the same transport

Under realistic load (a user typing in chat while the agent calls `giphy.search` via MCP while `message.sent` events fire to the same plugin), these all queue behind `t.mu`. Worst case: an `mcp/call_tool` waits on Giphy's upstream HTTP with a 30-second default; during those 30 seconds, every `message.sent` fire-and-forget `Notify` blocks (because `Notify` also takes `t.mu.Lock()` at line 78), and every `plugin/health` check times out. The health check timeout then closes the reader (see finding 03), killing the transport entirely.

Also note: `read()` at `transport.go:L101-139` uses a goroutine-per-read pattern. When that goroutine is spawned while the mutex is held, any concurrent `Call()` is blocked at `t.mu.Lock()` until the read goroutine completes and `Call` returns.

## Impact

The plan's proposed subprocess model has one RPC pipeline. With it, realistic plugins will experience:

1. **Event hook starvation.** High-frequency events (`message.sent`, `context.assembled`) pile up behind long-running `command/execute` or `mcp/call_tool` calls. Nanite emits events via `go` in many places, but those goroutines still serialize at `t.mu.Lock()`, so the host's goroutine count climbs while the plugin sees events with increasing latency.

2. **HTTP route latency spikes.** B.5+B.10 route HTTP requests to plugins via `http/handle` RPC. A single slow tool call on an unrelated route blocks the entire route set behind it.

3. **Health check interference.** `healthLoop` at `manager.go:L314-326` runs every 30s. If a command call is holding the mutex when the health interval fires, the health check waits; if the command exceeds the health check timeout (5s per `healthCheck` at `manager.go:L329`), the health goroutine logs a false "unhealthy" warning and continues without restarting the plugin — an observability hole.

4. **Cascading deadlock with finding 03.** When any call takes longer than the transport's 30-second default read timeout (`transport.go:L113`), `read()` calls `t.Close()` to unblock the pipe. After that, every subsequent Call on this transport fails. One slow RPC kills the plugin connection.

For a beta for developer friends: a developer installs giphy, types `/giphy balloons`, the API takes 7 seconds on first call, and during those 7 seconds the developer's `message.sent` events pile up and the Plugin Manager UI's health check shows a flapping status. It looks broken. It is broken.

## Recommendation

Add to Track B (or make it a new section under Track C's SDK work, since the wire protocol is now shared):

**B.0 — Make the transport concurrent.** Before any of the new RPC methods land, rework `transport.Call` to support multiple in-flight requests:

1. Spawn a single reader goroutine at transport startup that reads from stdout, parses each JSON-RPC response, and dispatches by `id` to a `map[int64]chan *RPCResponse`.
2. `Call` becomes: allocate ID, register a response channel in the map, write the request under a short-lived write mutex, `select { case resp := <-ch: case <-ctx.Done(): }`.
3. Writes still need to be serialized (stdin is one pipe, and newline-delimited JSON can't interleave), but the write section is tiny and doesn't include the read-wait.
4. On context cancel, clean up the map entry so the reader goroutine doesn't leak memory when a late response arrives.
5. On transport close, cancel all pending request channels with an error so no goroutine hangs.
6. Notify can share the same write mutex but doesn't need a response channel.

Sketch:

```go
type Transport struct {
    w        io.Writer
    r        *bufio.Reader
    rClose   io.Closer
    nextID   atomic.Int64
    writeMu  sync.Mutex
    pendingMu sync.Mutex
    pending  map[int64]chan *RPCResponse
    closed   atomic.Bool
}

// Start launches the reader goroutine. Called once by NewTransport.
func (t *Transport) startReader() {
    go func() {
        for {
            line, err := t.r.ReadBytes('\n')
            if err != nil {
                t.failPendingAndClose(err)
                return
            }
            var resp RPCResponse
            if json.Unmarshal(line, &resp) != nil { continue }
            t.pendingMu.Lock()
            ch := t.pending[resp.ID]
            delete(t.pending, resp.ID)
            t.pendingMu.Unlock()
            if ch != nil {
                ch <- &resp
            }
        }
    }()
}

func (t *Transport) Call(ctx context.Context, method string, params any) (*RPCResponse, error) {
    id := t.nextID.Add(1)
    ch := make(chan *RPCResponse, 1)
    t.pendingMu.Lock()
    t.pending[id] = ch
    t.pendingMu.Unlock()
    defer func() {
        t.pendingMu.Lock()
        delete(t.pending, id)
        t.pendingMu.Unlock()
    }()

    t.writeMu.Lock()
    err := t.write(RPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
    t.writeMu.Unlock()
    if err != nil { return nil, err }

    select {
    case resp := <-ch:
        if resp.Error != nil { return resp, resp.Error }
        return resp, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}
```

**Add a gate to Track B.13:** "multi-in-flight RPC test — spawn 10 concurrent `command/execute` calls against a plugin that sleeps 100ms each and verify total elapsed time < 200ms (not 1s)."

This also interacts with the plan's serialization assumptions around `event/handle`: the plan's B.10 extends `EventHandleResult` with `Envelopes []EnvelopeOut`, implying envelopes flow back synchronously. With the concurrent transport, that's fine. With the current serialized transport, emitting envelopes from an event hook blocks every other RPC.

## Alternative approaches

- **Drop RPC proliferation.** Keep the current serialized transport and move HTTP route handling to a separate channel (e.g., a named pipe per plugin or a unix socket). Higher OS complexity, but isolates latency domains.
- **One transport per call category.** Spawn two transports on the same subprocess (one for commands, one for events). Needs two stdin/stdout pairs or a multiplexed protocol. Not idiomatic; more OS plumbing.

**Recommended:** the concurrent-transport rework. It's a few hundred lines, localized to `transport.go`, and removes the concurrency bottleneck permanently.

## References

- `internal/plugin/subprocess/transport.go:L17-139` — current serialized transport
- `internal/plugin/subprocess/plugin.go:L270-298` — event hook proxy using `Call`/`Notify`
- `internal/plugin/subprocess/plugin.go:L318-389` — CRUD proxy using `Call`
- `internal/plugin/subprocess/manager.go:L313-351` — health loop that will starve
- Plan §B.5, B.10 — new RPC methods going through the same transport
- JSON-RPC 2.0 spec §8: "The Server SHOULD NOT assume that batched Requests will be processed in any specific order." — the plan assumes ordering; the spec doesn't require it, so a concurrent model is compliant.
