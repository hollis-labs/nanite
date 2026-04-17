package server

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/config"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

// newTestServer constructs a *Server with an empty store/API and the given
// HTTPConfig. Suitable for middleware-level tests that don't need a real
// backing store.
func newTestServer(t *testing.T, cfg config.HTTPConfig) *Server {
	t.Helper()
	mux := http.NewServeMux()
	s := &Server{
		mux:     mux,
		port:    0,
		httpCfg: resolveHTTPConfig(cfg),
	}
	// Minimal routes so the handler chain has something to hit.
	mux.HandleFunc("POST /api/echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			// http.MaxBytesReader returns *http.MaxBytesError -> translate to 413.
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				http.Error(w, "too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})
	return s
}

// testChain assembles the same middleware order as ListenAndServe for
// httptest-backed scenarios. Keeping this in the test file (rather than
// exporting a helper from server.go) avoids widening the package API.
func (s *Server) testChain() http.Handler {
	return s.recoverMiddleware(
		s.loggingMiddleware(
			s.corsMiddleware(
				basicAuthMiddleware(
					callerIdentityMiddleware(
						s.bodyLimitMiddleware(s.mux),
					),
				),
			),
		),
	)
}

// TestHTTPServerTimeouts_ReadHeader asserts that a client which stops sending
// header bytes after the first byte hits ReadHeaderTimeout rather than
// hanging indefinitely.
func TestHTTPServerTimeouts_ReadHeader(t *testing.T) {
	cfg := config.HTTPConfig{
		ReadHeaderTimeoutSeconds: 1,
		ReadTimeoutSeconds:       2,
		WriteTimeoutSeconds:      2,
		IdleTimeoutSeconds:       2,
		MaxRequestBodyBytes:      1 << 20,
		MaxUploadBodyBytes:       1 << 20,
	}
	srv := newTestServer(t, cfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := srv.newHTTPServer(ln.Addr().String())
	go func() { _ = httpSrv.Serve(ln) }()
	defer httpSrv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a single byte of what would be a request line, then stall.
	if _, err := conn.Write([]byte("G")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Expect the server to drop us within ReadHeaderTimeout + grace. It
	// may either (a) close the connection (Read returns io.EOF) or
	// (b) send a 408 Request Timeout response then close. Either proves
	// the timeout fired. What we must NOT see is a successful indefinite
	// block — that would mean no timeout is armed.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 512)
	start := time.Now()
	n, readErr := conn.Read(buf)
	elapsed := time.Since(start)

	// Drain any trailing bytes so the server-side goroutine finishes cleanly.
	if readErr == nil {
		// Server sent bytes (likely a 408 response). That's fine — the
		// signal we care about is that the server did not wait past
		// ReadHeaderTimeout + grace.
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, _ = conn.Read(buf)
	}

	// Server-side ReadHeaderTimeout is 1s; allow up to 4s of tolerance for
	// scheduling.
	if elapsed > 4*time.Second {
		t.Fatalf("server waited too long to act on stalled headers: %v (read n=%d err=%v)", elapsed, n, readErr)
	}
	if elapsed < 500*time.Millisecond {
		t.Fatalf("server acted suspiciously fast (%v) — expected ~1s from ReadHeaderTimeout", elapsed)
	}
}

// TestBodyLimitMiddleware asserts that a POST body exceeding the configured
// cap is rejected with 413, and that a body at the cap succeeds with 200.
func TestBodyLimitMiddleware(t *testing.T) {
	cfg := config.HTTPConfig{
		ReadTimeoutSeconds:       5,
		ReadHeaderTimeoutSeconds: 5,
		WriteTimeoutSeconds:      5,
		IdleTimeoutSeconds:       5,
		MaxRequestBodyBytes:      32, // 32 bytes for determinism
		MaxUploadBodyBytes:       32,
	}
	srv := newTestServer(t, cfg)

	ts := httptest.NewServer(srv.testChain())
	defer ts.Close()

	body := bytes.Repeat([]byte("A"), 33) // limit + 1
	resp, err := http.Post(ts.URL+"/api/echo", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d", resp.StatusCode)
	}

	// Body at exactly the limit should succeed.
	ok := bytes.Repeat([]byte("B"), 32)
	resp2, err := http.Post(ts.URL+"/api/echo", "application/octet-stream", bytes.NewReader(ok))
	if err != nil {
		t.Fatalf("post (ok): %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for body at limit, got %d", resp2.StatusCode)
	}
}

// TestCORSPreflightNoAuth asserts that an OPTIONS preflight from an allowed
// origin succeeds with 204 and CORS headers even when no Authorization
// header is present and basic-auth is enabled. This is the fix for
// audit finding 09-medium (CORS inside auth).
func TestCORSPreflightNoAuth(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "admin")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")

	srv := newTestServer(t, config.HTTPConfig{})

	ts := httptest.NewServer(srv.testChain())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodOptions, ts.URL+"/api/ping", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("expected ACAO=http://localhost:5173, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected ACAC=true, got %q", got)
	}
}

// TestAuthStillEnforcedOnNonPreflight confirms that moving CORS outside auth
// did not accidentally punch a hole for real requests — GET /api/ping with
// auth configured but no credentials must still 401.
func TestAuthStillEnforcedOnNonPreflight(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "admin")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")

	srv := newTestServer(t, config.HTTPConfig{})
	ts := httptest.NewServer(srv.testChain())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ping", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated GET, got %d", resp.StatusCode)
	}
}

