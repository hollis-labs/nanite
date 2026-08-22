package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- test helpers ---------------------------------------------------------

// newAdapterTestStore opens a real, file-backed *store.Store (not an
// in-memory-only fake) -- TASKS/scheduling/02-store-adapter.md's own Done
// means bullet requires the concurrent-claim regression test run against a
// real store, given the whole CAS-safety argument rests on
// sqlitekit.OpenSingle's single-writer-connection configuration. Mirrors
// the store.New(ctx, filepath.Join(t.TempDir(), "test.db")) pattern already
// used by internal/service's own tests (e.g. a2a_push_notifier_test.go),
// since internal/store's own newTestStore helper is unexported and package-
// scoped.
func newAdapterTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

func makeAdapterTestAgent(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{
		Name:         "Test Agent " + slug,
		Slug:         slug,
		SystemPrompt: "You are a test agent.",
	}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return a
}

func makeAdapterTestInstance(t *testing.T, s *store.Store, profileID, slug string) *store.DurableAgentInstance {
	t.Helper()
	inst := &store.DurableAgentInstance{
		Name:      "Instance " + slug,
		Slug:      slug,
		ProfileID: profileID,
	}
	if err := s.CreateDurableAgentInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	return inst
}

func mustInsertSchedule(t *testing.T, s *store.Store, row store.AgentSchedule) {
	t.Helper()
	if err := s.InsertAgentSchedule(context.Background(), row); err != nil {
		t.Fatalf("InsertAgentSchedule(%s): %v", row.ID, err)
	}
}

// --- ListDueSchedules: filtering ------------------------------------------

func TestStoreAdapter_ListDueSchedules_ExcludesDisabledFutureAndRespectsLimit(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "list-due-filter")
	makeAdapterTestInstance(t, s, agent.ID, "list-due-filter-inst")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute).Format(time.RFC3339)
	future := now.Add(time.Hour).Format(time.RFC3339)

	// Due, active -- should be included.
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "due-active", AgentID: agent.ID, Name: "due-active",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: due,
	})
	// Due, but paused -- excluded.
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "due-paused", AgentID: agent.ID, Name: "due-paused",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: due, Status: store.ScheduleStatusPaused,
	})
	// Due, but expired -- excluded.
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "due-expired", AgentID: agent.ID, Name: "due-expired",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: due, Status: store.ScheduleStatusExpired,
	})
	// Active, but not due yet -- excluded.
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "future-active", AgentID: agent.ID, Name: "future-active",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: future,
	})
	// Active, due, but next_run left NULL (never backfilled/scheduled) --
	// excluded, matching go-scheduler's own "zero NextRun is unscheduled"
	// convention.
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "unscheduled", AgentID: agent.ID, Name: "unscheduled",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	got, err := adapter.ListDueSchedules(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	if len(got) != 1 || got[0].ID != "due-active" {
		t.Fatalf("ListDueSchedules = %+v, want exactly [due-active]", got)
	}

	// Limit: insert two more due+active rows, confirm limit=2 caps the
	// result even though 3 rows now qualify.
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "due-active-2", AgentID: agent.ID, Name: "due-active-2",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "due-active-3", AgentID: agent.ID, Name: "due-active-3",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: due,
	})
	limited, err := adapter.ListDueSchedules(ctx, now, 2)
	if err != nil {
		t.Fatalf("ListDueSchedules (limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListDueSchedules with limit=2 returned %d rows, want 2", len(limited))
	}
}

// --- ListDueSchedules: durable_agent_wake payload / instance resolution --

