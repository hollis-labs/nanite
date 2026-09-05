package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
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

func newLoomDurableFixture(t *testing.T, st *store.Store) (*store.AgentProfile, *store.DurableAgentInstance) {
	t.Helper()
	profile := &store.AgentProfile{
		Name: "Loom Curator", Slug: loomCuratorDurableSlug, SystemPrompt: "Curate Loom fragments.", Durable: true,
	}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return profile, &store.DurableAgentInstance{
		Name: "Loom Curator", Slug: loomCuratorDurableSlug, ProfileID: profile.ID,
		LifecycleClass: store.DurableAgentClassProcess, LaunchSourceType: store.DurableAgentLaunchProcessTick,
	}
}

type failOnceBuiltinScheduleStore struct {
	DurableAgentStore
	mu       sync.Mutex
	attempts int
	err      error
}

func (s *failOnceBuiltinScheduleStore) InsertAgentScheduleIfNameMissing(ctx context.Context, row store.AgentSchedule) (bool, error) {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	s.mu.Unlock()
	if attempt == 1 {
		return false, s.err
	}
	return s.DurableAgentStore.InsertAgentScheduleIfNameMissing(ctx, row)
}

type pauseAfterInstanceCreateStore struct {
	DurableAgentStore
	once     sync.Once
	inserted chan struct{}
	release  chan struct{}
}

type pauseAfterInstanceListStore struct {
	DurableAgentStore
	once    sync.Once
	listed  chan struct{}
	proceed chan struct{}
}

func (s *pauseAfterInstanceListStore) ListDurableAgentInstances(ctx context.Context, includeArchived bool) ([]store.DurableAgentInstance, error) {
	instances, err := s.DurableAgentStore.ListDurableAgentInstances(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	wait := false
	s.once.Do(func() {
		wait = true
		close(s.listed)
	})
	if wait {
		<-s.proceed
	}
	return instances, nil
}

func (s *pauseAfterInstanceCreateStore) CreateDurableAgentInstance(ctx context.Context, inst *store.DurableAgentInstance) error {
	if err := s.DurableAgentStore.CreateDurableAgentInstance(ctx, inst); err != nil {
		return err
	}
	wait := false
	s.once.Do(func() {
		wait = true
		close(s.inserted)
	})
	if wait {
		<-s.release
	}
	return nil
}

func TestDurableAgentCreateProvisionsLoomCuratorBuiltinSchedule(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{
		Name: "Loom Curator", Slug: "loom-curator", SystemPrompt: "Curate Loom fragments.", Durable: true,
	}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name: "Loom Curator", Slug: "loom-curator", ProfileID: profile.ID,
		LifecycleClass: store.DurableAgentClassProcess, LaunchSourceType: store.DurableAgentLaunchProcessTick,
	}
	svc := NewDurableAgentService(st)
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	schedules, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("schedules = %d, want one builtin Loom schedule: %+v", len(schedules), schedules)
	}
	got := schedules[0]
	if got.Name != "lint-and-export" || got.ScheduleKind != store.ScheduleKindCron || got.ScheduleSpec != "0 3 * * *" {
		t.Fatalf("builtin schedule = %+v", got)
	}
	if wantID := builtinDurableAgentScheduleID(profile.ID, loomLintExportName); got.ID != wantID {
		t.Fatalf("builtin schedule ID = %q, want stable legacy ID %q", got.ID, wantID)
	}
	if got.NextRun == "" || got.CreatedBy != "builtin" {
		t.Fatalf("builtin schedule missing live next_run/provenance: %+v", got)
	}
	for _, want := range []string{"loom_bundle_conformance", "message_*", "loom_export_bundle"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("builtin schedule body missing %q: %q", want, got.Body)
		}
	}
	before := got
	if provisionErr := svc.(*durableAgentService).provisionBuiltinDurableSchedules(context.Background(), inst); provisionErr != nil {
		t.Fatalf("repeat provisioning: %v", provisionErr)
	}
	after, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(after) != 1 || after[0] != before {
		t.Fatalf("repeat provisioning was not idempotent: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestDurableAgentCreatePreservesCustomizedLoomCuratorSchedule(t *testing.T) {
	ctx := context.Background()
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{
		Name: "Loom Curator", Slug: "loom-curator", SystemPrompt: "Curate Loom fragments.", Durable: true,
	}
	if err := st.CreateAgent(ctx, profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	custom := store.AgentSchedule{
		ID: "operator-custom-loom-schedule", AgentID: profile.ID, Name: "lint-and-export",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "15 4 * * 1", Body: "Operator-customized body.",
		Priority: 42, Status: store.ScheduleStatusPaused, CreatedBy: "operator", NextRun: "2030-01-07T04:15:00Z",
		MaxRetries: 9, OnFail: store.ScheduleOnFailNotify, JobType: store.ScheduleJobTypeDurableAgentWake,
		JobPayload: `{"custom":true}`,
	}
	if err := st.InsertAgentSchedule(ctx, custom); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}
	before, err := st.GetAgentSchedule(ctx, custom.ID)
	if err != nil {
		t.Fatalf("GetAgentSchedule before: %v", err)
	}

	inst := &store.DurableAgentInstance{
		Name: "Loom Curator", Slug: "loom-curator", ProfileID: profile.ID,
		LifecycleClass: store.DurableAgentClassProcess, LaunchSourceType: store.DurableAgentLaunchProcessTick,
	}
	svc := NewDurableAgentService(st)
	if createErr := svc.Create(ctx, inst); createErr != nil {
		t.Fatalf("Create: %v", createErr)
	}
	if _, listErr := svc.List(ctx, false); listErr != nil {
		t.Fatalf("List reconciliation: %v", listErr)
	}
	after, err := st.GetAgentSchedule(ctx, custom.ID)
	if err != nil {
		t.Fatalf("GetAgentSchedule after: %v", err)
	}
	if *after != *before {
		t.Fatalf("builtin provisioning overwrote customized schedule:\n before=%+v\n  after=%+v", before, after)
	}
	schedules, err := st.ListAgentSchedules(ctx, profile.ID)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("schedules after create = %+v, %v; want only customized row", schedules, err)
	}
}

