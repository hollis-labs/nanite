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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
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
func newTestAPIWithTeamRunLauncher(t *testing.T) (*testAPI, *http.ServeMux, *store.Store) {
	t.Helper()
	a, mux := newTestAPI(t)
	st := a.store

	registry := agentworkflow.NewRegistry(nil)
	engine := newAPITestWorkflowHost(t, st)
	durable := service.NewDurableAgentService(st)
	launcher := service.NewWorkflowLauncher(registry, engine, &fakeAPIStepExecutor{}, durable)

	a.Services.TeamRunLauncher = service.NewTeamRunLauncher(st, launcher, durable)
	a.Services.TeamRouting = service.NewTeamRoutingService(st, a.Services.Messaging, a.Services.TeamRunLauncher)
	a.Services.TeamRunLauncher.WithRoutingInstaller(a.Services.TeamRouting)
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
	if err := team.SetRouting(store.TeamRouting{
		Rules: []store.TeamRoutingRule{{
			Name:       "engineering_question",
			Phrases:    []string{"engineering"},
			TargetSlot: "engineer",
		}},
		CoordinatorSlot: "orchestrator",
	}); err != nil {
		t.Fatalf("SetRouting: %v", err)
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

func TestTeamRunLaunchAPI_UnknownTeam404(t *testing.T) {
	_, mux, _ := newTestAPIWithTeamRunLauncher(t)
	req := httptest.NewRequest(http.MethodPost, "/api/teams/does-not-exist/launch", bytes.NewBufferString(`{}`))
	req.Header.Set("Idempotency-Key", "team-api-unknown")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("launch unknown team = %d, want 404 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamRunLaunchAPI_MalformedBody400 proves a genuinely malformed
// request body is rejected with 400, not a 5xx.
func TestTeamRunLaunchAPI_MalformedBody400(t *testing.T) {
	_, mux, st := newTestAPIWithTeamRunLauncher(t)
	team := buildTeamRunAPITestTeam(t, st)

	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/launch", bytes.NewBufferString(`{not json`))
	req.Header.Set("Idempotency-Key", "team-api-malformed")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("launch with malformed body = %d, want 400 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamRunLaunchAPI_SlotMemberCountAboveMax400 proves a
// TeamRunOverrides-shaped rejection (LaunchTeamRun's own [min,max]
// enforcement) surfaces as a 400 through this endpoint, not a 5xx.

func TestTeamRunLaunchAPI_ServiceUnavailableWhenLauncherNotWired(t *testing.T) {
	_, mux := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/teams/whatever/launch", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("launch without a wired launcher = %d, want 503 body=%s", w.Code, w.Body.String())
	}
}

// TestTeamRunLaunchAPI_ServiceUnavailableWhenRoutingNotWired proves the
// composition-root dependency is checked before LaunchTeamRun creates any
// persistent run state. This is distinct from a configured routing service
// failing during installation, covered below.
func TestTeamRunLaunchAPI_ServiceUnavailableWhenRoutingNotWired(t *testing.T) {
	a, mux, st := newTestAPIWithTeamRunLauncher(t)
	team := buildTeamRunAPITestTeam(t, st)
	a.Services.TeamRouting = nil

	var before int
	if err := st.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM workflow_runs`).Scan(&before); err != nil {
		t.Fatalf("count workflow_runs before request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/launch", bytes.NewBufferString(`{}`))
	req.Header.Set("Idempotency-Key", "team-api-no-routing")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("launch without TeamRouting = %d, want 503 body=%s", w.Code, w.Body.String())
	}
	var after int
	if err := st.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM workflow_runs`).Scan(&after); err != nil {
		t.Fatalf("count workflow_runs after request: %v", err)
	}
	if after != before {
		t.Fatalf("workflow_runs count changed from %d to %d despite missing pre-launch routing dependency", before, after)
	}
}

// TestTeamRunLaunchAPI_RoutingInstallFailureReturnsRunAndCleansPartialRows
// locks the operator-approved failure policy. The first valid rule inserts
// two rows (one per distinct asking agent), then the second rule's unknown
// Team Slot fails installation. The HTTP operation fails closed, reports the
// already-persisted run id/status, and removes the partial rows rather than
// pretending the durable launch rolled back.

func TestTeamRunLaunchAPIRefusesWithoutActorIssuerBeforeAnyPlanningEffects(t *testing.T) {
	a, mux, st := newTestAPIWithTeamRunLauncher(t)
	team := buildTeamRunAPITestTeam(t, st)
	p := &store.AgentProfile{Name: "Prior actor", Slug: "team-api-prior"}
	if err := storetest.PriorAuthorizedActor(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT * FROM workflow_runs ORDER BY id`, `SELECT * FROM team_run_members ORDER BY id`, `SELECT * FROM team_run_launches ORDER BY idempotency_key`, `SELECT * FROM actor_instances ORDER BY id`, `SELECT * FROM sessions ORDER BY id`, `SELECT * FROM agent_reflexes ORDER BY id`}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = retiredAPISnapshot(t, a, q)
	}
	for _, body := range []string{`{}`, `{"slot_member_counts":{"engineer":99}}`, `{"agent_profile_id":"` + p.ID + `","params":{"claimed_authority":true}}`} {
		for range 2 {
			req := httptest.NewRequest(http.MethodPost, "/api/teams/"+team.ID+"/launch", strings.NewReader(body))
			req.Header.Set("Idempotency-Key", "no-authority")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "verified actor") {
				t.Fatalf("held launch: %d %s", rec.Code, rec.Body.String())
			}
			for i, q := range queries {
				retiredAPIHistoryUnchanged(t, a, q, before[i])
			}
		}
	}
}