func TestStoreAdapter_ListDueSchedules_DurableAgentWake_ResolvesInstanceID(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "wake-resolve")
	inst := makeAdapterTestInstance(t, s, agent.ID, "wake-resolve-inst")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "wake-1", AgentID: agent.ID, Name: "wake-1",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "0 3 * * *",
		Body:    "## Wake\nDo the thing.",
		JobType: store.ScheduleJobTypeDurableAgentWake,
		NextRun: now.Add(-time.Minute).Format(time.RFC3339),
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	due, err := adapter.ListDueSchedules(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("ListDueSchedules = %d rows, want 1", len(due))
	}
	sched := due[0]
	if sched.JobType != JobTypeDurableAgentWake {
		t.Errorf("JobType = %q, want %q", sched.JobType, JobTypeDurableAgentWake)
	}
	var payload DurableAgentWakePayload
	if err := json.Unmarshal(sched.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.InstanceID != inst.ID {
		t.Errorf("InstanceID = %q, want %q", payload.InstanceID, inst.ID)
	}
	if payload.Prompt != "## Wake\nDo the thing." {
		t.Errorf("Prompt = %q, want row.Body forwarded verbatim", payload.Prompt)
	}
}

func TestStoreAdapter_ListDueSchedules_DurableAgentWake_NoMatchingInstance_Skipped(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "wake-no-instance")
	// Deliberately no durable_agent_instances row for this profile.

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "wake-orphan", AgentID: agent.ID, Name: "wake-orphan",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType: store.ScheduleJobTypeDurableAgentWake,
		NextRun: now.Add(-time.Minute).Format(time.RFC3339),
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	due, err := adapter.ListDueSchedules(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("ListDueSchedules = %+v, want empty (unresolvable instance skipped, not erroring the tick)", due)
	}
}

func TestStoreAdapter_ListDueSchedules_DurableAgentWake_AmbiguousInstances_Skipped(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "wake-ambiguous")
	makeAdapterTestInstance(t, s, agent.ID, "wake-ambiguous-inst-1")
	makeAdapterTestInstance(t, s, agent.ID, "wake-ambiguous-inst-2")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "wake-ambig", AgentID: agent.ID, Name: "wake-ambig",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType: store.ScheduleJobTypeDurableAgentWake,
		NextRun: now.Add(-time.Minute).Format(time.RFC3339),
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	due, err := adapter.ListDueSchedules(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("ListDueSchedules = %+v, want empty (ambiguous instance target skipped)", due)
	}
}

// --- ListDueSchedules: pass-through job_payload for the other three types

func TestStoreAdapter_ListDueSchedules_PassThroughJobPayload(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "pass-through")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute).Format(time.RFC3339)

	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "wf-1", AgentID: agent.ID, Name: "wf-1",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "0 9 * * *", Body: "b",
		JobType:    store.ScheduleJobTypeAgentWorkflowRun,
		JobPayload: `{"workflow_name":"daily-audit","agent_profile_id":"profile-1","timeout_seconds":60}`,
		NextRun:    due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "cmd-1", AgentID: agent.ID, Name: "cmd-1",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "*/10 * * * *", Body: "b",
		JobType:    store.ScheduleJobTypeCommandRun,
		JobPayload: `{"agent_id":"agent-1","command":"audit_report_generate"}`,
		NextRun:    due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "rfx-1", AgentID: agent.ID, Name: "rfx-1",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType:    store.ScheduleJobTypeReflexDispatch,
		JobPayload: `{"reflex_id":"rfx-42"}`,
		NextRun:    due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "loop-tick-1", AgentID: agent.ID, Name: "loop-tick-1",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType:    store.ScheduleJobTypeLoopRunTick,
		JobPayload: `{"loop_run_id":"lr-42"}`,
		NextRun:    due,
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	got, err := adapter.ListDueSchedules(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	byID := map[string]gosched.Schedule{}
	for _, sched := range got {
		byID[sched.ID] = sched
	}
	if len(byID) != 4 {
		t.Fatalf("ListDueSchedules = %d rows, want 4", len(byID))
	}

	var wf AgentWorkflowRunPayload
	if err := json.Unmarshal(byID["wf-1"].Payload, &wf); err != nil {
		t.Fatalf("decode agent_workflow_run payload: %v", err)
	}
	if wf.WorkflowName != "daily-audit" || wf.AgentProfileID != "profile-1" || wf.TimeoutSeconds != 60 {
		t.Errorf("agent_workflow_run payload = %+v, want fields preserved from job_payload", wf)
	}

	var cmd CommandRunPayload
	if err := json.Unmarshal(byID["cmd-1"].Payload, &cmd); err != nil {
		t.Fatalf("decode command_run payload: %v", err)
	}
	if cmd.AgentID != "agent-1" || cmd.Command != "audit_report_generate" {
		t.Errorf("command_run payload = %+v, want fields preserved from job_payload", cmd)
	}

	var rfx ReflexDispatchPayload
	if err := json.Unmarshal(byID["rfx-1"].Payload, &rfx); err != nil {
		t.Fatalf("decode reflex_dispatch payload: %v", err)
	}
	if rfx.ReflexID != "rfx-42" {
		t.Errorf("reflex_dispatch payload = %+v, want ReflexID rfx-42", rfx)
	}

	var tick LoopRunTickPayload
	if err := json.Unmarshal(byID["loop-tick-1"].Payload, &tick); err != nil {
		t.Fatalf("decode loop_run_tick payload: %v", err)
	}
	if tick.LoopRunID != "lr-42" {
		t.Errorf("loop_run_tick payload = %+v, want LoopRunID lr-42", tick)
	}
}

