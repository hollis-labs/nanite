# [Critical] GetService("mcp") grants full MCP Manager access — tool execution, server lifecycle

**Scope:** Plugin capability model
**Topic:** Security — privilege escalation
**Date:** 2026-04-11

## Problem

Any plugin calling `host.GetService("mcp")` receives the full `*mcp.Manager`, which exposes `ExecuteTool`, `CallTool`, `AddServer`, `RemoveServer`, `ListServers`, `ListTools`, and transport lifecycle methods. A plugin can execute any registered tool (including dev tools like `write`, `edit`, `bash`), add rogue MCP servers, or remove legitimate ones.

## Evidence

**support-ticket plugin** — `plugins/support-ticket/plugin.go:L104-110`:

```go
mcpSvc, mcpErr := host.GetService("mcp")
if mcpErr == nil {
    if mgr, ok := mcpSvc.(*mcp.Manager); ok {
        mgr.AddServer("support-kb", kbTransport)
    }
}
```

**fragments-engine plugin** — `plugins/fragments-engine/plugin.go:L43-47`:

```go
if svc, err := host.GetService("mcp"); err == nil {
    if mgr, ok := svc.(*mcp.Manager); ok {
        p.mcpManager = mgr
    }
}
```

The fragments-engine plugin then calls `p.mcpManager.ExecuteTool()` at `plugin.go:L178` to proxy Engine MCP calls.

**Service registration** — `cmd/nanite/main.go:176`:

```go
pluginHost.RegisterService("mcp", mcpManager)
```

## Impact

A malicious plugin with MCP Manager access can:

1. **Execute `nanite_code_execute`** — run arbitrary shell commands on the host machine, bypassing any sandbox because it calls through the internal tool path.
2. **Execute `nanite_file_write` / `nanite_file_edit`** — write to any file the process can access.
3. **Execute `nanite_web_fetch`** — make outbound HTTP requests (SSRF from a plugin).
4. **Add a rogue MCP server** that returns malicious tool results, poisoning the tool broker for all sessions.
5. **Remove legitimate MCP servers** — denial of service for tool execution.
6. **List all tools** — discover the full capability surface of the system.

Combined with finding 01 (full DB access), a malicious plugin has complete control of the system.

## Recommendation

1. Do not register the raw MCP Manager as a service. Instead, provide a scoped proxy:
   - `PluginMCPClient` that only allows `AddServer` (for the plugin's own servers) and a restricted `ExecuteTool` (only for tools the plugin declared in its manifest).
   - Reject `ExecuteTool` calls for tools not in the plugin's declared dependency list.
2. For tool execution, route through the permission system (`toolclient/permissions.go`) rather than the raw MCP Manager.

## References

- `cmd/nanite/main.go:L176` — registration
- `internal/mcp/manager.go` — full Manager API
- Prior audit: `toolbroker` finding 01 (permission bypass via MCPManager.CallTool fallback)
