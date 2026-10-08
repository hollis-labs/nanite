package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hollis-labs/substrate/agent/approval"

	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentV1ApprovalFacadesShareBoundOutcome(t *testing.T) {
	a, mux := newTestAPI(t)
	view := &store.Session{Provider: "anthropic"}
	other := &store.Session{Provider: "anthropic"}
	for _, v := range []*store.Session{view, other} {
		if err := a.store.CreateSession(t.Context(), v); err != nil {
			t.Fatal(err)
		}
	}
	post := func(path, decision, scope string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		body := fmt.Sprintf(`{"decision":%q,"scope":%q}`, decision, scope)
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)))
		return w
	}
	for _, firstFacade := range []string{"retained", "agent-v1"} {
		t.Run(firstFacade, func(t *testing.T) {
			req := a.Services.CognitiveApprovals.Request(view.ID, "run-"+firstFacade, "call-"+firstFacade, "dev_write", nil, "permission required")
			retained := "/api/sessions/" + view.ID + "/approvals/" + req.ID
			native := agentV1RoutePrefix + "/sessions/" + view.ID + "/approvals/" + req.ID + "/responses"
			if w := post("/api/sessions/"+other.ID+"/approvals/"+req.ID, "allow", "once"); w.Code != 404 {
				t.Fatalf("wrong-owner retained reply = %d %s", w.Code, w.Body.String())
			}
			for _, path := range []string{retained, native} {
				if w := post(path, "allow", "session"); w.Code != 422 {
					t.Fatalf("bound session grant = %d %s", w.Code, w.Body.String())
				}
			}
			first := retained
			if firstFacade == "agent-v1" {
				first = native
			}
			if w := post(first, "allow", "once"); w.Code != 200 {
				t.Fatalf("first reply = %d %s", w.Code, w.Body.String())
			}
			if answer := a.Services.Permissions.WaitForApproval(t.Context(), req); answer.Decision != permissionlib.DecisionAllow || answer.Scope != permissionlib.ScopeOnce {
				t.Fatalf("waiter outcome = %+v", answer)
			}
			a.Services.CognitiveApprovals.Finish(req.ID)
			for _, path := range []string{retained, native} {
				if w := post(path, "allow", "once"); w.Code != 200 {
					t.Fatalf("matching repeat = %d %s", w.Code, w.Body.String())
				}
				if w := post(path, "deny", "once"); w.Code != 409 {
					t.Fatalf("conflicting repeat = %d %s", w.Code, w.Body.String())
				}
			}
			w := post(native, "allow", "once")
			var outcome approval.Decision
			if err := json.Unmarshal(w.Body.Bytes(), &outcome); err != nil || outcome.RunID != "run-"+firstFacade || outcome.CallID != "call-"+firstFacade {
				t.Fatalf("bound outcome = %+v err=%v", outcome, err)
			}
			if next := a.Services.Permissions.Check(t.Context(), view.ID, "dev_write", nil, permissionlib.ToolMeta{IsDestructive: true}); next.Decision == permissionlib.DecisionAllow {
				t.Fatalf("once approval granted a subsequent call: %+v", next)
			}
		})
	}

	// A separate unbound admin prompt keeps its retained session-grant semantics.
	unbound := a.Services.Permissions.RequestApproval(view.ID, "dev_write", nil, "admin approval")
	if w := post(agentV1RoutePrefix+"/sessions/"+view.ID+"/approvals/"+unbound.ID+"/responses", "allow", "once"); w.Code != 404 {
		t.Fatalf("unbound admin prompt appeared on agent facade: %d %s", w.Code, w.Body.String())
	}
	if w := post("/api/sessions/"+view.ID+"/approvals/"+unbound.ID, "allow", "session"); w.Code != 200 {
		t.Fatalf("retained admin reply = %d %s", w.Code, w.Body.String())
	}
	if answer := a.Services.Permissions.WaitForApproval(t.Context(), unbound); answer.Scope != permissionlib.ScopeSession {
		t.Fatalf("retained scope changed: %+v", answer)
	}
	if next := a.Services.Permissions.Check(t.Context(), view.ID, "dev_write", nil, permissionlib.ToolMeta{IsDestructive: true}); next.Decision != permissionlib.DecisionAllow {
		t.Fatalf("retained admin grant lost: %+v", next)
	}
}

func TestAgentV1ApprovalCompetingFacades(t *testing.T) {
	a, mux := newTestAPI(t)
	view := &store.Session{Provider: "anthropic"}
	if err := a.store.CreateSession(t.Context(), view); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"matching", "conflicting", "session-grant"} {
		t.Run(scenario, func(t *testing.T) {
			req := a.Services.CognitiveApprovals.Request(view.ID, "run", "call", "dev_write", nil, "permission required")
			paths := []string{"/api/sessions/" + view.ID + "/approvals/" + req.ID, agentV1RoutePrefix + "/sessions/" + view.ID + "/approvals/" + req.ID + "/responses"}
			decisions := []string{"allow", "allow"}
			if scenario == "conflicting" {
				decisions[1] = "deny"
			}
			scopes := []string{"once", "once"}
			if scenario == "session-grant" {
				scopes[0] = "session"
			}
			var results [2]*httptest.ResponseRecorder
			start := make(chan struct{})
			var group sync.WaitGroup
			for i := range paths {
				group.Go(func() {
					<-start
					w := httptest.NewRecorder()
					body := fmt.Sprintf(`{"decision":%q,"scope":%q}`, decisions[i], scopes[i])
					mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, paths[i], bytes.NewBufferString(body)))
					results[i] = w
				})
			}
			close(start)
			group.Wait()
			if scenario == "session-grant" {
				if results[0].Code != 422 || results[1].Code != 200 {
					t.Fatalf("competing session grant = %d, %d", results[0].Code, results[1].Code)
				}
			} else if scenario == "matching" {
				if results[0].Code != 200 || results[1].Code != 200 {
					t.Fatalf("matching responders = %d, %d", results[0].Code, results[1].Code)
				}
			} else if (results[0].Code != 200 || results[1].Code != 409) && (results[1].Code != 200 || results[0].Code != 409) {
				t.Fatalf("competing responders = %d, %d", results[0].Code, results[1].Code)
			}
			answer := a.Services.Permissions.WaitForApproval(t.Context(), req)
			a.Services.CognitiveApprovals.Finish(req.ID)
			outcome, err := a.Services.CognitiveApprovals.Respond(t.Context(), view.ID, req.ID, answer.Decision, answer.Scope)
			if err != nil || outcome.Decision != answer.Decision || answer.Scope != permissionlib.ScopeOnce {
				t.Fatalf("recorded result differs from waiter: %+v, %+v, %v", outcome, answer, err)
			}
			if next := a.Services.Permissions.Check(t.Context(), view.ID, "dev_write", nil, permissionlib.ToolMeta{IsDestructive: true}); next.Decision == permissionlib.DecisionAllow {
				t.Fatalf("competing responses granted subsequent tool call: %+v", next)
			}
		})
	}
}