// TestStoreAdapter_Payload_CompatibleWithRunnerAdapter_AllJobTypes proves
// this adapter's Payload encoding is provably compatible with 03's already-
// committed RunnerAdapter.Enqueue decoding -- not just internally self-
// consistent -- by feeding a real gosched.Job built from this adapter's
// ListDueSchedules output straight into RunnerAdapter.Enqueue (reusing the
// same fakes runner_adapter_test.go already defines, since both files are
// package scheduler).
func TestStoreAdapter_Payload_CompatibleWithRunnerAdapter_AllJobTypes(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "compat")
	inst := makeAdapterTestInstance(t, s, agent.ID, "compat-inst")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute).Format(time.RFC3339)

	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "compat-wake", AgentID: agent.ID, Name: "compat-wake",
		ScheduleKind: store.ScheduleKindOneShot, Body: "do the thing",
		JobType: store.ScheduleJobTypeDurableAgentWake,
		NextRun: due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "compat-wf", AgentID: agent.ID, Name: "compat-wf",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType:    store.ScheduleJobTypeAgentWorkflowRun,
		JobPayload: `{"workflow_name":"daily-audit","agent_profile_id":"profile-1"}`,
		NextRun:    due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "compat-cmd", AgentID: agent.ID, Name: "compat-cmd",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType:    store.ScheduleJobTypeCommandRun,
		JobPayload: `{"command":"lint"}`,
		NextRun:    due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "compat-rfx", AgentID: agent.ID, Name: "compat-rfx",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType:    store.ScheduleJobTypeReflexDispatch,
		JobPayload: `{"reflex_id":"rfx-1"}`,
		NextRun:    due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "compat-loop-tick", AgentID: agent.ID, Name: "compat-loop-tick",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType:    store.ScheduleJobTypeLoopRunTick,
		JobPayload: `{"loop_run_id":"lr-1"}`,
		NextRun:    due,
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	due2, err := adapter.ListDueSchedules(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	byID := map[string]gosched.Schedule{}
	for _, sched := range due2 {
		byID[sched.ID] = sched
	}
	if len(byID) != 5 {
		t.Fatalf("ListDueSchedules = %d rows, want 5", len(byID))
	}

	waker := &fakeDurableAgentWaker{}
	launcher := &fakeWorkflowLauncher{}
	exec := &fakeCommandExecutor{}
	reflex := &store.AgentReflex{
		ID: "rfx-1", AgentID: agent.ID, ActionKind: store.ReflexActionInjectReminder,
		Status: store.ReflexStatusActive,
	}
	resumer := &fakeLoopResumer{}
	runner := &RunnerAdapter{
		Wake:           waker,
		Workflows:      launcher,
		Commands:       exec,
		ReflexLookup:   fakeReflexLookupFor(map[string]*store.AgentReflex{"rfx-1": reflex}),
		ReflexExecutor: testExecutor(),
		Loops:          resumer,
		LoopRunLookup:  fakeLoopRunLookupFor(map[string]*store.LoopRun{"lr-1": {ID: "lr-1", Status: store.LoopRunStatusWaitingOnEscalation}}),
	}

	for schedID, wantJobType := range map[string]string{
		"compat-wake":      JobTypeDurableAgentWake,
		"compat-wf":        JobTypeAgentWorkflowRun,
		"compat-cmd":       JobTypeCommandRun,
		"compat-rfx":       JobTypeReflexDispatch,
		"compat-loop-tick": JobTypeLoopRunTick,
	} {
		sched := byID[schedID]
		if sched.JobType != wantJobType {
			t.Fatalf("%s: JobType = %q, want %q", schedID, sched.JobType, wantJobType)
		}
		job := gosched.Job{
			ScheduleID: sched.ID,
			RunID:      "run-" + sched.ID,
			JobType:    sched.JobType,
			Payload:    sched.Payload,
			FiredAt:    now,
		}
		if err := runner.Enqueue(ctx, job); err != nil {
			t.Fatalf("Enqueue(%s): unexpected error: %v", schedID, err)
		}
	}

	if !waker.called || waker.gotInstanceID != inst.ID {
		t.Errorf("Wake: called=%v gotInstanceID=%q, want called with instance %q", waker.called, waker.gotInstanceID, inst.ID)
	}
	if !launcher.called || launcher.gotReq.WorkflowName != "daily-audit" {
		t.Errorf("Launch: called=%v gotReq=%+v", launcher.called, launcher.gotReq)
	}
	if !exec.called || exec.gotTool != "lint" {
		t.Errorf("Execute: called=%v gotTool=%q", exec.called, exec.gotTool)
	}
	if !resumer.called || resumer.gotLoopRunID != "lr-1" {
		t.Errorf("Resume: called=%v gotLoopRunID=%q, want called with lr-1", resumer.called, resumer.gotLoopRunID)
	}
}

