package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newTeamRunLauncherTestFixtures(t *testing.T) (*store.Store, *agentworkflow.Registry, *TeamRunLauncher) {
	t.Helper()
	st := newDurableAgentServiceTestStore(t)
	registry := agentworkflow.NewRegistry(nil)
	durable := NewDurableAgentService(st)
	return st, registry, NewTeamRunLauncher(st, NewWorkflowLauncher(registry, &a2aLaunchRecordingHost{}, &fakeStepExecutor{}, durable), durable)
}

// Retained records are inserted directly into a private database. Existing
// actor bindings are separately simulated as previously host-authorized;
// neither set is enrollment input for the team launcher.
type heldTeamFixture struct {
	team       *store.Team
	historical *store.AgentProfile
	actor      *store.AgentProfile
	runID      string
	hostID     string
}

func newHeldTeamFixture(t *testing.T, st *store.Store) heldTeamFixture {
	t.Helper()
	ctx := t.Context()
	historical := &store.AgentProfile{ID: "retained-team-profile", Name: "Retained team profile", Slug: "retained-team-profile", SystemPrompt: "Private retained instructions"}
	if err := storetest.HistoricalProfile(ctx, st, historical); err != nil {
		t.Fatal(err)
	}
	actor := &store.AgentProfile{Name: "Prior team actor", Slug: "prior-team-actor", SystemPrompt: "Private immutable instructions"}
	if err := persistTestActor(ctx, st, actor); err != nil {
		t.Fatal(err)
	}
	var hostID string
	if err := st.DB.QueryRowContext(ctx, `SELECT host_settings_id FROM agent_actor_bindings WHERE actor_uri=?`, actor.ID).Scan(&hostID); err != nil {
		t.Fatal(err)
	}
	team := &store.Team{Name: "Retained team", RoutingJSON: `{"coordinator_slot":"lead","rules":[{"name":"review","target_slot":"worker","phrases":["review"]}]}`}
	if err := team.SetSlots([]store.TeamSlotDefinition{
		{Name: "lead", Resolution: "durable", AgentID: &actor.ID, Required: true},
		{Name: "worker", Resolution: "fresh", RoleSlug: "retained-team-role", Required: false},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTeam(ctx, team); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{TeamID: team.ID, FromSlot: "lead", Verb: store.TeamAuthorityVerbMaySpawn, ToSlot: "worker"}); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO roles(id,name,slug,system_prompt,created_at,updated_at) VALUES('retained-team-role','Retained role','retained-team-role','Private retained role instructions','2026-10-09','2026-10-09')`, nil},
		{`UPDATE agent_profiles SET role_id='retained-team-role' WHERE id=?`, []any{historical.ID}},
		{`INSERT INTO workflow_runs(id,definition_name,status,input_json,started_at) VALUES('retained-team-run','retained-team','completed','{"retained":true}','2026-10-09T00:00:00Z')`, nil},
		{`INSERT INTO sessions(id,short_code,title) VALUES('retained-team-session','TMHIST','Retained session')`, nil},
		{`INSERT INTO sessions(id,short_code,title) VALUES('prior-team-session','TMPRIOR','Prior authorized session')`, nil},
		{`INSERT INTO team_run_members(id,workflow_run_id,slot_name,agent_id,session_id) VALUES('retained-team-member','retained-team-run','lead',?,'retained-team-session')`, []any{historical.ID}},
		{`INSERT INTO actor_team_run_members(id,workflow_run_id,slot_name,agent_id,session_id) VALUES('prior-team-member','retained-team-run','lead',?,'prior-team-session')`, []any{actor.ID}},
		{`INSERT INTO team_run_launches(idempotency_key,team_id,request_digest,planning_json,workflow_run_id,status,created_at,updated_at) VALUES('retained-launch',?,'retained-digest','{"private_plan":true}','retained-team-run','members_ready','2026-10-09','2026-10-09')`, []any{team.ID}},
		{`INSERT INTO team_run_launches(idempotency_key,team_id,request_digest,planning_json,status,created_at,updated_at) VALUES('retained-prepared',?,'retained-prepared-digest','{"private_plan":true}','prepared','2026-10-09','2026-10-09')`, []any{team.ID}},
		{`INSERT INTO team_run_member_intents(idempotency_key,ordinal,member_id,team_id,slot_name,agent_id,session_id,provisioning_kind,status,created_at,updated_at) VALUES('retained-prepared',0,'retained-intent',?,'worker',?,'retained-intent-session','fresh','planned','2026-10-09','2026-10-09')`, []any{team.ID, historical.ID}},
		{`INSERT INTO team_signal_resolutions(workflow_run_id,step_id,responder_reference,output,member_ids_json,status,created_at,updated_at) VALUES('retained-team-run','private-step','retained-responder','Private retained output','["retained-team-member"]','prepared','2026-10-09','2026-10-09')`, nil},
	}
	for _, statement := range statements {
		if _, err := st.DB.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("private retained fixture: %v", err)
		}
	}
	inst := &store.DurableAgentInstance{ID: "prior-team-instance", Name: "Prior team instance", Slug: "prior-team-instance", ProfileID: actor.ID, Status: store.DurableAgentStatusSleeping}
	if err := persistTestDurableInstance(ctx, st, inst); err != nil {
		t.Fatal(err)
	}
	return heldTeamFixture{team: team, historical: historical, actor: actor, runID: "retained-team-run", hostID: hostID}
}

func heldTeamSnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	out := retainedDispatchSnapshot(t, st)
	for _, query := range []string{
		`SELECT * FROM roles ORDER BY id`, `SELECT * FROM teams ORDER BY id`, `SELECT * FROM team_authority_grants ORDER BY id`,
		`SELECT * FROM team_run_launches ORDER BY idempotency_key`, `SELECT * FROM team_run_member_intents ORDER BY idempotency_key,ordinal`,
		`SELECT * FROM actor_team_run_member_intents ORDER BY idempotency_key,ordinal`, `SELECT * FROM team_signal_resolutions ORDER BY workflow_run_id,step_id`,
		`SELECT * FROM workflow_runs ORDER BY id`, `SELECT * FROM workflow_run_steps ORDER BY id`,
		`SELECT * FROM sessions ORDER BY id`, `SELECT * FROM session_agents ORDER BY session_id,agent_id`,
		`SELECT * FROM agent_messages ORDER BY id`, `SELECT * FROM event_log ORDER BY id`,
	} {
		out[query] = immutableConfigSnapshot(t, st, query)
	}
	return out
}

func assertHeldTeamUnchanged(t *testing.T, st *store.Store, before map[string][][]any) {
	t.Helper()
	if after := heldTeamSnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("held team operation changed retained history, planning, messages, sessions, grants, or runtime state")
	}
}

func TestHeldTeamLaunchRefusesBeforePlanningOrRetryEffects(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	f := newHeldTeamFixture(t, st)
	host := &a2aLaunchRecordingHost{}
	runtime := &fakeDurableRuntimeController{}
	durable := NewDurableAgentServiceWithRuntime(st, runtime)
	launcher.launcher = NewWorkflowLauncher(agentworkflow.NewRegistry(nil), host, &fakeStepExecutor{}, durable)
	launcher.durable = durable
	before := heldTeamSnapshot(t, st)
	for _, tc := range []struct{ name, team, key, identity string }{
		{"new", f.team.ID, "new-key", f.actor.ID},
		{"retained retry", f.team.ID, "retained-launch", f.historical.ID},
		{"prepared recovery", f.team.ID, "retained-prepared", f.actor.ID},
		{"host UUID", f.team.ID, "new-host-key", f.hostID},
		{"missing team", "missing-team", "new-missing-key", f.actor.ID},
		{"empty request", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := launcher.LaunchTeamRun(t.Context(), tc.team, TeamRunOverrides{IdempotencyKey: tc.key, AgentProfileID: tc.identity, Params: map[string]any{"changed": true}, SlotMemberCounts: map[string]int{"worker": 2}})
			if result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("launch=%+v,%v; want nil, ErrVerifiedActorRequired", result, err)
			}
			assertHeldTeamUnchanged(t, st, before)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := launcher.LaunchTeamRun(ctx, f.team.ID, TeamRunOverrides{IdempotencyKey: "canceled-key"}); result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("canceled launch=%+v,%v", result, err)
	}
	assertHeldTeamUnchanged(t, st, before)
	if len(host.calls)+len(runtime.stopped)+len(runtime.recovered)+len(runtime.sent) != 0 {
		t.Fatal("held launch invoked workflow or runtime")
	}
}

func TestHeldTeamLazyResolutionRefusesWithoutMemberIntent(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	f := newHeldTeamFixture(t, st)
	before := heldTeamSnapshot(t, st)
	for _, slot := range []string{"worker", "lead", "missing", ""} {
		members, err := launcher.ResolveLazySlot(t.Context(), f.runID, f.team.ID, slot, TeamRunOverrides{IdempotencyKey: "retained-prepared"})
		if members != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("lazy %q=%+v,%v", slot, members, err)
		}
		assertHeldTeamUnchanged(t, st, before)
	}
	// The public cut precedes even dependency and identifier lookups.
	empty := NewTeamRunLauncher(nil, nil, nil)
	if result, err := empty.LaunchTeamRun(t.Context(), "", TeamRunOverrides{}); result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("unconfigured launch=%+v,%v", result, err)
	}
	if result, err := empty.ResolveLazySlot(t.Context(), "", "", "", TeamRunOverrides{}); result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("unconfigured lazy=%+v,%v", result, err)
	}
}

func TestHeldTeamIdentityReadRequiresExistingVerifiedActor(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	f := newHeldTeamFixture(t, st)
	before := heldTeamSnapshot(t, st)
	slot := store.TeamSlotDefinition{Name: "lead", Resolution: "durable", AgentID: &f.actor.ID}
	id, err := launcher.resolveSlotAgentIdentity(t.Context(), slot)
	if err != nil || id != f.actor.ID {
		t.Fatalf("existing actor identity=%q,%v", id, err)
	}
	for _, id := range []string{f.historical.ID, f.hostID, f.actor.Slug, "missing"} {
		slot.AgentID = &id
		resolved, err := launcher.resolveSlotAgentIdentity(t.Context(), slot)
		if resolved != "" || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("unverified %q=%q,%v", id, resolved, err)
		}
	}
	slot = store.TeamSlotDefinition{Name: "worker", Resolution: "fresh", RoleSlug: "retained-team-role"}
	if resolved, err := launcher.resolveSlotAgentIdentity(t.Context(), slot); resolved != "" || !errors.Is(err, store.ErrImmutableAgentProfile) {
		t.Fatalf("historical role resolution=%q,%v", resolved, err)
	}
	assertHeldTeamUnchanged(t, st, before)
}

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

	if _, err = st.CreateTeamAuthorityGrant(ctx, store.TeamAuthorityGrant{
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

func TestHeldTeamMemberCountBoundsRemainDeclarative(t *testing.T) {
	for _, tc := range []struct {
		name, mode               string
		min, max, override, want int
		wantErr                  bool
	}{
		{"singleton", "singleton", 1, 1, 8, 1, false}, {"concurrent default", "concurrent", 2, 4, 0, 2, false},
		{"concurrent override", "concurrent", 2, 4, 3, 3, false}, {"below minimum", "concurrent", 2, 4, 1, 0, true},
		{"above maximum", "concurrent", 2, 4, 5, 0, true}, {"unbounded", "concurrent", 0, 0, 12, 12, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			overrides := TeamRunOverrides{}
			if tc.override != 0 {
				overrides.SlotMemberCounts = map[string]int{"worker": tc.override}
			}
			got, err := resolveMemberCount(store.TeamSlotDefinition{Name: "worker", ActivationMode: tc.mode, Min: tc.min, Max: tc.max}, overrides)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("count=%d,%v; want %d error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
