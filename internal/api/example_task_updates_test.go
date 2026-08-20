package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleExampleTaskUpdate_RejectsNonLoopback is the regression proof for
// the gap found in Orchestrator review of TASKS/harness-reactive-self-tools/
// 07-worked-example-task-update-report.md: internal/server/auth.go exempts
// this route from basic auth on the claimed premise that it's a same-host,
// same-process call "exactly like /api/tools/call" — but unlike
// handleSelfToolCall, this handler previously had no loopback check at all,
// so the exemption left it wide open to any caller that could reach the
// port. Mirrors TestHandleSelfToolCall_RejectsNonLoopback in
// tools_call_test.go.
func TestHandleExampleTaskUpdate_RejectsNonLoopback(t *testing.T) {
	a, _ := newToolCallTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/example/task-updates", bytes.NewReader([]byte(`{"id":"t-1","msg":"hi"}`)))
	req.RemoteAddr = "203.0.113.7:40000" // non-loopback
	rec := httptest.NewRecorder()
	a.handleExampleTaskUpdate(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-loopback caller", rec.Code)
	}
}

// TestHandleExampleTaskUpdate_LoopbackSucceeds pins that a loopback caller
// (the same-process internal_api_call reaction's own traffic pattern) still
// gets through and receives the {"received": true} confirmation.
func TestHandleExampleTaskUpdate_LoopbackSucceeds(t *testing.T) {
	a, _ := newToolCallTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/example/task-updates", bytes.NewReader([]byte(`{"id":"t-1","msg":"hi"}`)))
	// The endpoint is loopback-only; httptest.NewRequest defaults RemoteAddr
	// to a non-loopback test address, so pin it to loopback here.
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	a.handleExampleTaskUpdate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", rec.Code, rec.Body.String())
	}
	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, rec.Body.String())
	}
	if !body["received"] {
		t.Errorf("body = %+v, want received=true", body)
	}
}
