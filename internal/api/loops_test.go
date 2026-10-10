package api

// TASKS/loops/10-loop-launcher-and-api.md's own "Done means" coverage,
// exercised through the real mux (mux.ServeHTTP against a real
// *http.Request/httptest.NewRecorder round trip, not by calling a handler
// function directly), mirroring team_runs_test.go's own regression-test
// shape for the identical "real store, real launcher, stub StepExecutor"
// convention.
//
// This package's own live-verification safety note (per
// TASKS/scheduling/09-operator-http-api.md's precedent, cited by this
// task's Context section): every test below builds a real, t.TempDir()-
// rooted SQLite *store.Store via newTestAPI, with only the leaf
// StepExecutor stubbed (fakeAPIStepExecutor, already declared in this
// package's own team_runs_test.go) -- no real LLM call.
//
// In addition to this file's own Go-level regression tests, every one of
// these endpoints was also round-trip tested against a real, separately
// running `nanite serve` process (scratch DB, no mocks) -- see this task
// file's own Work Log for the transcript and how a real running server
// could exercise a Loop launch/iteration with zero external network
// dependency (a tool-only WorkflowDefinition calling the tool_list
// self-tool, never an LLM call).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/loop"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// newTestAPIWithLoopLauncher builds on newTestAPI's real store/container,
// then wires the extra pieces main.go itself wires post-hoc for production
// traffic -- a fresh *agentworkflow.Registry, a real BuiltinWorkflowEngine,
// a real WorkflowLauncher (stub StepExecutor), a real *loop.LoopEngine, and
// this task's own LoopLauncher -- mirroring team_runs_test.go's own
// newTestAPIWithTeamRunLauncher shape one level up. Returns the registry
// too (unlike that helper) since these tests need to register plain
// WorkflowDefinitions directly, not compile one from a Team.
func newTestAPIWithLoopLauncher(t *testing.T) (*testAPI, *http.ServeMux, *store.Store, *agentworkflow.Registry) {
	t.Helper()
	a, mux := newTestAPI(t)
	st := a.store

	registry := agentworkflow.NewRegistry(nil)
	engine := newAPITestWorkflowHost(t, st)
	durable := service.NewDurableAgentService(st)
	launcher := service.NewWorkflowLauncher(registry, engine, &fakeAPIStepExecutor{}, durable)
	loopEngine := loop.NewLoopEngine(st, registry, launcher)
	a.SetLoopLauncher(loop.NewLoopLauncher(loopEngine, st))

	return a, mux, st, registry
}

// createLoopsAPITestAgentProfile mirrors internal/loop's own
// createTestLoopAgentProfile (engine_test.go) -- a bare AgentProfile with
// no Role, sufficient for WorkflowLaunchRequest.AgentProfileID's own FK
// requirement.
func createLoopsAPITestAgentProfile(t *testing.T, st *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	p := &store.AgentProfile{Name: slug, Slug: slug, SystemPrompt: "you are " + slug}
	if err := storetest.PriorAuthorizedActor(context.Background(), st, p); err != nil {
		t.Fatalf("CreateAgent(%s): %v", slug, err)
	}
	return p
}

// oneStepLoopIterationDefinition mirrors internal/loop's own
// oneStepIterationDefinition (engine_test.go) -- a trivial single-llm-step
// WorkflowDefinition with no Verify modifier, so a successful run
// classifies as PROGRESS.
func oneStepLoopIterationDefinition(name string) agentworkflow.WorkflowDefinition {
	return agentworkflow.WorkflowDefinition{
		Name: name,
		Steps: []agentworkflow.StepDefinition{
			{
				ID:   "work",
				Kind: agentworkflow.StepKindLLM,
				Config: map[string]any{
					"provider": "anthropic",
					"prompt":   "do the thing",
					"agent_id": "agent-1",
				},
			},
		},
	}
}

func doJSONRequest(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func launchLoopViaAPI(t *testing.T, mux *http.ServeMux, req loopLaunchRequest) loopResultResponse {
	t.Helper()
	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops", req)
	if w.Code != http.StatusOK {
		t.Fatalf("launch status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp loopResultResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal launch response: %v", err)
	}
	return resp
}

// forceEscalationViaAPI launches a fresh inline-goal loop with a budget
// that escalates after exactly one iteration (mirrors internal/loop's own
// forceEscalation, launcher_test.go, at the HTTP boundary instead of a
// direct LoopLauncher.Launch call).
func forceEscalationViaAPI(t *testing.T, mux *http.ServeMux, defName, profileID string) string {
	t.Helper()
	resp := launchLoopViaAPI(t, mux, loopLaunchRequest{
		InlineGoal:     &loopGoalSpecRequest{Intent: "escalation fixture " + defName},
		DefinitionName: defName,
		AgentProfileID: profileID,
		Budget:         &loopBudgetRequest{MaxIterations: 1},
	})
	if resp.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q, want waiting_on_escalation (last_decision %+v)", resp.Status, resp.LastDecision)
	}
	return resp.LoopRunID
}

