package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentV1ApprovalResponseRepetitionAndOwnership(t *testing.T) {
	a, mux := newTestAPI(t)
	view := &store.Session{Provider: "anthropic"}
	other := &store.Session{Provider: "anthropic"}
	for _, v := range []*store.Session{view, other} {
		if err := a.store.CreateSession(t.Context(), v); err != nil {
			t.Fatal(err)
		}
	}
	req := a.Services.Permissions.RequestApproval(view.ID, "dev_write", nil, "permission required")
	a.Services.CognitiveApprovals.Bind(req, "accepted-run", "tool-call")
	post := func(viewID, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, agentV1RoutePrefix+"/sessions/"+viewID+"/approvals/"+req.ID+"/responses", bytes.NewBufferString(body)))
		return w
	}
	allow := `{"decision":"allow","scope":"once"}`
	if w := post(other.ID, allow); w.Code != 404 {
		t.Fatalf("wrong view = %d %s", w.Code, w.Body.String())
	}
	if w := post(view.ID, `{"decision":"allow","scope":"session"}`); w.Code != 422 {
		t.Fatalf("unsupported scope = %d %s", w.Code, w.Body.String())
	}
	w := post(view.ID, allow)
	if w.Code != 200 {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	var decision service.CognitiveApprovalDecision
	if err := json.Unmarshal(w.Body.Bytes(), &decision); err != nil || decision.RunID != "accepted-run" || decision.CallID != "tool-call" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if response := a.Services.Permissions.WaitForApproval(t.Context(), req); response.Decision != permissionlib.DecisionAllow {
		t.Fatalf("waiter received %+v", response)
	}
	a.Services.CognitiveApprovals.Finish(req.ID)
	if repeat := post(view.ID, allow); repeat.Code != 200 || repeat.Body.String() != w.Body.String() {
		t.Fatalf("repeat = %d %s", repeat.Code, repeat.Body.String())
	}
	if w := post(view.ID, `{"decision":"deny"}`); w.Code != 409 {
		t.Fatalf("conflict = %d %s", w.Code, w.Body.String())
	}
}
