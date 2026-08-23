package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestValidateAgentSchedule_ProducerParity pins the shared malformed-cron
// rule against the row shapes built by each of the three production
// producers. Their transport-specific error wrappers may differ, but the
// domain rejection they call is identical.
func TestValidateAgentSchedule_ProducerParity(t *testing.T) {
	base := AgentSchedule{
		ID:           "schedule-id",
		AgentID:      "agent-id",
		Name:         "name",
		ScheduleKind: ScheduleKindCron,
		ScheduleSpec: "not a cron expression",
		Body:         "body",
	}
	producerRows := map[string]AgentSchedule{
		"http": base,
		"reflex": func() AgentSchedule {
			row := base
			row.ID = "reflex-schedule-id"
			row.CreatedBy = "reflex:add_schedule"
			return row
		}(),
		"self-tool": func() AgentSchedule {
			row := base
			row.ID = "self-schedule-id"
			row.CreatedBy = "self:agent-id"
			row.MaxRetries = 3
			row.OnFail = ScheduleOnFailRetry
			row.JobType = ScheduleJobTypeDurableAgentWake
			return row
		}(),
	}

	const sharedRule = "schedule_spec is not a valid cron expression"
	for producer, row := range producerRows {
		t.Run(producer, func(t *testing.T) {
			err := ValidateAgentSchedule(row)
			if err == nil || !strings.Contains(err.Error(), sharedRule) {
				t.Fatalf("ValidateAgentSchedule() = %v, want %q", err, sharedRule)
			}
		})
	}
}

func TestValidateAgentSchedule_AcceptsLoopRunTick(t *testing.T) {
	err := ValidateAgentSchedule(AgentSchedule{
		ID:           "loop-schedule-id",
		AgentID:      "agent-id",
		Name:         "loop tick",
		ScheduleKind: ScheduleKindCron,
		ScheduleSpec: "*/5 * * * *",
		Body:         "tick",
		JobType:      ScheduleJobTypeLoopRunTick,
		JobPayload:   `{"loop_id":"loop-id"}`,
	})
	if err != nil {
		t.Fatalf("ValidateAgentSchedule(loop_run_tick) = %v", err)
	}
}

// TestAgentSchedule_RoundTrip exercises the basic CRUD path: insert, get,
// list, status update, fire-count bump, delete.
func TestAgentSchedule_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-rt")

	row := AgentSchedule{
		ID:           "sched-001",
		AgentID:      agent.ID,
		Name:         "audit-cycle",
		ScheduleKind: ScheduleKindCron,
		ScheduleSpec: "0 9 * * *",
		Body:         "## Audit\nThis tick, audit the most recent run.",
		Priority:     10,
		CreatedBy:    "operator",
	}
	if err := s.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	got, err := s.GetAgentSchedule(ctx, "sched-001")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if got.Name != "audit-cycle" || got.Priority != 10 || got.Status != ScheduleStatusActive {
		t.Errorf("unexpected row: %+v", got)
	}

	list, err := s.ListAgentSchedules(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListAgentSchedules: got %d, want 1", len(list))
	}

	if err := s.UpdateAgentScheduleStatus(ctx, "sched-001", ScheduleStatusPaused); err != nil {
		t.Fatalf("UpdateAgentScheduleStatus: %v", err)
	}
	got, _ = s.GetAgentSchedule(ctx, "sched-001")
	if got.Status != ScheduleStatusPaused {
		t.Errorf("status after pause: %q", got.Status)
	}

	now := time.Date(2026, 5, 20, 22, 51, 0, 0, time.UTC)
	if err := s.BumpAgentScheduleFireCount(ctx, "sched-001", now); err != nil {
		t.Fatalf("BumpAgentScheduleFireCount: %v", err)
	}
	got, _ = s.GetAgentSchedule(ctx, "sched-001")
	if got.FiredCount != 1 || got.LastFiredAt == "" {
		t.Errorf("fire bump: count=%d last=%q", got.FiredCount, got.LastFiredAt)
	}

	if err := s.DeleteAgentSchedule(ctx, "sched-001"); err != nil {
		t.Fatalf("DeleteAgentSchedule: %v", err)
	}
	if _, err := s.GetAgentSchedule(ctx, "sched-001"); !errors.Is(err, ErrAgentScheduleNotFound) {
		t.Errorf("post-delete get: got %v, want ErrAgentScheduleNotFound", err)
	}
}

// TestAgentSchedule_InvalidStatus confirms the validator rejects unknown
// status strings before issuing the UPDATE.
func TestAgentSchedule_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-invalid-status")
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "x", AgentID: agent.ID, Name: "n", ScheduleKind: ScheduleKindOneShot, Body: "b",
	}))
	if err := s.UpdateAgentScheduleStatus(ctx, "x", "wat"); err == nil {
		t.Fatal("expected error for invalid status")
	}
}

