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
