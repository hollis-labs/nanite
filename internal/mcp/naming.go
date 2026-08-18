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

// SelfServerName is the canonical name reserved for the in-process
// self-tools transport. The reserved-namespace defense in
// assignUniformNameLocked is server-scoped: a tool is "reserved" iff it
// was published by this server. Third-party servers that publish a
// colliding name are force-prefixed at registration so the bare slot
// stays bound to the harness's own tool.
//
// CW-20260508-0015: the defense was previously prefix-based (`nanite_*`).
// The sp-20260429-0001 rename arc drops that prefix from self-tool names,
// so the defense rebases onto server identity instead — the invariant
// "tool name `card_show` resolves to the harness, not a third-party MCP"
// holds during and after the rename cutover.
const SelfServerName = "self"

// DevServerName, CodeServerName, and GeneralServerName are the other three
// in-process builtin server names nanite registers at startup (cmd/nanite/
// main.go), alongside SelfServerName. Together with SelfServerName these
// are the names main.go's builtin-registration call sites pass to
// Manager.AddBuiltinServer, which is what actually marks a server as
// first-party (see Manager.isFirstPartyBuiltinServerLocked) — the names
// themselves are no longer a closed set checked anywhere in this package.
const (
	DevServerName     = "dev"
	CodeServerName    = "code"
	GeneralServerName = "general"
)

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
// Reserved-namespace defense: when a third-party server publishes a tool
// whose bare name is already owned by the self server, the registration
// path uses this helper to compute the force-prefixed slot. The defense
// itself lives in Manager.assignUniformNameLocked (server-scoped, see
// IsReservedSelfToolName).
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

// IsReservedSelfToolName reports whether a (server, toolName) pair
// belongs to the harness's reserved self-tool namespace.
//
// The defense is server-scoped (CW-20260508-0015): a tool is reserved
// iff it was published by the canonical self server (SelfServerName).
// Third-party servers publishing a colliding name MUST NOT take the
// bare slot — the registration path (Manager.assignUniformNameLocked)
// force-prefixes them via DisambiguatedToolName.
//
// The previous implementation gated on a `nanite_` name prefix. That
// guard breaks once the sp-20260429-0001 rename arc drops the prefix
// (e.g., `card_show` → `card_show`), so this function now keys
// on server identity to keep the invariant intact during and after the
// cutover.
//
// `toolName` is accepted for completeness (call sites already have it
// in scope) and for forward-compatibility, but the current rule looks
// only at the server.
func IsReservedSelfToolName(server, toolName string) bool {
	if strings.TrimSpace(toolName) == "" {
		return false
	}
	return server == SelfServerName
}

// First-party builtin protection (self/dev/code/general and any future
// addition) used to live here as IsFirstPartyBuiltinServerName, a
// hand-maintained 4-name switch statement kept in sync by hand against
// the separate list of mcpManager.AddServer(...) calls in
// cmd/nanite/main.go — the exact "two lists, nothing enforces they
// match" shape that let commit 5144590's bug (a proxied server stealing
// nanite's own dev_bash) happen in the first place, one level up.
//
// It's replaced by Manager.isFirstPartyBuiltinServerLocked, backed by
// Manager.firstPartyBuiltinNames — state populated ONLY by
// Manager.AddBuiltinServer, the single call cmd/nanite/main.go's real
// builtin registrations use. "Register a builtin" and "protect that
// builtin's bare tool-name slot" are now the same action, so adding a
// fifth first-party builtin requires touching exactly one call site.
//
// Deliberately still independent of TrustTier: test fixtures and future
// callers legitimately register arbitrary/hostile servers at TierBuiltin
// via the ordinary AddServer path to exercise plain collision behavior,
// so tier alone can't distinguish "one of nanite's real builtins" from
// "some other server that happens to carry that tier." See
// Manager.assignUniformNameLocked.