// TestResolveHTTPConfigDefaults confirms that zero-valued config falls back
// to the conservative defaults rather than 0-duration timeouts (which would
// disable the protection entirely).
func TestResolveHTTPConfigDefaults(t *testing.T) {
	got := resolveHTTPConfig(config.HTTPConfig{})
	cases := []struct {
		name string
		v    int
	}{
		{"ReadTimeoutSeconds", got.ReadTimeoutSeconds},
		{"ReadHeaderTimeoutSeconds", got.ReadHeaderTimeoutSeconds},
		{"WriteTimeoutSeconds", got.WriteTimeoutSeconds},
		{"IdleTimeoutSeconds", got.IdleTimeoutSeconds},
	}
	for _, c := range cases {
		if c.v <= 0 {
			t.Errorf("%s should have a positive default, got %d", c.name, c.v)
		}
	}
	if got.MaxRequestBodyBytes <= 0 {
		t.Errorf("MaxRequestBodyBytes default not applied: %d", got.MaxRequestBodyBytes)
	}
	if got.MaxUploadBodyBytes <= 0 {
		t.Errorf("MaxUploadBodyBytes default not applied: %d", got.MaxUploadBodyBytes)
	}
	// Upload cap must be >= request cap by design (uploads are the
	// headroom exception).
	if got.MaxUploadBodyBytes < got.MaxRequestBodyBytes {
		t.Errorf("upload cap %d must be >= request cap %d", got.MaxUploadBodyBytes, got.MaxRequestBodyBytes)
	}
}

// newTestServerWithHost wires a minimal Server with a real plugin.Host so
// handlers that depend on the host (handleEmitEvent) can be exercised
// without the full bootstrap sequence.
func newTestServerWithHost(t *testing.T) *Server {
	t.Helper()
	host := naniteplugin.NewHostWithStore(nil)
	mux := http.NewServeMux()
	s := &Server{
		mux:        mux,
		pluginHost: host,
		httpCfg:    resolveHTTPConfig(config.HTTPConfig{}),
	}
	mux.HandleFunc("POST /api/plugins/events", s.handleEmitEvent)
	return s
}

