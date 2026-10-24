package apiauth

import (
	"net/http"
	"regexp"
	"strings"
)

// routeRule classifies registered mux patterns into the scope required to
// call them. Rules are ordered, first match wins. A pattern no rule names
// falls back to admin for writes and read for reads, and the route-table
// test fails until someone classifies it on purpose (default deny).
type routeRule struct {
	methods []string // empty means any method; "!GET" means every method but GET
	path    *regexp.Regexp
	scope   Scope
	// alt, when set, is a second scope that also satisfies the rule. It keeps
	// an existing scope working where a narrower one is added beside it.
	alt Scope
}

func rule(methods string, path string, scope Scope) routeRule {
	var ms []string
	if methods != "" {
		ms = strings.Split(methods, ",")
	}
	return routeRule{methods: ms, path: regexp.MustCompile(path), scope: scope}
}

func altRule(methods, path string, scope, alt Scope) routeRule {
	r := rule(methods, path, scope)
	r.alt = alt
	return r
}

func (r routeRule) matches(method, path string) bool {
	if !r.path.MatchString(path) {
		return false
	}
	if len(r.methods) == 0 {
		return true
	}
	for _, m := range r.methods {
		if m == "!GET" && method != http.MethodGet && method != http.MethodHead {
			return true
		}
		if m == method {
			return true
		}
		if m == http.MethodGet && method == http.MethodHead {
			return true
		}
	}
	return false
}

// routeRules is the route-to-scope table. Order matters: specific before
// general.
var routeRules = []routeRule{
	// The credential API itself. Session introspection and logout are read;
	// everything else (list, create, revoke, rotate, login links) is admin.
	rule("GET", `^/api/auth/session$`, ScopeRead),
	rule("POST", `^/api/auth/logout$`, ScopeRead),
	rule("", `^/api/auth(/|$)`, ScopeAdmin),
	// The admin MCP endpoint is agent management for agents:manage holders.
	// Each tool re-checks its own scope (grants stay admin), because the inner
	// call does not pass this table again.
	rule("", `^/api/admin/mcp$`, ScopeAgentsManage),
	rule("", `^/api/admin(/|$)`, ScopeAdmin),
	// Native-view tool authority: reading it is agent management; creating,
	// granting, withdrawing and revoking it are admin (grants are the
	// privilege-escalation path).
	rule("GET", `^/api/native-authority(/|$)`, ScopeAgentsManage),
	rule("", `^/api/native-authority(/|$)`, ScopeAdmin),
	// One-call agent setup and the agent views. Setup granting tools is
	// refused inside the handler for a caller without admin.
	rule("", `^/api/(agent-setup|native-agents)(/|$)`, ScopeAgentsManage),
	// Agent host settings writes: agents:manage, or maintain as before.
	altRule("!GET", `^/api/agent-host-settings(/.*)?$`, ScopeAgentsManage, ScopeMaintain),

	// Code execution, credentials, retirement and posture: admin.
	rule("!GET", `^/api/agents/\{[^}]+\}/(retire|retirement-export)$`, ScopeAdmin),
	rule("POST", `^/api/agents/(install|\{[^}]+\}/sync|provision/general-chat|\{[^}]+\}/copy-to-managed)$`, ScopeAdmin),
	rule("!GET", `^/api/plugins/(install-archive|install-local|uninstall|enable|disable|reload|catalog/.*)$`, ScopeAdmin),
	rule("POST", `^/api/plugins/events$`, ScopeMaintain),
	rule("!GET", `^/api/mcp-servers(/.*)?$`, ScopeAdmin),
	rule("GET", `^/api/mcp-servers(/.*)?$`, ScopeMaintain),
	rule("POST", `^/api/providers/\{[^}]+\}/test$`, ScopeMaintain),
	rule("!GET", `^/api/providers(/.*)?$`, ScopeAdmin),
	rule("!GET", `^/api/sessions/\{[^}]+\}/(shell-exec|shell-mode)$`, ScopeAdmin),
	rule("POST", `^/api/commands/execute$`, ScopeAdmin),
	rule("POST", `^/api/processes/kill-stale$`, ScopeAdmin),
	rule("PUT", `^/api/permissions/mode$`, ScopeAdmin),
	rule("GET", `^/api/plugin-config(/.*)?$`, ScopeMaintain),
	// Plugin host callbacks carry their own per-plugin bearer (see
	// handlerAuthenticated); the scope here is only a floor.
	rule("", `^/api/plugin-host(/.*)?$`, ScopeMaintain),

	// Tool calls forwarded by an agent's nanite mcp subprocess.
	rule("POST", `^/api/tools/call$`, ScopeTools),
	rule("!GET", `^/api/tools(/.*)?$`, ScopeMaintain),

	// Conversation and work: operate. This includes stopping a session and
	// choosing a work_root on create. Both are deliberate: operate already
	// sends turns to, and DELETEs, any session (DELETE /api/sessions/{id} ends
	// a session by the same path), and work_root can only narrow into
	// directories the host settings already allow. Agent credentials hold only
	// the tools scope and cannot reach these routes.
	rule("", `^/api/agent/v1(/.*)?$`, ScopeOperate),
	rule("!GET", `^/api/(sessions|a2a|echo|workers|work|workflows|stream|messaging|teams|plans|todos|tasks|memories|projects|loops|goals|handoffs|pending|documents|artifacts|items|envelopes|drawer-cards|reflexes|durable-agent-wake|approvals|metrics|envelope)(/.*)?$`, ScopeOperate),

	// Configuration: maintain.
	rule("!GET", `^/api/(agents|agent-host-settings|agent-definitions|agent-builder|durable-agents|durable-agent-recipes|skills|roles|consumers|schedules|settings|plugin-config|plugins|catalog|models)(/.*)?$`, ScopeMaintain),

	// Everything else that only reads.
	rule("GET", `^/api(/.*)?$`, ScopeRead),
}

