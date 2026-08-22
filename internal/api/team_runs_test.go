package api

// TASKS/teams/11-team-run-launch-api.md's own end-to-end "Done means"
// coverage: a launch through the real, chosen surface (POST
// /api/teams/{id}/launch), not by calling TeamRunLauncher.LaunchTeamRun
// directly, reaches a real workflow_runs row and correct team_run_members
// rows -- plus the required registry-growth/agent-card-pollution
// regression, exercised end-to-end through the real
// GET /.well-known/agent-card.json handler.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// newTestAPIWithTeamRunLauncher builds on newTestAPI's real store/container,
// then wires the extra pieces main.go itself wires post-NewContainer for
// production traffic -- a fresh *agentworkflow.Registry, a real
// BuiltinWorkflowEngine with flex support, a real WorkflowLauncher, and
// TeamRunLauncher (this task's own container.TeamRunLauncher wiring) --
// plus an AgentCardGenerator built from that SAME registry instance, so the
// registry-growth/agent-card-pollution assertions below exercise the exact
// shared-registry relationship cmd/nanite/main.go's own doc comments
// describe.
func newTestAPIWithTeamRunLauncher(t *testing.T) (*API, *http.ServeMux, *store.Store) {
	t.Helper()
	a, mux := newTestAPI(t)
	st := a.Services.Store

	registry := agentworkflow.NewRegistry(nil)
	engine := service.NewBuiltinWorkflowEngine(st).WithFlexSupport(st, &reflexes.StateCollector{Store: st, Window: 5})
	engines := map[string]agentworkflow.WorkflowEngine{agentworkflow.EngineBuiltin: engine}
	durable := service.NewDurableAgentService(st)
	launcher := service.NewWorkflowLauncher(registry, engines, &fakeAPIStepExecutor{}, durable)

	a.Services.TeamRunLauncher = service.NewTeamRunLauncher(st, registry, launcher, durable)
	a.Services.AgentCardGenerator = service.NewAgentCardGenerator(registry, "http://example.test", "test")

	return a, mux, st
}

// fakeAPIStepExecutor mirrors internal/service's own fakeStepExecutor
// (team_run_launcher_test.go): never actually invoked by any test here,
// since the phase sequences below have only flex/gate steps.
type fakeAPIStepExecutor struct{}

func (fakeAPIStepExecutor) ExecuteLLMStep(context.Context, agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	return agentworkflow.LLMStepResult{}, nil
}

func (fakeAPIStepExecutor) ExecuteToolStep(context.Context, agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	return agentworkflow.ToolStepResult{}, nil
}

func (fakeAPIStepExecutor) Verify(context.Context, agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{}, nil
}

// createTeamRunTestRoleBoundAgent mirrors internal/service's own
// createTestRoleBoundAgent (team_run_launcher_test.go) -- a Role plus one
// AgentProfile bound to it via role_id, the concrete Agent a
// "resolution: fresh" Team Slot's role_slug resolves against.
func createTeamRunTestRoleBoundAgent(t *testing.T, st *store.Store, roleSlug string) *store.AgentProfile {
	t.Helper()
	role := &store.Role{
		Slug:            roleSlug,
		Name:            roleSlug,
		SystemPrompt:    "you are the " + roleSlug + " role",
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-test-model",
	}
	if err := st.CreateRole(context.Background(), role); err != nil {
		t.Fatalf("CreateRole(%s): %v", roleSlug, err)
	}
	profile := &store.AgentProfile{
		Name:            roleSlug + " agent",
		Slug:            roleSlug + "-agent-" + role.ID[:8],
		SystemPrompt:    "you are a " + roleSlug + " agent",
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-test-model",
		RoleID:          role.ID,
	}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent(%s): %v", roleSlug, err)
	}
	return profile
}

