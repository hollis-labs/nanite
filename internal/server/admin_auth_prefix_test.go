package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuthAdminPrefixAdmission(t *testing.T) {
	for _, cfg := range []struct{ name, user, password string }{
		{"disabled", "", ""}, {"user-only", "operator", ""}, {"password-only", "", "fixture-password"}, {"configured", "operator", "fixture-password"},
	} {
		t.Run(cfg.name, func(t *testing.T) {
			t.Setenv("NANITE_AUTH_USER", cfg.user)
			t.Setenv("NANITE_AUTH_PASSWORD", cfg.password)
			for _, path := range []string{"/api/admin", "/api/admin/manifest", "/api/admin/settings/preferences", "/api/admin/unknown"} {
				for _, matching := range []bool{false, true} {
					request := httptest.NewRequest(http.MethodGet, path, nil)
					if matching {
						request.SetBasicAuth(cfg.user, cfg.password)
					}
					admitted := false
					handler := basicAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { admitted = true; w.WriteHeader(http.StatusAccepted) }))
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					allowed := (cfg.user == "" && cfg.password == "") || matching
					status := http.StatusUnauthorized
					if allowed {
						status = http.StatusAccepted
					}
					if response.Code != status || admitted != allowed {
						t.Fatalf("auth admission changed for %s", path)
					}
					if !allowed && response.Header().Get("WWW-Authenticate") != `Basic realm="nanite"` {
						t.Fatal("challenge changed")
					}
					t.Logf("%s matching=%t allowed=%t status=%d headers=%v body=%q", path, matching, admitted, response.Code, response.Header(), response.Body.String())
				}
			}
		})
	}
}

func TestBasicAuthAdminJSONDenial(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "operator")
	t.Setenv("NANITE_AUTH_PASSWORD", "fixture-password")
	handler := basicAuthMiddleware(dummyHandler)
	for _, path := range []string{"/api/admin", "/api/admin/manifest", "/api/admin/settings/preferences", "/api/admin/unknown"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("wrong admin denial for %s", path)
		}
		var body struct {
			Error struct{ Code, Message string }
		}
		if decodeErr := json.Unmarshal(response.Body.Bytes(), &body); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if body.Error.Code != "unauthenticated" || body.Error.Message != "Authentication is required." {
			t.Fatal("wrong normalized denial")
		}
	}
}
