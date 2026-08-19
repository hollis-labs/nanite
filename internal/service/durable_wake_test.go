package service

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestDurableWakeListDueAndDryRun(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Wake Agent", Slug: "wake-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:           "Wake Instance",
		Slug:           "wake-instance",
		ProfileID:      profile.ID,
		LifecycleClass: store.DurableAgentClassProcess,
		Provider:       "anthropic",
		Model:          "model-a",
		RuntimeKind:    "api",
	}
	if err := st.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	ctx := context.Background()
	if err := st.InsertAgentSchedule(ctx, store.AgentSchedule{
		ID:           "sched-1",
		AgentID:      profile.ID,
		Name:         "wake once",
		ScheduleKind: store.ScheduleKindOneShot,
		Body:         "wake",
		Status:       store.ScheduleStatusActive,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	items, err := wakeSvc.ListDue(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(items) != 1 || items[0].Schedule.ID != "sched-1" || items[0].SkipReason != "workspace unavailable" {
		t.Fatalf("due items = %+v", items)
	}

	run, err := wakeSvc.RunDue(ctx, DurableAgentWakeRunRequest{Now: time.Now().UTC(), DryRun: true})
	if err != nil {
		t.Fatalf("RunDue dry-run: %v", err)
	}
	if len(run.Results) != 1 || !run.Results[0].Skipped || run.Results[0].SkipReason != "workspace unavailable" {
		t.Fatalf("dry-run results = %+v", run.Results)
	}
	schedule, err := st.GetAgentSchedule(ctx, "sched-1")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if schedule.FiredCount != 0 {
		t.Fatalf("dry-run mutated schedule: %+v", schedule)
	}
}

func TestDurableWakeRunDueStartsAttachedSessionAndBumpsSchedule(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Wake Start Agent", Slug: "wake-start-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "workspace-a", Name: "Workspace A"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	seedSession := &store.Session{WorkspaceID: "workspace-a", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(seedSession); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Wake Start Instance",
		Slug:             "wake-start-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		CurrentSessionID: seedSession.ID,
	}
	if err := st.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(inst.ID, seedSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}
	if err := st.InsertAgentSchedule(ctx, store.AgentSchedule{
		ID:           "sched-run",
		AgentID:      profile.ID,
		Name:         "run now",
		ScheduleKind: store.ScheduleKindOneShot,
		Body:         "wake",
		Status:       store.ScheduleStatusActive,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	durableSvc := NewDurableAgentService(st)
	wakeSvc := NewDurableAgentWakeService(st, durableSvc)
	run, err := wakeSvc.RunDue(ctx, DurableAgentWakeRunRequest{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if len(run.Results) != 1 || run.Results[0].LaunchResult == nil {
		t.Fatalf("run results = %+v", run.Results)
	}
	if run.Results[0].LaunchResult.Session == nil || run.Results[0].LaunchResult.Session.ID == seedSession.ID {
		t.Fatalf("expected fresh wake session, got %+v", run.Results[0].LaunchResult)
	}
	schedule, err := st.GetAgentSchedule(ctx, "sched-run")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if schedule.FiredCount != 1 || schedule.Status != store.ScheduleStatusExpired {
		t.Fatalf("schedule after run = %+v", schedule)
	}
	events, err := durableSvc.ListEvents(ctx, inst.ID, 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if !durableAgentEventsContain(events, store.DurableAgentEventWakeRequested) || !durableAgentEventsContain(events, store.DurableAgentEventWakeStarted) {
		t.Fatalf("wake events missing: %+v", events)
	}
}

func TestDurableWakeRunDueSkipsPausedAndActiveInstances(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Wake Skip Agent", Slug: "wake-skip-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	instances := []store.DurableAgentInstance{
		{Name: "paused", Slug: "paused", ProfileID: profile.ID, LifecycleClass: store.DurableAgentClassProcess, Status: store.DurableAgentStatusPaused},
		{Name: "active", Slug: "active", ProfileID: profile.ID, LifecycleClass: store.DurableAgentClassProcess, Status: store.DurableAgentStatusActive},
	}
	for i := range instances {
		if err := st.CreateDurableAgentInstance(&instances[i]); err != nil {
			t.Fatalf("CreateDurableAgentInstance %d: %v", i, err)
		}
	}
	if err := st.InsertAgentSchedule(ctx, store.AgentSchedule{
		ID:           "sched-skip",
		AgentID:      profile.ID,
		Name:         "skip me",
		ScheduleKind: store.ScheduleKindOneShot,
		Body:         "wake",
		Status:       store.ScheduleStatusActive,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	run, err := wakeSvc.RunDue(ctx, DurableAgentWakeRunRequest{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if len(run.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(run.Results))
	}
	for _, result := range run.Results {
		if !result.Skipped || result.SkipReason == "" {
			t.Fatalf("expected skipped result, got %+v", result)
		}
	}
}

func TestDurableWakeFailurePersistsFailureReason(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Wake Fail Agent", Slug: "wake-fail-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:           "Wake Fail Instance",
		Slug:           "wake-fail-instance",
		ProfileID:      profile.ID,
		LifecycleClass: store.DurableAgentClassProcess,
		Provider:       "anthropic",
		Model:          "model-a",
		RuntimeKind:    "api",
	}
	if err := st.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeProcessTick}})
	if err != nil {
		t.Fatalf("Wake: unexpected error: %v", err)
	}
	if result == nil || !result.Skipped || result.SkipReason != "workspace unavailable" {
		t.Fatalf("wake result = %+v", result)
	}
	events, err := st.ListDurableAgentEvents(inst.ID, 20)
	if err != nil {
		t.Fatalf("ListDurableAgentEvents: %v", err)
	}
	if !durableAgentEventsContain(events, store.DurableAgentEventWakeSkipped) {
		t.Fatalf("expected wake_skipped event, got %+v", events)
	}
}

// TestDurableWakeProcessClassRewakeableWhileActive is the regression test for
// the CW-20260817 finding: nothing anywhere transitions a durable-agent
// instance back out of "active" once Start() sets it (no completion hook
// from chat's async generation), so before this fix a process-class instance
// (SessionPolicyFreshPerWake — a brand new session every wake, by design)
// was wakeable exactly once, ever — every wake after the first was silently
// skipped with "wake already active" forever, including CW-20260816-0021's
// own daily scheduled tick. This proves a process-class instance already
// sitting Active still wakes (and gets a genuinely fresh session, distinct
// from whatever session it was "active" with before).
//
// The profile explicitly sets ActivationMode: "fresh-per-wake" -- Phase 1
// item 02 (TASKS/phase-1/02-add-agents-composition-columns.md) rewired
// wakeSkipReason to read this column instead of the hardcoded
// `lifecycle_class != process` check the CW-20260817 fix originally used,
// so this fixture now has to carry the same real-world value migration
// 110's backfill gives every actual process-class row (loom-curator,
// atlas-curator) to keep exercising the exact behavior this test's name
// promises.
func TestDurableWakeProcessClassRewakeableWhileActive(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Rewake Agent", Slug: "rewake-agent", SystemPrompt: "x", Class: "process", ActivationMode: "fresh-per-wake"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "workspace-rewake", Name: "Workspace Rewake"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	priorSession := &store.Session{WorkspaceID: "workspace-rewake", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(priorSession); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Rewake Instance",
		Slug:             "rewake-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		Status:           store.DurableAgentStatusActive,
		CurrentSessionID: priorSession.ID,
	}
	if err := st.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(inst.ID, priorSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{
		WorkspaceID: "workspace-rewake",
		WakePayload: DurableAgentWakePayload{Reason: "callback:wiki_page"},
	})
	if err != nil {
		t.Fatalf("Wake: unexpected error: %v", err)
	}
	if result.Skipped {
		t.Fatalf("process-class wake skipped while instance was already active: %+v", result)
	}
	if result.LaunchResult == nil || result.LaunchResult.Session == nil {
		t.Fatalf("wake result missing launch_result/session: %+v", result)
	}
	if result.LaunchResult.Session.ID == priorSession.ID {
		t.Fatalf("expected a fresh session distinct from the prior active one, got the same session %s", priorSession.ID)
	}
}

// TestDurableWakeAdvisorClassStillBlockedWhileActive confirms the fix above
// is scoped to compositions whose activation_mode actually says
// fresh-per-wake/concurrent: an advisor-class instance (which reuses one
// long-lived session — SessionPolicyReuseLatestOrCreate) has a profile
// that defaults to activation_mode='singleton' (DefaultActivationModeForClass),
// and must still skip a wake while already active, since "active" there
// means "has a live session to reuse", not "finished its one-shot work".
func TestDurableWakeAdvisorClassStillBlockedWhileActive(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Advisor Agent", Slug: "advisor-agent", SystemPrompt: "x", Class: "advisor"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if profile.ActivationMode != "singleton" {
		t.Fatalf("test assumption broken: advisor-class default ActivationMode = %q, want 'singleton'", profile.ActivationMode)
	}
	inst := &store.DurableAgentInstance{
		Name:           "Advisor Instance",
		Slug:           "advisor-instance",
		ProfileID:      profile.ID,
		LifecycleClass: store.DurableAgentClassAdvisor,
		Provider:       "anthropic",
		Model:          "model-a",
		RuntimeKind:    "api",
		Status:         store.DurableAgentStatusActive,
	}
	if err := st.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{
		WorkspaceID: "workspace-does-not-exist",
		WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeManual},
	})
	if err != nil {
		t.Fatalf("Wake: unexpected error: %v", err)
	}
	if !result.Skipped || result.SkipReason != "wake already active" {
		t.Fatalf("expected advisor-class wake to stay blocked while active, got %+v", result)
	}
}

// TestDurableWakeTemplateClassRewakeableWhileActive is the generalization
// this task's rewrite adds on top of the CW-20260817 fix: the original fix
// only exempted lifecycle_class == process from the "wake already active"
// block, leaving template-class instances (SessionPolicyFreshOneShot — a
// brand new session per invocation, same as process's FreshPerWake, with
// the same "no reuse collision to guard against" reasoning) with the exact
// same latent bug. content-writer (a real, currently-active template-class
// agent per the production backup inspected for this task) is exactly this
// case. Once wakeSkipReason reads activation_mode instead of
// lifecycle_class, a template-class composition with activation_mode=
// 'fresh-per-wake' (the value migration 110's backfill gives content-writer)
// gets the same rewake fix process already had.
func TestDurableWakeTemplateClassRewakeableWhileActive(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Template Rewake Agent", Slug: "template-rewake-agent", SystemPrompt: "x", Class: "template", ActivationMode: "fresh-per-wake"}
	if err := st.CreateAgent(profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := st.CreateWorkspace(&store.Workspace{ID: "workspace-template-rewake", Name: "Workspace Template Rewake"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	priorSession := &store.Session{WorkspaceID: "workspace-template-rewake", Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(priorSession); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Template Rewake Instance",
		Slug:             "template-rewake-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassTemplate,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		Status:           store.DurableAgentStatusActive,
		CurrentSessionID: priorSession.ID,
	}
	if err := st.CreateDurableAgentInstance(inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(inst.ID, priorSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{
		WorkspaceID: "workspace-template-rewake",
		WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeManual},
	})
	if err != nil {
		t.Fatalf("Wake: unexpected error: %v", err)
	}
	if result.Skipped {
		t.Fatalf("template-class wake with activation_mode=fresh-per-wake skipped while instance was already active: %+v", result)
	}
}
