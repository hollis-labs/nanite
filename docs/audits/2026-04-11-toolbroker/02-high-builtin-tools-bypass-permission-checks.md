# [High] Built-in tools bypass permission checks entirely

**Scope:** internal/toolclient
**Topic:** Security — permission checking
**Date:** 2026-04-11

## Problem

Built-in tools (registered via `BuiltinToolRegistry`) are always prepended to the tool list in `SelectToolsAsProvider()` without any permission filtering. The permission check only applies to broker-selected MCP tools.

## Evidence

`internal/toolclient/broker.go:L112-L144`:

```go
func (tb *ToolClient) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string) ([]provider.ToolDefinition, error) {
	tools, err := tb.SelectTools(ctx, intent, hints, workspaceID, agentID)
	// ...

	// Start with built-in tools — always available regardless of MCP status.
	var defs []provider.ToolDefinition
	if tb.Builtins != nil {
		builtins := tb.Builtins.GetBuiltins()
		defs = make([]provider.ToolDefinition, 0, len(builtins)+len(tools))
		defs = append(defs, builtins...)  // <-- NO permission check
	}

	// Append broker-selected MCP tools, filtered by agent permissions.
	for _, t := range tools {
		// ...
		if !tb.CheckPermission(agentID, name) {  // <-- only MCP tools checked
			continue
		}
		// ...
	}
	return defs, nil
}
```

Built-in tools include `dev_bash`, `dev_read`, `dev_write`, `dev_edit`, `dev_grep`, `dev_glob`, `web_fetch`, and others (registered in `cmd/nanite/main.go`). These are security-sensitive tools — `dev_bash` in particular provides shell command execution (the prior `dev-tools-input-validation` audit found RCE via LLM-controlled commands in this tool).

An agent configured with `deny_list: ["dev_bash"]` or `allow_list: ["mcp__engine__*"]` would still receive `dev_bash` in its tool set because built-in tools skip the permission check loop entirely.

## Impact

Permission restrictions configured on agents are ineffective for the most dangerous tools in the system. The deny list and allow list in `ToolPermissions` cannot restrict access to built-in tools like `dev_bash`, `dev_write`, or `web_fetch`. This is a privilege escalation: an agent intended to be read-only (e.g., `allow_list: ["mcp__engine__engine_task_get", "mcp__engine__engine_tasks_list"]`) still receives shell execution capability.

Note: the `CallTool()` path (broker.go:L150) does check permissions for all tool names including unprefixed ones, so execution-time permission checks would catch calls. However, the LLM is presented with the tool definitions and will attempt to call them, and the permission error surfaces as a runtime failure rather than preventing the tool from being offered.

## Recommendation

Filter built-in tools through the same permission check before prepending:

```go
if tb.Builtins != nil {
    for _, bt := range tb.Builtins.GetBuiltins() {
        if tb.CheckPermission(agentID, bt.Name) {
            defs = append(defs, bt)
        }
    }
}
```

This ensures the LLM never sees tools the agent is denied, preventing unnecessary call attempts and error churn.

## References

- `internal/toolclient/broker.go:L150-L153` — `CallTool()` does check permissions for all tools (execution-time safety net)
- `internal/toolclient/builtin.go:L26-L29` — `RegisterBuiltins()` stores tools by category
- `docs/audits/2026-04-10-dev-tools-input-validation/` — prior audit flagging `dev_bash` RCE risk
- `internal/toolclient/permissions.go:L127-L146` — `CheckPermission()` logic
