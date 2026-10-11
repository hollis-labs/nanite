package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
)

// A proxied DNS name is refused until the operator lists it in allowed_hosts;
// listing one name does not open any other.
func TestAllowedHostsAdmitsOnlyListedNames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hosts []string
		host  string
		want  int
	}{
		{"unlisted name refused", nil, "nanite.example.test", 403},
		{"listed name admitted", []string{"nanite.example.test"}, "nanite.example.test", 401},
		{"match ignores port and case", []string{"Nanite.Example.Test"}, "nanite.example.test:443", 401},
		{"other name still refused", []string{"nanite.example.test"}, "evil.example.test", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, _ := enforcedAuth(t)
			srv, err := New(nil, nil, 0, false, nil, config.HTTPConfig{AllowedHosts: tc.hosts}, auth)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/api/admin/manifest", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()
			srv.handlerChain().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("host %q: got %d, want %d: %s", tc.host, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
