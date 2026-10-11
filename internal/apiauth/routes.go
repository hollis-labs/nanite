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
}

func rule(methods string, path string, scope Scope) routeRule {
	var ms []string
	if methods != "" {
		ms = strings.Split(methods, ",")
	}
	return routeRule{methods: ms, path: regexp.MustCompile(path), scope: scope}
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
	rule("", `^/api/admin(/|$)`, ScopeAdmin),

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

	// Conversation and work: operate.
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
			return r.scope, true
		}
	}
	if method == http.MethodGet || method == http.MethodHead {
		return ScopeRead, false
	}
	return ScopeAdmin, false
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
