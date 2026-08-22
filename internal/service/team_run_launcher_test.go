package service

// TASKS/teams/08-team-run-launcher.md's own regression coverage.
//
// Live-verification safety (EXECUTION-PROCESS.md, this task file's own
// "Done means"): every test below constructs synthetic, fixture-only Roles
// and AgentProfiles via the real store API against a t.TempDir()-rooted
// SQLite database (newTestWorkflowStore, already used across this whole
// batch's other test files) — never a real tracked .nanite/agents/*.md
// file (no test here ever touches the filesystem at all beyond that
// scratch DB) and never a real production durable_agent_instances row
// (every DurableAgentInstance here is created fresh, in-memory-DB, by
// this file's own test setup).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- fixtures ---

// newTeamRunLauncherTestFixtures wires a real (t.TempDir()-scoped)
// *store.Store, a fresh *agentworkflow.Registry, a real BuiltinWorkflowEngine
// with WithFlexSupport wired (mirroring cmd/nanite/main.go's own production
// wiring, TASKS/teams/06-stepkindflex-executor.md's Work Log), a real
// WorkflowLauncher, and the TeamRunLauncher under test — the same
// dependency shape production wiring uses, no mocks below the
// fakeStepExecutor (which is never actually called by any test here: the
// SME example's phase sequence has no llm/tool steps, only flex/gate).
func newTeamRunLauncherTestFixtures(t *testing.T) (*store.Store, *agentworkflow.Registry, *TeamRunLauncher) {
	t.Helper()
	st := newTestWorkflowStore(t)
	registry := agentworkflow.NewRegistry(nil)
	engine := NewBuiltinWorkflowEngine(st).WithFlexSupport(st, &reflexes.StateCollector{Store: st, Window: 5})
	engines := map[string]agentworkflow.WorkflowEngine{agentworkflow.EngineBuiltin: engine}
	durable := NewDurableAgentService(st)
	wl := NewWorkflowLauncher(registry, engines, &fakeStepExecutor{}, durable)
	trl := NewTeamRunLauncher(st, registry, wl, durable)
	return st, registry, trl
}

// createTestRoleBoundAgent creates a Role (roleSlug) and one AgentProfile
// bound to it via role_id — the concrete Agent a "resolution: fresh" Team
// Slot's RoleSlug resolves against (ListAgentsByRoleID, this task's own
// small store addition). Both rows are synthetic fixtures in the test's
// own scratch DB — no source_ref set, so this never touches any real
// tracked agent file.
func createTestRoleBoundAgent(t *testing.T, st *store.Store, roleSlug string) *store.AgentProfile {
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

// createTestDurableCandidateAgent creates a plain, standalone AgentProfile
// with no role_id — the profile a "resolution: durable" Team Slot's
// AgentID names. Deliberately does NOT set Durable=true or a
// "durable-agent" tag — this task's own corrected-semantics finding is
// that resolveDurableMember must work regardless of that unrelated flag.
func createTestDurableCandidateAgent(t *testing.T, st *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	profile := &store.AgentProfile{
		Name:         slug + " agent",
		Slug:         slug,
		SystemPrompt: "you are " + slug,
	}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent(%s): %v", slug, err)
	}
	return profile
}