func TestDurableAgentCanceledCreateRepairsMissingBuiltinScheduleDuringReconcile(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile, inst := newLoomDurableFixture(t, st)
	svc := NewDurableAgentService(st)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Create(canceled, inst); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want context.Canceled after instance commit", err)
	}
	instances, err := st.ListDurableAgentInstances(context.Background(), true)
	if err != nil || len(instances) != 1 {
		t.Fatalf("committed instances = %+v, %v; want one positive-control partial instance", instances, err)
	}
	before, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(before) != 0 {
		t.Fatalf("schedules before recovery = %+v, %v; want deterministic missing-schedule case", before, err)
	}

	if _, listErr := svc.List(context.Background(), false); listErr != nil {
		t.Fatalf("List reconciliation: %v", listErr)
	}
	after, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(after) != 1 || after[0].Name != loomLintExportName {
		t.Fatalf("schedules after reconciliation = %+v, %v; want repaired builtin", after, err)
	}
}

func TestDurableAgentCreateRetryRepairsTransientBuiltinScheduleFailure(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile, inst := newLoomDurableFixture(t, st)
	injected := errors.New("injected transient schedule write failure")
	faults := &failOnceBuiltinScheduleStore{DurableAgentStore: st, err: injected}
	svc := NewDurableAgentService(faults)

	if err := svc.Create(context.Background(), inst); !errors.Is(err, injected) {
		t.Fatalf("first Create error = %v, want injected schedule failure", err)
	}
	before, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(before) != 0 {
		t.Fatalf("schedules before retry = %+v, %v; want committed partial state", before, err)
	}
	if retryErr := svc.Create(context.Background(), inst); retryErr != nil {
		t.Fatalf("idempotent Create retry: %v", retryErr)
	}
	after, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(after) != 1 || after[0].Name != loomLintExportName {
		t.Fatalf("schedules after retry = %+v, %v; want repaired builtin", after, err)
	}
	invalid := *inst
	invalid.LifecycleClass = "not-a-lifecycle-class"
	if err := svc.Create(context.Background(), &invalid); err == nil {
		t.Fatal("different invalid Create request was masked as an idempotent retry")
	}
}