// --- ClaimAndUpdateScheduleRun: the CAS claim ------------------------------

// TestStoreAdapter_ClaimAndUpdateScheduleRun_ConcurrentClaim is the single
// most important correctness property in this task: two goroutines racing
// ClaimAndUpdateScheduleRun on the same due schedule -- exactly one must
// win, the other must see (false, nil), never an error and never a silent
// double-claim. Run against a real *store.Store (sqlitekit.OpenSingle's
// single-writer connection pool), not an in-memory-only fake, per the
// task's own Done-means bullet.
func TestStoreAdapter_ClaimAndUpdateScheduleRun_ConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "cas-race")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	expectedNext := now.Add(-time.Minute)
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "cas-race-1", AgentID: agent.ID, Name: "cas-race-1",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: expectedNext.Format(time.RFC3339),
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}

	const attempts = 8
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	claimed := make([]bool, attempts)
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			ok, err := adapter.ClaimAndUpdateScheduleRun(ctx, "cas-race-1", expectedNext, now, now.Add(time.Minute))
			claimed[i] = ok
			errs[i] = err
		}(i)
	}
	start.Done() // release all goroutines together
	done.Wait()

	winners := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, err)
		}
		if claimed[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 (claimed=%v)", winners, claimed)
	}

	got, err := s.GetAgentSchedule(ctx, "cas-race-1")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	wantNext := now.Add(time.Minute).Format(time.RFC3339)
	if got.NextRun != wantNext {
		t.Errorf("NextRun after claim = %q, want %q", got.NextRun, wantNext)
	}
	wantLastFired := now.Format(time.RFC3339)
	if got.LastFiredAt != wantLastFired {
		t.Errorf("LastFiredAt after claim = %q, want %q", got.LastFiredAt, wantLastFired)
	}
}