// TestAgentSchedule_RetryPolicyAndJobFields_RoundTrip is the regression
// test for TASKS/scheduling/01-schema-schedule-kind-collapse-and-retry-
// columns.md's Done means bullet: MaxRetries/OnFail/NextRun/JobType/
// JobPayload round-trip correctly through InsertAgentSchedule/
// GetAgentSchedule.
func TestAgentSchedule_RetryPolicyAndJobFields_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-retry-fields")

	nextRun := time.Date(2026, 8, 21, 3, 0, 0, 0, time.UTC).Format(time.RFC3339)
	row := AgentSchedule{
		ID:           "sched-retry-001",
		AgentID:      agent.ID,
		Name:         "explicit-fields",
		ScheduleKind: ScheduleKindCron,
		ScheduleSpec: "0 3 * * *",
		Body:         "irrelevant for non-durable_agent_wake job types, but NOT NULL",
		MaxRetries:   5,
		OnFail:       ScheduleOnFailDisable,
		NextRun:      nextRun,
		JobType:      ScheduleJobTypeCommandRun,
		JobPayload:   `{"command":"lint","args":["--fix"]}`,
	}
	if err := s.InsertAgentSchedule(ctx, row); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}

	got, err := s.GetAgentSchedule(ctx, "sched-retry-001")
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if got.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", got.MaxRetries)
	}
	if got.OnFail != ScheduleOnFailDisable {
		t.Errorf("OnFail = %q, want %q", got.OnFail, ScheduleOnFailDisable)
	}
	if got.NextRun != nextRun {
		t.Errorf("NextRun = %q, want %q", got.NextRun, nextRun)
	}
	if got.JobType != ScheduleJobTypeCommandRun {
		t.Errorf("JobType = %q, want %q", got.JobType, ScheduleJobTypeCommandRun)
	}
	if got.JobPayload != `{"command":"lint","args":["--fix"]}` {
		t.Errorf("JobPayload = %q, want the inserted JSON", got.JobPayload)
	}

	// Zero-value MaxRetries/OnFail/JobType/JobPayload on insert fall back to
	// the documented Go-side defaults (mirroring the column DDL defaults),
	// and NextRun stays empty (DB NULL) rather than defaulting to anything.
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "sched-retry-002", AgentID: agent.ID, Name: "defaults",
		ScheduleKind: ScheduleKindOneShot, Body: "b",
	}))
	defaults, err := s.GetAgentSchedule(ctx, "sched-retry-002")
	if err != nil {
		t.Fatalf("GetAgentSchedule (defaults): %v", err)
	}
	if defaults.MaxRetries != 3 {
		t.Errorf("default MaxRetries = %d, want 3", defaults.MaxRetries)
	}
	if defaults.OnFail != ScheduleOnFailRetry {
		t.Errorf("default OnFail = %q, want %q", defaults.OnFail, ScheduleOnFailRetry)
	}
	if defaults.JobType != ScheduleJobTypeDurableAgentWake {
		t.Errorf("default JobType = %q, want %q", defaults.JobType, ScheduleJobTypeDurableAgentWake)
	}
	if defaults.JobPayload != "{}" {
		t.Errorf("default JobPayload = %q, want {}", defaults.JobPayload)
	}
	if defaults.NextRun != "" {
		t.Errorf("default NextRun = %q, want empty (NULL)", defaults.NextRun)
	}
}

// TestAgentSchedule_ScheduleKindCheckRejectsRetiredValues confirms
// migration 127's narrowed CHECK constraint actually rejects the three
// retired schedule_kind values at the DB level, not just at the (now-
// removed) Go constant level.
func TestAgentSchedule_ScheduleKindCheckRejectsRetiredValues(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-retired-kind")

	for _, kind := range []string{"every_n_ticks", "on_tick", "on_event"} {
		err := s.InsertAgentSchedule(ctx, AgentSchedule{
			ID: "sched-retired-" + kind, AgentID: agent.ID, Name: kind,
			ScheduleKind: kind, Body: "b",
		})
		if err == nil {
			t.Errorf("schedule_kind=%q: expected CHECK violation, got nil error", kind)
		}
	}
}

