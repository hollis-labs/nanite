package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Retained Basic-auth responses remain stable; tools/call is deliberately
// authenticated rather than exempt.
// The table verifies response bytes/headers and admission, including partially
// configured credentials (which the legacy middleware intentionally accepts).
func TestBasicAuthLegacyResponseCompatibility(t *testing.T) {
	configurations := []struct{ name, user, password string }{
		{"disabled", "", ""}, {"user-only", "operator", ""},
		{"password-only", "", "fixture-password"}, {"configured", "operator", "fixture-password"},
	}
	routes := []struct {
		method, path string
		exempt       bool
	}{
		{"GET", "/api/settings", false}, {"PUT", "/api/settings", false},
		{"GET", "/api/sessions", false}, {"GET", "/api/health", true},
		{"GET", "/api/health/", false}, {"POST", "/api/tools/call", false},
		{"GET", "/", true}, {"GET", "/api", true},
		{"GET", "/api/adminish/manifest", false},
	}
	for _, cfg := range configurations {
		t.Run(cfg.name, func(t *testing.T) {
			t.Setenv("NANITE_AUTH_USER", cfg.user)
			t.Setenv("NANITE_AUTH_PASSWORD", cfg.password)
			credentials := []struct {
				name, user, password    string
				basic, malformed, valid bool
			}{
				{name: "missing"}, {name: "malformed", malformed: true},
				{name: "matching", user: cfg.user, password: cfg.password, basic: true, valid: true},
				{name: "wrong-user", user: "other", password: cfg.password, basic: true},
				{name: "wrong-password", user: cfg.user, password: "wrong", basic: true},
			}
			for _, route := range routes {
				for _, cred := range credentials {
					t.Run(route.method+route.path+"/"+cred.name, func(t *testing.T) {
						admitted := false
						downstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
							admitted = true
							w.Header().Set("Content-Type", "application/test")
							w.Header().Set("X-Downstream", "present")
							w.WriteHeader(http.StatusAccepted)
							if _, writeErr := w.Write([]byte("downstream")); writeErr != nil {
								t.Fatal(writeErr)
							}
						})
						request := httptest.NewRequest(route.method, route.path, nil)
						// Caller identity is never an authentication substitute.
						request.Header.Set("X-Nanite-Caller-Agent", "operator")
						if cred.basic {
							request.SetBasicAuth(cred.user, cred.password)
						}
						if cred.malformed {
							request.Header.Set("Authorization", "Basic invalid")
						}
						recorder := httptest.NewRecorder()
						basicAuthMiddleware(downstream).ServeHTTP(recorder, request)
						allowed := route.exempt || (cfg.user == "" && cfg.password == "") || cred.valid
						status, body := http.StatusAccepted, "downstream"
						headers := http.Header{"Content-Type": {"application/test"}, "X-Downstream": {"present"}}
						if !allowed {
							status, body = http.StatusUnauthorized, "unauthorized\n"
							headers = http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "Www-Authenticate": {`Basic realm="nanite"`}, "X-Content-Type-Options": {"nosniff"}}
						}
						if recorder.Code != status || recorder.Body.String() != body || admitted != allowed || !reflect.DeepEqual(recorder.Header(), headers) {
							t.Fatalf("compatibility mismatch: admission=%v status=%d headers=%v body=%q", admitted, recorder.Code, recorder.Header(), recorder.Body.String())
						}
						t.Logf("%s %s: allowed=%t status=%d headers=%v body=%q", route.method, route.path, admitted, recorder.Code, recorder.Header(), recorder.Body.String())
					})
				}
			}
		})
	}
}
