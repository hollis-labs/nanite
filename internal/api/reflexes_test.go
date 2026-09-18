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
	if err := a.Services.Store.CreateAgent(context.Background(), agent); err != nil {
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

// TestReflexesAPI_RecurrenceOverrideSecondsSetPatchClear pins the create/
// patch wiring added for migration 124's cascade knob
// (agent_reflexes.recurrence_override_seconds): a positive value sets an
// explicit override at create time, PATCH with a different positive value
// changes it, and PATCH with 0 clears it back to nil ("inherit the
// kind/system default") -- the documented sentinel, since a zero-second
// recurrence is never a meaningful override.
func TestReflexesAPI_RecurrenceOverrideSecondsSetPatchClear(t *testing.T) {
	a, mux := newTestAPI(t)
	agent := &store.AgentProfile{Name: "Recurrence Agent", Slug: "recurrence-agent", SystemPrompt: "x", Class: "advisor"}
	if err := a.Services.Store.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	body := bytes.NewBufferString(`{
		"name":"recurring-reflex",
		"trigger_kind":"predicate",
		"trigger_spec":"{\"kind\":\"tool_calls_window\",\"window\":2,\"op\":\"=\",\"value\":0}",
		"action_kind":"inject_reminder",
		"action_spec":"{\"body\":\"ground\"}",
		"priority":5,
		"recurrence_override_seconds":300
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
	if created.RecurrenceOverrideSeconds == nil || *created.RecurrenceOverrideSeconds != 300 {
		t.Fatalf("expected recurrence_override_seconds=300 after create, got %v", created.RecurrenceOverrideSeconds)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/agents/"+agent.ID+"/reflexes/"+created.ID, bytes.NewBufferString(`{"recurrence_override_seconds":600}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch reflex = %d body=%s", w.Code, w.Body.String())
	}
	var patched store.AgentReflex
	if err := json.NewDecoder(w.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.RecurrenceOverrideSeconds == nil || *patched.RecurrenceOverrideSeconds != 600 {
		t.Fatalf("expected recurrence_override_seconds=600 after patch, got %v", patched.RecurrenceOverrideSeconds)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/agents/"+agent.ID+"/reflexes/"+created.ID, bytes.NewBufferString(`{"recurrence_override_seconds":0}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("patch reflex (clear) = %d body=%s", w.Code, w.Body.String())
	}
	var cleared store.AgentReflex
	if err := json.NewDecoder(w.Body).Decode(&cleared); err != nil {
		t.Fatalf("decode cleared: %v", err)
	}
	if cleared.RecurrenceOverrideSeconds != nil {
		t.Fatalf("expected recurrence_override_seconds=nil after patching 0 (clear sentinel), got %v", *cleared.RecurrenceOverrideSeconds)
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

	defaultAgent, err := a.Services.Store.GetAgentBySlug(context.Background(), "default")
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

// TestReflexesAPI_OptOutOfClassWideReflex is the Done-means test for
// CW-20260918-0023: the opt-out store functions (SetAgentReflexOptOut/
// ClearAgentReflexOptOut) existed with zero callers before this endpoint --
// this pins the actual write path end to end. Opting out of a class-wide
// reflex with opt_out_allowed=true removes it from the agent's resolved
// list; a reflex with opt_out_allowed=false rejects the opt-out with 400;
// clearing the opt-out restores the reflex to the resolved list.
func TestReflexesAPI_OptOutOfClassWideReflex(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()
	agent := &store.AgentProfile{Name: "Opt Out Agent", Slug: "opt-out-agent", SystemPrompt: "x", Class: "advisor"}
	if err := a.Services.Store.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	optable, err := a.Services.Store.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:      "advisor",
		Name:          "class-wide-optable",
		TriggerKind:   store.ReflexTriggerPredicate,
		TriggerSpec:   `{"kind":"tool_calls_window","window":2,"op":"=","value":0}`,
		ActionKind:    store.ReflexActionInjectReminder,
		ActionSpec:    `{"body":"ground"}`,
		CreatedBy:     "system",
		OptOutAllowed: true,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(optable): %v", err)
	}
	notOptable, err := a.Services.Store.InsertAgentReflex(ctx, store.AgentReflex{
		ClassTag:      "advisor",
		Name:          "class-wide-not-optable",
		TriggerKind:   store.ReflexTriggerPredicate,
		TriggerSpec:   `{"kind":"tool_calls_window","window":2,"op":"=","value":0}`,
		ActionKind:    store.ReflexActionInjectReminder,
		ActionSpec:    `{"body":"ground"}`,
		CreatedBy:     "system",
		OptOutAllowed: false,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(notOptable): %v", err)
	}

	listIncludes := func(id string) bool {
		req := httptest.NewRequest(http.MethodGet, "/api/agents/"+agent.ID+"/reflexes", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list reflexes = %d body=%s", w.Code, w.Body.String())
		}
		var rows []store.AgentReflex
		if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
			t.Fatalf("decode reflexes: %v", err)
		}
		for _, row := range rows {
			if row.ID == id {
				return true
			}
		}
		return false
	}

	if !listIncludes(optable) {
		t.Fatalf("expected class-wide optable reflex to be resolved before opt-out")
	}

	// opt_out_allowed=false rejects the opt-out.
	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/reflexes/"+notOptable+"/opt-out", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("opt-out of non-optable reflex = %d, want 400; body=%s", w.Code, w.Body.String())
	}

	// opt_out_allowed=true succeeds and removes it from the resolved list.
	req = httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/reflexes/"+optable+"/opt-out", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("opt-out of optable reflex = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if listIncludes(optable) {
		t.Fatalf("expected class-wide optable reflex to be excluded after opt-out")
	}

	// Re-opting-out is idempotent.
	req = httptest.NewRequest(http.MethodPost, "/api/agents/"+agent.ID+"/reflexes/"+optable+"/opt-out", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("repeat opt-out = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// Clearing the opt-out restores it.
	req = httptest.NewRequest(http.MethodDelete, "/api/agents/"+agent.ID+"/reflexes/"+optable+"/opt-out", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("clear opt-out = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !listIncludes(optable) {
		t.Fatalf("expected class-wide optable reflex to be resolved again after clearing opt-out")
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
