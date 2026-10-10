package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

type expiryStatusFailingStore struct {
	*store.Store
	err error
}

func (s *expiryStatusFailingStore) UpdateAgentScheduleStatus(context.Context, string, string) error {
	return s.err
}

type bumpFailingStore struct {
	*store.Store
	err error
}

func (s *bumpFailingStore) BumpAgentScheduleFireCount(context.Context, string, time.Time) error {
	return s.err
}

func TestDurableWakeListDueAndDryRun(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	profile := &store.AgentProfile{Name: "Wake Agent", Slug: "wake-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
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
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
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
		// NextRun set explicitly (TASKS/scheduling/05-engine-wiring-and-
		// full-replace.md) -- ListDue's due-check now reads next_run
		// (scheduleDueByNextRun, durable_wake.go) instead of the removed
		// wakeScheduleDue's FiredCount==0-means-due-now heuristic for
		// one_shot rows; InsertAgentSchedule itself never defaults
		// NextRun (an empty value is a real "unscheduled", not a gap to
		// fill in), so a "due now" test fixture must set it explicitly,
		// same as any real schedule producer now must.
		NextRun: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	// Phase 0 item 20 (retire workspaces): a due item with no attached
	// session and default status is no longer skipped with "workspace
	// unavailable" — that gate is retired along with sessions.workspace_id.
	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	items, err := wakeSvc.ListDue(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(items) != 1 || items[0].Schedule.ID != "sched-1" || items[0].SkipReason != "" {
		t.Fatalf("due items = %+v", items)
	}

	run, err := wakeSvc.RunDue(ctx, DurableAgentWakeRunRequest{Now: time.Now().UTC(), DryRun: true})
	if err != nil {
		t.Fatalf("RunDue dry-run: %v", err)
	}
	if len(run.Results) != 1 || run.Results[0].Skipped {
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
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	seedSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), seedSession); err != nil {
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
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(context.Background(), inst.ID, seedSession.ID, store.DurableAgentSessionRelationWake); err != nil {
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
		NextRun:      time.Now().UTC().Format(time.RFC3339),
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

func TestDurableWakeRunDueLogsExpiryFailureAndPreservesSuccess(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Expiry Log Agent", Slug: "expiry-log-agent", SystemPrompt: "x"}
	if err := persistTestActor(ctx, st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	seedSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(ctx, seedSession); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Expiry Log Instance",
		Slug:             "expiry-log-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		CurrentSessionID: seedSession.ID,
	}
	if err := persistTestDurableInstance(ctx, st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(ctx, inst.ID, seedSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}
	const scheduleID = "sched-expiry-log"
	if err := st.InsertAgentSchedule(ctx, store.AgentSchedule{
		ID:           scheduleID,
		AgentID:      profile.ID,
		Name:         "expiry logging",
		ScheduleKind: store.ScheduleKindOneShot,
		Body:         "wake",
		Status:       store.ScheduleStatusActive,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		NextRun:      time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	expiryErr := errors.New("expiry write unavailable")
	failingStore := &expiryStatusFailingStore{Store: st, err: expiryErr}
	wakeSvc := NewDurableAgentWakeService(failingStore, NewDurableAgentService(failingStore))
	run, err := wakeSvc.RunDue(ctx, DurableAgentWakeRunRequest{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("RunDue returned expiry failure instead of preserving best-effort success: %v", err)
	}
	if len(run.Results) != 1 || run.Results[0].LaunchResult == nil || run.Results[0].FailureReason != "" {
		t.Fatalf("RunDue result = %+v, want one successful launch", run.Results)
	}
	schedule, err := st.GetAgentSchedule(ctx, scheduleID)
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if schedule.FiredCount != 1 {
		t.Fatalf("FiredCount = %d, want 1", schedule.FiredCount)
	}
	if schedule.Status != store.ScheduleStatusActive {
		t.Fatalf("Status = %q, want active because the injected expiry write failed", schedule.Status)
	}

	logOutput := logs.String()
	for _, want := range []string{
		`"msg":"durable wake: failed to expire one-shot schedule"`,
		`"schedule_id":"` + scheduleID + `"`,
		`"instance_id":"` + inst.ID + `"`,
		`"err":"` + expiryErr.Error() + `"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("warning log %q missing from %s", want, logOutput)
		}
	}
}

func TestDurableWakeRunDueSkipsPausedAndActiveInstances(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Wake Skip Agent", Slug: "wake-skip-agent", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	// "active" uses DurableAgentClassTemplate, not Process: per the
	// CW-20260817 fix (see TestDurableWakeProcessClassRewakeableWhileActive),
	// a process-class instance already Active is intentionally rewakeable,
	// not skipped. Advisor class isn't wake-managed at all (wakeManagedLifecycle
	// only includes Process/Template), so it would drop out of ListDue
	// entirely rather than surface as a skipped result — Template is the
	// wake-managed class that still stays blocked while active
	// (LifecycleClass != Process triggers "wake already active").
	instances := []store.DurableAgentInstance{
		{Name: "paused", Slug: "paused", ProfileID: profile.ID, LifecycleClass: store.DurableAgentClassProcess, Status: store.DurableAgentStatusPaused},
		{Name: "active", Slug: "active", ProfileID: profile.ID, LifecycleClass: store.DurableAgentClassTemplate, Status: store.DurableAgentStatusActive},
	}
	for i := range instances {
		if err := persistTestDurableInstance(context.Background(), st, &instances[i]); err != nil {
			t.Fatalf("persist prior instance %d: %v", i, err)
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
		NextRun:      time.Now().UTC().Format(time.RFC3339),
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

// TestDurableWakeNoAttachedSessionStillSucceeds is the post-Phase-0-item-20
// (retire workspaces) replacement for the old "no workspace_id means the
// wake is skipped with workspace unavailable" regression test — that gate
// (and sessions.workspace_id, the column it depended on) is retired in
// full. A process-class instance with no attached session and no
// caller-supplied ProjectID now succeeds (creates a fresh session) rather
// than being skipped, since Wake()/Start() no longer require a workspace
// to create one.
func TestDurableWakeNoAttachedSessionStillSucceeds(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Wake Agent 2", Slug: "wake-agent-2", SystemPrompt: "x"}
	if err := persistTestActor(context.Background(), st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:           "Wake Instance 2",
		Slug:           "wake-instance-2",
		ProfileID:      profile.ID,
		LifecycleClass: store.DurableAgentClassProcess,
		Provider:       "anthropic",
		Model:          "model-a",
		RuntimeKind:    "api",
	}
	if err := persistTestDurableInstance(context.Background(), st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeProcessTick}})
	if err != nil {
		t.Fatalf("Wake: unexpected error: %v", err)
	}
	if result == nil || result.Skipped {
		t.Fatalf("wake result = %+v, want a non-skipped wake", result)
	}
	if result.LaunchResult == nil || result.LaunchResult.Session == nil {
		t.Fatalf("wake result missing launch_result/session: %+v", result)
	}
	events, err := st.ListDurableAgentEvents(context.Background(), inst.ID, 20)
	if err != nil {
		t.Fatalf("ListDurableAgentEvents: %v", err)
	}
	if !durableAgentEventsContain(events, store.DurableAgentEventWakeRequested) || !durableAgentEventsContain(events, store.DurableAgentEventWakeStarted) {
		t.Fatalf("expected wake_requested and wake_started events, got %+v", events)
	}
}

// Fresh pinned policy has no supported concurrent activation field. Journal
// class and historical activation_mode cannot grant repeated active wakes.
func TestDurableWakeFreshActiveInstancesStayConservative(t *testing.T) {
	for _, class := range []string{store.DurableAgentClassAdvisor, store.DurableAgentClassProcess, store.DurableAgentClassTemplate} {
		t.Run(class, func(t *testing.T) {
			st := newDurableAgentServiceTestStore(t)
			profile := &store.AgentProfile{Name: "Active prior", Slug: "active-" + class, SystemPrompt: "Private pin"}
			if err := persistTestActor(t.Context(), st, profile); err != nil {
				t.Fatal(err)
			}
			// A retained namesake claims concurrency; it is historical data only.
			durableHistoricalFixture(t, st, profile.Slug, class, `["durable-agent"]`)
			session := &store.Session{Provider: "anthropic", Model: "model-a"}
			if err := st.CreateSession(t.Context(), session); err != nil {
				t.Fatal(err)
			}
			inst := &store.DurableAgentInstance{Name: "Active prior", Slug: "active-" + class, ProfileID: profile.ID, LifecycleClass: class, Status: store.DurableAgentStatusActive, CurrentSessionID: session.ID, MetadataJSON: `{"activation_mode":"concurrent"}`}
			if err := persistTestDurableInstance(t.Context(), st, inst); err != nil {
				t.Fatal(err)
			}
			if err := st.AttachDurableAgentInstanceSession(t.Context(), inst.ID, session.ID, store.DurableAgentSessionRelationPrimary); err != nil {
				t.Fatal(err)
			}
			runtime := &fakeDurableRuntimeController{}
			svc := NewDurableAgentWakeService(st, NewDurableAgentServiceWithRuntime(st, runtime))
			before := durableAuthoritySnapshot(t, st)
			result, err := svc.Wake(t.Context(), inst.ID, DurableAgentWakeRequest{WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeManual, Prompt: "must not run"}})
			if err != nil || !result.Skipped || result.SkipReason != "wake already active" || result.LaunchResult != nil {
				t.Fatalf("active wake=%+v err=%v", result, err)
			}
			got, err := st.GetDurableAgentInstance(t.Context(), inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != store.DurableAgentStatusActive || got.CurrentSessionID != session.ID || got.ProfileID != profile.ID || got.URN != profile.ID {
				t.Fatalf("skipped wake changed journal/identity: %+v", got)
			}
			after := durableAuthoritySnapshot(t, st)
			// Wake audit events are expected; every authority/journal/relation row
			// must remain intact and no session may be attached.
			delete(before, `SELECT * FROM actor_instance_events ORDER BY id`)
			delete(after, `SELECT * FROM actor_instance_events ORDER BY id`)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("active wake changed authority or journal")
			}
			if len(runtime.sent) != 0 || len(runtime.recovered) != 0 {
				t.Fatalf("skipped wake invoked runtime: %+v", runtime)
			}
		})
	}
}

func TestDurableWakeActivationResolutionRequiresActualEnabledBinding(t *testing.T) {
	for _, binding := range []string{"disabled", "empty-receipt", "missing-port"} {
		t.Run(binding, func(t *testing.T) {
			st := newDurableAgentServiceTestStore(t)
			profile := &store.AgentProfile{Name: "Prior", Slug: "activation-" + binding, SystemPrompt: "Private pin"}
			if err := persistTestActor(t.Context(), st, profile); err != nil {
				t.Fatal(err)
			}
			inst := &store.DurableAgentInstance{Name: "Prior", Slug: profile.Slug, ProfileID: profile.ID, LifecycleClass: store.DurableAgentClassProcess, Status: store.DurableAgentStatusActive}
			if err := persistTestDurableInstance(t.Context(), st, inst); err != nil {
				t.Fatal(err)
			}
			var source durableWakeStore = st
			switch binding {
			case "disabled":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, profile.ID); err != nil {
					t.Fatal(err)
				}
			case "empty-receipt":
				if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_actor_bindings SET binding_receipt=' ' WHERE actor_uri=?`, profile.ID); err != nil {
					t.Fatal(err)
				}
			case "missing-port":
				source = struct{ durableWakeStore }{st}
			}
			svc := &durableWakeService{store: source}
			if mode := svc.activationModeForInstance(inst); mode != "" || wakeAllowsConcurrentActive(mode) {
				t.Fatalf("unavailable binding supplied activation=%q", mode)
			}
		})
	}
}

func TestDurableWakeAllowsConcurrentActiveOnlyForExplicitSupportedMode(t *testing.T) {
	for _, mode := range []string{"fresh-per-wake", "concurrent", "singleton", "", "process", "template", "unknown"} {
		want := mode == "fresh-per-wake" || mode == "concurrent"
		if got := wakeAllowsConcurrentActive(mode); got != want {
			t.Errorf("mode %q allows active=%v want %v", mode, got, want)
		}
		inst := &store.DurableAgentInstance{Status: store.DurableAgentStatusActive, LifecycleClass: store.DurableAgentClassProcess}
		reason := wakeSkipReason(inst, mode)
		if want && reason != "" || !want && reason != "wake already active" {
			t.Errorf("mode %q skip=%q", mode, reason)
		}
	}
}

// TestDurableWakeBumpFailureLoggedAndDoesNotBlockOneShotExpiry is the
// regression test for CW-20260824-0004: BumpAgentScheduleFireCount failures
// were silently dropped (no log), and the error gated one-shot expiry — a
// one-shot schedule that fired successfully but whose counter failed to
// increment stayed armed and could fire again forever. This test confirms:
// 1. Bump failures are logged with schedule_id and schedule_kind
// 2. One-shot schedules expire even when bump fails
// 3. Recurring schedules are unaffected (no expiry attempted)
func TestDurableWakeBumpFailureLoggedAndDoesNotBlockOneShotExpiry(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Bump Fail Agent", Slug: "bump-fail-agent", SystemPrompt: "x"}
	if err := persistTestActor(ctx, st, profile); err != nil {
		t.Fatalf("persist prior actor: %v", err)
	}
	seedSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(ctx, seedSession); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.DurableAgentInstance{
		Name:             "Bump Fail Instance",
		Slug:             "bump-fail-instance",
		ProfileID:        profile.ID,
		LifecycleClass:   store.DurableAgentClassProcess,
		Provider:         "anthropic",
		Model:            "model-a",
		RuntimeKind:      "api",
		CurrentSessionID: seedSession.ID,
	}
	if err := persistTestDurableInstance(ctx, st, inst); err != nil {
		t.Fatalf("persist prior instance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(ctx, inst.ID, seedSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}

	// Create one-shot and cron schedules
	oneShotID := "sched-bump-fail-oneshot"
	cronID := "sched-bump-fail-cron"
	if err := st.InsertAgentSchedule(ctx, store.AgentSchedule{
		ID:           oneShotID,
		AgentID:      profile.ID,
		Name:         "one-shot bump fail",
		ScheduleKind: store.ScheduleKindOneShot,
		Body:         "wake",
		Status:       store.ScheduleStatusActive,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		NextRun:      time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertAgentSchedule one-shot: %v", err)
	}
	if err := st.InsertAgentSchedule(ctx, store.AgentSchedule{
		ID:           cronID,
		AgentID:      profile.ID,
		Name:         "cron bump fail",
		ScheduleKind: store.ScheduleKindCron,
		ScheduleSpec: "0 0 * * *",
		Body:         "wake",
		Status:       store.ScheduleStatusActive,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		NextRun:      time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("InsertAgentSchedule cron: %v", err)
	}

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	bumpErr := errors.New("bump counter unavailable")
	failingStore := &bumpFailingStore{Store: st, err: bumpErr}
	wakeSvc := NewDurableAgentWakeService(failingStore, NewDurableAgentService(failingStore))
	run, err := wakeSvc.RunDue(ctx, DurableAgentWakeRunRequest{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("RunDue returned bump failure instead of preserving best-effort success: %v", err)
	}
	if len(run.Results) != 2 {
		t.Fatalf("RunDue results len = %d, want 2", len(run.Results))
	}

	// Verify one-shot schedule was expired despite bump failure
	oneShotSchedule, err := st.GetAgentSchedule(ctx, oneShotID)
	if err != nil {
		t.Fatalf("GetAgentSchedule one-shot: %v", err)
	}
	if oneShotSchedule.Status != store.ScheduleStatusExpired {
		t.Errorf("one-shot Status = %q, want expired (bump failure must not block expiry)", oneShotSchedule.Status)
	}
	// FiredCount will be 0 because the bump failed, but expiry still happened
	if oneShotSchedule.FiredCount != 0 {
		t.Errorf("one-shot FiredCount = %d, want 0 (bump failed)", oneShotSchedule.FiredCount)
	}

	// Verify cron schedule was not expired (cron schedules never expire)
	cronSchedule, err := st.GetAgentSchedule(ctx, cronID)
	if err != nil {
		t.Fatalf("GetAgentSchedule cron: %v", err)
	}
	if cronSchedule.Status != store.ScheduleStatusActive {
		t.Errorf("cron Status = %q, want active (cron schedules don't expire)", cronSchedule.Status)
	}

	// Verify bump failures were logged for both schedules
	logOutput := logs.String()
	for _, scheduleID := range []string{oneShotID, cronID} {
		for _, want := range []string{
			`"msg":"durable wake: failed to bump schedule fire count"`,
			`"schedule_id":"` + scheduleID + `"`,
			`"instance_id":"` + inst.ID + `"`,
			`"err":"` + bumpErr.Error() + `"`,
		} {
			if !strings.Contains(logOutput, want) {
				t.Errorf("bump failure log for schedule %s missing %q from %s", scheduleID, want, logOutput)
			}
		}
	}
	// Verify schedule_kind is logged (should appear twice, once for each schedule)
	if strings.Count(logOutput, `"schedule_kind":"one_shot"`) < 1 {
		t.Errorf("bump failure log missing schedule_kind for one-shot")
	}
	if strings.Count(logOutput, `"schedule_kind":"cron"`) < 1 {
		t.Errorf("bump failure log missing schedule_kind for cron")
	}
}
