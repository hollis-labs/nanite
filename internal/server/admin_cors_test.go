package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
)

// Run unchanged against the pre-policy server.go via a Go overlay, then against
// this tree. These are response/admission assertions, not source-file checks.
func TestAdminLegacyCORSCompatibility(t *testing.T) {
	for _, path := range []string{"/api/settings", "/api/adminish", "/api/adminish/manifest", "/api/tools/call", "/"} {
		for _, policy := range []string{"finite", "wildcard"} {
			allowed := []string{"http://localhost:5173"}
			if policy == "wildcard" {
				allowed = []string{"*"}
			}
			s := &Server{httpCfg: config.HTTPConfig{CORSAllowedOrigins: allowed}}
			for _, origin := range []string{"", "http://localhost:5173", "http://foreign.test", "null"} {
				for _, method := range []string{"POST", "OPTIONS"} {
					called := false
					next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						called = true
						w.WriteHeader(202)
						_, _ = w.Write([]byte("downstream"))
					})
					r := httptest.NewRequest(method, path, nil)
					if origin != "" {
						r.Header.Set("Origin", origin)
					}
					w := httptest.NewRecorder()
					s.corsMiddleware(next).ServeHTTP(w, r)
					expected := http.Header{}
					if origin != "" {
						expected.Add("Vary", "Origin")
					}
					if origin != "" && (policy == "wildcard" || origin == allowed[0]) {
						if policy == "wildcard" {
							expected.Set("Access-Control-Allow-Origin", "*")
						} else {
							expected.Set("Access-Control-Allow-Origin", origin)
							expected.Set("Access-Control-Allow-Credentials", "true")
						}
						expected.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
						expected.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Nanite-Caller-Session, X-Nanite-Caller-Agent, X-Nanite-Agent-Kind")
					}
					status, body := 202, "downstream"
					if method == "OPTIONS" {
						status = 204
						body = ""
					}
					if w.Code != status || w.Body.String() != body || called != (method != "OPTIONS") || !reflect.DeepEqual(w.Header(), expected) {
						t.Fatalf("legacy response changed: %s %s %s %q", policy, method, path, origin)
					}
					t.Logf("legacy %s %s %s origin=%q admitted=%v status=%d headers=%v body=%q", policy, method, path, origin, called, w.Code, w.Header(), w.Body.String())
				}
			}
		}
	}
}

func TestAdminFiniteCORS(t *testing.T) {
	for _, path := range []string{"/api/admin", "/api/admin/", "/api/admin/settings/preferences/update"} {
		for _, allowed := range [][]string{{"http://localhost:5173"}, {"*"}, {"*", "http://localhost:5173"}} {
			s := &Server{httpCfg: config.HTTPConfig{CORSAllowedOrigins: allowed}, adminAllowedOrigins: allowed}
			for _, origin := range []string{"http://localhost:5173", "http://foreign.test", "null", ""} {
				called := false
				r := httptest.NewRequest("OPTIONS", "http://nanite.test"+path, nil)
				r.Header.Set("Origin", origin)
				r.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, If-Match")
				w := httptest.NewRecorder()
				s.corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
				want := origin == "http://localhost:5173" && (len(allowed) > 1 || allowed[0] != "*")
				if w.Code != 204 || called {
					t.Fatal("preflight called downstream")
				}
				if w.Header().Get("Vary") != "Origin" {
					t.Fatal("preflight missing Origin variance")
				}
				if want {
					if w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" || w.Header().Get("Access-Control-Expose-Headers") != "ETag" || w.Header().Get("Access-Control-Allow-Headers") != "Content-Type, Authorization, If-Match" {
						t.Fatal("missing credentialed CORS/ETag headers")
					}
				} else if w.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("wildcard/foreign Origin granted")
				}
			}
		}
	}
}
