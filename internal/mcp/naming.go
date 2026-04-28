package mcp

import (
	"strings"
)

// LegacyPrefix is the historical "mcp__<server>__<tool>" prefix used to
// disambiguate MCP tool names from native ones. Post-internalization
// (CW-20260427-0017, ADR-002) the agent never sees this prefix; it is
// retained here only as a defensive guard for input sanitization (e.g.,
// rejecting an LLM that smuggles the legacy form).
const LegacyPrefix = "mcp__"

// reservedPrefix is the prefix reserved for nanite's own self-tools
// (nanite_execute_task, nanite_chat_search, nanite_todo_*, etc.). MCP
// servers MUST NOT publish a tool that uniformly collides with this
// prefix; if one does, registration force-prefixes it with the server
// name to keep the reserved namespace inviolate.
const reservedPrefix = "nanite_"

// UniformToolName returns the agent-facing name for a tool published by
// an MCP server. The default rule is "strip the server" — a tool
// "memory_write" on the "mux" server is exposed to the LLM as
// "memory_write", not "mcp__mux__memory_write".
//
// The function is total — it never returns the empty string for a
// non-empty input — and it never reintroduces the legacy prefix.
//
// Collision policy lives in the registration path (Manager.DiscoverTools);
// this helper is the unconditional canonicalizer used at registration AND
// at the few residual call sites (audit logging, permission rule
// migration) that need to compute the agent-facing name from a (server,
// tool) pair.
func UniformToolName(server, tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return ""
	}
	// Defensive: strip a stray legacy prefix on the way in. An MCP server
	// that mis-reports its own name with the prefix (or a caller that
	// passes the prefixed form) gets the canonical bare form.
	if strings.HasPrefix(tool, LegacyPrefix) {
		if rest := strings.TrimPrefix(tool, LegacyPrefix); rest != "" {
			if idx := strings.Index(rest, "__"); idx >= 0 && idx+2 < len(rest) {
				tool = rest[idx+2:]
			}
		}
	}
	return tool
}

// DisambiguatedToolName returns the prefixed form "<server>_<tool>" used
// when two MCP servers publish a colliding tool name. Single underscore
// disambiguator — the legacy double-underscore "mcp__" form is gone.
//
// Reserved-namespace defense: if `tool` already has the nanite_ prefix,
// the server is force-prefixed unconditionally (regardless of collision)
// to keep nanite_* exclusively for first-party self-tools.
func DisambiguatedToolName(server, tool string) string {
	tool = UniformToolName(server, tool)
	if tool == "" {
		return ""
	}
	server = strings.TrimSpace(server)
	if server == "" {
		return tool
	}
	return server + "_" + tool
}

// IsReservedSelfToolName reports whether name lives in the reserved
// nanite_* namespace. Used at MCP-tool registration time to force-prefix
// any third-party tool that would otherwise collide with a self-tool.
func IsReservedSelfToolName(name string) bool {
	return strings.HasPrefix(name, reservedPrefix)
}
