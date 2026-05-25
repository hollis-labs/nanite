package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestReflexesAPI_CreatePatchDeleteAgentReflex(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Reflex Agent", Slug: "reflex-agent", SystemPrompt: "x", Class: "advisor"}
	if err := a.Services.Store.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body := bytes.NewBufferString(`{
		"name":"custom-reflex",
		"trigger_kind":"predicate",
		"trigger_spec":"{\"kind\":\"tool_calls_window\",\"window\":2,\"op\":\"=\",\"value\":0}",
		"action_kind":"inject_reminder",
		"action_spec":"{\"body\":\"ground\"}",
		"priority":5
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/reflexes", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create reflex = %d body=%s", w.Code, w.Body.String())
	}
	var created store.AgentReflex
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/agents/"+agent.ID+"/reflexes/"+created.ID, bytes.NewBufferString(`{"priority":9}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch reflex = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+agent.ID+"/reflexes/"+created.ID, nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete reflex = %d body=%s", w.Code, w.Body.String())
	}
}

func TestReflexesAPI_ListSupportsFileBackedAgent(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/file-default/reflexes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list file-backed reflexes = %d body=%s", w.Code, w.Body.String())
	}

	var rows []store.AgentReflex
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode reflexes: %v", err)
	}
	if rows == nil {
		t.Fatal("expected JSON array, got null")
	}
}

func TestReflexesAPI_PendingReviewAndValidate(t *testing.T) {
	a, mux := newTestAPI(t)
	pendingID, err := a.Services.Store.InsertPendingReflex(context.Background(), store.PendingReflex{
		ProposedBy:  "test",
		Name:        "proposed",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"tool_calls_window","window":2,"op":"=","value":0}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"body":"ground"}`,
		Rationale:   "test proposal",
	})
	if err != nil {
		t.Fatalf("InsertPendingReflex: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pending/reflexes?status=pending", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list pending = %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/pending/reflexes/"+pendingID+"/approve", bytes.NewBufferString(`{"reviewed_by":"operator"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve pending = %d body=%s", w.Code, w.Body.String())
	}

	body := bytes.NewBufferString(`{
		"trigger_kind":"predicate",
		"trigger_spec":"{\"kind\":\"tool_calls_window\",\"window\":2,\"op\":\"=\",\"value\":0}",
		"action_kind":"inject_reminder",
		"action_spec":"{\"body\":\"ground\"}",
		"state":{"messages":[{"tool_calls":0},{"tool_calls":0}]}
	}`)
	req = httptest.NewRequest(http.MethodPost, "/api/reflexes/validate", body)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("validate reflex = %d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Valid bool `json:"valid"`
		Fired bool `json:"fired"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode validate: %v", err)
	}
	if !got.Valid || !got.Fired {
		t.Fatalf("validate = %+v, want valid and fired", got)
	}
}
