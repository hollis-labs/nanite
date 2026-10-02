package api

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestAdminOriginPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		host    string
		origins []string
		allowed []string
		tls     bool
		want    bool
	}{
		{name: "missing"}, {name: "empty", origins: []string{""}}, {name: "null", origins: []string{"null"}},
		{name: "foreign", origins: []string{"https://evil.example"}},
		{name: "duplicate", origins: []string{"http://nanite.test", "http://nanite.test"}},
		{name: "list", origins: []string{"http://nanite.test https://evil.example"}},
		{name: "path", origins: []string{"http://nanite.test/"}},
		{name: "query", origins: []string{"http://nanite.test?"}},
		{name: "fragment", origins: []string{"http://nanite.test#"}},
		{name: "userinfo", origins: []string{"http://operator@nanite.test"}},
		{name: "bad-port", origins: []string{"http://nanite.test:99999"}},
		{name: "same", origins: []string{"http://nanite.test"}, want: true},
		{name: "mixed-host", origins: []string{"http://NANITE.TEST"}, want: true},
		{name: "mixed-scheme", origins: []string{"HTTP://nanite.test"}, want: true},
		{name: "ipv6", host: "[::1]:8080", origins: []string{"http://[::1]:8080"}, want: true},
		{name: "trailing-dot", host: "nanite.test.", origins: []string{"http://nanite.test."}, want: true},
		{name: "different-trailing-dot", origins: []string{"http://nanite.test."}},
		{name: "zero-port", origins: []string{"http://nanite.test:0"}},
		{name: "trailing-colon", origins: []string{"http://nanite.test:"}},
		{name: "tls-http-origin", tls: true, origins: []string{"http://nanite.test"}},
		{name: "comma", origins: []string{"http://nanite.test,"}},
		{name: "backslash", origins: []string{`http://nanite.test\`}},
		{name: "percent", origins: []string{"http://nanite.test%"}},
		{name: "bracketed-dns", origins: []string{"http://[example.com]"}, allowed: []string{"http://[example.com]"}},
		{name: "underscore", host: "nanite_web:8080", origins: []string{"http://nanite_web:8080"}},
		{name: "empty-dns-label", origins: []string{"http://nanite..test"}, allowed: []string{"http://nanite..test"}},
		{name: "invalid-dns-label", origins: []string{"http://-nanite.test"}, allowed: []string{"http://-nanite.test"}},
		{name: "same-default-port", origins: []string{"http://nanite.test:80"}, want: true},
		{name: "tls", origins: []string{"https://nanite.test"}, tls: true, want: true},
		{name: "wrong-scheme", origins: []string{"https://nanite.test"}},
		{name: "explicit-proxy", origins: []string{"https://nanite.test"}, allowed: []string{"https://nanite.test"}, want: true},
		{name: "finite", origins: []string{"http://localhost:5173"}, allowed: []string{"http://localhost:5173"}, want: true},
		{name: "wildcard", origins: []string{"https://evil.example"}, allowed: []string{"*"}},
		{name: "suffix", origins: []string{"https://nanite.test.evil.example"}, allowed: []string{"https://nanite.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://nanite.test/api/admin/unknown", nil)
			if tc.host != "" {
				r.Host = tc.host
			}
			r.Header["Origin"] = tc.origins
			r.Header.Set("X-Forwarded-Host", "evil.example")
			r.Header.Set("X-Forwarded-Proto", "https")
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := AdminOriginAllowed(r, tc.allowed); got != tc.want {
				t.Fatalf("allowed=%v want %v", got, tc.want)
			}
		})
	}
}

func TestAdminOriginBeforeBody(t *testing.T) {
	h, err := (&API{}).NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"), "*")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/settings/preferences/validate", "/api/admin/settings/preferences/update", "/api/admin/settings/preferences/reset", "/api/admin/unknown"} {
		for _, origins := range [][]string{nil, {"null"}, {"http://foreign.test"}, {"http://example.com", "http://example.com"}} {
			body := &adminUnreadBody{}
			r := httptest.NewRequest("POST", path, body)
			r.SetBasicAuth("operator", "fixture-password")
			r.Header["Origin"] = origins
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 || body.reads != 0 {
				t.Fatal("Origin denial touched body or admitted request")
			}
		}
	}
}
