package apiauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// registeredPatterns reads every mux.Handle/HandleFunc string literal in the
// packages that register Nanite's routes.
func registeredPatterns(t *testing.T) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for _, dir := range []string{"../api", "../server"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				p, err := strconv.Unquote(lit.Value)
				if err != nil || seen[p] {
					return true
				}
				seen[p] = true
				out = append(out, p)
				return true
			})
		}
	}
	sort.Strings(out)
	return out
}

// Every registered /api/ route must be named by a rule: an unclassified route
// would silently become admin-only (writes) or readable (reads) by fallback,
// so a new route has to be classified on purpose.
func TestEveryRegisteredAPIRouteIsClassified(t *testing.T) {
	patterns := registeredPatterns(t)
	if len(patterns) < 200 {
		t.Fatalf("found only %d registered patterns; the scanner is broken", len(patterns))
	}
	var unclassified []string
	for _, p := range patterns {
		path := p
		if _, rest, ok := strings.Cut(p, " "); ok {
			path = rest
		}
		if !strings.HasPrefix(path, "/api") {
			continue
		}
		if _, ok := ScopeFor("", p); !ok {
			unclassified = append(unclassified, p)
		}
	}
	if len(unclassified) > 0 {
		t.Fatalf("routes with no scope rule (add them to routeRules): %v", unclassified)
	}
}

func TestScopeForKeyRoutes(t *testing.T) {
	cases := []struct {
		pattern string
		want    Scope
	}{
		{"GET /api/sessions", ScopeRead},
		{"GET /api/sessions/{id}/messages", ScopeRead},
		{"POST /api/sessions", ScopeOperate},
		{"POST /api/sessions/{id}/shell-exec", ScopeAdmin},
		{"POST /api/agent/v1/sessions/{id}/turns", ScopeOperate},
		{"POST /api/tools/call", ScopeTools},
		{"GET /api/tools", ScopeRead},
		{"POST /api/agents/{id}/retire", ScopeAdmin},
		{"PUT /api/agents/{id}", ScopeMaintain},
		{"POST /api/plugins/install-archive", ScopeAdmin},
		{"POST /api/mcp-servers", ScopeAdmin},
		{"GET /api/mcp-servers", ScopeMaintain},
		{"POST /api/providers/{id}/api-key", ScopeAdmin},
		{"GET /api/auth/credentials", ScopeAdmin},
		{"GET /api/auth/session", ScopeRead},
		{"/api/admin/", ScopeAdmin},
		{"PUT /api/settings", ScopeMaintain},
		{"POST /api/never-heard-of-it", ScopeAdmin},
	}
	for _, c := range cases {
		got, _ := ScopeFor("", c.pattern)
		if got != c.want {
			t.Errorf("%s: scope %s, want %s", c.pattern, got, c.want)
		}
	}
	if got, matched := ScopeFor(http.MethodPost, "POST /api/never-heard-of-it"); matched || got != ScopeAdmin {
		t.Errorf("unknown write: scope %s matched=%v, want admin and unmatched", got, matched)
	}
}

func TestNativeAuthorityRoutesAreAdminOnly(t *testing.T) {
	for _, tc := range []struct{ method, pattern string }{
		{"POST", "POST /api/native-authority"},
		{"POST", "POST /api/native-authority/{id}/revoke"},
		{"PUT", "PUT /api/native-authority/{id}/tools/{tool}"},
		{"DELETE", "DELETE /api/native-authority/{id}/tools/{tool}"},
	} {
		scope, named := ScopeFor(tc.method, tc.pattern)
		if !named || scope != ScopeAdmin {
			t.Errorf("%s = %v (named %v), want admin", tc.pattern, scope, named)
		}
	}
}

// Stopping a session and creating one with a work_root are operate-scope by
// decision (see the comment above the /api/agent/v1 rule); this pins it so a
// change to the route table is a visible choice.
func TestAgentSessionLifecycleRoutesRequireOperate(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/agent/v1/sessions/{id}/stop"},
		{http.MethodHead, "/api/agent/v1/sessions/{id}/stop"},
		{http.MethodPost, "/api/agent/v1/sessions"},
	} {
		got, known := ScopeFor(tc.method, tc.path)
		if !known || got != ScopeOperate {
			t.Fatalf("%s %s = %v (known=%v), want operate", tc.method, tc.path, got, known)
		}
	}
	if got, _ := ScopeFor(http.MethodDelete, "/api/sessions/{id}"); got != ScopeOperate {
		t.Fatalf("DELETE session = %v, want operate (stop must not be stricter than the route it shares a body with, nor looser)", got)
	}
}

// agents:manage opens agent management and nothing that grants tools or
// touches credentials and providers (CW-20261024-0025).
func TestAgentsManageRouteClassification(t *testing.T) {
	manage := []string{
		"POST /api/agent-setup", "GET /api/native-agents", "GET /api/native-agents/{id}",
		"POST /api/agent-host-settings", "PUT /api/agent-host-settings/{id}", "DELETE /api/agent-host-settings/{id}",
		"GET /api/native-authority", "GET /api/native-authority/tools", "GET /api/native-authority/{id}",
		"POST /api/admin/mcp",
	}
	for _, p := range manage {
		if got, named := ScopeFor("", p); !named || got != ScopeAgentsManage {
			t.Errorf("%s = %v (named %v), want agents:manage", p, got, named)
		}
	}
	admin := []string{
		"POST /api/native-authority", "POST /api/native-authority/{id}/revoke",
		"PUT /api/native-authority/{id}/tools/{tool}", "DELETE /api/native-authority/{id}/tools/{tool}",
		"GET /api/auth/credentials", "POST /api/auth/credentials", "POST /api/auth/credentials/{id}/revoke",
		"PUT /api/providers/{id}/api-key", "POST /api/providers", "/api/admin/", "POST /api/admin/other",
	}
	for _, p := range admin {
		if got, _ := ScopeFor("", p); got != ScopeAdmin {
			t.Errorf("%s = %v, want admin", p, got)
		}
	}
	// Host settings writes also stay open to maintain.
	if need, alt, _ := ScopesFor("", "PUT /api/agent-host-settings/{id}"); need != ScopeAgentsManage || alt != ScopeMaintain {
		t.Errorf("host settings write = %v/%v, want agents:manage or maintain", need, alt)
	}
}

func TestAgentsManageScopeRules(t *testing.T) {
	s, err := ParseScope("agents:manage")
	if err != nil || s != ScopeAgentsManage {
		t.Fatalf("ParseScope = %v, %v", s, err)
	}
	if !(Credential{Kind: KindClient, Scopes: []Scope{ScopeAdmin}}).Has(ScopeAgentsManage) {
		t.Error("admin must include agents:manage")
	}
	if (Credential{Kind: KindClient, Scopes: []Scope{ScopeAgentsManage}}).Has(ScopeAdmin) {
		t.Error("agents:manage must not include admin")
	}
	if (Principal{Kind: KindClient, Scopes: []Scope{ScopeMaintain, ScopeOperate}}).Has(ScopeAgentsManage) {
		t.Error("maintain and operate must not include agents:manage")
	}
}
