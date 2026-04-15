package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

func seedEnvelopeInstance(t *testing.T, a *API, sessionID, envelopeType string) *store.EnvelopeInstance {
	t.Helper()
	inst := &store.EnvelopeInstance{
		SessionID:    sessionID,
		EnvelopeType: envelopeType,
		EnvelopeJSON: `{"kind":"envelope","version":1,"type":"` + envelopeType + `"}`,
	}
	if err := a.Services.Store.CreateEnvelopeInstance(inst); err != nil {
		t.Fatalf("CreateEnvelopeInstance: %v", err)
	}
	return inst
}

func seedSessionForEnvelope(t *testing.T, a *API) string {
	t.Helper()
	if err := a.Services.Store.CreateWorkspace(&store.Workspace{ID: "ws-env", Name: "env"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws-env"}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess.ID
}

func doPost(mux *http.ServeMux, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestEnvelopeRespond_SubmittedDefaultHandler(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)
	inst := seedEnvelopeInstance(t, a, sessID, "some-form")

	body, _ := json.Marshal(chat.ResponseV1{
		V: 1, Kind: "some-form", ID: inst.ID, Status: chat.StatusSubmitted,
		Data: map[string]any{"approved": true},
	})
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Fatalf("expected ok:true, got %+v", resp)
	}
	if resp["message_id"] == "" || resp["message_id"] == nil {
		t.Fatalf("expected message_id, got %+v", resp)
	}

	// Verify persistence on the instance.
	got, _ := a.Services.Store.GetEnvelopeInstance(inst.ID)
	if got.RespondedAt == nil || got.ResponseStatus != "submitted" {
		t.Fatalf("instance not updated: %+v", got)
	}

	// Verify transcript message exists with envelope_response role.
	msgs, _ := a.Services.Store.ListMessages(sessID, 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != chat.RoleEnvelopeResponse {
		t.Fatalf("role: %q", msgs[0].Role)
	}
	if !bytes.Contains([]byte(msgs[0].Content), []byte("[envelope:some-form status:submitted]")) {
		t.Fatalf("content missing marker: %q", msgs[0].Content)
	}
}

func TestEnvelopeRespond_CancelledAndPartial(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)

	for _, status := range []chat.ResponseStatus{chat.StatusCancelled, chat.StatusPartial} {
		inst := seedEnvelopeInstance(t, a, sessID, "q")
		body, _ := json.Marshal(chat.ResponseV1{V: 1, Kind: "q", ID: inst.ID, Status: status})
		w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d; body: %s", status, w.Code, w.Body.String())
		}
		got, _ := a.Services.Store.GetEnvelopeInstance(inst.ID)
		if got.ResponseStatus != string(status) {
			t.Fatalf("expected %s, got %s", status, got.ResponseStatus)
		}
	}
}

func TestEnvelopeRespond_NotFound(t *testing.T) {
	_, mux := newTestAPI(t)
	body, _ := json.Marshal(chat.ResponseV1{V: 1, Kind: "x", ID: "missing", Status: chat.StatusSubmitted})
	w := doPost(mux, "/api/envelopes/missing/respond", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestEnvelopeRespond_DuplicateReturns409(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)
	inst := seedEnvelopeInstance(t, a, sessID, "x")

	body, _ := json.Marshal(chat.ResponseV1{V: 1, Kind: "x", ID: inst.ID, Status: chat.StatusSubmitted})
	if w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body); w.Code != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", w.Code)
	}
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("second: expected 409, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestEnvelopeRespond_SilentHandler(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)

	const kind = "silent-test"
	chat.RegisterResponseHandler(kind, chat.HandlerFunc(func(_ context.Context, _ store.EnvelopeInstance, _ chat.ResponseV1) (chat.HandlerResult, error) {
		return chat.HandlerResult{Silent: true, FollowUp: "done"}, nil
	}))
	t.Cleanup(func() { chat.UnregisterResponseHandler(kind) })

	inst := seedEnvelopeInstance(t, a, sessID, kind)
	body, _ := json.Marshal(chat.ResponseV1{V: 1, Kind: kind, ID: inst.ID, Status: chat.StatusSubmitted})
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["message_id"]; ok {
		t.Fatalf("silent handler should not produce message_id, got %+v", resp)
	}
	if resp["follow_up"] != "done" {
		t.Fatalf("expected follow_up=done, got %+v", resp)
	}
	msgs, _ := a.Services.Store.ListMessages(sessID, 10)
	if len(msgs) != 0 {
		t.Fatalf("silent handler should not create transcript messages, got %d", len(msgs))
	}
}

func TestEnvelopeRespond_SessionMismatch(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)
	inst := seedEnvelopeInstance(t, a, sessID, "x")

	payload := map[string]any{
		"v": 1, "kind": "x", "id": inst.ID, "status": "submitted",
		"session_id": "different-session",
	}
	body, _ := json.Marshal(payload)
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestEnvelopeRespond_BadJSON(t *testing.T) {
	_, mux := newTestAPI(t)
	w := doPost(mux, "/api/envelopes/whatever/respond", []byte("{not json"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestEnvelopeRespond_HandlerError(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)

	const kind = "err-handler"
	chat.RegisterResponseHandler(kind, chat.HandlerFunc(func(_ context.Context, _ store.EnvelopeInstance, _ chat.ResponseV1) (chat.HandlerResult, error) {
		return chat.HandlerResult{}, assertHandlerError{}
	}))
	t.Cleanup(func() { chat.UnregisterResponseHandler(kind) })

	inst := seedEnvelopeInstance(t, a, sessID, kind)
	body, _ := json.Marshal(chat.ResponseV1{V: 1, Kind: kind, ID: inst.ID, Status: chat.StatusSubmitted})
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d; body: %s", w.Code, w.Body.String())
	}
}

type assertHandlerError struct{}

func (assertHandlerError) Error() string { return "synthetic handler failure" }

func TestEnvelopeRespond_KindMismatchRejected(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)
	inst := seedEnvelopeInstance(t, a, sessID, "collect_feedback")

	// Response kind disagrees with stored envelope type.
	body, _ := json.Marshal(chat.ResponseV1{
		V: 1, Kind: "something-else", ID: inst.ID, Status: chat.StatusSubmitted,
	})
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestEnvelopeRespond_SessionIDNonStringRejected(t *testing.T) {
	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)
	inst := seedEnvelopeInstance(t, a, sessID, "x")

	// session_id as a number (present-but-non-string) must be treated as
	// mismatch — a forged body can't bypass the check by using the wrong
	// JSON type.
	payload := map[string]any{
		"v": 1, "kind": "x", "id": inst.ID, "status": "submitted",
		"session_id": 42,
	}
	body, _ := json.Marshal(payload)
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", w.Code, w.Body.String())
	}
}
