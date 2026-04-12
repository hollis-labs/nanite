# [Medium] Error messages from tool execution leak internal state to LLM

**Scope:** internal/toolclient, internal/service
**Topic:** Error handling — information leakage
**Date:** 2026-04-11

## Problem

When tool execution fails, the error message is returned verbatim to the LLM via `fmt.Sprintf("Error: %v", err)`. These errors can contain internal server names, file paths, and infrastructure details.

## Evidence

`internal/service/tool.go:L192-L207`:

```go
func (s *toolServiceImpl) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	if s.toolClient != nil {
		result, err := s.toolClient.CallTool(ctx, agentID, toolName, input)
		if err != nil {
			return &ToolResult{Output: fmt.Sprintf("Error: %v", err), IsError: true}, nil
		}
		// ...
	}

	if s.mcpManager != nil {
		result, err := s.mcpManager.ExecuteTool(ctx, toolName, input)
		if err != nil {
			return &ToolResult{Output: fmt.Sprintf("Error: %v", err), IsError: true}, nil
		}
		// ...
	}
}
```

`internal/toolclient/broker.go:L152-L153`:

```go
if !tb.CheckPermission(agentID, toolName) {
    return "", fmt.Errorf("tool %q denied for agent %q", toolName, agentID)
}
```

The permission denial error reveals the agent ID and the exact tool name that was denied. This information is returned to the LLM.

`internal/mcp/manager.go:L274-L276`:

```go
err = fmt.Errorf("call tool %s on %s: %w", toolName, serverName, err)
```

The MCP manager wraps errors with the server name. If the underlying transport error includes file paths, connection strings, or stack traces, all of that propagates to the LLM output.

Example error strings that could reach the LLM:
- `"Error: tool \"mcp__cerberus__cerberus_build\" denied for agent \"restricted-readonly\""` — reveals agent configuration
- `"Error: call tool engine_task_create on engine: dial tcp 127.0.0.1:8091: connection refused"` — reveals internal service topology
- `"Error: unknown MCP server: hadron"` — reveals which servers are expected vs. available

## Impact

The LLM receives detailed internal error information that could be used for reconnaissance in a prompt injection scenario. An attacker who controls the LLM's behavior (via injected instructions in tool results, as flagged by the `mcp-client-transport` audit) could systematically probe which tools exist, which agents have access, and which servers are running by triggering intentional failures.

## Recommendation

Sanitize error messages before returning them to the LLM. Return a generic user-facing message while logging the detailed error server-side:

```go
if err != nil {
    log.Printf("service/tool: execute %s failed: %v", toolName, err)
    return &ToolResult{
        Output:  fmt.Sprintf("Tool call failed: %s", sanitizeToolError(err)),
        IsError: true,
    }, nil
}
```

Where `sanitizeToolError` strips server names, file paths, and connection details, returning only the tool name and a generic failure reason.

## References

- `internal/toolclient/broker.go:L152-L168` — error messages with tool names, agent IDs, server names
- `internal/mcp/manager.go:L266-L276` — error wrapping with server name and transport details
- `docs/audits/2026-04-10-mcp-client-transport/` — MCP tool results are untrusted; error messages are part of that surface