// buildSMETeam creates a Team matching docs/engineering/architecture/
// 15-teams.md's Feature Development SME example verbatim: four Team Slots
// (architect: durable/dormant, orchestrator: fresh/required,
// engineer: fresh/concurrent min:1 max:4, reviewer: fresh/required), the
// same four-phase sequence (scope_work flex -> review_gate gate ->
// address_feedback flex -> merge_gate gate), and the SME's own literal
// authority grant (orchestrator.may_spawn: [engineer, reviewer]) — note
// this grant does NOT name "architect": the design doc's own illustrative
// authority list only ever authorizes engineer/reviewer, consistent with
// this task's own reading that resolving/waking architect is a message-
// authority (may_message, task 09) concern, not a may_spawn one — see
// TestResolveLazySlot_* below, which uses its own separate fixture with an
// explicit may_spawn grant for its own dormant slot instead of assuming
// this one covers it.
func buildSMETeam(t *testing.T, st *store.Store, architectProfileID string) *store.Team {
	t.Helper()
	ctx := context.Background()

	arch := architectProfileID
	slots := []store.TeamSlotDefinition{
		{Name: "architect", RoleSlug: "architecture-sme", Resolution: "durable", AgentID: &arch, ActivationMode: "singleton", Required: false},
		{Name: "orchestrator", RoleSlug: "orchestrator", Resolution: "fresh", ActivationMode: "singleton", Required: true},
		{Name: "engineer", RoleSlug: "engineer", Resolution: "fresh", ActivationMode: "concurrent", Min: 1, Max: 4, Required: false},
		{Name: "reviewer", RoleSlug: "code-reviewer", Resolution: "fresh", ActivationMode: "singleton", Required: true},
	}
	phases := []store.TeamPhase{
		{ID: "scope_work", Kind: "flex", ActiveSlots: []string{"orchestrator", "engineer", "architect"}, ExitTrigger: map[string]any{"self_tool": "mark_ready_for_review"}},
		{ID: "review_gate", Kind: "gate", ApproverSlot: "reviewer"},
		{ID: "address_feedback", Kind: "flex", ActiveSlots: []string{"engineer", "reviewer", "architect"}, ExitTrigger: map[string]any{"event": "review_approved"}},
		{ID: "merge_gate", Kind: "gate", ApproverSlot: "operator"},
	}

	team := &store.Team{Name: "Feature Development"}
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
		t.Fatalf("CreateTeamAuthorityGrant(engineer): %v", err)
	}
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "orchestrator", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "reviewer",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant(reviewer): %v", err)
	}
	return team
}

// --- tests ---

// TestLaunchTeamRun_SMEExample_ReachesFirstFlexStepWaiting is this task's
// "Done means" end-to-end requirement: launching the SME example Team
// produces a real workflow_runs row, correct team_run_members rows for
// every resolved slot, and reaches the first flex step in a waiting
// state.
func TestLaunchTeamRun_SMEExample_ReachesFirstFlexStepWaiting(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	createTestRoleBoundAgent(t, st, "orchestrator")
	createTestRoleBoundAgent(t, st, "engineer")
	createTestRoleBoundAgent(t, st, "code-reviewer")
	architect := createTestDurableCandidateAgent(t, st, "nanite-architect")

	team := buildSMETeam(t, st, architect.ID)

	result, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{})
	if err != nil {
		t.Fatalf("LaunchTeamRun: %v", err)
	}
	if result.RunID == "" {
		t.Fatal("result.RunID is empty")
	}
	if result.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("Status = %q, want %q (error=%s)", result.Status, agentworkflow.RunStatusWaitingOnFlex, result.Error)
	}

	// A real workflow_runs row exists.
	run, err := st.GetWorkflowRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.Status != string(agentworkflow.RunStatusWaitingOnFlex) {
		t.Fatalf("persisted run status = %q, want %q", run.Status, agentworkflow.RunStatusWaitingOnFlex)
	}

	// The first phase (scope_work) is a real, persisted waiting_on_flex
	// step.
	steps, err := st.ListWorkflowRunSteps(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	var scopeWork *store.WorkflowRunStepRow
	for _, s := range steps {
		if s.StepID == "scope_work" {
			scopeWork = s
		}
	}
	if scopeWork == nil {
		t.Fatal("no scope_work step persisted")
	}
	if scopeWork.Status != "waiting_on_flex" {
		t.Fatalf("scope_work status = %q, want waiting_on_flex", scopeWork.Status)
	}

	// Correct team_run_members rows for every resolved slot: orchestrator
	// (required), engineer (elastic, min-eager, 1 member), reviewer
	// (required) — architect (dormant, required:false, min:0) is NOT
	// resolved.
	members, err := st.ListTeamRunMembersByRun(ctx, result.RunID)
	if err != nil {
		t.Fatalf("ListTeamRunMembersByRun: %v", err)
	}
	bySlot := map[string][]store.TeamRunMember{}
	for _, m := range members {
		bySlot[m.SlotName] = append(bySlot[m.SlotName], m)
	}
	if len(bySlot["orchestrator"]) != 1 {
		t.Fatalf("orchestrator members = %d, want 1", len(bySlot["orchestrator"]))
	}
	if len(bySlot["engineer"]) != 1 {
		t.Fatalf("engineer members = %d, want 1 (min-eager)", len(bySlot["engineer"]))
	}
	if len(bySlot["reviewer"]) != 1 {
		t.Fatalf("reviewer members = %d, want 1", len(bySlot["reviewer"]))
	}
	if len(bySlot["architect"]) != 0 {
		t.Fatalf("architect members = %d, want 0 (lazy/dormant, never resolved at launch)", len(bySlot["architect"]))
	}
	for slot, ms := range bySlot {
		for _, m := range ms {
			if m.AgentID == "" || m.SessionID == "" {
				t.Fatalf("slot %q member has empty agent_id/session_id: %+v", slot, m)
			}
			if m.WorkflowRunID != result.RunID {
				t.Fatalf("slot %q member workflow_run_id = %q, want %q", slot, m.WorkflowRunID, result.RunID)
			}
			if m.Status != store.TeamRunMemberStatusActive {
				t.Fatalf("slot %q member status = %q, want active", slot, m.Status)
			}
		}
	}
}

