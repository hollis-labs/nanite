package stash

import (
	"strings"
)

const (
	categoryAgent   = "agent"
	categoryContext = "context"
	categoryCoreIO  = "core-io"
	categoryMCP     = "mcp"
	categorySearch  = "search"
)

// BuiltinCategories is the static name→category map for tools shipped in-tree.
// Plugin-authored tools are not listed here and fall through to CategoryOther
// (tracked follow-up: adding a `category:` field to the plugin manifest).
//
// The taxonomy matches the categories scored by the intent/rules layer.
var BuiltinCategories = map[string]string{
	// dev_* (MCP dev transport) — fine-grained so intent rules can target them.
	"dev_bash":  "code-exec",
	"dev_edit":  categoryCoreIO,
	"dev_glob":  categoryCoreIO,
	"dev_grep":  categorySearch,
	"dev_read":  categoryCoreIO,
	"dev_write": categoryCoreIO,

	// Web / HTTP.
	"web_fetch":  "http",
	"url_encode": "http",
	"url_decode": "http",

	// Think / memory / context.
	"think":            categoryContext,
	"tesseract_recall": categoryContext,
	"memory_write":     categoryContext,
	"engine_navigate":  categoryContext,
	"engine_refresh":   categoryContext,

	// Agent management + plans + todos.
	"agent_create":    categoryAgent,
	"agent_update":    categoryAgent,
	"agent_list":      categoryAgent,
	"skill_create":    categoryAgent,
	"skill_update":    categoryAgent,
	"skill_delete":    categoryAgent,
	"skill_list":      categoryAgent,
	"plan_create":     categoryAgent,
	"plan_update":     categoryAgent,
	"todo_create":     categoryAgent,
	"todo_list":       categoryAgent,
	"todo_update":     categoryAgent,
	"install_diff":    categoryAgent,
	"install_home":    categoryAgent,
	"install_project": categoryAgent,
	"builder_start":   categoryAgent,
	"builder_step":    categoryAgent,

	// Envelope card rendering — generic emission tool, reading the result is
	// the user-facing action.
	"card_show": categoryCoreIO,

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
// Unrecognized names return "" (stash buckets them under CategoryOther).
func BuiltinCategorizer() Categorizer {
	return CategorizerFunc(func(name string) string {
		if cat, ok := BuiltinCategories[name]; ok {
			return cat
		}
		// MCP-prefixed tools surface through the mcp bridge; always classify as mcp.
		if strings.HasPrefix(name, "mcp_") {
			return categoryMCP
		}
		// Heuristic nanite_ verb sniffing — covers plugin-authored or not-yet-
		// registered nanite tools without requiring a map update.
		if strings.HasPrefix(name, "nanite_") {
			rest := strings.TrimPrefix(name, "nanite_")
			switch {
			case strings.Contains(rest, "recall"), strings.Contains(rest, "memory"),
				strings.Contains(rest, "navigate"), strings.Contains(rest, "refresh"):
				return categoryContext
			case strings.Contains(rest, "search"), strings.Contains(rest, "find"),
				strings.Contains(rest, "lookup"):
				return categorySearch
			case strings.Contains(rest, "show"), strings.Contains(rest, "read"),
				strings.Contains(rest, "write"), strings.Contains(rest, "edit"):
				return categoryCoreIO
			case strings.Contains(rest, "agent"), strings.Contains(rest, "skill"),
				strings.Contains(rest, "plan"), strings.Contains(rest, "todo"),
				strings.Contains(rest, "install"), strings.Contains(rest, "builder"):
				return categoryAgent
			}
			return ""
		}
		return ""
	})
}

// ComposeCategorizers returns a Categorizer that tries each input in order and
// returns the first non-empty result. This lets callers layer more-specific
// categorizers ahead of broader fallbacks.
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
