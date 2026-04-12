# [Medium] Tool name collision: first-match-wins in ResolveToolServer is non-deterministic

**Scope:** internal/toolclient, internal/mcp
**Topic:** Security — tool selection logic
**Date:** 2026-04-11

## Problem

When an unprefixed tool name is passed to `ToolClient.CallTool()`, it is resolved to a server via `MCPManager.ResolveToolServer()`, which iterates the `m.tools` slice and returns the first match. If two servers register a tool with the same name, the winner depends on map iteration order during `DiscoverTools()`.

## Evidence

`internal/mcp/manager.go:L305-L313`:

```go
func (m *Manager) ResolveToolServer(toolName string) (serverName, prefixedName string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, entry := range m.tools {
		if entry.tool.Name == toolName {
			return entry.serverName, fmt.Sprintf("mcp__%s__%s", entry.serverName, toolName)
		}
	}
	return "", ""
}
```

`internal/mcp/manager.go:L126`:

```go
for name, transport := range m.servers {  // map iteration — non-deterministic order
	tools, err := transport.ListTools(ctx)
	// ...
	for _, t := range tools {
		m.tools = append(m.tools, toolEntry{serverName: name, tool: t})
	}
}
```

The `m.servers` map iteration order is non-deterministic in Go. If two MCP servers register a tool named `task_create`, the `DiscoverTools()` loop appends them in arbitrary order. `ResolveToolServer()` then returns whichever entry landed first in the slice. This means the same unprefixed tool name could route to different servers across restarts.

`internal/toolclient/broker.go:L162-L169`:

```go
if !strings.HasPrefix(toolName, "mcp__") {
	if server, prefixed := tb.MCPManager.ResolveToolServer(toolName); server != "" {
		execName = prefixed
	} else {
		return "", fmt.Errorf("tool %q not found in any registered server", toolName)
	}
}
```

## Impact

Non-deterministic routing of unprefixed tool calls. If a malicious or compromised MCP server registers a tool with the same name as a trusted server's tool, the call may be routed to the attacker's server on some restarts. The prefixed naming convention (`mcp__server__tool`) prevents this for normal LLM-initiated calls, but unprefixed calls from internal code paths (like `orchestrator.go` or `mcpserver/server.go`) or custom integrations are vulnerable.

The practical risk is moderate because most call sites already use prefixed names, and name collisions between configured MCP servers are unlikely in current deployments.

## Recommendation

1. Log a warning during `DiscoverTools()` when a tool name collision is detected across servers.
2. Consider sorting `m.servers` keys before iterating to make the order deterministic (priority-based or alphabetical).
3. Document that unprefixed tool names are ambiguous and should only be used when the caller knows there is exactly one provider.

```go
// In DiscoverTools(), after appending:
seen := make(map[string]string) // tool name -> first server
for _, entry := range m.tools {
    if prev, ok := seen[entry.tool.Name]; ok {
        log.Printf("mcp: WARNING tool name collision: %q registered by both %q and %q", 
            entry.tool.Name, prev, entry.serverName)
    } else {
        seen[entry.tool.Name] = entry.serverName
    }
}
```

## References

- `internal/toolclient/broker.go:L162-L169` — unprefixed name resolution path
- `internal/mcp/manager.go:L116-L155` — `DiscoverTools()` map iteration
- `internal/mcp/manager.go:L305-L313` — `ResolveToolServer()` first-match behavior
