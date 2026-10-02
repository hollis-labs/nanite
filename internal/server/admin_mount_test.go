package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
)

func TestAdminMountedFailClosed(t *testing.T) {
	for _, cfg := range []struct{ name, user, password string }{{"off", "", ""}, {"user-only", "operator", ""}, {"password-only", "", "fixture-password"}, {"complete", "operator", "fixture-password"}} {
		t.Run(cfg.name, func(t *testing.T) {
			t.Setenv("NANITE_AUTH_USER", cfg.user)
			t.Setenv("NANITE_AUTH_PASSWORD", cfg.password)
			server, err := New(nil, nil, 0, false, nil, config.HTTPConfig{})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/admin", "/api/admin/manifest", "/api/admin/unknown"} {
				for _, authenticated := range []bool{false, true} {
					request := httptest.NewRequest(http.MethodGet, path, nil)
					if authenticated {
						request.SetBasicAuth(cfg.user, cfg.password)
					}
					request.Header.Set("X-Nanite-Caller-Agent", "operator")
					response := httptest.NewRecorder()
					server.handlerChain().ServeHTTP(response, request)
					allowed := cfg.user != "" && cfg.password != "" && authenticated
					if !allowed {
						if response.Code != 401 || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("WWW-Authenticate") != `Basic realm="nanite"` {
							t.Fatalf("admin failed open: %s %d %s", path, response.Code, response.Body.String())
						}
					} else if path == "/api/admin/manifest" {
						if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"nanite"`) {
							t.Fatal("manifest not mounted")
						}
					} else if response.Code != 404 {
						t.Fatal("unknown admin fell through to SPA or redirect")
					}
				}
			}
		})
	}
}
