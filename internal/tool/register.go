package tool

import (
	"github.com/hollis-labs/go-providers/provider"
)

// devToolNames are the known dev (core-io + search) tools.
var devToolCategories = map[string]string{
	"dev_read":  CategoryCoreIO,
	"dev_write": CategoryCoreIO,
	"dev_edit":  CategoryCoreIO,
	"dev_bash":  CategoryCoreIO,
	"dev_grep":  CategorySearch,
	"dev_glob":  CategorySearch,
}

// generalToolCategories maps general utility tools to categories.
var generalToolCategories = map[string]string{
	"web_fetch":     CategorySearch,
	"web_search":    CategorySearch,
	"json_parse":    CategorySearch,
	"datetime":      CategorySearch,
	"base64_encode": CategorySearch,
	"base64_decode": CategorySearch,
	"url_encode":    CategorySearch,
	"url_decode":    CategorySearch,
	"hash":          CategorySearch,
	"math_eval":     CategorySearch,
}

// selfToolCategories maps self-service tools to categories.
var selfToolCategories = map[string]string{
	"nanite_create_skill":    CategorySession,
	"nanite_list_skills":     CategorySession,
	"nanite_update_skill":    CategorySession,
	"nanite_delete_skill":    CategorySession,
	"nanite_create_agent":    CategoryAgent,
	"nanite_list_agents":     CategoryAgent,
	"nanite_update_agent":    CategoryAgent,
	"nanite_navigate_engine": CategoryAgent,
}

// WrapExistingTools converts a slice of provider.ToolDefinition from the
// existing MCP/builtin tool system into tool.Tool implementations suitable
// for broker registration. Each tool is categorized and tagged based on
// known tool name mappings.
func WrapExistingTools(defs []provider.ToolDefinition) []Tool {
	tools := make([]Tool, 0, len(defs))
	for _, def := range defs {
		cat, source, tags := classifyExisting(def.Name)
		tools = append(tools, WrapProviderDef(def, cat, source, tags))
	}
	return tools
}

// classifyExisting determines category, source, and tags for a known tool name.
// With MCP internalization (ADR-002) the agent-facing surface no longer
// emits a `mcp__server__` prefix; classification consults the explicit
// known-name maps and otherwise defaults to session/builtin.
func classifyExisting(name string) (category, source string, tags []string) {
	// Check dev tools.
	if cat, ok := devToolCategories[name]; ok {
		return cat, SourceBuiltin, []string{"dev", "builtin"}
	}

	// Check general tools.
	if cat, ok := generalToolCategories[name]; ok {
		return cat, SourceBuiltin, []string{"general", "builtin"}
	}

	// Check self-service tools.
	if cat, ok := selfToolCategories[name]; ok {
		return cat, SourceBuiltin, []string{"self-service", "builtin"}
	}

	// Unknown — default to session/builtin. Pre-ADR-002 we tagged tool
	// names containing the `mcp__` prefix as SourceMCP, but that signal
	// is gone from the agent-facing surface. Callers that need to mark
	// a tool as MCP-origin should call WrapProviderDef directly with
	// SourceMCP and tag "mcp"; bulk classification by name alone is
	// no longer possible.
	return CategorySession, SourceBuiltin, []string{"builtin"}
}