// TestLaunchTeamRun_ConcurrentSlot_MinEagerMultiMemberResolution covers
// this task's other required "Done means" test: min-eager multi-member
// resolution for a concurrent slot, plus an invocation-time override count
// between min and max.
func TestLaunchTeamRun_ConcurrentSlot_MinEagerMultiMemberResolution(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	createTestRoleBoundAgent(t, st, "orchestrator")
	createTestRoleBoundAgent(t, st, "engineer")
	createTestRoleBoundAgent(t, st, "code-reviewer")
	architect := createTestDurableCandidateAgent(t, st, "nanite-architect")
	team := buildSMETeam(t, st, architect.ID)

	t.Run("default resolves slot.Min eagerly", func(t *testing.T) {
		result, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{})
		if err != nil {
			t.Fatalf("LaunchTeamRun: %v", err)
		}
		members, err := st.ListTeamRunMembersBySlot(ctx, result.RunID, "engineer")
		if err != nil {
			t.Fatalf("ListTeamRunMembersBySlot: %v", err)
		}
		if len(members) != 1 {
			t.Fatalf("engineer members = %d, want 1 (slot.Min default)", len(members))
		}
	})

	t.Run("override count between min and max resolves that many distinct sessions on the same agent", func(t *testing.T) {
		result, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{
			SlotMemberCounts: map[string]int{"engineer": 3},
		})
		if err != nil {
			t.Fatalf("LaunchTeamRun: %v", err)
		}
		members, err := st.ListTeamRunMembersBySlot(ctx, result.RunID, "engineer")
		if err != nil {
			t.Fatalf("ListTeamRunMembersBySlot: %v", err)
		}
		if len(members) != 3 {
			t.Fatalf("engineer members = %d, want 3", len(members))
		}
		seenSessions := map[string]bool{}
		agentID := ""
		for _, m := range members {
			if agentID == "" {
				agentID = m.AgentID
			}
			if m.AgentID != agentID {
				t.Fatalf("concurrent members resolved to different agent_ids: %q vs %q — a concurrent fresh slot should reuse the same resolved Agent across members", m.AgentID, agentID)
			}
			if seenSessions[m.SessionID] {
				t.Fatalf("duplicate session_id %q across concurrent engineer members", m.SessionID)
			}
			seenSessions[m.SessionID] = true
		}
	})

	t.Run("override count above max is rejected", func(t *testing.T) {
		_, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{
			SlotMemberCounts: map[string]int{"engineer": 5},
		})
		if err == nil || !strings.Contains(err.Error(), "exceeds the slot's own max") {
			t.Fatalf("err = %v, want max-exceeded rejection", err)
		}
	})

	t.Run("override count below min is rejected", func(t *testing.T) {
		_, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{
			SlotMemberCounts: map[string]int{"engineer": 0},
		})
		if err == nil || !strings.Contains(err.Error(), "below the slot's own min") {
			t.Fatalf("err = %v, want min-violation rejection", err)
		}
	})
}

