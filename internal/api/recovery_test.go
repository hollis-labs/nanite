package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// newTestAPIWithRecovery wires an API + Container that exposes a real
// broker.Broker so the cancel endpoint can be exercised end-to-end.
// Distinct from newTestAPI (which omits broker wiring) so tests that
// don't need the recovery surface don't pay for it.
func newTestAPIWithRecovery(t *testing.T) (*API, *http.ServeMux, *broker.Broker) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:     s,
		Providers: provider.NewRegistry(),
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	if svc.Recovery == nil {
		t.Fatal("Container.Recovery is nil; expected NewContainer to wire the broker")
	}

	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return a, mux, svc.Recovery
}

// TestRecoveryCancelEndpoint_HappyPath registers a token on the broker
// for sessionID="s1" and confirms the endpoint cancels it cleanly:
// 200 + the bound CancelFunc fires.
func TestRecoveryCancelEndpoint_HappyPath(t *testing.T) {
	_, mux, broker := newTestAPIWithRecovery(t)

	called := false
	_, cancel := context.WithCancel(context.Background())
	broker.RegisterActiveRetryForTest("s1", "tok-happy", func() {
		called = true
		cancel()
	})

	body, _ := json.Marshal(CancelRecoveryRetryRequest{Token: "tok-happy"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/recovery/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "cancelled" {
		t.Errorf("status = %q, want cancelled", got["status"])
	}
	if !called {
		t.Error("bound CancelFunc not invoked")
	}
}

// TestRecoveryCancelEndpoint_TokenForOtherSession exercises the cross-
// session replay guard: a token issued for sessionID="real" cannot be
// cancelled via the endpoint path-bound to sessionID="other". 404 + the
// token stays active so the legitimate session can still cancel it.
func TestRecoveryCancelEndpoint_TokenForOtherSession(t *testing.T) {
	_, mux, broker := newTestAPIWithRecovery(t)

	called := false
	broker.RegisterActiveRetryForTest("real", "tok-replay", func() { called = true })

	body, _ := json.Marshal(CancelRecoveryRetryRequest{Token: "tok-replay"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/other/recovery/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
	if called {
		t.Error("CancelFunc fired despite cross-session replay")
	}

	// Legitimate session can still cancel.
	w2 := httptest.NewRecorder()
	body2, _ := json.Marshal(CancelRecoveryRetryRequest{Token: "tok-replay"})
	req2 := httptest.NewRequest(http.MethodPost, "/api/sessions/real/recovery/cancel", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("legitimate session cancel: expected 200, got %d", w2.Code)
	}
	if !called {
		t.Error("CancelFunc not invoked on legitimate cancel")
	}
}

// TestRecoveryCancelEndpoint_UnknownToken covers the stale-FE / typo
// path: a token that was never registered (or has already been
// cancelled) returns 404. No state mutation.
func TestRecoveryCancelEndpoint_UnknownToken(t *testing.T) {
	_, mux, _ := newTestAPIWithRecovery(t)

	body, _ := json.Marshal(CancelRecoveryRetryRequest{Token: "never-existed"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/recovery/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestRecoveryCancelEndpoint_MissingToken — empty token in the body is
// a malformed request, distinct from "valid-shape but unknown token"
// (which is 404). 400 with a useful message.
func TestRecoveryCancelEndpoint_MissingToken(t *testing.T) {
	_, mux, _ := newTestAPIWithRecovery(t)

	body, _ := json.Marshal(CancelRecoveryRetryRequest{Token: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/recovery/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestRecoveryCancelEndpoint_InvalidJSON — malformed body decodes as
// 400 (consistent with other endpoints).
func TestRecoveryCancelEndpoint_InvalidJSON(t *testing.T) {
	_, mux, _ := newTestAPIWithRecovery(t)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/recovery/cancel", bytes.NewReader([]byte("{not-json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}
