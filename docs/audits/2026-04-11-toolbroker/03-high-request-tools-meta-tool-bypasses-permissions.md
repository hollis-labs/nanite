# [High] request_tools meta-tool returns tool schemas without permission filtering

**Scope:** internal/toolclient
**Topic:** Security — progressive discovery information leak
**Date:** 2026-04-11

## Problem

The `HandleRequestTools()` method returns full tool definitions (name, description, and input schema) for any tool the LLM requests by name or intent, without checking whether the requesting agent has permission to use those tools.

## Evidence

`internal/toolclient/meta_tools.go:L42-L110`:

```go
func (tb *ToolClient) HandleRequestTools(input map[string]any) ([]provider.ToolDefinition, string) {
	var byName []provider.ToolDefinition
	var byIntent []provider.ToolDefinition

	// Resolve by explicit tool names.
	if names, ok := input["tool_names"]; ok {
		// ... resolves without permission check
		byName = tb.GetToolsByNames(requested)
	}

	// Resolve by intent.
	if intentStr, ok := input["intent"]; ok {
		// ... resolves without permission check
		byIntent = tb.SelectByIntent(s, DefaultMaxIntentResults)
	}

	// Merge and return — no agentID parameter, no permission filtering
	// ...
}
```

The caller in `internal/service/tool.go:L216-L221` also passes no agent context:

```go
func (s *toolServiceImpl) HandleRequestTools(_ context.Context, input map[string]any) ([]provider.ToolDefinition, string, error) {
	if s.toolClient == nil {
		return nil, "No tool client configured.", fmt.Errorf("no tool client configured")
	}
	tools, summary := s.toolClient.HandleRequestTools(input)
	return tools, summary, nil
}
```

No `agentID` is threaded through to `HandleRequestTools`. The method signature does not accept one.

Additionally, `ListToolSummaries()` (broker.go:L203) and the tool catalog built via `BuildToolCatalog()` in the progressive discovery path (service/tool.go:L146-L147) expose the full tool catalog (names and descriptions) to any agent, regardless of permissions. An agent with `allow_list: ["mcp__engine__*"]` still sees every Hadron, Cerberus, and Conduit tool name and description in the catalog.

## Impact

Progressive discovery leaks information about tools an agent should not know about. An agent restricted to Engine-only tools will see Hadron automation blueprints, Cerberus service management tools, and Conduit context tools in the catalog. The LLM can then request full schemas for those tools via `request_tools`, receive them, and attempt to call them. While `CallTool()` will deny execution, the information leak reveals the system's full capability surface to restricted agents.

In a multi-tenant or security-sensitive deployment, this leaks internal infrastructure information (server names, tool capabilities, API shapes) to agents that should be scoped to a narrow tool set.

## Recommendation

1. Add `agentID` as a parameter to `HandleRequestTools()` and filter results through `CheckPermission()` before returning.
2. Filter `ListToolSummaries()` by agent permissions when building the progressive discovery catalog.
3. In `SelectForAgent()`, filter the catalog string to only include tools the agent is permitted to use.

```go
func (tb *ToolClient) HandleRequestTools(agentID string, input map[string]any) ([]provider.ToolDefinition, string) {
    // ... existing resolution logic ...
    
    // Filter by permissions before returning.
    var permitted []provider.ToolDefinition
    for _, t := range merged {
        if tb.CheckPermission(agentID, t.Name) {
            permitted = append(permitted, t)
        }
    }
    // ... build summary from permitted, not merged ...
}
```

## References

- `internal/toolclient/broker.go:L203-L213` — `ListToolSummaries()` returns unfiltered catalog
- `internal/service/tool.go:L146-L147` — progressive discovery catalog built from unfiltered summaries
- `internal/toolclient/intent.go:L19-L62` — `SelectByIntent()` also unfiltered
- `internal/toolclient/permissions.go:L127-L146` — `CheckPermission()` available but not called
