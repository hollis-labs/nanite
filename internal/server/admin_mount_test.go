package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/config"
)

// The admin mount fails closed: no credential, or a credential without the
// admin scope, never reaches it; an operator does.
func TestAdminMountedFailClosed(t *testing.T) {
	auth, operator := enforcedAuth(t)
	server, err := New(nil, nil, 0, false, nil, config.HTTPConfig{}, auth)
	if err != nil {
		t.Fatal(err)
	}
	_, client, err := auth.Guard.Store.Create(t.Context(), apiauth.CreateParams{Kind: apiauth.KindClient, Label: "reader", Scopes: []apiauth.Scope{apiauth.ScopeRead}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin", "/api/admin/manifest", "/api/admin/unknown"} {
		for _, tc := range []struct {
			name, bearer string
			status       int
		}{{"no credential", "", 401}, {"garbage", "nope", 401}, {"read-only client", client, 403}, {"operator", operator, 0}} {
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
			request.RemoteAddr = "127.0.0.1:12345"
			if tc.bearer != "" {
				request.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			request.Header.Set("X-Nanite-Caller-Agent", "operator")
			response := httptest.NewRecorder()
			server.handlerChain().ServeHTTP(response, request)
			if tc.status != 0 {
				if response.Code != tc.status || response.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("admin failed open: %s as %s: %d %s", path, tc.name, response.Code, response.Body.String())
				}
				continue
			}
			if path == "/api/admin/manifest" {
				if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"nanite"`) {
					t.Fatalf("manifest not mounted for the operator: %d %s", response.Code, response.Body.String())
				}
			} else if response.Code != 404 {
				t.Fatalf("unknown admin %s fell through to SPA or redirect: %d", path, response.Code)
			}
		}
	}
}
