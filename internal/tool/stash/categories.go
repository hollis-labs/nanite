package stash

import (
	"strings"

	"github.com/hollis-labs/nanite/internal/tool"
)

// registryView is the minimal surface of a broker registry that stash needs to
// classify tools by name. Defined at the point of consumption to avoid pulling
// in the broker package — the real broker.Registry satisfies this interface
// via its Get method.
type registryView interface {
	Get(name string) tool.Tool
}

// RegistryCategorizer returns a Categorizer backed by a broker registry. The
// registry is authoritative for tool categories; any tool missing from the
// registry returns empty, which the stash buckets under CategoryOther.
func RegistryCategorizer(reg registryView) Categorizer {
	if reg == nil {
		return CategorizerFunc(func(string) string { return "" })
	}
	return CategorizerFunc(func(name string) string {
		t := reg.Get(name)
		if t == nil {
			return ""
		}
		return t.Category()
	})
}

// BuiltinCategories is the static name→category map for tools shipped in-tree.
// Plugin-authored tools are not listed here and fall through to CategoryOther
// (tracked follow-up: adding a `category:` field to the plugin manifest).
//
// The taxonomy matches tool.CategoryCoreIO, CategorySearch, CategoryAgent,
// CategoryContext, CategoryMode, CategoryMCP, CategorySession — plus the
// operational extras "code-exec" and "http" that the intent/rules layer scores
// against.
var BuiltinCategories = map[string]string{
	// dev_* (MCP dev transport) — fine-grained so intent rules can target them.
	"dev_bash":  "code-exec",
	"dev_edit":  "core-io",
	"dev_glob":  "core-io",
	"dev_grep":  "search",
	"dev_read":  "core-io",
	"dev_write": "core-io",

	// Web / HTTP.
	"web_fetch":  "http",
	"url_encode": "http",
	"url_decode": "http",

	// Think / memory / context.
	"think":                  "context",
	"nanite_memory_recall":   "context",
	"nanite_memory_save":     "context",
	"nanite_navigate_engine": "context",
	"nanite_refresh_engine":  "context",

	// Agent management + plans + todos.
	"nanite_create_agent":     "agent",
	"nanite_update_agent":     "agent",
	"nanite_list_agents":      "agent",
	"nanite_create_skill":     "agent",
	"nanite_update_skill":     "agent",
	"nanite_delete_skill":     "agent",
	"nanite_list_skills":      "agent",
	"nanite_plan_create":      "agent",
	"nanite_plan_update":      "agent",
	"nanite_todo_create":      "agent",
	"nanite_todo_list":        "agent",
	"nanite_todo_update":      "agent",
	"nanite_install_diff":     "agent",
	"nanite_install_home":     "agent",
	"nanite_install_project":  "agent",
	"nanite_install_rollback": "agent",
	"nanite_start_builder":    "agent",
	"nanite_builder_step":     "agent",

	// Document rendering — reading is the user-facing action.
	"nanite_show_document": "core-io",
	"nanite_show_report":   "core-io",
	"nanite_show_giphy":    "other",

	// Small utilities — bucket under "other"; intent rules don't target them
	// (and if the user asks for them by name, the explicit-signals layer
	// handles it).
	"datetime":   "other",
	"echo":       "other",
	"hash":       "other",
	"json_parse": "other",
	"math_eval":  "other",
}

// BuiltinCategorizer classifies by BuiltinCategories first, then falls back to
// structural hints:
//
//   - `mcp_<server>_<tool>` — CategoryMCP
//   - `nanite_<verb>_<noun>` — Categorize by verb where possible ("search",
//     "recall") else CategoryOther
//
// Unrecognised names return "" (stash buckets them under CategoryOther).
func BuiltinCategorizer() Categorizer {
	return CategorizerFunc(func(name string) string {
		if cat, ok := BuiltinCategories[name]; ok {
			return cat
		}
		// MCP-prefixed tools surface through the mcp bridge; always classify as mcp.
		if strings.HasPrefix(name, "mcp_") {
			return tool.CategoryMCP
		}
		// Heuristic nanite_ verb sniffing — covers plugin-authored or not-yet-
		// registered nanite tools without requiring a map update.
		if strings.HasPrefix(name, "nanite_") {
			rest := strings.TrimPrefix(name, "nanite_")
			switch {
			case strings.Contains(rest, "recall"), strings.Contains(rest, "memory"),
				strings.Contains(rest, "navigate"), strings.Contains(rest, "refresh"):
				return tool.CategoryContext
			case strings.Contains(rest, "search"), strings.Contains(rest, "find"),
				strings.Contains(rest, "lookup"):
				return tool.CategorySearch
			case strings.Contains(rest, "show"), strings.Contains(rest, "read"),
				strings.Contains(rest, "write"), strings.Contains(rest, "edit"):
				return tool.CategoryCoreIO
			case strings.Contains(rest, "agent"), strings.Contains(rest, "skill"),
				strings.Contains(rest, "plan"), strings.Contains(rest, "todo"),
				strings.Contains(rest, "install"), strings.Contains(rest, "builder"):
				return tool.CategoryAgent
			}
			return ""
		}
		return ""
	})
}

// ComposeCategorizers returns a Categorizer that tries each input in order and
// returns the first non-empty result. Useful for stacking (e.g., plugin-
// specific map + registry fallback).
func ComposeCategorizers(cs ...Categorizer) Categorizer {
	return CategorizerFunc(func(name string) string {
		for _, c := range cs {
			if c == nil {
				continue
			}
			if cat := c.Categorize(name); cat != "" {
				return cat
			}
		}
		return ""
	})
}
