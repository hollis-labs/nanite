package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
)

// TestCallerIdentityMiddleware_StampsBothHeaders confirms the G-6.3
// happy path: both X-Nanite-Caller-* headers set → ctx carries the
// stamped CallerIdentity.
func TestCallerIdentityMiddleware_StampsBothHeaders(t *testing.T) {
	var gotID messaging.CallerIdentity
	var present bool
	handler := callerIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID, present = messaging.CallerFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/ping", nil)
	req.Header.Set(CallerSessionHeader, "sess-1")
	req.Header.Set(CallerAgentHeader, "file-backend")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !present {
		t.Fatal("expected CallerIdentity on ctx")
	}
	want := messaging.CallerIdentity{SessionID: "sess-1", AgentID: "file-backend"}
	if gotID != want {
		t.Fatalf("gotID=%+v, want %+v", gotID, want)
	}
}

// TestCallerIdentityMiddleware_MissingHeadersFallOpen confirms the
// G-6.3 backwards-compat path: absent headers → ctx has NO stamped
// identity, handlers fall back to legacy body/query behavior.
func TestCallerIdentityMiddleware_MissingHeadersFallOpen(t *testing.T) {
	var present bool
	handler := callerIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = messaging.CallerFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if present {
		t.Fatal("missing headers must NOT stamp ctx (fall-open behavior)")
	}
}

// TestCallerIdentityMiddleware_PartialHeadersFallOpen confirms the
// boundary is all-or-nothing: if either header is missing or empty,
// neither is stamped. This avoids half-populated identities silently
// trusting one field.
func TestCallerIdentityMiddleware_PartialHeadersFallOpen(t *testing.T) {
	cases := []struct {
		name    string
		session string
		agent   string
	}{
		{"session only", "sess-1", ""},
		{"agent only", "", "file-backend"},
		{"empty session header present", "", "file-backend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var present bool
			handler := callerIdentityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, present = messaging.CallerFromCtx(r.Context())
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest("GET", "/api/ping", nil)
			if tc.session != "" {
				req.Header.Set(CallerSessionHeader, tc.session)
			}
			if tc.agent != "" {
				req.Header.Set(CallerAgentHeader, tc.agent)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if present {
				t.Fatalf("partial headers (%q, %q) must not stamp ctx", tc.session, tc.agent)
			}
		})
	}
}
