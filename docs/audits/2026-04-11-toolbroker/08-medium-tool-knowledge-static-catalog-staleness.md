# [Medium] Tool knowledge catalog is hardcoded and will drift from actual tool availability

**Scope:** internal/toolclient
**Topic:** Memory & resources — staleness and cache invalidation
**Date:** 2026-04-11

## Problem

`DefaultToolKnowledge()` returns a hardcoded catalog of tool entries. This catalog is built at compile time and has no mechanism to sync with the actual tools discovered by the MCP manager at runtime.

## Evidence

`internal/toolclient/tool_knowledge.go:L119-L495`:

```go
func DefaultToolKnowledge() *ToolKnowledge {
	return &ToolKnowledge{
		Categories: map[string][]ToolEntry{
			"project-management": {
				{Name: "engine_task_create", ...},
				// ... 10 more hardcoded entries
			},
			"automation": {
				{Name: "hadron_run_enqueue", ...},
				// ... 8 more hardcoded entries
			},
			// ... 3 more categories, ~50 total entries
		},
	}
}
```

The catalog references specific tool names (`engine_task_create`, `hadron_run_enqueue`, `cerberus_start`, etc.) and server names (`engine`, `hadron`, `conduit`, `cerberus`, `self`). If:

1. A tool is renamed in an MCP server, the knowledge catalog still references the old name.
2. A new MCP server is added, its tools do not appear in the catalog.
3. A server is removed, the catalog still advertises its tools.
4. Tool descriptions change, the catalog shows stale descriptions.

The `ForIntent()` method searches this static catalog, not the live tool registry. The `Summary()` method renders this catalog for progressive discovery prompts. Both will present stale information.

## Impact

Progressive discovery quality degrades over time as the live tool set drifts from the hardcoded catalog. The LLM receives stale tool descriptions and may request tools that no longer exist or miss tools that were recently added. This is a correctness issue, not a security issue, but it affects the reliability of intent-based tool selection.

The catalog also has no size bound — while currently ~50 entries, there is no mechanism to prevent unbounded growth if entries are added without removing obsolete ones.

## Recommendation

Generate the tool knowledge catalog from the live MCP tool registry at startup or on tool discovery, rather than hardcoding it:

```go
func BuildToolKnowledge(allTools []provider.ToolDefinition) *ToolKnowledge {
    tk := &ToolKnowledge{Categories: make(map[string][]ToolEntry)}
    for _, t := range allTools {
        cat := inferCategory(t.Name) // from prefix: engine_ -> project-management, hadron_ -> automation, etc.
        tk.Categories[cat] = append(tk.Categories[cat], ToolEntry{
            Name:             t.Name,
            ShortDescription: truncate(t.Description, 80),
            Server:           inferServer(t.Name),
        })
    }
    return tk
}
```

Use `DefaultToolKnowledge()` as a fallback for enrichment data (use cases, category assignments) that can't be inferred from the tool definition alone.

## References

- `internal/toolclient/tool_knowledge.go:L26-L47` — `ForIntent()` searches static catalog
- `internal/toolclient/tool_knowledge.go:L67-L105` — `Summary()` renders static catalog
- `internal/toolclient/tool_knowledge.go:L119-L495` — hardcoded `DefaultToolKnowledge()`
- `internal/mcp/manager.go:L116-L155` — `DiscoverTools()` builds the live tool registry