// TestLaunchTeamRun_ElasticSlotWithoutAuthorityGrant_Rejected is this
// task's required may_spawn-enforcement test: an unauthorized elastic
// resolution attempt is rejected, not silently skipped or silently
// allowed.
func TestLaunchTeamRun_ElasticSlotWithoutAuthorityGrant_Rejected(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	createTestRoleBoundAgent(t, st, "orchestrator")
	createTestRoleBoundAgent(t, st, "engineer")
	createTestRoleBoundAgent(t, st, "code-reviewer")
	architect := createTestDurableCandidateAgent(t, st, "nanite-architect")

	// Same team shape as buildSMETeam, but with NO authority grants at
	// all — engineer (elastic, min:1) has nothing authorizing its launch-
	// time eager resolution.
	arch := architect.ID
	slots := []store.TeamSlotDefinition{
		{Name: "architect", RoleSlug: "architecture-sme", Resolution: "durable", AgentID: &arch, Required: false},
		{Name: "orchestrator", RoleSlug: "orchestrator", Resolution: "fresh", ActivationMode: "singleton", Required: true},
		{Name: "engineer", RoleSlug: "engineer", Resolution: "fresh", ActivationMode: "concurrent", Min: 1, Max: 4, Required: false},
		{Name: "reviewer", RoleSlug: "code-reviewer", Resolution: "fresh", ActivationMode: "singleton", Required: true},
	}
	phases := []store.TeamPhase{
		{ID: "scope_work", Kind: "flex", ActiveSlots: []string{"orchestrator", "engineer"}, ExitTrigger: map[string]any{"event": "done"}},
	}
	team := &store.Team{Name: "Ungoverned Team"}
	if err := team.SetSlots(slots); err != nil {
		t.Fatalf("SetSlots: %v", err)
	}
	if err := team.SetPhases(phases); err != nil {
		t.Fatalf("SetPhases: %v", err)
	}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}

	_, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{})
	if err == nil {
		t.Fatal("LaunchTeamRun succeeded, want rejection for unauthorized elastic slot")
	}
	if !errors.Is(err, ErrTeamRunElasticResolutionUnauthorized) {
		t.Fatalf("err = %v, want wrapping ErrTeamRunElasticResolutionUnauthorized", err)
	}

	// No side effects: no run was ever launched, so there is nothing to
	// find a team_run_members row against — reject-before-resolve, not
	// resolve-then-rollback. The authority pre-check runs to completion
	// (planEagerResolution) before Step 1 of LaunchTeamRun ever resolves a
	// single member, so not even the required orchestrator/reviewer slots
	// should have created a session.
	var sessionCount int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Fatalf("expected zero sessions created before the authorization check failed, got %d", sessionCount)
	}
}

// TestAuthorizedForElasticResolution_FailsClosed exercises
// authorizedForElasticResolution directly (unit-level, not through the
// full LaunchTeamRun path): no grant at all, and a grant for a different
// verb/target, both fail closed.
func TestAuthorizedForElasticResolution_FailsClosed(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	team := &store.Team{Name: "Fail Closed Team"}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	slots := []store.TeamSlotDefinition{
		{Name: "lead", RoleSlug: "lead", Resolution: "fresh", ActivationMode: "singleton", Required: true},
	}

	ok, err := trl.authorizedForElasticResolution(ctx, team.ID, slots, "worker")
	if err != nil {
		t.Fatalf("authorizedForElasticResolution: %v", err)
	}
	if ok {
		t.Fatal("expected false with zero grants, got true")
	}

	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
		TeamID: team.ID, FromSlot: "lead", Verb: store.TeamAuthorityVerbMayMessage, ToSlot: "worker",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}
	ok, err = trl.authorizedForElasticResolution(ctx, team.ID, slots, "worker")
	if err != nil {
		t.Fatalf("authorizedForElasticResolution: %v", err)
	}
	if ok {
		t.Fatal("a may_message grant must not authorize may_spawn elastic resolution — expected false, got true")
	}
}

