# [Medium] Tool arguments are passed through to execution without any validation

**Scope:** internal/toolclient
**Topic:** Security — injection via tool arguments
**Date:** 2026-04-11

## Problem

The toolbroker passes LLM-generated tool arguments directly to the MCP transport layer without any validation, schema checking, or sanitization. The `CallTool()` method accepts `args map[string]any` and forwards it unchanged.

## Evidence

`internal/toolclient/broker.go:L150-L172`:

```go
func (tb *ToolClient) CallTool(ctx context.Context, agentID, toolName string, args map[string]any) (string, error) {
	// Check permissions.
	if !tb.CheckPermission(agentID, toolName) {
		return "", fmt.Errorf("tool %q denied for agent %q", toolName, agentID)
	}
	// ...
	return tb.MCPManager.ExecuteTool(ctx, execName, args)  // args forwarded as-is
}
```

`internal/mcp/manager.go:L273`:

```go
result, err := transport.CallTool(ctx, toolName, input)  // input forwarded as-is to transport
```

The chain is: LLM generates arguments -> `ToolService.Execute()` -> `ToolClient.CallTool()` -> `MCPManager.ExecuteTool()` -> transport `CallTool()`. At no point are the arguments validated against the tool's declared `InputSchema`. The schema is available in the tool definitions but never used for validation.

For external MCP servers (stdio/HTTP), this means LLM-crafted arguments are sent directly to the subprocess or HTTP endpoint. For built-in tools, the individual tool handlers perform their own validation (e.g., `dev_tools.go` checks for required fields), but there is no centralized defense.

## Impact

This is a defense-in-depth gap rather than a standalone vulnerability. The prior `dev-tools-input-validation` audit already found that individual tool handlers have their own validation weaknesses (e.g., `dev_bash` RCE). The toolbroker is the natural chokepoint where centralized argument validation could prevent classes of issues:

- Schema validation could reject arguments with unexpected fields, wrong types, or missing required fields before they reach the transport.
- Length limits on string arguments could prevent denial-of-service via oversized inputs.
- Pattern validation on known-dangerous fields (file paths, commands) could provide a second line of defense.

Without centralized validation, every tool handler must independently implement its own input validation, and any gap in a handler becomes exploitable.

## Recommendation

Add optional schema validation at the `CallTool()` level:

1. When the tool's `InputSchema` is available (it is, in the broker's tool registry), validate the `args` map against it before forwarding.
2. At minimum, validate that required fields are present and that field types match the schema.
3. Consider a configurable "strict mode" that rejects unexpected fields.

This is a defense-in-depth measure, not a replacement for handler-level validation. Both layers should exist.

## References

- `internal/toolclient/broker.go:L150-L172` — `CallTool()` passthrough
- `internal/mcp/manager.go:L246-L300` — `ExecuteTool()` passthrough
- `docs/audits/2026-04-10-dev-tools-input-validation/` — handler-level validation gaps in built-in tools
- `docs/audits/2026-04-10-mcp-client-transport/` — MCP transport layer also does not validate arguments