// ScopeFor returns the scope a mux pattern (as returned by
// ServeMux.Handler, "METHOD /path" or "/path") requires, and whether a rule
// named it. An unmatched pattern is treated as admin for writes, read for
// reads, and reported unmatched so tests can fail.
func ScopeFor(method, pattern string) (Scope, bool) {
	need, _, matched := ScopesFor(method, pattern)
	return need, matched
}

// ScopesFor is ScopeFor plus the alternative scope that also satisfies the
// route, or "" when there is none.
func ScopesFor(method, pattern string) (need, alt Scope, matched bool) {
	path := pattern
	if m, p, ok := strings.Cut(pattern, " "); ok {
		if method == "" {
			method = m
		}
		path = p
	}
	if method == "" {
		method = http.MethodGet
	}
	for _, r := range routeRules {
		if r.matches(method, path) {
			return r.scope, r.alt, true
		}
	}
	if method == http.MethodGet || method == http.MethodHead {
		return ScopeRead, "", false
	}
	return ScopeAdmin, "", false
}

// isPublic reports routes that need no credential. Each public route does
// its own checking where it needs any (login, link) or carries nothing
// sensitive (health, descriptors).
func isPublic(method, path string) bool {
	switch {
	case method == http.MethodGet && path == "/api/health":
		return true
	case method == http.MethodGet && path == "/api/agent/v1/initialize":
		return true
	case method == http.MethodGet && path == "/.well-known/agent-card.json":
		return true
	case method == http.MethodPost && path == "/api/auth/login":
		return true
	case method == http.MethodGet && path == "/api/auth/link":
		return true
	}
	return false
}

// handlerAuthenticated reports routes that carry their own per-plugin bearer
// (host queries and wake). They are not operator routes.
func handlerAuthenticated(method, path string) bool {
	return (method == http.MethodGet && strings.HasPrefix(path, "/api/plugin-host/query/")) ||
		(method == http.MethodPost && path == "/api/plugin-host/durable-wake")
}

// needsCredential reports whether a request path is part of the protected
// surface: every /api/ route. Static assets and the SPA shell carry no data.
func needsCredential(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}
