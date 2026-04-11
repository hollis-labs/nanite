# [High] No panic recovery anywhere in MCP transport dispatch — a single panic in any handler crashes the host

**Scope:** MCP client transport — dispatch robustness
**Topic:** Error handling / Security
**Date:** 2026-04-10

## Problem

A ripgrep for `recover()` across `internal/mcp/` returns zero matches. Not one handler, dispatcher, or transport in the MCP client wraps its work in a deferred `recover()`. Any panic inside a tool handler — a nil map dereference on `args`, a failed type assertion, a nil receiver on a post-construction-injected service (`SelfToolsTransport.A2A`, `TodoStore`), a panic in a dependency — propagates up through the goroutine that invoked `ExecuteTool` and crashes the whole chat engine.

This is the same class as chat engine audit finding 02 (`docs/audits/2026-04-10-chat-engine/` — no panic recovery in `generateResponse`). The chat engine finding was that a plugin filter panic crashes the server; this finding is that an MCP tool handler panic does the same thing, via a different call path.

## Evidence

No matches for `recover(` in `internal/mcp/`:

```bash
$ rg 'recover\(\)' internal/mcp/
(no results)
```

`SelfToolsTransport.CallTool` at `internal/mcp/self_tools_transport.go:L66-L138` is a switch with 35 cases. Each case dispatches to a handler that manipulates `args map[string]any` without guard checks. Examples of panic-prone code:

`internal/mcp/self_tools_transport.go:L1092`:

```go
if err := st.A2A.ApproveHandoff(context.Background(), strArg(args, "handoff_id", "")); err != nil {
```

`st.A2A` is documented as "nil-safe; set after construction from the container's A2A service" — but the dispatch code does not check for nil. If A2A wasn't injected (e.g., a test harness or a configuration path that doesn't wire it), this call dereferences a nil pointer → panic → crash.

Similarly at `internal/mcp/self_tools_transport.go:L968`:

```go
out, err := st.A2A.SendMessage(context.Background(), msg)
```

The same nil risk applies to `st.TodoStore`:

```go
// Field declared at L46:
TodoStore TodoStoreInterface // nil-safe; set after construction
// But callTodoCreate at L???-??? calls st.TodoStore.CreateTodo(...) with no nil check.
```

And `st.BuilderSessions` / `st.BuilderRegistry` — not checked.

In `internal/mcp/general_tools.go`, `internal/mcp/dev_tools.go`, and `internal/mcp/code_exec_tools.go` the pattern is similar: handlers do type assertions on `args["foo"]` without the `, ok` comma-ok form, and call downstream services without recover.

`Manager.ExecuteTool` at `internal/mcp/manager.go:L246-L301` also has no `defer recover()`. A panic in `transport.CallTool` propagates up through `Manager.ExecuteTool` to `toolclient.ToolClient.CallTool` to `service.tool.Execute` to the chat engine to the HTTP handler. The only `recover()` in the chain is the server-level middleware (if present) — which means the HTTP request dies, and worse, if the panic is inside a goroutine the server middleware can't catch it.

## Impact

**Single-request crash.** A tool call with `args["id"]` missing — an LLM generating a malformed tool call, which is not hypothetical under prompt injection — triggers a nil type assertion in a handler. The goroutine panics. The chat session returns 500. The whole nanite process stays up only because `net/http` recovers per-connection — but if any MCP dispatch runs inside a background goroutine (e.g., `AutoDiscover` runs in a goroutine after plugin install in `internal/api/mcp_servers.go:L63`), that goroutine's panic is unrecovered and crashes the whole process.

**Compromised-server crash.** A malicious MCP server that returns a `ToolResult.Content` with a crafted `Text` field can't directly trigger a panic here because the transport layer is JSON-safe. But a malicious server that causes a panic in downstream code (e.g., by returning data that makes `strings.Builder` overflow, or by triggering an infinite recursion in a markdown parser reading the result) has no safety net in the MCP layer.

**Prompt injection as a DoS vector.** An attacker who gets an LLM to emit `{"tool_name": "nanite_a2a_send", "args": {"bad": "data that triggers a nil deref in the handler"}}` causes a panic. With no recovery, every panic is a crash. The beta's "first hour of use" is exactly when this will surface: a developer friend runs a chat that happens to include a malformed tool call, and nanite dies.

**Reliability gap.** Per the reviewer-backend context, the plugin audit already flagged this class for plugin event hooks (`04-high-event-hook-panic-crashes-host.md`). The MCP dispatch path is a different code path with the same problem. Both need recovers.

## Recommendation

Add a single `defer recover()` at the dispatch boundary. The right place is `Manager.ExecuteTool`:

```go
func (m *Manager) ExecuteTool(ctx context.Context, name string, input map[string]any) (result string, err error) {
    defer func() {
        if r := recover(); r != nil {
            stack := debug.Stack()
            log.Printf("mcp: panic in ExecuteTool %s: %v\n%s", name, r, stack)
            err = fmt.Errorf("internal error in tool %s: %v", name, r)
            // span.RecordError + span.SetStatus if span still in scope
        }
    }()
    // ... existing body ...
}
```

This:
1. Converts panic to an error the chat engine can handle.
2. Logs the panic with a stack trace for debugging.
3. Keeps the process alive. The LLM sees a tool error and can retry or continue.

Complementary changes:

- **Inside each in-process handler**, fix the nil-service-pointer assumption. Example for A2A:
  ```go
  func (st *SelfToolsTransport) callA2ASend(args map[string]any) (*ToolResult, error) {
      if st.A2A == nil {
          return errorResult("A2A service not configured"), nil
      }
      // ...
  }
  ```
  This is the correct "nil-safe" pattern the field comment promises but the code doesn't implement.

- **Argument-access pattern.** Replace `args["foo"].(string)` with the comma-ok form:
  ```go
  name, ok := args["name"].(string)
  if !ok || name == "" {
      return errorResult("name required"), nil
  }
  ```
  Several handlers already do this (`callCreateSkill` at L143 uses `_` on the ok — same crash risk if the field is missing and the caller forgot to handle empty string). Harmonize.

- **AutoDiscover panic surface.** `internal/api/mcp_servers.go:L63` calls `a.Services.MCP.AutoDiscover(context.Background(), ...)` from an HTTP handler synchronously; a panic there crashes the request. If AutoDiscover ever moves to a background goroutine (noted in plan), it needs its own recover. Note as a related concern for the `api-privilege-boundary` scope.

- **Plugin audit alignment.** The plugin audit recommended a recover in `Host.EmitEvent`. Ensure the MCP recover uses the same shape so they're consistent.

## References

- `internal/mcp/manager.go:L246-L301` — `ExecuteTool` with no recover
- `internal/mcp/self_tools_transport.go:L42-L49` — fields documented nil-safe but code doesn't check
- `internal/mcp/self_tools_transport.go:L968`, `L1092` — nil A2A dereference sites
- Chat engine audit finding 02 — `docs/audits/2026-04-10-chat-engine/` panic recovery gap
- Plugin audit finding 04 — `docs/audits/2026-04-10-plugin-system-plan-eval/04-high-event-hook-panic-crashes-host.md`