func TestStoreAdapter_ClaimAndUpdateScheduleRun_WrongExpectedNext_ReturnsFalseNotError(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "cas-mismatch")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	realNext := now.Add(-time.Minute)
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "cas-mismatch-1", AgentID: agent.ID, Name: "cas-mismatch-1",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: realNext.Format(time.RFC3339),
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	wrongExpected := realNext.Add(-time.Hour)
	claimed, err := adapter.ClaimAndUpdateScheduleRun(ctx, "cas-mismatch-1", wrongExpected, now, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAndUpdateScheduleRun: unexpected error: %v", err)
	}
	if claimed {
		t.Fatalf("ClaimAndUpdateScheduleRun: claimed=true for a mismatched expectedNext, want false")
	}

	got, err := s.GetAgentSchedule(ctx, "cas-mismatch-1")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if got.NextRun != realNext.Format(time.RFC3339) {
		t.Errorf("NextRun changed on a losing claim: got %q, want unchanged %q", got.NextRun, realNext.Format(time.RFC3339))
	}
}

// --- SetScheduleNextRun / DisableSchedule ---------------------------------

func TestStoreAdapter_SetScheduleNextRun(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "set-next-run")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "set-next-run-1", AgentID: agent.ID, Name: "set-next-run-1",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "* * * * *", Body: "b",
		NextRun: now.Format(time.RFC3339),
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	rollback := now.Add(-30 * time.Minute)
	if err := adapter.SetScheduleNextRun(ctx, "set-next-run-1", rollback); err != nil {
		t.Fatalf("SetScheduleNextRun: %v", err)
	}
	got, err := s.GetAgentSchedule(ctx, "set-next-run-1")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if got.NextRun != rollback.UTC().Format(time.RFC3339) {
		t.Errorf("NextRun = %q, want %q", got.NextRun, rollback.UTC().Format(time.RFC3339))
	}
}

func TestStoreAdapter_DisableSchedule(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "disable")

	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "disable-1", AgentID: agent.ID, Name: "disable-1",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	if err := adapter.DisableSchedule(ctx, "disable-1"); err != nil {
		t.Fatalf("DisableSchedule: %v", err)
	}
	got, err := s.GetAgentSchedule(ctx, "disable-1")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if got.Status != store.ScheduleStatusExpired {
		t.Errorf("Status after DisableSchedule = %q, want %q", got.Status, store.ScheduleStatusExpired)
	}
}

// --- next-run round trip: one_shot and cron -------------------------------

