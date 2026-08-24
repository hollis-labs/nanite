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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/loop"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// newTestAPIWithLoopLauncher builds on newTestAPI's real store/container,
// then wires the extra pieces main.go itself wires post-hoc for production
// traffic -- a fresh *agentworkflow.Registry, a real BuiltinWorkflowEngine,
// a real WorkflowLauncher (stub StepExecutor), a real *loop.LoopEngine, and
// this task's own LoopLauncher -- mirroring team_runs_test.go's own
// newTestAPIWithTeamRunLauncher shape one level up. Returns the registry
// too (unlike that helper) since these tests need to register plain
// WorkflowDefinitions directly, not compile one from a Team.
func newTestAPIWithLoopLauncher(t *testing.T) (*API, *http.ServeMux, *store.Store, *agentworkflow.Registry) {
	t.Helper()
	a, mux := newTestAPI(t)
	st := a.Services.Store

	registry := agentworkflow.NewRegistry(nil)
	engine := service.NewBuiltinWorkflowEngine(st)
	engines := map[string]agentworkflow.WorkflowEngine{agentworkflow.EngineBuiltin: engine}
	durable := service.NewDurableAgentService(st)
	launcher := service.NewWorkflowLauncher(registry, engines, &fakeAPIStepExecutor{}, durable)
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
	if err := st.CreateAgent(context.Background(), p); err != nil {
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
		Budget:         &store.Budget{MaxIterations: 1},
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
	if err := a.Services.Store.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := a.Services.Store.RecordGoalEvidence(ctx, &store.GoalEvidence{
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

func TestLoopsAPI_LaunchLoop_InlineGoal_CreatesGoalAndLoopRun(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-inline-agent")
	wf := oneStepLoopIterationDefinition("loops-api-inline-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp := launchLoopViaAPI(t, mux, loopLaunchRequest{
		InlineGoal:     &loopGoalSpecRequest{Intent: "ship via api", AcceptanceCriteria: []string{"tests pass"}},
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
		// Bounded on purpose: no goal evidence is ever recorded in this
		// test, so goal_met never fires -- an unbounded (zero-value)
		// Budget would make Decide return CONTINUE forever (see
		// internal/loop's own budgetExhausted doc comment). This test
		// only cares that Launch produced a real Goal+LoopRun.
		Budget: &store.Budget{MaxIterations: 1},
	})
	if resp.LoopRunID == "" {
		t.Fatalf("empty loop_run_id in launch response")
	}

	lr, err := st.GetLoopRun(context.Background(), resp.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	goal, err := st.GetGoal(context.Background(), lr.GoalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if goal.Intent != "ship via api" {
		t.Fatalf("goal.Intent = %q, want %q", goal.Intent, "ship via api")
	}
}

func TestLoopsAPI_LaunchLoop_ExistingGoalID_ReusesGoal(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-existing-agent")
	wf := oneStepLoopIterationDefinition("loops-api-existing-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	goal := store.Goal{Intent: "pre-existing api goal"}
	if err := st.CreateGoal(context.Background(), &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	goalID := goal.ID
	resp := launchLoopViaAPI(t, mux, loopLaunchRequest{
		GoalID:         &goalID,
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
		// Bounded on purpose -- see the identical comment on
		// TestLoopsAPI_LaunchLoop_InlineGoal_CreatesGoalAndLoopRun above.
		Budget: &store.Budget{MaxIterations: 1},
	})

	lr, err := st.GetLoopRun(context.Background(), resp.LoopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.GoalID != goal.ID {
		t.Fatalf("lr.GoalID = %q, want %q", lr.GoalID, goal.ID)
	}
}

func TestLoopsAPI_LaunchLoop_ConflictOnActiveGoal409s(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-conflict-agent")
	wf := oneStepLoopIterationDefinition("loops-api-conflict-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	goal := store.Goal{Intent: "conflict api goal"}
	if err := st.CreateGoal(context.Background(), &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	goalID := goal.ID

	// First launch escalates after one iteration -- waiting_on_escalation
	// is still an "active" status for the one-active-LoopRun-per-goal_id
	// check.
	launchLoopViaAPI(t, mux, loopLaunchRequest{
		GoalID:         &goalID,
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
		Budget:         &store.Budget{MaxIterations: 1},
	})

	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops", loopLaunchRequest{
		GoalID:         &goalID,
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("second launch status = %d, want 409, body = %s", w.Code, w.Body.String())
	}
}

func TestLoopsAPI_LaunchLoop_InvalidJSONBody400s(t *testing.T) {
	_, mux, _, _ := newTestAPIWithLoopLauncher(t)
	req := httptest.NewRequest(http.MethodPost, "/api/loops", bytes.NewBufferString("{not json"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// --- Get/List loops, iterations ---

func TestLoopsAPI_GetAndListLoops(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-list-agent")
	wf := oneStepLoopIterationDefinition("loops-api-list-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp := launchLoopViaAPI(t, mux, loopLaunchRequest{
		InlineGoal:     &loopGoalSpecRequest{Intent: "list me"},
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
		Budget:         &store.Budget{MaxIterations: 1},
	})

	w := doJSONRequest(t, mux, http.MethodGet, "/api/loops/"+resp.LoopRunID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", w.Code, w.Body.String())
	}
	var lr store.LoopRun
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if lr.ID != resp.LoopRunID {
		t.Fatalf("lr.ID = %q, want %q", lr.ID, resp.LoopRunID)
	}

	w = doJSONRequest(t, mux, http.MethodGet, "/api/loops?goal_id="+lr.GoalID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", w.Code, w.Body.String())
	}
	var list []store.LoopRun
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 || list[0].ID != resp.LoopRunID {
		t.Fatalf("list = %+v, want exactly one row matching %s", list, resp.LoopRunID)
	}

	w = doJSONRequest(t, mux, http.MethodGet, "/api/loops/"+resp.LoopRunID+"/iterations", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("iterations status = %d, body = %s", w.Code, w.Body.String())
	}
	var iterations []store.LoopRunIteration
	if err := json.Unmarshal(w.Body.Bytes(), &iterations); err != nil {
		t.Fatalf("unmarshal iterations: %v", err)
	}
	if len(iterations) != 1 {
		t.Fatalf("iterations = %+v, want exactly one row", iterations)
	}

	w = doJSONRequest(t, mux, http.MethodGet, "/api/loops/does-not-exist/iterations", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("iterations for unknown loop run status = %d, want 404", w.Code)
	}
}

// --- Cancel ---

func TestLoopsAPI_CancelLoop(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-cancel-agent")
	wf := oneStepLoopIterationDefinition("loops-api-cancel-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalationViaAPI(t, mux, wf.Name, profile.ID)

	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/"+loopRunID+"/cancel", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", w.Code, w.Body.String())
	}
	var lr store.LoopRun
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if lr.Status != store.LoopRunStatusCanceled {
		t.Fatalf("Status = %q, want canceled", lr.Status)
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

func TestLoopsAPI_ResolveEscalation_NoBody_ResumesNormally(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-resolve-noop-agent")
	wf := oneStepLoopIterationDefinition("loops-api-resolve-noop-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalationViaAPI(t, mux, wf.Name, profile.ID)

	req := httptest.NewRequest(http.MethodPost, "/api/loops/"+loopRunID+"/resolve", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp loopResultResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.CurrentIteration != 2 {
		t.Fatalf("CurrentIteration = %d, want 2 (resume should have launched a second iteration)", resp.CurrentIteration)
	}
}

func TestLoopsAPI_ResolveEscalation_ForceComplete(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-resolve-complete-agent")
	wf := oneStepLoopIterationDefinition("loops-api-resolve-complete-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalationViaAPI(t, mux, wf.Name, profile.ID)

	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/"+loopRunID+"/resolve", loop.EscalationOverride{ForceComplete: true})
	if w.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp loopResultResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != store.LoopRunStatusCompleted {
		t.Fatalf("Status = %q, want completed", resp.Status)
	}
}

func TestLoopsAPI_ResolveEscalation_ForceCancel(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-resolve-cancel-agent")
	wf := oneStepLoopIterationDefinition("loops-api-resolve-cancel-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	loopRunID := forceEscalationViaAPI(t, mux, wf.Name, profile.ID)

	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/"+loopRunID+"/resolve", loop.EscalationOverride{ForceCancel: true})
	if w.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp loopResultResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != store.LoopRunStatusCanceled {
		t.Fatalf("Status = %q, want canceled", resp.Status)
	}
}

func TestLoopsAPI_ResolveEscalation_Replan(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-resolve-replan-agent")
	wf := oneStepLoopIterationDefinition("loops-api-resolve-replan-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	revisedWF := oneStepLoopIterationDefinition("loops-api-resolve-replan-revised-def")
	if err := registry.Register(revisedWF); err != nil {
		t.Fatalf("Register (revised): %v", err)
	}
	loopRunID := forceEscalationViaAPI(t, mux, wf.Name, profile.ID)

	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/"+loopRunID+"/resolve", loop.EscalationOverride{
		Replan: &loop.ReplanOverride{
			DefinitionName: revisedWF.Name,
			DesiredState:   []string{"revised via api"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, body = %s", w.Code, w.Body.String())
	}

	lr, err := st.GetLoopRun(context.Background(), loopRunID)
	if err != nil {
		t.Fatalf("GetLoopRun: %v", err)
	}
	if lr.DefinitionName != revisedWF.Name {
		t.Fatalf("DefinitionName = %q, want %q", lr.DefinitionName, revisedWF.Name)
	}
}

func TestLoopsAPI_ResolveEscalation_NotWaitingOnEscalation409s(t *testing.T) {
	_, mux, st, registry := newTestAPIWithLoopLauncher(t)
	profile := createLoopsAPITestAgentProfile(t, st, "loops-api-resolve-notwaiting-agent")
	wf := oneStepLoopIterationDefinition("loops-api-resolve-notwaiting-def")
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	goal := store.Goal{Intent: "already satisfied api goal"}
	if err := goal.SetAcceptanceCriteria([]string{"done"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(context.Background(), &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if err := st.RecordGoalEvidence(context.Background(), &store.GoalEvidence{
		GoalID: goal.ID, EvidenceType: store.GoalEvidenceTypeTestSuite,
		RefTable: "workflow_run_steps", RefID: "step-1", Summary: "done",
	}); err != nil {
		t.Fatalf("RecordGoalEvidence: %v", err)
	}

	goalID := goal.ID
	resp := launchLoopViaAPI(t, mux, loopLaunchRequest{
		GoalID:         &goalID,
		DefinitionName: wf.Name,
		AgentProfileID: profile.ID,
		Budget:         &store.Budget{MaxIterations: 10},
	})
	if resp.Status != store.LoopRunStatusCompleted {
		t.Fatalf("Status = %q, want completed", resp.Status)
	}

	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/"+resp.LoopRunID+"/resolve", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", w.Code, w.Body.String())
	}
}

func TestLoopsAPI_ResolveEscalation_UnknownID404s(t *testing.T) {
	_, mux, _, _ := newTestAPIWithLoopLauncher(t)
	w := doJSONRequest(t, mux, http.MethodPost, "/api/loops/does-not-exist/resolve", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
