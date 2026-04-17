# [High] Permission bypass via MCPManager fallback in ToolService.Execute

**Scope:** internal/toolclient, internal/service
**Topic:** Security — permission checking
**Date:** 2026-04-11

## Problem

`ToolService.Execute()` has a fallback path that routes tool calls directly through `MCPManager.ExecuteTool()` when `toolClient` is nil, completely bypassing the permission system in `permissions.go`.

## Evidence

`internal/service/tool.go:L192-L213`:

```go
func (s *toolServiceImpl) Execute(ctx context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	if s.toolClient != nil {
		result, err := s.toolClient.CallTool(ctx, agentID, toolName, input)
		// ...
	}

	if s.mcpManager != nil {
		result, err := s.mcpManager.ExecuteTool(ctx, toolName, input)
		// ... no permission check at all
	}
	// ...
}
```

When `toolClient` is nil (which `NewToolService` explicitly allows — `tool.go:L84` says "Both toolClient and mcpManager may be nil"), every tool call goes through the MCPManager path with zero permission enforcement. The `agentID` parameter is silently ignored.

Additionally, `internal/chat/orchestrator.go:L169,L190` calls `MCPManager.ExecuteTool()` directly, also bypassing permissions entirely. While this is a system-initiated call (not user-triggered), it still represents a call path that ignores permission configuration.

## Impact

If the system is misconfigured or encounters an initialization failure where `toolClient` is nil but `mcpManager` is present, all agent permission restrictions (allow lists, deny lists) become inert. An agent configured with `deny_list: ["mcp__hadron__hadron_bp_*"]` would have full access to those tools via the fallback path.

The `orchestrator.go` direct calls are lower risk because they use hardcoded tool names for system operations, but they establish a pattern where developers reach for `MCPManager.ExecuteTool()` directly rather than routing through the permission-checked `ToolClient.CallTool()`.

## Recommendation

Add a permission check to the MCPManager fallback path in `Execute()`:

```go
if s.mcpManager != nil {
    // Still enforce permissions even without the full ToolClient.
    perms := toolclient.ParsePermissions(/* load from store if available */)
    if !perms.CheckPermission(toolName) {
        return &ToolResult{Output: fmt.Sprintf("Error: tool %q denied for agent %q", toolName, agentID), IsError: true}, nil
    }
    result, err := s.mcpManager.ExecuteTool(ctx, toolName, input)
    // ...
}
```

Alternatively, make `toolClient` required (non-nil) in `NewToolService` and remove the fallback path entirely.

## References

- `internal/toolclient/broker.go:L150-L172` — the permission-checked path in `CallTool()`
- `internal/toolclient/permissions.go:L127-L146` — `CheckPermission()` logic
- `internal/chat/orchestrator.go:L169,L190` — direct `ExecuteTool` calls bypassing permissions
- `docs/audits/2026-04-10-mcp-client-transport/index.md` — noted `internal/toolclient/broker.go` as a one-hop consumer; did not audit the service-layer fallback path
