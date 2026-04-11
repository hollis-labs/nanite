# [Medium] Manager.ListServers always reports Connected=true and Manager has no concurrency limit

**Scope:** MCP client transport — Manager observability and bounds
**Topic:** State / Concurrency
**Date:** 2026-04-10

## Problem

Two adjacent gaps in `Manager`:

1. `Manager.ListServers` at `internal/mcp/manager.go:L331-L350` hardcodes `Connected: true` for every registered server, regardless of the actual transport state. The code comment admits it: "`// registered means connected`". The UI and any API consumer reading this cannot distinguish a healthy server from a dead one.

2. `Manager` has no bound on the number of concurrent `ExecuteTool` calls in flight. With no per-server queue limit and no global limit, a chat session that spawns many tool calls (e.g., via the delegation / orchestrator pattern) can saturate goroutines on the host side even if individual tool calls are fast. When combined with finding 03's serialization, the result is a queue of goroutines waiting on one mutex per server.

Both are real but contained issues. Each is not a release-blocker on its own; together they're worth flagging as a Manager-layer gap.

## Evidence

`internal/mcp/manager.go:L331-L350`:

```go
func (m *Manager) ListServers() []ServerInfo {
    m.mu.RLock()
    defer m.mu.RUnlock()

    toolCounts := make(map[string]int)
    for _, entry := range m.tools {
        toolCounts[entry.serverName]++
    }

    infos := make([]ServerInfo, 0, len(m.servers))
    for name := range m.servers {
        infos = append(infos, ServerInfo{
            Name:      name,
            ToolCount: toolCounts[name],
            Connected: true, // registered means connected
        })
    }
    return infos
}
```

`Manager` struct at `manager.go:L33-L39`:

```go
type Manager struct {
    servers     map[string]MCPTransport
    tools       []toolEntry
    Broker      *broker.LocalBroker
    LoadChecker ToolLoadChecker
    mu          sync.RWMutex
}
```

No semaphore, no per-server concurrency channel, no rate limiter.

`Manager.ExecuteTool` at `manager.go:L246-L301` does:

```go
m.mu.RLock()
transport, ok := m.servers[serverName]
m.mu.RUnlock()
// ...
result, err := transport.CallTool(ctx, toolName, input)
```

There is no guard against N concurrent callers spawning N goroutines that each block at the transport's `t.mu.Lock()`. Combined with finding 01's subprocess leak on timeout, a misbehaving server can accumulate blocked callers quickly.

## Impact

**Observability.** The "Connected: true" lie is a UX gap. The UI's MCP server panel will show all servers as healthy even after half of them have stopped responding. This feeds directly into finding 04 (no crash detection) — there is no way to surface the crash to the user.

**Bound gap.** Without a per-server concurrency limit:
- A sub-agent orchestrator that issues 50 tool calls to the same server spawns 50 goroutines, 49 of which block on `t.mu.Lock()`.
- If the server has finding 01's timeout-leak bug, each cancelled goroutine also leaks a subprocess. 50 concurrent cancels = 50 leaked subprocesses.
- The goroutine count per chat session becomes proportional to pending tool calls, not a bounded constant.

Per the reviewer-backend context's release framing, this is a "first hour of use" problem only if a developer friend's orchestrator chain stresses the same server. It's not guaranteed to hit. But the pieces all point the same direction: lack of bounds compounds the other findings.

## Recommendation

1. **Real connectivity in `ListServers`.** Give `MCPTransport` an optional `State() TransportState` method:

   ```go
   type TransportState int
   const (
       StateUnknown TransportState = iota
       StateRunning
       StateCrashed
       StateStopped
   )

   type StateProbe interface {
       State() TransportState
   }
   ```

   `Manager.ListServers` type-asserts each transport to `StateProbe` and reports the real state, falling back to `StateUnknown` for transports that don't implement it. Pair with finding 04's supervisor goroutine to actually populate the state.

2. **Per-server concurrency limit.** Give each transport an optional `MaxConcurrent int` setting that bounds in-flight requests. Use a buffered channel as a semaphore:

   ```go
   sem := make(chan struct{}, maxConcurrent)

   func (m *Manager) ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error) {
       select {
       case sem <- struct{}{}:
           defer func() { <-sem }()
       case <-ctx.Done():
           return "", ctx.Err()
       }
       // ... existing body ...
   }
   ```

   A default of 8 is conservative and matches typical MCP server throughput.

3. **Global call cap** (optional). A Manager-level limit on total concurrent tool calls across all servers protects against an orchestrator spawning N^2 goroutines. This interacts with plan design; defer until the concurrent-transport rework lands.

## References

- `internal/mcp/manager.go:L331-L350` — hardcoded Connected=true
- `internal/mcp/manager.go:L246-L301` — `ExecuteTool` without concurrency bound
- Related: finding 01, 03, 04 — this finding is what closes the loop on actually observing and bounding the bugs those findings describe
