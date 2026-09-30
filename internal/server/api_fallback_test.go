package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
)

// newFallbackTestServer is newTestServer plus the production fallbacks and a
// wildcard route, served through the real middleware chain. dev mode makes
// the SPA fallback answer with its placeholder page.
func newFallbackTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newTestServer(t, config.HTTPConfig{})
	srv.dev = true
	ok := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }
	srv.mux.HandleFunc("GET /api/items/{id}", ok)
	srv.mux.HandleFunc("DELETE /api/items/{id}", ok)
	srv.registerFallbacks()
	// Plugin routes are added to the same mux at runtime, after the
	// fallbacks; a method-prefixed one must still win.
	srv.mux.HandleFunc("GET /api/plugins/demo/thing", ok)

	ts := httptest.NewServer(srv.testChain())
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, ts *httptest.Server, method, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Redirects are not followed: without its own pattern, the bare /api
	// would be a 301 to /api/ — which turns a POST into a GET.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestAPIFallback_UnknownPathIsJSON404(t *testing.T) {
	ts := newFallbackTestServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/nope"},
		{http.MethodPost, "/api/providers/anthropic-001/test"}, // the CW-20260930-0101 shape
		{http.MethodDelete, "/api/nope/deeper/still"},
		{http.MethodGet, "/api"},
		{http.MethodPost, "/api"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp, body := do(t, ts, tc.method, tc.path)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %q)", resp.StatusCode, body)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
				t.Fatalf("Cache-Control = %q, want the API's no-store", cc)
			}
			if allow := resp.Header.Get("Allow"); allow != "" {
				t.Fatalf("Allow = %q on a path no route serves", allow)
			}
			var got map[string]string
			if err := json.Unmarshal([]byte(body), &got); err != nil || got["error"] != "not found" || len(got) != 1 {
				t.Fatalf("body = %q, want {\"error\":\"not found\"}", body)
			}
		})
	}
}

func TestAPIFallback_WrongMethodOnRegisteredPathIs405(t *testing.T) {
	ts := newFallbackTestServer(t)
	for _, tc := range []struct{ method, path, allow string }{
		{http.MethodPost, "/api/ping", "GET, HEAD"},
		{http.MethodGet, "/api/echo", "POST"},
		{http.MethodPut, "/api/items/7", "GET, HEAD, DELETE"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp, body := do(t, ts, tc.method, tc.path)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405 (body %q)", resp.StatusCode, body)
			}
			if got := resp.Header.Get("Allow"); got != tc.allow {
				t.Fatalf("Allow = %q, want %q", got, tc.allow)
			}
			if !strings.Contains(body, `"error":"method not allowed"`) {
				t.Fatalf("body = %q", body)
			}
		})
	}
}

func TestAPIFallback_RegisteredRoutesAndSPAUnaffected(t *testing.T) {
	ts := newFallbackTestServer(t)
	for _, tc := range []struct {
		method, path string
		wantStatus   int
		wantCT       string
	}{
		{http.MethodGet, "/api/ping", http.StatusOK, ""},
		{http.MethodGet, "/api/items/7", http.StatusOK, ""},
		{http.MethodGet, "/api/plugins/demo/thing", http.StatusOK, ""},
		{http.MethodGet, "/", http.StatusOK, "text/html"},
		{http.MethodGet, "/sessions/abc", http.StatusOK, "text/html"},
		{http.MethodPost, "/sessions/abc", http.StatusOK, "text/html"},
		{http.MethodGet, "/apiary", http.StatusOK, "text/html"}, // not /api/
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp, body := do(t, ts, tc.method, tc.path)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", resp.StatusCode, tc.wantStatus, body)
			}
			if tc.wantCT != "" && !strings.HasPrefix(resp.Header.Get("Content-Type"), tc.wantCT) {
				t.Fatalf("Content-Type = %q, want %s", resp.Header.Get("Content-Type"), tc.wantCT)
			}
		})
	}
}

// Auth runs before the mux, so an unauthenticated request for an unknown
// /api path is still refused before it can learn whether the path exists.
func TestAPIFallback_AuthStillFirst(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "admin")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")
	ts := newFallbackTestServer(t)
	if resp, _ := do(t, ts, http.MethodGet, "/api/nope"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
