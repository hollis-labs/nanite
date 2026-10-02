package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// dummyHandler returns 200 with "ok" body.
var dummyHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
})

func TestAuthMiddlewareDisabled(t *testing.T) {
	// Ensure env vars are unset.
	t.Setenv("NANITE_AUTH_USER", "")
	t.Setenv("NANITE_AUTH_PASSWORD", "")

	handler := basicAuthMiddleware(dummyHandler)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when auth disabled, got %d", w.Code)
	}
}

func TestAuthMiddlewareEnabled(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "admin")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")

	handler := basicAuthMiddleware(dummyHandler)

	// Request without credentials.
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", w.Code)
	}

	// Request with correct credentials.
	req = httptest.NewRequest("GET", "/api/sessions", nil)
	req.SetBasicAuth("admin", "secret")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with correct credentials, got %d", w.Code)
	}

	// Request with wrong credentials.
	req = httptest.NewRequest("GET", "/api/sessions", nil)
	req.SetBasicAuth("admin", "wrong")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong credentials, got %d", w.Code)
	}
}

func TestAuthMiddlewareHealthExempt(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "admin")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")

	handler := basicAuthMiddleware(dummyHandler)

	// /api/health should pass through without credentials.
	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for /api/health without credentials, got %d", w.Code)
	}
}

func TestAuthMiddlewarePluginQueryUsesHandlerCredential(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "admin")
	t.Setenv("NANITE_AUTH_PASSWORD", "secret")
	handler := basicAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped-connection" {
			http.Error(w, "query credential required", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, test := range []struct {
		method, path, credential string
		status                   int
	}{
		{"GET", "/api/plugin-host/query/sessions", "Bearer scoped-connection", 204},
		{"GET", "/api/plugin-host/query/sessions", "", 401},
		{"POST", "/api/plugin-host/query/sessions", "Bearer scoped-connection", 401},
		{"GET", "/api/plugin-host/query-elsewhere/sessions", "Bearer scoped-connection", 401},
		{"GET", "/api/sessions", "Bearer scoped-connection", 401},
	} {
		req := httptest.NewRequest(test.method, test.path, nil)
		req.Header.Set("Authorization", test.credential)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != test.status {
			t.Fatalf("%s %s: %d", test.method, test.path, rec.Code)
		}
	}
}