// --- Goal CRUD ---

func TestLoopsAPI_GoalCRUD_Lifecycle(t *testing.T) {
	_, mux := newTestAPI(t)

	w := doJSONRequest(t, mux, http.MethodPost, "/api/goals", goalCreateRequest{
		Intent:             "ship it",
		DesiredState:       []string{"a"},
		AcceptanceCriteria: []string{"tests pass"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	var created store.Goal
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created.ID is empty")
	}
	if created.Status != store.GoalStatusDraft {
		t.Fatalf("Status = %q, want draft", created.Status)
	}

	w = doJSONRequest(t, mux, http.MethodGet, "/api/goals/"+created.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, mux, http.MethodGet, "/api/goals?status=draft", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", w.Code, w.Body.String())
	}
	var list []store.Goal
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	found := false
	for _, g := range list {
		if g.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created goal %s missing from status=draft filtered list", created.ID)
	}

	statusActive := store.GoalStatusActive
	newIntent := "ship it v2"
	w = doJSONRequest(t, mux, http.MethodPatch, "/api/goals/"+created.ID, goalPatchRequest{
		Intent: &newIntent,
		Status: &statusActive,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", w.Code, w.Body.String())
	}
	var patched store.Goal
	if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}
	if patched.Intent != newIntent {
		t.Fatalf("Intent = %q, want %q", patched.Intent, newIntent)
	}
	if patched.Status != store.GoalStatusActive {
		t.Fatalf("Status = %q, want active", patched.Status)
	}
	if patched.ActivatedAt == "" {
		t.Fatalf("ActivatedAt not set after status -> active")
	}

	w = doJSONRequest(t, mux, http.MethodDelete, "/api/goals/"+created.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, mux, http.MethodGet, "/api/goals/"+created.ID, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", w.Code)
	}
}

func TestLoopsAPI_GetGoal_UnknownID404s(t *testing.T) {
	_, mux := newTestAPI(t)
	w := doJSONRequest(t, mux, http.MethodGet, "/api/goals/does-not-exist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestLoopsAPI_CreateGoal_MissingIntentRejected(t *testing.T) {
	_, mux := newTestAPI(t)
	w := doJSONRequest(t, mux, http.MethodPost, "/api/goals", goalCreateRequest{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestLoopsAPI_ListGoalEvidence(t *testing.T) {
	a, mux := newTestAPI(t)
	ctx := context.Background()

	goal := store.Goal{Intent: "evidence goal"}
	if err := a.store.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := a.store.RecordGoalEvidence(ctx, &store.GoalEvidence{
		GoalID: goal.ID, EvidenceType: store.GoalEvidenceTypeTestSuite,
		RefTable: "workflow_run_steps", RefID: "step-1", Summary: "tests pass",
	}); err != nil {
		t.Fatalf("RecordGoalEvidence: %v", err)
	}

	w := doJSONRequest(t, mux, http.MethodGet, "/api/goals/"+goal.ID+"/evidence", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var rows []store.GoalEvidence
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 || rows[0].RefID != "step-1" {
		t.Fatalf("rows = %+v, want one row with ref_id step-1", rows)
	}

	w = doJSONRequest(t, mux, http.MethodGet, "/api/goals/does-not-exist/evidence", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("evidence for unknown goal status = %d, want 404", w.Code)
	}
}

// --- Loop launch ---

func TestLoopsAPI_LaunchLoop_NotWired503s(t *testing.T) {
	_, mux := newTestAPI(t)
	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops", loopLaunchRequest{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestLoopsAPI_CancelLoop_UnknownID404s(t *testing.T) {
	_, mux, _, _ := newTestAPIWithLoopLauncher(t)
	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/does-not-exist/cancel", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// --- ResolveEscalation ---

func TestLoopsAPI_ResolveEscalation_UnknownID404s(t *testing.T) {
	_, mux, _, _ := newTestAPIWithLoopLauncher(t)
	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/does-not-exist/resolve", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestLoopsAPI_LaunchRefusesMissingIssuerBeforeGoalOrRunEffects(t *testing.T) {
	a, mux, _, registry := newTestAPIWithLoopLauncher(t)
	registry.Register(oneStepLoopIterationDefinition("private-held-loop"))
	actor := createLoopsAPITestAgentProfile(t, a.store, "held-loop")
	for _, body := range []string{`{`, fmt.Sprintf(`{"inline_goal":{"intent":"never committed"},"definition_name":"private-held-loop","agent_profile_id":%q}`, actor.ID), `{"agent_profile_id":"claimed-host"}`} {
		before := retiredAgentState(t, a)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/loops", strings.NewReader(body)))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "verified actor") {
			t.Fatal(w.Code, w.Body.String())
		}
		assertRetiredAgentState(t, a, before)
		for _, table := range []string{"goals", "loop_runs", "workflow_runs", "durable_agent_instances", "actor_instances"} {
			var count int
			if err := a.store.DB.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial %s effects: %d %v", table, count, err)
			}
		}
	}
}
