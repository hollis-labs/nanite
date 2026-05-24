package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

type fakeDurableRuntimeController struct {
	stopped []string
	err     error
}

func (f *fakeDurableRuntimeController) StopSession(_ context.Context, sessionID string) error {
	f.stopped = append(f.stopped, sessionID)
	return f.err
}

func (f *fakeDurableRuntimeController) RebootSession(context.Context, string) error {
	return nil
}

func (f *fakeDurableRuntimeController) CancelSession(context.Context, string) error {
	return nil
}

func newDurableAgentServiceTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestDurableAgentServiceLifecycleRequests(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Svc Agent", Slug: "svc-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "workspace-a", Name: "Workspace A"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
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

	started, err := svc.RequestStart(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("RequestStart: %v", err)
	}
	if started.Status != store.DurableAgentStatusStartRequested {
		t.Fatalf("status = %q, want start_requested", started.Status)
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
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "workspace-a", Name: "Workspace A"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
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

	first, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{WorkspaceID: "workspace-a"})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if !first.CreatedSession || first.ReusedSession || first.Session == nil {
		t.Fatalf("first start result = %+v", first)
	}
	if first.Instance.Status != store.DurableAgentStatusActive || first.Instance.CurrentSessionID != first.Session.ID {
		t.Fatalf("first instance state = %+v session=%+v", first.Instance, first.Session)
	}
	rels, err := svc.ListSessions(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rels) != 1 || rels[0].Relation != store.DurableAgentSessionRelationPrimary {
		t.Fatalf("relations = %+v", rels)
	}

	second, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{WorkspaceID: "workspace-a"})
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
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "workspace-a", Name: "Workspace A"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
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
	first, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{WorkspaceID: "workspace-a"})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	second, err := svc.Start(context.Background(), inst.ID, DurableAgentStartRequest{WorkspaceID: "workspace-a"})
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if first.Session.ID == second.Session.ID || second.Policy.AttachmentRelation != store.DurableAgentSessionRelationWake {
		t.Fatalf("process starts did not create fresh wake sessions: first=%+v second=%+v", first, second)
	}
}

func TestDurableAgentResumeNoResumableSession(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Resume Agent", Slug: "resume-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
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
	if _, err := svc.Resume(context.Background(), inst.ID, DurableAgentStartRequest{}); err != ErrDurableAgentNoResumableSession {
		t.Fatalf("Resume error = %v, want ErrDurableAgentNoResumableSession", err)
	}
	got, err := st.GetDurableAgentInstance(inst.ID)
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

func TestDurableAgentStopCallsRuntimeAndMarksStopped(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Stop Agent", Slug: "stop-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sess := &store.Session{Title: "current", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(sess); err != nil {
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
	if err := st.CreateDurableAgentInstance(inst); err != nil {
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
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "No Current Instance",
		Slug:             "no-current-instance",
		ProfileID:        profile.ID,
		Status:           store.DurableAgentStatusPaused,
		LaunchSourceType: store.DurableAgentLaunchDurableAdvisor,
	}
	if err := st.CreateDurableAgentInstance(inst); err != nil {
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
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sess := &store.Session{Title: "current", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(sess); err != nil {
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
	if err := st.CreateDurableAgentInstance(inst); err != nil {
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
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sess := &store.Session{Title: "current", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(sess); err != nil {
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
	if err := st.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(inst.ID, sess.ID, store.DurableAgentSessionRelationPrimary); err != nil {
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