// TestBackfillScheduleNextRun_Cron proves an active, pre-existing cron row
// with NULL next_run gets backfilled to the real next cron occurrence
// after "now", not just any non-null placeholder.
func TestBackfillScheduleNextRun_Cron(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-backfill-cron")

	// Matches the one real production row found in the backup DB during
	// this task's verification: cron, "0 3 * * *", active, never fired.
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "cron-row", AgentID: agent.ID, Name: "lint-and-export",
		ScheduleKind: ScheduleKindCron, ScheduleSpec: "0 3 * * *",
		Body: "run lint and export",
	}))
	// InsertAgentSchedule leaves NextRun empty (NULL) when not given —
	// confirm that starting assumption before backfilling.
	before, err := s.GetAgentSchedule(ctx, "cron-row")
	if err != nil {
		t.Fatalf("GetAgentSchedule (before): %v", err)
	}
	if before.NextRun != "" {
		t.Fatalf("precondition: NextRun = %q, want empty before backfill", before.NextRun)
	}

	now := time.Date(2026, 8, 20, 22, 0, 0, 0, time.UTC) // well past 03:00 today
	if err := s.backfillScheduleNextRun(ctx, now); err != nil {
		t.Fatalf("backfillScheduleNextRun: %v", err)
	}

	after, err := s.GetAgentSchedule(ctx, "cron-row")
	if err != nil {
		t.Fatalf("GetAgentSchedule (after): %v", err)
	}
	if after.NextRun == "" {
		t.Fatal("NextRun still empty after backfill — row silently lost its due-ness")
	}
	got, err := time.Parse(time.RFC3339, after.NextRun)
	if err != nil {
		t.Fatalf("NextRun %q did not parse as RFC3339: %v", after.NextRun, err)
	}
	want := time.Date(2026, 8, 21, 3, 0, 0, 0, time.UTC) // next 03:00 after "now"
	if !got.Equal(want) {
		t.Errorf("NextRun = %v, want %v (next real 03:00 UTC occurrence)", got, want)
	}

	// Idempotent: a second call must not overwrite an already-backfilled
	// value.
	if err := s.backfillScheduleNextRun(ctx, now.Add(time.Hour)); err != nil {
		t.Fatalf("backfillScheduleNextRun (second call): %v", err)
	}
	again, err := s.GetAgentSchedule(ctx, "cron-row")
	if err != nil {
		t.Fatalf("GetAgentSchedule (second call): %v", err)
	}
	if again.NextRun != after.NextRun {
		t.Errorf("second backfillScheduleNextRun call changed NextRun: %q -> %q", after.NextRun, again.NextRun)
	}
}

// TestBackfillScheduleNextRun_OneShotAndNonActive proves: an active
// one_shot row backfills to "due now" (matching wakeScheduleDue's real
// existing semantics — a one_shot row has no independent target-time
// encoding and is due immediately until it fires once), while paused and
// expired rows are left alone (NextRun stays empty/NULL).
func TestBackfillScheduleNextRun_OneShotAndNonActive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-backfill-oneshot")

	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "one-shot-active", AgentID: agent.ID, Name: "active-one-shot",
		ScheduleKind: ScheduleKindOneShot, Body: "b",
	}))
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "cron-paused", AgentID: agent.ID, Name: "paused-cron",
		ScheduleKind: ScheduleKindCron, ScheduleSpec: "0 9 * * *", Body: "b",
		Status: ScheduleStatusPaused,
	}))
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "cron-expired", AgentID: agent.ID, Name: "expired-cron",
		ScheduleKind: ScheduleKindCron, ScheduleSpec: "0 9 * * *", Body: "b",
		Status: ScheduleStatusExpired,
	}))

	now := time.Date(2026, 8, 20, 22, 0, 0, 0, time.UTC)
	if err := s.backfillScheduleNextRun(ctx, now); err != nil {
		t.Fatalf("backfillScheduleNextRun: %v", err)
	}

	oneShot, err := s.GetAgentSchedule(ctx, "one-shot-active")
	if err != nil {
		t.Fatalf("GetAgentSchedule (one-shot): %v", err)
	}
	got, err := time.Parse(time.RFC3339, oneShot.NextRun)
	if err != nil {
		t.Fatalf("one_shot NextRun %q did not parse: %v", oneShot.NextRun, err)
	}
	if !got.Equal(now) {
		t.Errorf("one_shot NextRun = %v, want %v (due now)", got, now)
	}

	paused, err := s.GetAgentSchedule(ctx, "cron-paused")
	if err != nil {
		t.Fatalf("GetAgentSchedule (paused): %v", err)
	}
	if paused.NextRun != "" {
		t.Errorf("paused row NextRun = %q, want empty (backfill should not touch non-active rows)", paused.NextRun)
	}

	expired, err := s.GetAgentSchedule(ctx, "cron-expired")
	if err != nil {
		t.Fatalf("GetAgentSchedule (expired): %v", err)
	}
	if expired.NextRun != "" {
		t.Errorf("expired row NextRun = %q, want empty", expired.NextRun)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