func TestDurableAgentConcurrentCreateAndReconcileProvisionOneBuiltinSchedule(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile, inst := newLoomDurableFixture(t, st)
	paused := &pauseAfterInstanceCreateStore{
		DurableAgentStore: st,
		inserted:          make(chan struct{}),
		release:           make(chan struct{}),
	}
	svc := NewDurableAgentService(paused)
	var releaseOnce sync.Once
	releaseCreate := func() { releaseOnce.Do(func() { close(paused.release) }) }
	defer releaseCreate()
	createDone := make(chan error, 1)
	go func() {
		createDone <- svc.Create(context.Background(), inst)
	}()
	<-paused.inserted

	const reconcilers = 24
	reconcileErrs := make(chan error, reconcilers)
	var reconcileWG sync.WaitGroup
	for i := 0; i < reconcilers; i++ {
		reconcileWG.Add(1)
		go func() {
			defer reconcileWG.Done()
			_, err := svc.List(context.Background(), false)
			reconcileErrs <- err
		}()
	}
	reconcileWG.Wait()
	close(reconcileErrs)
	for err := range reconcileErrs {
		if err != nil {
			t.Fatalf("concurrent reconciliation: %v", err)
		}
	}
	releaseCreate()
	if err := <-createDone; err != nil {
		t.Fatalf("concurrent Create: %v", err)
	}

	schedules, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(schedules) != 1 || schedules[0].Name != loomLintExportName {
		t.Fatalf("schedules after concurrent create/reconcile = %+v, %v; want exactly one builtin", schedules, err)
	}
}

func TestDurableAgentReconcileRepairsCanceledConcurrentCreateAfterStaleSnapshot(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile, inst := newLoomDurableFixture(t, st)
	paused := &pauseAfterInstanceListStore{
		DurableAgentStore: st,
		listed:            make(chan struct{}),
		proceed:           make(chan struct{}),
	}
	reconcileSvc := NewDurableAgentService(paused)
	reconcileDone := make(chan error, 1)
	go func() {
		_, err := reconcileSvc.List(context.Background(), false)
		reconcileDone <- err
	}()
	<-paused.listed

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewDurableAgentService(st).Create(canceled, inst); !errors.Is(err, context.Canceled) {
		close(paused.proceed)
		t.Fatalf("concurrent Create error = %v, want context.Canceled", err)
	}
	before, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(before) != 0 {
		close(paused.proceed)
		t.Fatalf("schedules before stale reconciliation resumes = %+v, %v; want missing", before, err)
	}
	close(paused.proceed)
	if reconcileErr := <-reconcileDone; reconcileErr != nil {
		t.Fatalf("stale-snapshot reconciliation: %v", reconcileErr)
	}

	schedules, err := st.ListAgentSchedules(context.Background(), profile.ID)
	if err != nil || len(schedules) != 1 || schedules[0].Name != loomLintExportName {
		t.Fatalf("schedules after stale-snapshot recovery = %+v, %v; want exactly one builtin", schedules, err)
	}
}

