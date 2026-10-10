package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

type fakeSentMessage struct {
	sessionID string
	content   string
}

type fakeDurableRuntimeController struct {
	stopped    []string
	recovered  []string
	sent       []fakeSentMessage
	err        error
	recoverErr error
	sendErr    error
}

func (f *fakeDurableRuntimeController) StopSession(_ context.Context, sessionID string) error {
	f.stopped = append(f.stopped, sessionID)
	return f.err
}

func (f *fakeDurableRuntimeController) RebootSession(context.Context, string) error {
	return nil
}

func (f *fakeDurableRuntimeController) RecoverSession(_ context.Context, sessionID string) error {
	f.recovered = append(f.recovered, sessionID)
	return f.recoverErr
}

func (f *fakeDurableRuntimeController) CancelSession(context.Context, string) error {
	return nil
}

func (f *fakeDurableRuntimeController) SendMessage(_ context.Context, sessionID, content string) error {
	f.sent = append(f.sent, fakeSentMessage{sessionID: sessionID, content: content})
	return f.sendErr
}

func newDurableAgentServiceTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	return st
}

// These private retained rows are audit history, never issuer inputs.
func durableHistoricalFixture(t *testing.T, st *store.Store, slug, class, tags string) *store.AgentProfile {
	t.Helper()
	p := &store.AgentProfile{Name: "Retained " + slug, Slug: slug, SystemPrompt: "Private retained body"}
	if err := storetest.HistoricalProfile(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_profiles SET class=?,tags=?,durable=1,activation_mode='fresh-per-wake' WHERE id=?`, class, tags, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO durable_agent_instances(id,name,slug,profile_id,lifecycle_class,metadata_json,urn) VALUES(?,?,?,?,?,'{"historical":true}',?)`, "historical-"+slug, p.Name, slug, p.ID, class, "msg://agent/retained/"+slug); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO agent_schedules(id,agent_id,name,schedule_kind,body,status,next_run) VALUES(?,?,'custom retained schedule','cron','Private customized schedule','paused','2026-10-11T00:00:00Z')`, "historical-schedule-"+slug, p.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

func durableAuthoritySnapshot(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, query := range []string{
		`SELECT * FROM agent_profiles ORDER BY id`, `SELECT * FROM durable_agent_instances ORDER BY id`, `SELECT * FROM agent_schedules ORDER BY id`,
		`SELECT * FROM agent_host_settings ORDER BY id`, `SELECT * FROM agent_actor_bindings ORDER BY actor_uri`,
		`SELECT * FROM actor_instances ORDER BY id`, `SELECT * FROM actor_schedules ORDER BY id`, `SELECT * FROM actor_instance_events ORDER BY id`, `SELECT * FROM actor_instance_sessions ORDER BY instance_id,session_id`, `SELECT * FROM session_actor_bindings ORDER BY session_id,agent_id`,
	} {
		rows, err := st.DB.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		var data [][]any
		for rows.Next() {
			cells := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range cells {
				dest[i] = &cells[i]
			}
			if err = rows.Scan(dest...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			for i, c := range cells {
				if b, ok := c.([]byte); ok {
					cells[i] = append([]byte(nil), b...)
				}
			}
			data = append(data, cells)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[query] = data
	}
	return out
}

func TestDurableAgentCreateAndSyncRefuseUnissuedIdentitiesWithoutEffects(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	historical := durableHistoricalFixture(t, st, loomCuratorDurableSlug, store.DurableAgentClassProcess, `["durable-agent","process"]`)
	prior := &store.AgentProfile{Name: "Prior", Slug: "prior-create", SystemPrompt: "Private pin"}
	if err := persistTestActor(t.Context(), st, prior); err != nil {
		t.Fatal(err)
	}
	var hostID string
	if err := st.DB.QueryRowContext(t.Context(), `SELECT host_settings_id FROM agent_actor_bindings WHERE actor_uri=?`, prior.ID).Scan(&hostID); err != nil {
		t.Fatal(err)
	}
	before := durableAuthoritySnapshot(t, st)
	svc := NewDurableAgentService(st)
	for _, profileID := range []string{historical.ID, hostID, "msg://agent/claimed/unissued", "missing", prior.ID} {
		inst := &store.DurableAgentInstance{Name: "Refused", Slug: loomCuratorDurableSlug, ProfileID: profileID, LifecycleClass: store.DurableAgentClassProcess, URN: profileID}
		if err := svc.Create(t.Context(), inst); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("service Create(%s): %v", profileID, err)
		}
		if err := st.CreateDurableAgentInstance(t.Context(), inst); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("store Create(%s): %v", profileID, err)
		}
		got, err := st.SyncDurableAgentInstanceConfig(t.Context(), inst)
		if got != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("Sync(%s): result=%+v err=%v", profileID, got, err)
		}
	}
	if err := svc.Create(t.Context(), nil); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("nil Create: %v", err)
	}
	if after := durableAuthoritySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused creation changed history/authority: before=%v after=%v", before, after)
	}
}

func TestDurableAgentCreateRetryDoesNotRepairPriorJournalOrSeedSchedules(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	durableHistoricalFixture(t, st, loomCuratorDurableSlug, store.DurableAgentClassProcess, `["durable-agent","process"]`)
	p := &store.AgentProfile{Name: "Prior Loom", Slug: "prior-loom", SystemPrompt: "Private pin"}
	if err := persistTestActor(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	inst := &store.DurableAgentInstance{Name: "Prior instance", Slug: loomCuratorDurableSlug, ProfileID: p.ID, LifecycleClass: store.DurableAgentClassProcess, LaunchSourceType: store.DurableAgentLaunchProcessTick, MetadataJSON: `{"prior":true}`}
	if err := persistTestDurableInstance(t.Context(), st, inst); err != nil {
		t.Fatal(err)
	}
	before := durableAuthoritySnapshot(t, st)
	svc := NewDurableAgentService(st)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{t.Context(), canceled} {
		for _, change := range []bool{false, true} {
			retry := *inst
			if change {
				retry.Name = "replacement"
				retry.MetadataJSON = `{"repair":true}`
			}
			if err := svc.Create(ctx, &retry); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("retry change=%v: %v", change, err)
			}
			got, err := st.SyncDurableAgentInstanceConfig(ctx, &retry)
			if got != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("sync retry: result=%+v err=%v", got, err)
			}
		}
	}
	if _, err := svc.List(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if after := durableAuthoritySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("retry/list replayed history or repaired authority: before=%v after=%v", before, after)
	}
}

func TestDurableAgentServiceLifecycleRequests(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Svc Agent", Slug: "svc-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	svc := NewDurableAgentService(st)
	inst := &store.DurableAgentInstance{
		Name:             "Svc Instance",
		Slug:             "svc-instance",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchAPIChat,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	// RequestStart calls through to Start with an empty
	// DurableAgentStartRequest{} (no workspace_id, same as
	// RequestResume/Resume) — so a fresh instance with no attached
	// session needs a pre-attached, reusable session for the
	// advisor-class default ReuseLatestOrCreate policy to find. This
	// mirrors a real "sleeping instance that already has a session, wake
	// it back up" scenario.
	sess := &store.Session{Title: "svc instance session", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(context.Background(), inst.ID, sess.ID, store.DurableAgentSessionRelationPrimary); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}

	started, err := svc.RequestStart(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestStart: %v", err)
	}
	if started.Status != store.DurableAgentStatusActive {
		t.Fatalf("status = %q, want active", started.Status)
	}
	if started.CurrentSessionID != sess.ID {
		t.Fatalf("current_session_id = %q, want %q", started.CurrentSessionID, sess.ID)
	}
	paused, err := svc.RequestPause(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if paused.Status != store.DurableAgentStatusPaused {
		t.Fatalf("status = %q, want paused", paused.Status)
	}
	stopped, err := svc.RequestStop(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if stopped.Status != store.DurableAgentStatusStopped {
		t.Fatalf("status = %q, want stopped", stopped.Status)
	}
}

func TestDurableAgentListDoesNotPromoteHistoricalTagsOrFreshHosts(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	for _, class := range []string{store.DurableAgentClassAdvisor, store.DurableAgentClassProcess, store.DurableAgentClassTemplate, store.DurableAgentClassHarness} {
		durableHistoricalFixture(t, st, "retained-"+class, class, `["durable-agent","`+class+`"]`)
	}
	p := &store.AgentProfile{Name: "Prior", Slug: "list-prior", SystemPrompt: "Private pin"}
	if err := persistTestActor(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	// A verified actor and its host settings still do not issue an instance.
	before := durableAuthoritySnapshot(t, st)
	svc := NewDurableAgentService(st)
	for _, archived := range []bool{false, true} {
		instances, err := svc.List(t.Context(), archived)
		if err != nil {
			t.Fatal(err)
		}
		if len(instances) != 0 {
			t.Fatalf("List promoted historical tags/host projection: %+v", instances)
		}
	}
	if after := durableAuthoritySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatalf("List changed history or issued an instance/schedule: before=%v after=%v", before, after)
	}
}

func TestDurableAgentListReturnsOnlyPriorFreshInstancesWithArchiveFilter(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	durableHistoricalFixture(t, st, "retained-list", store.DurableAgentClassAdvisor, `["durable-agent"]`)
	p := &store.AgentProfile{Name: "Prior", Slug: "fresh-list", SystemPrompt: "Private pin"}
	if err := persistTestActor(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	inst := &store.DurableAgentInstance{Name: "Prior instance", Slug: "fresh-list", ProfileID: p.ID, URN: p.ID}
	if err := persistTestDurableInstance(t.Context(), st, inst); err != nil {
		t.Fatal(err)
	}
	svc := NewDurableAgentService(st)
	instances, err := svc.List(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ID != inst.ID || instances[0].ProfileID != p.ID || instances[0].URN != p.ID {
		t.Fatalf("fresh-only list=%+v", instances)
	}
	if _, err = st.ArchiveDurableAgentInstance(t.Context(), inst.ID); err != nil {
		t.Fatal(err)
	}
	before := durableAuthoritySnapshot(t, st)
	instances, err = svc.List(t.Context(), false)
	if err != nil || len(instances) != 0 {
		t.Fatalf("archived filtered: %+v %v", instances, err)
	}
	instances, err = svc.List(t.Context(), true)
	if err != nil || len(instances) != 1 || instances[0].ArchivedAt == nil {
		t.Fatalf("archived included: %+v %v", instances, err)
	}
	if after := durableAuthoritySnapshot(t, st); !reflect.DeepEqual(before, after) {
		t.Fatal("List replayed archived history")
	}
}

func TestDurableAgentLaunchPolicyByLifecycleClass(t *testing.T) {
	cases := []struct {
		class    string
		policy   string
		relation string
	}{
		{store.DurableAgentClassAdvisor, DurableAgentSessionPolicyReuseLatestOrCreate, store.DurableAgentSessionRelationPrimary},
		{store.DurableAgentClassProcess, DurableAgentSessionPolicyFreshPerWake, store.DurableAgentSessionRelationWake},
		{store.DurableAgentClassTemplate, DurableAgentSessionPolicyFreshOneShot, store.DurableAgentSessionRelationRun},
		{store.DurableAgentClassHarness, DurableAgentSessionPolicyReuseManaged, store.DurableAgentSessionRelationHarness},
	}
	for _, tc := range cases {
		inst := &store.DurableAgentInstance{ID: tc.class, LifecycleClass: tc.class, RuntimeKind: "api"}
		plan, err := durableAgentLaunchPolicyFor(inst, DurableAgentWakePayload{Reason: DurableAgentWakeManual})
		if err != nil {
			t.Fatalf("policy for %s: %v", tc.class, err)
		}
		if plan.SessionPolicy != tc.policy || plan.AttachmentRelation != tc.relation {
			t.Fatalf("policy for %s = %+v", tc.class, plan)
		}
	}
}

func TestDurableAgentStartCreatesOrReusesSession(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Start Agent", Slug: "start-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	svc := NewDurableAgentService(st)
	inst := &store.DurableAgentInstance{
		Name:             "Start Instance",
		Slug:             "start-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassAdvisor,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}

	first, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if !first.CreatedSession || first.ReusedSession || first.Session == nil {
		t.Fatalf("first start result = %+v", first)
	}
	if first.Instance.Status != store.DurableAgentStatusActive || first.Instance.CurrentSessionID != first.Session.ID {
		t.Fatalf("first instance state = %+v session=%+v", first.Instance, first.Session)
	}
	if first.Session.Title != "Start Instance" {
		t.Fatalf("first session title = %q, want durable agent name", first.Session.Title)
	}
	rels, err := svc.ListSessions(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rels) != 1 || rels[0].Relation != store.DurableAgentSessionRelationPrimary {
		t.Fatalf("relations = %+v", rels)
	}

	second, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if second.CreatedSession || !second.ReusedSession || second.Session.ID != first.Session.ID {
		t.Fatalf("second start result = %+v first_session=%s", second, first.Session.ID)
	}
	if second.Session.Provider != "anthropic" || second.Session.Model != "model-a" {
		t.Fatalf("immutable provider/model not preserved: %+v", second.Session)
	}
	events, err := svc.ListEvents(context.Background(), inst.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if !durableAgentEventsContain(events, store.DurableAgentEventStartSucceeded) {
		t.Fatalf("start success event missing: %+v", events)
	}
}

func TestDurableAgentProcessStartCreatesFreshWakeSession(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Process Agent", Slug: "process-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	svc := NewDurableAgentService(st)
	inst := &store.DurableAgentInstance{
		Name:             "Process Instance",
		Slug:             "process-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchProcessTick,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	first, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	second, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if first.Session.ID == second.Session.ID || second.Policy.AttachmentRelation != store.DurableAgentSessionRelationWake {
		t.Fatalf("process starts did not create fresh wake sessions: first=%+v second=%+v", first, second)
	}
	if first.Session.Title != "Process Instance" || second.Session.Title != "Process Instance" {
		t.Fatalf("process session titles = %q / %q, want durable agent name", first.Session.Title, second.Session.Title)
	}
}

func TestDurableAgentResumeNoResumableSession(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Resume Agent", Slug: "resume-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	svc := NewDurableAgentService(st)
	inst := &store.DurableAgentInstance{
		Name:             "Resume Instance",
		Slug:             "resume-instance",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	if _, err := svc.Resume(context.Background(), inst.ID, DurableAgentStartRequest{}); !errors.Is(err, ErrDurableAgentNoResumableSession) {
		t.Fatalf("Resume error = %v, want ErrDurableAgentNoResumableSession", err)
	}
	got, err := st.GetDurableAgentInstance(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("GetDurableAgentInstance: %v", err)
	}
	if got.Status != store.DurableAgentStatusFailed || got.FailureReason == "" {
		t.Fatalf("resume failure not persisted: %+v", got)
	}
	events, err := svc.ListEvents(context.Background(), inst.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if !durableAgentEventsContain(events, store.DurableAgentEventResumeFailed) || events[0].Message == "" {
		t.Fatalf("resume failure event missing: %+v", events)
	}
}

// TestDurableAgentResumeArmsRecovery verifies CW-20260525-0001 Slice 5: a
// durable resume reattaches the latest session AND evicts its runtime via the
// recovery path so the next turn cold-boots with prior context, rather than
// just reattaching the row and waiting for a normal cold turn.
func TestDurableAgentResumeArmsRecovery(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Resume Recover Agent", Slug: "resume-recover-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	runtime := &fakeDurableRuntimeController{}
	svc := NewDurableAgentServiceWithRuntime(st, runtime)
	inst := &store.DurableAgentInstance{
		Name:             "Resume Recover Instance",
		Slug:             "resume-recover-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassAdvisor,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	started, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resumed, err := svc.Resume(context.Background(), inst.ID, DurableAgentStartRequest{})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !resumed.ReusedSession || resumed.Session.ID != started.Session.ID {
		t.Fatalf("resume should reuse the started session: %+v", resumed)
	}
	if len(runtime.recovered) != 1 || runtime.recovered[0] != started.Session.ID {
		t.Fatalf("runtime recovered = %+v, want [%s]", runtime.recovered, started.Session.ID)
	}

	events, err := svc.ListEvents(context.Background(), inst.ID, 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var succeeded *store.DurableAgentEvent
	for i := range events {
		if events[i].EventType == store.DurableAgentEventResumeSucceeded {
			succeeded = &events[i]
			break
		}
	}
	if succeeded == nil {
		t.Fatalf("resume succeeded event missing: %+v", events)
	}
	if !strings.Contains(succeeded.MetadataJSON, `"recovery_armed":"true"`) {
		t.Fatalf("resume succeeded metadata should record recovery_armed=true, got %q", succeeded.MetadataJSON)
	}
}

func TestDurableAgentStopCallsRuntimeAndMarksStopped(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Stop Agent", Slug: "stop-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	sess := &store.Session{Title: "current", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Stop Instance",
		Slug:             "stop-instance",
		ProfileID:        profile.ID,
		Status:           store.DurableAgentStatusActive,
		CurrentSessionID: sess.ID,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	runtime := &fakeDurableRuntimeController{}
	svc := NewDurableAgentServiceWithRuntime(st, runtime)
	stopped, err := svc.RequestStop(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if stopped.Status != store.DurableAgentStatusStopped || stopped.FailureReason != "" {
		t.Fatalf("stopped = %+v", stopped)
	}
	if len(runtime.stopped) != 1 || runtime.stopped[0] != sess.ID {
		t.Fatalf("runtime stopped = %+v, want [%s]", runtime.stopped, sess.ID)
	}
	events, err := svc.ListEvents(context.Background(), inst.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if !durableAgentEventsContain(events, store.DurableAgentEventRuntimeStopSucceeded) ||
		!durableAgentEventsContain(events, store.DurableAgentEventStopSucceeded) {
		t.Fatalf("stop success events missing: %+v", events)
	}
}

func TestDurableAgentStopWithoutCurrentSessionMarksStopped(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "No Current Agent", Slug: "no-current-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "No Current Instance",
		Slug:             "no-current-instance",
		ProfileID:        profile.ID,
		Status:           store.DurableAgentStatusPaused,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	runtime := &fakeDurableRuntimeController{}
	svc := NewDurableAgentServiceWithRuntime(st, runtime)
	stopped, err := svc.RequestStop(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if stopped.Status != store.DurableAgentStatusStopped {
		t.Fatalf("status = %q, want stopped", stopped.Status)
	}
	if len(runtime.stopped) != 0 {
		t.Fatalf("runtime should not be called: %+v", runtime.stopped)
	}
}

func TestDurableAgentStopRuntimeErrorMarksFailed(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Fail Stop Agent", Slug: "fail-stop-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	sess := &store.Session{Title: "current", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Fail Stop Instance",
		Slug:             "fail-stop-instance",
		ProfileID:        profile.ID,
		Status:           store.DurableAgentStatusActive,
		CurrentSessionID: sess.ID,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	runtimeErr := errors.New("runtime stop failed")
	svc := NewDurableAgentServiceWithRuntime(st, &fakeDurableRuntimeController{err: runtimeErr})
	failed, err := svc.RequestStop(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestStop returns persisted failed instance, not error: %v", err)
	}
	if failed.Status != store.DurableAgentStatusFailed || failed.FailureReason != runtimeErr.Error() {
		t.Fatalf("failed = %+v", failed)
	}
	events, err := svc.ListEvents(context.Background(), inst.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 || events[0].EventType != store.DurableAgentEventStopFailed || events[0].Message != runtimeErr.Error() {
		t.Fatalf("stop failure event = %+v", events)
	}
}

func durableAgentEventsContain(events []store.DurableAgentEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func TestDurableAgentPausePreservesCurrentSession(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Pause Agent", Slug: "pause-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	sess := &store.Session{Title: "current", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Pause Instance",
		Slug:             "pause-instance",
		ProfileID:        profile.ID,
		Status:           store.DurableAgentStatusActive,
		CurrentSessionID: sess.ID,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(context.Background(), inst.ID, sess.ID, store.DurableAgentSessionRelationPrimary); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}
	svc := NewDurableAgentService(st)
	paused, err := svc.RequestPause(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if paused.Status != store.DurableAgentStatusPaused || paused.CurrentSessionID != sess.ID {
		t.Fatalf("paused = %+v", paused)
	}
	rels, err := svc.ListSessions(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rels) != 1 || rels[0].SessionID != sess.ID || rels[0].DetachedAt != nil {
		t.Fatalf("relations = %+v", rels)
	}
}
