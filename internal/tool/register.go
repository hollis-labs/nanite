package tool

import (
	"github.com/hollis-labs/conduit/internal/provider"
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
	"conduit_create_skill":    CategorySession,
	"conduit_list_skills":     CategorySession,
	"conduit_update_skill":    CategorySession,
	"conduit_delete_skill":    CategorySession,
	"conduit_create_agent":    CategoryAgent,
	"conduit_list_agents":     CategoryAgent,
	"conduit_update_agent":    CategoryAgent,
	"conduit_navigate_engine": CategoryAgent,
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

	// MCP tools (mcp__server__name pattern).
	if len(name) > 5 && name[:5] == "mcp__" {
		return CategoryMCP, SourceMCP, []string{"mcp"}
	}

	// Unknown — default to session/builtin.
	return CategorySession, SourceBuiltin, []string{"builtin"}
}
