package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
)

// Raising a host to broad access is an admin act; keeping or clearing it is not.
func TestHostAccessAllowed_BroadNeedsAdmin(t *testing.T) {
	a, _ := newTestAPI(t)
	check := func(p *apiauth.Principal, current, requested string) (bool, int) {
		r := httptest.NewRequest(http.MethodPut, "/api/agent-host-settings/x", nil)
		if p != nil {
			r = r.WithContext(apiauth.WithPrincipal(r.Context(), *p))
		}
		w := httptest.NewRecorder()
		ok := a.hostAccessAllowed(w, r, current, requested)
		return ok, w.Code
	}
	admin := apiauth.Principal{Kind: apiauth.KindOperator, CredentialID: "op", Scopes: []apiauth.Scope{apiauth.ScopeAdmin}}
	manage := apiauth.Principal{Kind: apiauth.KindClient, CredentialID: "c", Scopes: []apiauth.Scope{apiauth.ScopeAgentsManage, apiauth.ScopeMaintain}}

	if ok, _ := check(&admin, "", "broad"); !ok {
		t.Fatal("admin must be able to set broad")
	}
	if ok, code := check(&manage, "", "broad"); ok || code != http.StatusForbidden {
		t.Fatalf("agents:manage set broad: ok=%v code=%d", ok, code)
	}
	if ok, code := check(nil, "", "broad"); ok || code != http.StatusForbidden {
		t.Fatalf("no principal set broad: ok=%v code=%d", ok, code)
	}
	if ok, _ := check(&manage, "broad", "broad"); !ok {
		t.Fatal("keeping broad unchanged is not an escalation")
	}
	if ok, _ := check(&manage, "broad", ""); !ok {
		t.Fatal("clearing broad must be allowed")
	}
	if ok, _ := check(&manage, "", ""); !ok {
		t.Fatal("unrelated updates must be allowed")
	}
}
