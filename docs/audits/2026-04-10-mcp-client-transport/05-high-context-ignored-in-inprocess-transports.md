# [High] In-process MCP transports systematically ignore the ctx parameter, breaking cancellation across SelfToolsTransport, DevToolsTransport, GeneralToolsTransport, and CodeExecTransport

**Scope:** MCP client transport — context propagation in built-in transports
**Topic:** Concurrency / Idiomatic Go
**Date:** 2026-04-10

## Problem

The `MCPTransport` interface at `internal/mcp/manager.go:L20-L23` declares:

```go
type MCPTransport interface {
    ListTools(ctx context.Context) ([]Tool, error)
    CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error)
}
```

Four of the five in-process transports that back this interface discard the supplied `ctx` by binding it to `_` and then dispatch handlers that either use `context.Background()` or accept no context at all. The only in-process transport that honors the interface is `MemoryToolsTransport`. This means that when a chat turn is cancelled — via user Ctrl-C, session takeover, or session shutdown — the in-flight tool call continues to completion. The cancellation never reaches the store, A2A service, Giphy HTTP call, cross-app IPC, or any downstream blocking work.

## Evidence

`internal/mcp/self_tools_transport.go:L66`:

```go
func (st *SelfToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
```

Every dispatch inside that function either ignores context entirely or calls `context.Background()` directly. Examples at `internal/mcp/self_tools_transport.go`:

- L369, L385, L463 — `ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)` — the parent context is never threaded in
- L968 — `st.A2A.SendMessage(context.Background(), msg)`
- L980 — `context.Background()` passed to A2A
- L1002 — `st.A2A.Thread(context.Background(), ...)`
- L1021, L1036, L1054, L1076, L1092, L1103 — `context.Background()` to A2A methods

That is 11 downstream call sites where cancellation from the chat turn is dropped on the floor.

The other in-process transports follow the same anti-pattern:

- `internal/mcp/general_tools.go:L148` — `func (g *GeneralToolsTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error)` — note the parallel audit `dev-tools-input-validation` covers the handler contents, but the context binding itself is a transport-contract concern.
- `internal/mcp/dev_tools.go:L141` — same pattern.
- `internal/mcp/code_exec_tools.go:L72` — same pattern.

Only `internal/mcp/memory_tools.go:L34` honors the contract:

```go
func (mt *MemoryToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
```

And `internal/mcp/manager.go:L273` passes the caller's context through to the transport:

```go
result, err := transport.CallTool(ctx, toolName, input)
```

So the caller is doing the right thing. The transports just throw it away.

## Impact

**Cancellation gap.** When a user Ctrl-Cs a chat turn, `chat.generateResponse` cancels its context. That cancellation propagates through `toolclient.ToolClient.CallTool` to `Manager.ExecuteTool` to `transport.CallTool` — and dies there. The running tool call keeps executing:

- An in-flight `nanite_a2a_send` keeps trying to persist to the store.
- An in-flight `nanite_show_giphy` keeps its 5 s HTTP call open to api.giphy.com even after the user cancelled.
- An in-flight `nanite_refresh_engine` keeps its cross-app IPC call going.
- An in-flight Code Exec call continues running code in its subprocess, ignoring the parent cancel (exacerbates sandbox audit findings).

For a user who hits Ctrl-C, the UI says "cancelled," but the backend keeps working on the previous request. Subsequent requests race against the ghost of the cancelled one. Store writes from the "cancelled" tool call land after the new turn has started.

**Goroutine leak class.** A session that ends while a tool call is in flight can't tear down cleanly. The running call keeps the store, A2A service, and any HTTP clients busy past session lifetime. Under stress (a test that spins up and tears down sessions quickly), this manifests as flaky shutdown and `t.TempDir()` cleanup errors.

**Interface-contract regression surface.** The `MCPTransport` interface explicitly requires a context. A future reviewer seeing the signature reasonably assumes cancellation works. The `_ context.Context` binding makes the drop invisible at every call site — a grep for `context.Background()` inside handlers won't catch the transport-level drops. The compiler accepts it.

**Cross-audit note.** This is the transport-contract side of the dev-tools-input-validation audit's scope. Any fix there should include threading the context through rather than just validating args.

## Recommendation

1. **Rename the parameter.** Change `_ context.Context` to `ctx context.Context` in the four transports. This alone catches downstream `context.Background()` calls during review: once `ctx` is in scope, reviewers will notice when they're about to pass `context.Background()` instead.

2. **Thread the context to every downstream call.** In `SelfToolsTransport.CallTool`, replace every `context.Background()` with the supplied `ctx`. Do the same in the other transports. Example:

   ```go
   func (st *SelfToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
       switch name {
       case "nanite_a2a_send":
           return st.callA2ASend(ctx, args)   // take ctx as first param
       // ...
       }
   }
   ```

3. **For in-transport HTTP calls (Giphy, crossapp),** propagate the caller's context to `context.WithTimeout`:

   ```go
   callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
   defer cancel()
   req, err := http.NewRequestWithContext(callCtx, ...)
   ```

4. **Linter rule.** Add `contextcheck` to the project's `golangci-lint` config. It catches exactly this class: functions that take a `context.Context` but pass `context.Background()` to an inner call. Defer the enforcement pass to the `mcp-client-tooling-and-tests` scope if you don't want to fail the current lint run.

The change is mechanical but touches 15+ call sites. Worth doing as a single refactor with a focused test: a fake A2A service that returns `ctx.Err()` on `<-ctx.Done()`, driven by a top-level context the test cancels before the handler finishes.

## References

- `internal/mcp/manager.go:L20-L23` — `MCPTransport` interface contract
- `internal/mcp/manager.go:L273` — caller correctly passes ctx
- `internal/mcp/self_tools_transport.go:L66` — drop
- `internal/mcp/self_tools_transport.go:L369`, `L385`, `L463`, `L968`, `L980`, `L1002`, `L1021`, `L1036`, `L1054`, `L1076`, `L1092`, `L1103` — `context.Background()` call sites
- `internal/mcp/general_tools.go:L148`, `internal/mcp/dev_tools.go:L141`, `internal/mcp/code_exec_tools.go:L72` — same anti-pattern in parallel transports
- `internal/mcp/memory_tools.go:L34` — the one transport that does it right
- Related: the parallel `dev-tools-input-validation` audit covers handler-level arg validation; this finding covers the transport-level contract
