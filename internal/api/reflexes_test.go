package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestReflexesAPI_ListWorksForInternalBuiltinAgent replaces the pre-
// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md
// TestReflexesAPI_ListSupportsFileBackedAgent, which asserted
// GET /api/agents/file-default/reflexes resolved through the now-removed
// in-memory file-definition registry. The 9 internal builtin profiles
// (including "default") are real agent_profiles rows with real IDs from
// boot-time AutoIngestAgents now — there is no more "file-<slug>" alias to
// address them by, so this pins the equivalent, still-real requirement
// (listing reflexes for an internal/embedded agent works, same as any
// other) against the agent's actual ID.
func TestReflexesAPI_ListWorksForInternalBuiltinAgent(t *testing.T) {
	a, mux := newTestAPI(t)

	defaultAgent, err := a.Services.Store.GetAgentBySlug("default")
	if err != nil || defaultAgent == nil {
		t.Fatalf("GetAgentBySlug(default): %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/"+defaultAgent.ID+"/reflexes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list reflexes for internal builtin agent = %d body=%s", w.Code, w.Body.String())
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

// TestValidateReflexDefinition_ProvenanceTierGate is the regression test for
// TASKS/reflex-taxonomy/05-provenance-tier-enforcement.md: attempting to
// declare a halt_session reflex at plugin tier is rejected with a clear
// error naming both the kind and the tier; the same attempt at system or
// operator tier succeeds (no provenance-tier error in the result). No live
// plugin-tier insert path exists today (the task's own step 5), so
// "plugin" is set directly on the row here to exercise the gate the same
// way a future plugin-registration caller would hit it.
func TestValidateReflexDefinition_ProvenanceTierGate(t *testing.T) {
	a, _ := newTestAPI(t)
	ctx := context.Background()

	baseRow := func(tier string) store.AgentReflex {
		return store.AgentReflex{
			Name:           "halt-gate-test",
			TriggerKind:    store.ReflexTriggerPredicate,
			TriggerSpec:    `{"kind":"tool_calls_window","window":1,"op":"=","value":0}`,
			ActionKind:     store.ReflexActionHaltSession,
			ActionSpec:     `{"reason":"gate test"}`,
			Status:         store.ReflexStatusActive,
			ProvenanceTier: tier,
		}
	}

	t.Run("plugin tier rejected", func(t *testing.T) {
		errs := a.validateReflexDefinition(ctx, baseRow("plugin"))
		found := false
		for _, e := range errs {
			if strings.Contains(e, "plugin") && strings.Contains(e, "halt_session") {
				found = true
			}
		}
		if !found {
			t.Fatalf("validateReflexDefinition(halt_session @ plugin) errs = %v, want an error naming both %q and %q", errs, "plugin", "halt_session")
		}
	})

	t.Run("system tier succeeds", func(t *testing.T) {
		errs := a.validateReflexDefinition(ctx, baseRow("system"))
		for _, e := range errs {
			if strings.Contains(e, "provenance tier") {
				t.Fatalf("validateReflexDefinition(halt_session @ system) errs = %v, want no provenance-tier error", errs)
			}
		}
	})

	t.Run("operator tier succeeds", func(t *testing.T) {
		errs := a.validateReflexDefinition(ctx, baseRow("operator"))
		for _, e := range errs {
			if strings.Contains(e, "provenance tier") {
				t.Fatalf("validateReflexDefinition(halt_session @ operator) errs = %v, want no provenance-tier error", errs)
			}
		}
	})
}
