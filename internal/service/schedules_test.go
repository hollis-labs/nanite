package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func newScheduleTestService(t *testing.T) (*ScheduleService, *store.Store, string) {
	t.Helper()
	st := newConfigTestStore(t)
	agent := &store.AgentProfile{Name: "Scheduled", Slug: "scheduled-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return NewScheduleService(st), st, agent.ID
}

// expectedNextRuns returns the next_run values ComputeAgentScheduleNextRun
// gives for spec just before and just after fn runs, so a test is not
// flaky across a cron boundary.
func expectedNextRuns(spec string, fn func()) (before, after string) {
	format := func() string {
		return store.ComputeAgentScheduleNextRun(store.ScheduleKindCron, spec, time.Now()).UTC().Format(time.RFC3339)
	}
	before = format()
	fn()
	after = format()
	return before, after
}

func TestScheduleServiceCreateSetsIDAndNextRun(t *testing.T) {
	svc, _, agentID := newScheduleTestService(t)
	var created *store.AgentSchedule
	var err error
	before, after := expectedNextRuns("0 9 * * *", func() {
		created, err = svc.Create(context.Background(), store.AgentSchedule{
			AgentID: agentID, Name: "n", ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "0 9 * * *", Body: "b",
		})
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(created.ID) <= len("sched-") || created.ID[:6] != "sched-" {
		t.Fatalf("id = %q, want sched-<ulid>", created.ID)
	}
	if created.NextRun != before && created.NextRun != after {
		t.Fatalf("next_run = %q, want %q or %q", created.NextRun, before, after)
	}
}

// Changing schedule_spec recomputes next_run for the new expression.
func TestScheduleServicePatchSpecRecomputesNextRun(t *testing.T) {
	ctx := context.Background()
	svc, _, agentID := newScheduleTestService(t)
	created, err := svc.Create(ctx, store.AgentSchedule{
		AgentID: agentID, Name: "n", ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "0 9 * * *", Body: "b",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spec := "30 17 * * *"
	var patched *store.AgentSchedule
	before, after := expectedNextRuns(spec, func() {
		patched, err = svc.Patch(ctx, created.ID, SchedulePatch{ScheduleSpec: &spec})
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if patched.NextRun != before && patched.NextRun != after {
		t.Fatalf("next_run = %q after spec change, want %q or %q (was %q)", patched.NextRun, before, after, created.NextRun)
	}
}

// Moving status into active from a non-active status recomputes a stale
// next_run; staying non-active does not.
func TestScheduleServicePatchIntoActiveRecomputesNextRun(t *testing.T) {
	ctx := context.Background()
	svc, st, agentID := newScheduleTestService(t)
	const stale = "2000-01-01T00:00:00Z"
	row := store.AgentSchedule{
		ID: "sched-paused", AgentID: agentID, Name: "n", ScheduleKind: store.ScheduleKindCron,
		ScheduleSpec: "0 9 * * *", Body: "b", Status: store.ScheduleStatusPaused, NextRun: stale,
	}
	if err := st.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	name := "renamed"
	still, err := svc.Patch(ctx, row.ID, SchedulePatch{Name: &name})
	if err != nil {
		t.Fatalf("Patch (paused): %v", err)
	}
	if still.NextRun != stale {
		t.Fatalf("next_run = %q after a non-activating patch, want it left at %q", still.NextRun, stale)
	}

	active := store.ScheduleStatusActive
	var patched *store.AgentSchedule
	before, after := expectedNextRuns("0 9 * * *", func() {
		patched, err = svc.Patch(ctx, row.ID, SchedulePatch{Status: &active})
	})
	if err != nil {
		t.Fatalf("Patch (activate): %v", err)
	}
	if patched.NextRun != before && patched.NextRun != after {
		t.Fatalf("next_run = %q after reactivation, want %q or %q", patched.NextRun, before, after)
	}
}

func TestScheduleServicePatchRejectsEmptyStatusAndOnFail(t *testing.T) {
	ctx := context.Background()
	svc, _, agentID := newScheduleTestService(t)
	created, err := svc.Create(ctx, store.AgentSchedule{
		AgentID: agentID, Name: "n", ScheduleKind: store.ScheduleKindCron, ScheduleSpec: "0 9 * * *", Body: "b",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	empty := ""
	for name, p := range map[string]SchedulePatch{
		"status must not be empty":  {Status: &empty},
		"on_fail must not be empty": {OnFail: &empty},
	} {
		_, err := svc.Patch(ctx, created.ID, p)
		var writeErr *ScheduleWriteError
		if !errors.As(err, &writeErr) || err.Error() != name {
			t.Fatalf("err = %v, want *ScheduleWriteError %q", err, name)
		}
	}
	if _, err := svc.Patch(ctx, "missing", SchedulePatch{}); !errors.Is(err, store.ErrAgentScheduleNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}