// TestStoreAdapter_NextRunRoundTrip_OneShotAndCron proves both a one_shot
// schedule's next-run handling and a cron schedule's (computed via
// gosched.NextRun, exactly as the real engine's tick() does) round-trip
// correctly through the adapter: ListDueSchedules -> engine-style claim ->
// persisted state matches what the engine would have computed.
func TestStoreAdapter_NextRunRoundTrip_OneShotAndCron(t *testing.T) {
	ctx := context.Background()
	s := newAdapterTestStore(t)
	agent := makeAdapterTestAgent(t, s, "next-run-roundtrip")

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute).Format(time.RFC3339)

	// job_type=command_run (not the default durable_agent_wake) so this
	// test's focus -- next-run computation -- isn't entangled with
	// durable_agent_wake's separate instance-resolution requirement
	// (covered by its own tests above).
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "oneshot-rt", AgentID: agent.ID, Name: "oneshot-rt",
		ScheduleKind: store.ScheduleKindOneShot, Body: "b",
		JobType: store.ScheduleJobTypeCommandRun, JobPayload: `{"command":"noop"}`,
		NextRun: due,
	})
	mustInsertSchedule(t, s, store.AgentSchedule{
		ID: "cron-rt", AgentID: agent.ID, Name: "cron-rt",
		ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "*/5 * * * *", Body: "b",
		JobType: store.ScheduleJobTypeCommandRun, JobPayload: `{"command":"noop"}`,
		NextRun: due,
	})

	adapter := &StoreAdapter{Store: s, Logger: slog.Default()}
	got, err := adapter.ListDueSchedules(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	byID := map[string]gosched.Schedule{}
	for _, sched := range got {
		byID[sched.ID] = sched
	}
	if len(byID) != 2 {
		t.Fatalf("ListDueSchedules = %d rows, want 2", len(byID))
	}

	oneShot := byID["oneshot-rt"]
	if oneShot.CronExpr != "" {
		t.Errorf("one_shot CronExpr = %q, want empty (engine's one-time signal)", oneShot.CronExpr)
	}
	cron := byID["cron-rt"]
	if cron.CronExpr != "*/5 * * * *" {
		t.Errorf("cron CronExpr = %q, want %q", cron.CronExpr, "*/5 * * * *")
	}

	// Simulate exactly what libs/go-scheduler/engine.go's tick() does for a
	// one-time schedule: claim with a far-future placeholder, then disable
	// after a successful dispatch.
	const oneTimeHorizon = 100 * 365 * 24 * time.Hour
	oneShotNext := now.Add(oneTimeHorizon)
	claimed, err := adapter.ClaimAndUpdateScheduleRun(ctx, oneShot.ID, oneShot.NextRun, now, oneShotNext)
	if err != nil {
		t.Fatalf("ClaimAndUpdateScheduleRun (one_shot): %v", err)
	}
	if !claimed {
		t.Fatalf("ClaimAndUpdateScheduleRun (one_shot): claimed=false, want true")
	}
	if err := adapter.DisableSchedule(ctx, oneShot.ID); err != nil {
		t.Fatalf("DisableSchedule: %v", err)
	}
	gotOneShot, err := s.GetAgentSchedule(ctx, "oneshot-rt")
	if err != nil {
		t.Fatalf("GetAgentSchedule (one_shot): %v", err)
	}
	if gotOneShot.Status != store.ScheduleStatusExpired {
		t.Errorf("one_shot Status after disable = %q, want %q", gotOneShot.Status, store.ScheduleStatusExpired)
	}
	parsedOneShotNext, err := time.Parse(time.RFC3339, gotOneShot.NextRun)
	if err != nil {
		t.Fatalf("parse one_shot NextRun: %v", err)
	}
	if !parsedOneShotNext.Equal(oneShotNext) {
		t.Errorf("one_shot NextRun = %v, want %v", parsedOneShotNext, oneShotNext)
	}

	// Simulate the cron case: the engine computes the real next occurrence
	// via gosched.NextRun (not any hand-rolled cron math of this adapter's
	// own), then claims with it.
	cronNext, err := gosched.NextRun(cron.CronExpr, now)
	if err != nil {
		t.Fatalf("gosched.NextRun: %v", err)
	}
	wantCronNext := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	if !cronNext.Equal(wantCronNext) {
		t.Fatalf("gosched.NextRun(%q, %v) = %v, want %v", cron.CronExpr, now, cronNext, wantCronNext)
	}
	claimed, err = adapter.ClaimAndUpdateScheduleRun(ctx, cron.ID, cron.NextRun, now, cronNext)
	if err != nil {
		t.Fatalf("ClaimAndUpdateScheduleRun (cron): %v", err)
	}
	if !claimed {
		t.Fatalf("ClaimAndUpdateScheduleRun (cron): claimed=false, want true")
	}
	gotCron, err := s.GetAgentSchedule(ctx, "cron-rt")
	if err != nil {
		t.Fatalf("GetAgentSchedule (cron): %v", err)
	}
	parsedCronNext, err := time.Parse(time.RFC3339, gotCron.NextRun)
	if err != nil {
		t.Fatalf("parse cron NextRun: %v", err)
	}
	if !parsedCronNext.Equal(cronNext) {
		t.Errorf("cron NextRun after claim = %v, want %v", parsedCronNext, cronNext)
	}
	// A cron schedule is not disabled by the engine -- confirm status stays
	// active.
	if gotCron.Status != store.ScheduleStatusActive {
		t.Errorf("cron Status = %q, want %q (engine never disables a recurring schedule)", gotCron.Status, store.ScheduleStatusActive)
	}
}

// Compile-time assertion mirrored here too, matching runner_adapter_test.go's
// own precedent, so a future edit that breaks interface satisfaction fails
// this package's own tests, not just the build.
var _ gosched.Store = (*StoreAdapter)(nil)