// TestResolveLazySlot_ResolvesDormantSlotAndIsIdempotent is this task's
// documented lazy-resolution mechanism, exercised directly: a
// required:false, min:0 ("normally dormant") Team Slot is not resolved by
// LaunchTeamRun at all, and ResolveLazySlot resolves it on demand,
// idempotently.
//
// Uses its own minimal fixture (not buildSMETeam's literal authority set)
// with an explicit may_spawn grant naming the dormant slot — the SME
// example's own illustrative authority list (orchestrator.may_spawn:
// [engineer, reviewer]) never names "architect" at all, which this test's
// sibling (TestLaunchTeamRun_SMEExample_ReachesFirstFlexStepWaiting)
// deliberately does not attempt to lazily-wake, precisely because it
// wouldn't be authorized under buildSMETeam's own grants — a real,
// documented finding (see buildSMETeam's own doc comment), not test
// rigging.
func TestResolveLazySlot_ResolvesDormantSlotAndIsIdempotent(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	createTestRoleBoundAgent(t, st, "lead")
	architect := createTestDurableCandidateAgent(t, st, "architect")

	arch := architect.ID
	slots := []store.TeamSlotDefinition{
		{Name: "lead", RoleSlug: "lead", Resolution: "fresh", ActivationMode: "singleton", Required: true},
		{Name: "architect", RoleSlug: "architecture-sme", Resolution: "durable", AgentID: &arch, Required: false},
	}
	phases := []store.TeamPhase{
		{ID: "work", Kind: "flex", ActiveSlots: []string{"lead"}, ExitTrigger: map[string]any{"event": "done"}},
	}
	team := &store.Team{Name: "Lazy Wake Team"}
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
		TeamID: team.ID, FromSlot: "lead", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "architect",
	}); err != nil {
		t.Fatalf("CreateTeamAuthorityGrant: %v", err)
	}

	result, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{})
	if err != nil {
		t.Fatalf("LaunchTeamRun: %v", err)
	}

	// Confirm the dormant slot was NOT resolved at launch.
	before, err := st.ListTeamRunMembersBySlot(ctx, result.RunID, "architect")
	if err != nil {
		t.Fatalf("ListTeamRunMembersBySlot (before): %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("architect members before ResolveLazySlot = %d, want 0", len(before))
	}

	first, err := trl.ResolveLazySlot(ctx, result.RunID, team.ID, "architect", TeamRunOverrides{})
	if err != nil {
		t.Fatalf("ResolveLazySlot: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("ResolveLazySlot returned %d members, want 1", len(first))
	}
	if first[0].AgentID != architect.ID {
		t.Fatalf("resolved agent_id = %q, want %q", first[0].AgentID, architect.ID)
	}
	if first[0].SessionID == "" {
		t.Fatal("resolved session_id is empty")
	}

	// Idempotent: calling again returns the same already-active row, no
	// new session/durable wake.
	second, err := trl.ResolveLazySlot(ctx, result.RunID, team.ID, "architect", TeamRunOverrides{})
	if err != nil {
		t.Fatalf("second ResolveLazySlot: %v", err)
	}
	if len(second) != 1 || second[0].ID != first[0].ID || second[0].SessionID != first[0].SessionID {
		t.Fatalf("second ResolveLazySlot did not return the same already-resolved row: first=%+v second=%+v", first[0], second[0])
	}

	after, err := st.ListTeamRunMembersBySlot(ctx, result.RunID, "architect")
	if err != nil {
		t.Fatalf("ListTeamRunMembersBySlot (after): %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("architect members after two ResolveLazySlot calls = %d, want exactly 1 (idempotent, no double-resolve)", len(after))
	}
}

func TestResolveLazySlot_UnauthorizedDormantSlot_Rejected(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	createTestRoleBoundAgent(t, st, "lead")
	architect := createTestDurableCandidateAgent(t, st, "architect")

	arch := architect.ID
	slots := []store.TeamSlotDefinition{
		{Name: "lead", RoleSlug: "lead", Resolution: "fresh", ActivationMode: "singleton", Required: true},
		{Name: "architect", RoleSlug: "architecture-sme", Resolution: "durable", AgentID: &arch, Required: false},
	}
	phases := []store.TeamPhase{
		{ID: "work", Kind: "flex", ActiveSlots: []string{"lead"}, ExitTrigger: map[string]any{"event": "done"}},
	}
	team := &store.Team{Name: "Lazy Wake Team Ungoverned"}
	if err := team.SetSlots(slots); err != nil {
		t.Fatalf("SetSlots: %v", err)
	}
	if err := team.SetPhases(phases); err != nil {
		t.Fatalf("SetPhases: %v", err)
	}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	// Deliberately no may_spawn grant naming "architect".

	result, err := trl.LaunchTeamRun(ctx, team.ID, TeamRunOverrides{})
	if err != nil {
		t.Fatalf("LaunchTeamRun: %v", err)
	}

	_, err = trl.ResolveLazySlot(ctx, result.RunID, team.ID, "architect", TeamRunOverrides{})
	if err == nil || !errors.Is(err, ErrTeamRunElasticResolutionUnauthorized) {
		t.Fatalf("err = %v, want ErrTeamRunElasticResolutionUnauthorized", err)
	}

	members, err := st.ListTeamRunMembersBySlot(ctx, result.RunID, "architect")
	if err != nil {
		t.Fatalf("ListTeamRunMembersBySlot: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("architect members after rejected lazy resolve = %d, want 0", len(members))
	}
}

// TestResolveDurableMember_NewInstance_CallsStart_NotGatedOnDurableFlag is
// this task's own corrected-semantics finding, made concrete and tested:
// a Team Slot's `resolution: durable` wakes an agent_profiles row that has
// NEITHER agent_profiles.durable=true NOR a "durable-agent" tag set
// (createTestDurableCandidateAgent deliberately leaves both unset) — if
// resolveDurableMember were (incorrectly) gated on that flag the way
// reconcileProfileBackedInstances is, this would fail to find/create an
// instance at all.
func TestResolveDurableMember_NewInstance_CallsStart_NotGatedOnDurableFlag(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	profile := createTestDurableCandidateAgent(t, st, "sme")
	if profile.Durable {
		t.Fatal("test fixture unexpectedly set Durable=true — this test needs it false")
	}

	slot := store.TeamSlotDefinition{Name: "architect", Resolution: "durable", AgentID: &profile.ID, ActivationMode: "singleton"}
	agentID, sessionID, err := trl.resolveDurableMember(ctx, slot)
	if err != nil {
		t.Fatalf("resolveDurableMember: %v", err)
	}
	if agentID != profile.ID {
		t.Fatalf("agentID = %q, want %q", agentID, profile.ID)
	}
	if sessionID == "" {
		t.Fatal("sessionID is empty")
	}

	inst, err := st.GetDurableAgentInstanceByProfileID(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("GetDurableAgentInstanceByProfileID: %v", err)
	}
	if inst.Status != store.DurableAgentStatusActive {
		t.Fatalf("instance status = %q, want active", inst.Status)
	}
	if inst.CurrentSessionID != sessionID {
		t.Fatalf("instance current_session_id = %q, want %q", inst.CurrentSessionID, sessionID)
	}

	// Resolving the SAME slot again should Resume (reuse) the existing
	// instance and its already-attached session, not create a second one.
	agentID2, sessionID2, err := trl.resolveDurableMember(ctx, slot)
	if err != nil {
		t.Fatalf("second resolveDurableMember: %v", err)
	}
	if agentID2 != agentID {
		t.Fatalf("second agentID = %q, want %q", agentID2, agentID)
	}
	if sessionID2 != sessionID {
		t.Fatalf("second call resumed a different session (%q) than the first (%q); resolution=durable should wake the SAME existing identity", sessionID2, sessionID)
	}
}

func TestResolveDurableMember_ConcurrentMultiMember_Rejected(t *testing.T) {
	st, _, trl := newTeamRunLauncherTestFixtures(t)
	ctx := context.Background()

	profile := createTestDurableCandidateAgent(t, st, "sme-concurrent")
	slot := store.TeamSlotDefinition{
		Name: "architect", Resolution: "durable", AgentID: &profile.ID,
		ActivationMode: "concurrent", Min: 1, Max: 3,
	}
	if _, _, err := trl.resolveDurableMember(ctx, slot); err == nil || !strings.Contains(err.Error(), "does not support concurrent multi-member resolution") {
		t.Fatalf("err = %v, want concurrent-multi-member rejection", err)
	}
}

func TestLaunchTeamRun_UnknownTeam_ReturnsError(t *testing.T) {
	_, _, trl := newTeamRunLauncherTestFixtures(t)
	_, err := trl.LaunchTeamRun(context.Background(), "does-not-exist", TeamRunOverrides{})
	if err == nil {
		t.Fatal("expected error for unknown team id")
	}
}
