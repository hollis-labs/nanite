package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
	if err := st.CreateAgent(ctx, profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := st.CreateDurableAgentInstance(ctx, inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
		if err := st.CreateDurableAgentInstance(context.Background(), &instances[i]); err != nil {
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	priorSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), priorSession); err != nil {
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(context.Background(), inst.ID, priorSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{
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
	if err := st.CreateAgent(context.Background(), profile); err != nil {
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{
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
// 'fresh-per-wake' (the value migration 117's backfill gives content-writer)
// gets the same rewake fix process already had.
func TestDurableWakeTemplateClassRewakeableWhileActive(t *testing.T) {
	st := newDurableAgentServiceTestStore(t)
	ctx := context.Background()
	profile := &store.AgentProfile{Name: "Template Rewake Agent", Slug: "template-rewake-agent", SystemPrompt: "x", Class: "template", ActivationMode: "fresh-per-wake"}
	if err := st.CreateAgent(context.Background(), profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	priorSession := &store.Session{Provider: "anthropic", Model: "model-a"}
	if err := st.CreateSession(context.Background(), priorSession); err != nil {
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
	if err := st.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := st.AttachDurableAgentInstanceSession(context.Background(), inst.ID, priorSession.ID, store.DurableAgentSessionRelationWake); err != nil {
		t.Fatalf("AttachDurableAgentInstanceSession: %v", err)
	}

	wakeSvc := NewDurableAgentWakeService(st, NewDurableAgentService(st))
	result, err := wakeSvc.Wake(ctx, inst.ID, DurableAgentWakeRequest{
		WakePayload: DurableAgentWakePayload{Reason: DurableAgentWakeManual},
	})
	if err != nil {
		t.Fatalf("Wake: unexpected error: %v", err)
	}
	if result.Skipped {
		t.Fatalf("template-class wake with activation_mode=fresh-per-wake skipped while instance was already active: %+v", result)
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
	profile := &store.AgentProfile{Name: "Bump Fail Agent", Slug: "bump-fail-agent", SystemPrompt: "x", Class: "process", ActivationMode: "fresh-per-wake"}
	if err := st.CreateAgent(ctx, profile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
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
	if err := st.CreateDurableAgentInstance(ctx, inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
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
