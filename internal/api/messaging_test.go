package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
)

// seedMessageInbox inserts one unread message addressed to
// (sess-1, file-backend) so handler tests can assert inbox reads and
// unread-count return values.
func seedMessageInbox(t *testing.T, a *API) {
	t.Helper()
	if err := a.Services.Store.CreateAgent(&store.AgentProfile{
		ID:   "file-backend",
		Slug: "file-backend",
		Name: "Backend",
		Kind: "external",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := a.Services.Store.CreateAgent(&store.AgentProfile{
		ID:   "file-frontend",
		Slug: "file-frontend",
		Name: "Frontend",
		Kind: "external",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	// Seed via direct store so we don't need to stand up the sender's
	// auto-register path — the row is the only thing these tests read.
	sqlStore := messaging.NewSQLiteStore(a.Services.Store.DB)
	if _, err := sqlStore.Send(context.Background(), messaging.SendInput{
		FromSessionID: "sess-other",
		FromAgentID:   messaging.UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-backend",
		Body:          "hi",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestHandleMessageUnreadCount_MatchingCallerHeadersPass verifies the
// G-6.3 happy path: when the caller headers match the target inbox
// tuple, the handler returns the unread count normally.
func TestHandleMessageUnreadCount_MatchingCallerHeadersPass(t *testing.T) {
	a, mux := newTestAPI(t)
	seedMessageInbox(t, a)

	req := httptest.NewRequest("GET", "/api/messaging/unread?session_id=sess-1&agent_id=file-backend", nil)
	// In production the middleware stamps these headers onto ctx. This
	// test exercises the handler-level contract, so we stamp the
	// CallerIdentity manually via request ctx.
	req = req.WithContext(messaging.WithCaller(req.Context(), messaging.CallerIdentity{
		SessionID: "sess-1",
		AgentID:   "file-backend",
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]int
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["count"] != 1 {
		t.Errorf("count=%d, want 1", resp["count"])
	}
}

// TestHandleMessageUnreadCount_MismatchedCallerHeadersReject verifies
// the G-6.3 authz path: when caller headers are stamped but do NOT
// match the target inbox tuple, the service returns ErrForbidden and
// the handler maps that to 403 — no count leaks to a caller who does
// not own the inbox.
func TestHandleMessageUnreadCount_MismatchedCallerHeadersReject(t *testing.T) {
	a, mux := newTestAPI(t)
	seedMessageInbox(t, a)

	req := httptest.NewRequest("GET", "/api/messaging/unread?session_id=sess-1&agent_id=file-backend", nil)
	req = req.WithContext(messaging.WithCaller(req.Context(), messaging.CallerIdentity{
		SessionID: "sess-1",
		AgentID:   "file-frontend", // caller is NOT the target inbox owner
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for mismatched caller, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMessageUnreadCount_NoHeadersFallsOpen verifies the G-6.3
// backwards-compat fall-open: without any CallerIdentity on ctx, the
// handler behaves as it did pre-G-6.3 (service is a thin pass-through,
// returns the count). Existing FE/CLI clients continue to work.
func TestHandleMessageUnreadCount_NoHeadersFallsOpen(t *testing.T) {
	a, mux := newTestAPI(t)
	seedMessageInbox(t, a)

	req := httptest.NewRequest("GET", "/api/messaging/unread?session_id=sess-1&agent_id=file-backend", nil)
	// No WithCaller — simulates a client that has not adopted the header contract.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 fall-open, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]int
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["count"] != 1 {
		t.Errorf("count=%d, want 1", resp["count"])
	}
}

// TestHandleMessageInbox_MismatchedCallerHeadersReject verifies the
// G-6.3 authz path on the inbox handler: a caller whose identity
// does not match the target inbox tuple sees 403 — the service's
// caller-match check becomes effective over HTTP once ctx carries a
// distinct caller identity.
func TestHandleMessageInbox_MismatchedCallerHeadersReject(t *testing.T) {
	a, mux := newTestAPI(t)
	seedMessageInbox(t, a)

	req := httptest.NewRequest("GET", "/api/messaging/inbox?session_id=sess-1&agent_id=file-backend", nil)
	req = req.WithContext(messaging.WithCaller(req.Context(), messaging.CallerIdentity{
		SessionID: "sess-1",
		AgentID:   "file-frontend", // wrong agent
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for mismatched caller, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleMessageInbox_MatchingCallerHeadersPass verifies the happy
// path at the HTTP handler: matching headers → 200 + seeded rows.
func TestHandleMessageInbox_MatchingCallerHeadersPass(t *testing.T) {
	a, mux := newTestAPI(t)
	seedMessageInbox(t, a)

	req := httptest.NewRequest("GET", "/api/messaging/inbox?session_id=sess-1&agent_id=file-backend", nil)
	req = req.WithContext(messaging.WithCaller(req.Context(), messaging.CallerIdentity{
		SessionID: "sess-1",
		AgentID:   "file-backend",
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var msgs []messaging.Message
	if err := json.NewDecoder(rec.Body).Decode(&msgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("len=%d, want 1", len(msgs))
	}
}