// buildTeamRunAPITestTeam creates a minimal two-slot, one-flex-phase Team
// (a smaller shape than the full SME example -- this file's own concern is
// the API boundary, not slot-resolution edge cases already covered by
// internal/service/team_run_launcher_test.go's own thorough suite).
func buildTeamRunAPITestTeam(t *testing.T, st *store.Store) *store.Team {
	t.Helper()
	ctx := context.Background()

	slots := []store.TeamSlotDefinition{
		{Name: "orchestrator", RoleSlug: "orchestrator", Resolution: "fresh", ActivationMode: "singleton", Required: true},
		{Name: "engineer", RoleSlug: "engineer", Resolution: "fresh", ActivationMode: "concurrent", Min: 1, Max: 4, Required: false},
	}
	phases := []store.TeamPhase{
		{ID: "scope_work", Kind: "flex", ActiveSlots: []string{"orchestrator", "engineer"}, ExitTrigger: map[string]any{"event": "done"}},
	}

	team := &store.Team{Name: "API Launch Test Team"}
	if err := team.SetSlots(slots); err != nil {
		t.Fatalf("SetSlots: %v", err)
	}
	if err := team.SetPhases(phases); err != nil {
		t.Fatalf("SetPhases: %v", err)
	}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "orchestrator", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "engineer",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}
	return team
}