// TestHandleEmitEvent_UnknownType rejects event names not in the allowlist.
func TestHandleEmitEvent_UnknownType(t *testing.T) {
	srv := newTestServerWithHost(t)
	body := bytes.NewBufferString(`{"event_type":"totally.invented.event","data":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/events", body)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown event_type, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestHandleEmitEvent_RawStringPayload rejects non-object data payloads.
func TestHandleEmitEvent_RawStringPayload(t *testing.T) {
	srv := newTestServerWithHost(t)
	body := bytes.NewBufferString(`{"event_type":"session.start","data":"not-an-object"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/events", body)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for raw-string data, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestHandleEmitEvent_ValidObject accepts a well-formed allowlisted event.
func TestHandleEmitEvent_ValidObject(t *testing.T) {
	srv := newTestServerWithHost(t)
	body := bytes.NewBufferString(`{"event_type":"session.start","session_id":"s1","data":{"foo":"bar"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/events", body)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid event, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestIsKnownEmitEventType covers the normalization + allowlist lookup for
// both canonical Nanite names and Claude Code aliases.
func TestIsKnownEmitEventType(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"session.start", true},
		{"tool.executing", true},
		{"PreToolUse", true},        // alias -> tool.executing
		{"totally.invented", false}, // unknown
		{"", false},
	}
	for _, c := range cases {
		if got := isKnownEmitEventType(c.name); got != c.ok {
			t.Errorf("isKnownEmitEventType(%q) = %v, want %v", c.name, got, c.ok)
		}
	}
}

// TestCORSAllowlist_ExactMatch verifies that the CORS allowlist performs
// exact-string matching: allowlisted origins get their Origin reflected with
// credentials, while similar-looking but not-listed origins receive no ACAO.
// This is the regression guard for the audit Critical finding that the prior
// isOriginAllowed stub reflected any origin.
func TestCORSAllowlist_ExactMatch(t *testing.T) {
	cfg := config.HTTPConfig{
		CORSAllowedOrigins: []string{"http://allowed.example"},
	}
	srv := newTestServer(t, cfg)
	ts := httptest.NewServer(srv.testChain())
	defer ts.Close()

	// Allowed.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ping", nil)
	req.Header.Set("Origin", "http://allowed.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://allowed.example" {
		t.Fatalf("allowed origin: expected ACAO mirror, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allowed origin: expected ACAC=true, got %q", got)
	}
	if got := resp.Header.Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("expected Vary: Origin, got %q", got)
	}

	// Disallowed (sub-origin mismatch): suffix/substring variant.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ping", nil)
	req2.Header.Set("Origin", "http://evil.allowed.example")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp2.Body.Close()
	if got := resp2.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin: expected no ACAO, got %q", got)
	}
	if got := resp2.Header.Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("disallowed origin: Vary: Origin must still be set, got %q", got)
	}
	// Response must still succeed — CORS is advisory, the browser enforces.
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("disallowed origin: expected 200, got %d", resp2.StatusCode)
	}
}

// TestCORSAllowlist_DefaultDev verifies that a zero-valued config falls back
// to the dev-oriented default allowlist (localhost:5173 / 127.0.0.1:5173).
func TestCORSAllowlist_DefaultDev(t *testing.T) {
	srv := newTestServer(t, config.HTTPConfig{})
	ts := httptest.NewServer(srv.testChain())
	defer ts.Close()

	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:5173"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ping", nil)
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do %s: %v", origin, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("default allowlist: %s expected mirror ACAO, got %q", origin, got)
		}
	}

	// A non-default origin must not be reflected under default config.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ping", nil)
	req.Header.Set("Origin", "http://somewhere-else.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("default allowlist: non-default origin must not be reflected, got %q", got)
	}
}

// TestCORSAllowlist_Wildcard verifies that "*" reflects any origin but drops
// Access-Control-Allow-Credentials per CORS spec.
func TestCORSAllowlist_Wildcard(t *testing.T) {
	cfg := config.HTTPConfig{CORSAllowedOrigins: []string{"*"}}
	srv := newTestServer(t, cfg)
	ts := httptest.NewServer(srv.testChain())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ping", nil)
	req.Header.Set("Origin", "http://anything.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("wildcard: expected ACAO=*, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("wildcard: ACAC must be dropped, got %q", got)
	}
}

// TestIsUploadPath sanity-checks the upload-path prefix allowlist used by
// bodyLimitMiddleware.
func TestIsUploadPath(t *testing.T) {
	yes := []string{
		"/api/artifacts/upload",
		"/api/plugins/install",
		"/api/plugins/install/foo",
	}
	no := []string{
		"/api/artifacts",
		"/api/plugins",
		"/api/echo",
		"/",
	}
	for _, p := range yes {
		if !isUploadPath(p) {
			t.Errorf("expected %q to be an upload path", p)
		}
	}
	for _, p := range no {
		if isUploadPath(p) {
			t.Errorf("did not expect %q to be an upload path", p)
		}
	}
}
