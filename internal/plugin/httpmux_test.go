package plugin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler returns a deterministic 200 with a body so we can confirm the
// routing decision via response, not side-effects.
func okHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
}

func TestMutablePluginMux_HandleAndServe(t *testing.T) {
	mux := NewMutablePluginMux()
	mux.Handle("p1", "GET /api/one", okHandler("one"))
	mux.Handle("p2", "GET /api/two", okHandler("two"))

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/api/one", "one"},
		{"/api/two", "two"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status=%d want 200", tc.path, rr.Code)
		}
		if got := rr.Body.String(); got != tc.want {
			t.Fatalf("%s: body=%q want %q", tc.path, got, tc.want)
		}
	}
}

func TestMutablePluginMux_RemoveByPlugin(t *testing.T) {
	mux := NewMutablePluginMux()
	mux.Handle("p1", "GET /a", okHandler("a"))
	mux.Handle("p1", "GET /b", okHandler("b"))
	mux.Handle("p2", "GET /c", okHandler("c"))

	if n := mux.RemoveByPlugin("p1"); n != 2 {
		t.Fatalf("RemoveByPlugin(p1): got %d want 2", n)
	}
	if n := mux.CountByPlugin("p1"); n != 0 {
		t.Fatalf("CountByPlugin(p1) after remove: got %d want 0", n)
	}
	if n := mux.CountByPlugin("p2"); n != 1 {
		t.Fatalf("CountByPlugin(p2): got %d want 1", n)
	}

	// p2's route still answers.
	req := httptest.NewRequest(http.MethodGet, "/c", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "c" {
		t.Fatalf("p2 route: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// p1's old routes 404.
	for _, p := range []string{"/a", "/b"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s after remove: status=%d want 404", p, rr.Code)
		}
	}
}

func TestMutablePluginMux_ReplaceSamePattern(t *testing.T) {
	// Re-registering the same pattern must NOT panic (bare *http.ServeMux
	// would) and must serve the replacement handler.
	mux := NewMutablePluginMux()
	mux.Handle("p1", "GET /x", okHandler("first"))
	mux.Handle("p1", "GET /x", okHandler("second"))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if got := rr.Body.String(); got != "second" {
		t.Fatalf("replacement: body=%q want 'second'", got)
	}
}

func TestMutablePluginMux_RemoveByPluginNoMatch(t *testing.T) {
	mux := NewMutablePluginMux()
	mux.Handle("p1", "GET /a", okHandler("a"))
	if n := mux.RemoveByPlugin("does-not-exist"); n != 0 {
		t.Fatalf("RemoveByPlugin(unknown): got %d want 0", n)
	}
	// Existing route untouched.
	req := httptest.NewRequest(http.MethodGet, "/a", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Body.String() != "a" {
		t.Fatalf("untouched route: body=%q want 'a'", rr.Body.String())
	}
}