// TestTeamRunLaunchAPI_EndToEnd_ReachesRealWorkflowRunAndTeamRunMembers is
// this task's own required "Done means" test: a launch through the real
// POST /api/teams/{id}/launch surface -- not a direct LaunchTeamRun call --
// reaches a real workflow_runs row and correct team_run_members rows.
func TestTeamRunLaunchAPI_EndToEnd_ReachesRealWorkflowRunAndTeamRunMembers(t *testing.T) {
	_, mux, st := newTestAPIWithTeamRunLauncher(t)
	createTeamRunTestRoleBoundAgent(t, st, "orchestrator")
	createTeamRunTestRoleBoundAgent(t, st, "engineer")
	team := buildTeamRunAPITestTeam(t, st)

	body := `{"slot_member_counts":{"engineer":2},"params":{"objective":"ship it"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/launch", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("launch = %d body=%s", w.Code, w.Body.String())
	}

	var resp teamLaunchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.WorkflowRunID == "" {
		t.Fatal("response.workflow_run_id is empty")
	}
	if resp.Status != string(agentworkflow.RunStatusWaitingOnFlex) {
		t.Fatalf("response.status = %q, want %q", resp.Status, agentworkflow.RunStatusWaitingOnFlex)
	}
	if len(resp.Members) == 0 {
		t.Fatal("response.members is empty, want the resolved team_run_members rows")
	}

	// A real workflow_runs row exists in the store, independent of what
	// the HTTP response claims.
	run, err := st.GetWorkflowRun(context.Background(), resp.WorkflowRunID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.Status != string(agentworkflow.RunStatusWaitingOnFlex) {
		t.Fatalf("persisted run status = %q, want %q", run.Status, agentworkflow.RunStatusWaitingOnFlex)
	}

	// Correct team_run_members rows: orchestrator (required, 1) + engineer
	// (concurrent override, 2).
	members, err := st.ListTeamRunMembersByRun(context.Background(), resp.WorkflowRunID)
	if err != nil {
		t.Fatalf("ListTeamRunMembersByRun: %v", err)
	}
	bySlot := map[string]int{}
	for _, m := range members {
		bySlot[m.SlotName]++
		if m.WorkflowRunID != resp.WorkflowRunID {
			t.Fatalf("member workflow_run_id = %q, want %q", m.WorkflowRunID, resp.WorkflowRunID)
		}
	}
	if bySlot["orchestrator"] != 1 {
		t.Fatalf("orchestrator members = %d, want 1", bySlot["orchestrator"])
	}
	if bySlot["engineer"] != 2 {
		t.Fatalf("engineer members = %d, want 2 (slot_member_counts override)", bySlot["engineer"])
	}
	if len(members) != len(resp.Members) {
		t.Fatalf("response.members len = %d, store members len = %d, want equal", len(resp.Members), len(members))
	}
}

// TestTeamRunLaunchAPI_UnknownTeam404 proves the not-found path, matching
// task 10's own teams.go CRUD posture (errors.Is-dispatched 404).
func TestTeamRunLaunchAPI_UnknownTeam404(t *testing.T) {
	_, mux, _ := newTestAPIWithTeamRunLauncher(t)
	req := httptest.NewRequest(http.MethodPost, "/api/teams/does-not-exist/launch", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("launch unknown team = %d, want 404 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamRunLaunchAPI_MalformedBody400 proves a genuinely malformed
// request body is rejected with 400, not a 5xx.
func TestTeamRunLaunchAPI_MalformedBody400(t *testing.T) {
	_, mux, st := newTestAPIWithTeamRunLauncher(t)
	createTeamRunTestRoleBoundAgent(t, st, "orchestrator")
	createTeamRunTestRoleBoundAgent(t, st, "engineer")
	team := buildTeamRunAPITestTeam(t, st)

	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/launch", bytes.NewBufferString(`{not json`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("launch with malformed body = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamRunLaunchAPI_SlotMemberCountAboveMax400 proves a
// TeamRunOverrides-shaped rejection (LaunchTeamRun's own [min,max]
// enforcement) surfaces as a 400 through this endpoint, not a 5xx.
func TestTeamRunLaunchAPI_SlotMemberCountAboveMax400(t *testing.T) {
	_, mux, st := newTestAPIWithTeamRunLauncher(t)
	createTeamRunTestRoleBoundAgent(t, st, "orchestrator")
	createTeamRunTestRoleBoundAgent(t, st, "engineer")
	team := buildTeamRunAPITestTeam(t, st)

	body := `{"slot_member_counts":{"engineer":99}}`
	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/launch", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("launch with out-of-range override = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamRunLaunchAPI_ServiceUnavailableWhenLauncherNotWired confirms the
// endpoint degrades to 503 (not a panic or a misleading 500) when
// TeamRunLauncher isn't wired -- e.g. a Container built directly via
// service.NewContainer without main.go's own post-hoc wiring, the same
// nil-checked-at-use posture AgentCardGenerator/TaskManager already use.
func TestTeamRunLaunchAPI_ServiceUnavailableWhenLauncherNotWired(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/teams/whatever/launch", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("launch without a wired launcher = %d, want 503 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamRunLaunchAPI_RegistryGrowth_TeamRunDefinitionNeverPollutesAgentCard
// is this task's required registry-growth/agent-card-pollution regression,
// exercised end-to-end: launch a real Team through the real HTTP surface
// (leaving its compiled definition registered and reachable -- the run
// lands in waiting_on_flex, the common case per 15-teams.md's own
// illustrative examples, so Registry.Unregister's own narrower terminal-
// only eviction does NOT fire here), then fetch the real
// GET /.well-known/agent-card.json response and confirm the compiled
// TeamRun definition never appears as a public skill, even though it is
// still registered and still reachable by name (a later Resume call needs
// exactly that).
func TestTeamRunLaunchAPI_RegistryGrowth_TeamRunDefinitionNeverPollutesAgentCard(t *testing.T) {
	_, mux, st := newTestAPIWithTeamRunLauncher(t)
	createTeamRunTestRoleBoundAgent(t, st, "orchestrator")
	createTeamRunTestRoleBoundAgent(t, st, "engineer")
	team := buildTeamRunAPITestTeam(t, st)

	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/launch", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("launch = %d body=%s", w.Code, w.Body.String())
	}
	var resp teamLaunchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode launch response: %v", err)
	}
	if resp.Status != string(agentworkflow.RunStatusWaitingOnFlex) {
		t.Fatalf("launch response.status = %q, want %q (registry entry must still be live for this assertion to be meaningful)", resp.Status, agentworkflow.RunStatusWaitingOnFlex)
	}

	req = httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent-card = %d body=%s", w.Code, w.Body.String())
	}
	var card a2a.AgentCard
	if err := json.NewDecoder(w.Body).Decode(&card); err != nil {
		t.Fatalf("decode agent card: %v", err)
	}
	for _, sk := range card.Skills {
		if agentworkflow.IsTeamRunDefinitionName(sk.ID) {
			t.Fatalf("a per-launch compiled TeamRun definition leaked into the public agent card: %+v", sk)
		}
	}
}