// TestDurableAgentServiceLifecycleRequests is a Phase 0 item 2
// (RequestStart fix) regression test: RequestStart used to be a no-op
// status flip to start_requested with nothing downstream ever driving it
// further. It now calls straight through to Start (mirroring
// RequestResume's call-through-to-Resume shape), so a sleeping instance
// with an already-attached, reusable session actually launches and reaches
// active status with that session attached — not stuck at start_requested.
func TestDurableAgentServiceLifecycleRequests(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Svc Agent", Slug: "svc-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	svc := NewDurableAgentService(st)
	inst := &store.DurableAgentInstance{
		Name:             "Svc Instance",
		Slug:             "svc-instance",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchAPIChat,
	}
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
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

func TestDurableAgentList_ReconcilesTaggedProfilesIntoInstances(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{
		Name:         "Tagged Durable Agent",
		Slug:         "tagged-durable-agent",
		SystemPrompt: "x",
		Tags:         `["durable-agent","advisor"]`,
		Class:        store.DurableAgentClassAdvisor,
		DefaultState: store.DurableAgentStatusSleeping,
	}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	svc := NewDurableAgentService(st)
	instances, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances len = %d, want 1: %+v", len(instances), instances)
	}
	if instances[0].ProfileID != profile.ID || instances[0].Slug != profile.Slug {
		t.Fatalf("instance = %+v, want profile_id=%s slug=%s", instances[0], profile.ID, profile.Slug)
	}
	if instances[0].LaunchSourceType != store.DurableAgentLaunchDurableAdvisor {
		t.Fatalf("launch_source_type = %q, want %q", instances[0].LaunchSourceType, store.DurableAgentLaunchDurableAdvisor)
	}
}

// TestDurableAgentList_ReconcilesHarnessProfileWithCorrectLifecycleClass is
// a CW-20260815-0009 follow-up (Copilot review on PR #241): before that
// ticket, class="harness" could never reach agent_profiles at all, so
// durableAgentInstanceFromProfile's class switch never saw it and its
// default-to-advisor branch was unreachable for harness profiles. Once
// harness started ingesting, a harness-tagged durable candidate reconciled
// here (e.g. via GET /api/durable-agents before its recipe is ever applied)
// would silently downgrade to lifecycle_class="advisor". Assert it stays
// "harness" with the api_chat launch source, not the advisor default.
func TestDurableAgentList_ReconcilesHarnessProfileWithCorrectLifecycleClass(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{
		Name:         "Harness Durable Agent",
		Slug:         "harness-durable-agent",
		SystemPrompt: "x",
		Tags:         `["durable-agent","harness"]`,
		Class:        store.DurableAgentClassHarness,
		DefaultState: store.DurableAgentStatusSleeping,
	}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	svc := NewDurableAgentService(st)
	instances, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances len = %d, want 1: %+v", len(instances), instances)
	}
	if instances[0].LifecycleClass != store.DurableAgentClassHarness {
		t.Fatalf("lifecycle_class = %q, want %q (must not silently downgrade to advisor)", instances[0].LifecycleClass, store.DurableAgentClassHarness)
	}
	if instances[0].LaunchSourceType != store.DurableAgentLaunchAPIChat {
		t.Fatalf("launch_source_type = %q, want %q", instances[0].LaunchSourceType, store.DurableAgentLaunchAPIChat)
	}
}

func TestDurableAgentList_DoesNotPromoteNonDurableProfiles(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{
		Name:         "Regular Agent",
		Slug:         "regular-agent",
		SystemPrompt: "x",
		Tags:         `["advisor"]`,
		Class:        store.DurableAgentClassAdvisor,
	}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	svc := NewDurableAgentService(st)
	instances, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("instances = %+v, want empty", instances)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	svc := NewDurableAgentService(st)
	inst := &store.DurableAgentInstance{
		Name:             "Resume Instance",
		Slug:             "resume-instance",
		ProfileID:        profile.ID,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := svc.Create(context.Background(), inst); err != nil {
		t.Fatalf("Create: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "No Current Instance",
		Slug:             "no-current-instance",
		ProfileID:        profile.ID,
		Status:           store.DurableAgentStatusPaused,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
