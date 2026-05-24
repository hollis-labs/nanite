package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
		ScheduleKind: ScheduleKindEveryNTicks,
		ScheduleSpec: "5",
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

// TestScheduleFires_EveryNTicks verifies the every_n_ticks firing math:
// fires when tickN > 0 && tickN % spec == 0.
func TestScheduleFires_EveryNTicks(t *testing.T) {
	sch := AgentSchedule{ScheduleKind: ScheduleKindEveryNTicks, ScheduleSpec: "5"}
	now := time.Now()
	cases := []struct {
		tickN int
		want  bool
	}{
		{0, false}, {1, false}, {4, false}, {5, true}, {10, true}, {11, false},
	}
	for _, tc := range cases {
		got, err := scheduleFires(sch, tc.tickN, now)
		if err != nil {
			t.Fatalf("tick %d: %v", tc.tickN, err)
		}
		if got != tc.want {
			t.Errorf("tick %d every_n_ticks 5: got=%v want=%v", tc.tickN, got, tc.want)
		}
	}
}

// TestScheduleFires_OnTick verifies on_tick fires exactly when tickN == spec.
func TestScheduleFires_OnTick(t *testing.T) {
	sch := AgentSchedule{ScheduleKind: ScheduleKindOnTick, ScheduleSpec: "42"}
	now := time.Now()
	cases := []struct {
		tickN int
		want  bool
	}{
		{41, false}, {42, true}, {43, false},
	}
	for _, tc := range cases {
		got, err := scheduleFires(sch, tc.tickN, now)
		if err != nil {
			t.Fatalf("tick %d: %v", tc.tickN, err)
		}
		if got != tc.want {
			t.Errorf("tick %d on_tick 42: got=%v want=%v", tc.tickN, got, tc.want)
		}
	}
}

// TestScheduleFires_OneShot fires only when fired_count == 0.
func TestScheduleFires_OneShot(t *testing.T) {
	now := time.Now()

	first := AgentSchedule{ScheduleKind: ScheduleKindOneShot, FiredCount: 0}
	got, err := scheduleFires(first, 7, now)
	if err != nil || !got {
		t.Errorf("one_shot fired_count=0: got=%v err=%v want=true", got, err)
	}

	already := AgentSchedule{ScheduleKind: ScheduleKindOneShot, FiredCount: 1}
	got, err = scheduleFires(already, 7, now)
	if err != nil || got {
		t.Errorf("one_shot fired_count=1: got=%v err=%v want=false", got, err)
	}
}

// TestScheduleFires_Cron — at minute 9:00 UTC daily, the schedule `0 9 * * *`
// should fire when "now" is between 09:00 and 09:15 UTC (the spike's
// 15-min lookback window).
func TestScheduleFires_Cron(t *testing.T) {
	sch := AgentSchedule{ScheduleKind: ScheduleKindCron, ScheduleSpec: "0 9 * * *"}

	// 09:05 UTC — inside the 15-min lookback after 09:00, should fire.
	inside := time.Date(2026, 5, 20, 9, 5, 0, 0, time.UTC)
	got, err := scheduleFires(sch, 1, inside)
	if err != nil {
		t.Fatalf("cron inside window: %v", err)
	}
	if !got {
		t.Error("cron at 09:05 should fire (9:00 cron, 15-min lookback)")
	}

	// 10:30 UTC — well past the window, should not fire (next 09:00 is tomorrow).
	outside := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC)
	got, err = scheduleFires(sch, 1, outside)
	if err != nil {
		t.Fatalf("cron outside window: %v", err)
	}
	if got {
		t.Error("cron at 10:30 should NOT fire (window is past)")
	}
}

// TestScheduleFires_OnEvent — Phase A is a no-op for event-driven kinds.
// The composer's event resolver lands in Phase B.
func TestScheduleFires_OnEvent(t *testing.T) {
	sch := AgentSchedule{ScheduleKind: ScheduleKindOnEvent, ScheduleSpec: "mail_received"}
	got, err := scheduleFires(sch, 1, time.Now())
	if err != nil {
		t.Fatalf("on_event Phase A: %v", err)
	}
	if got {
		t.Error("on_event Phase A should be a no-op (never fires)")
	}
}

// TestGetDueSchedules_PriorityAndPausedExclusion exercises the full DB
// query: active + matching tick + ordered by priority DESC.
func TestGetDueSchedules_PriorityAndPausedExclusion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-due")
	now := time.Date(2026, 5, 20, 22, 51, 0, 0, time.UTC)

	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "p-hi", AgentID: agent.ID, Name: "hi", Priority: 20,
		ScheduleKind: ScheduleKindEveryNTicks, ScheduleSpec: "5",
		Body: "high-priority",
	}))
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "p-lo", AgentID: agent.ID, Name: "lo", Priority: 5,
		ScheduleKind: ScheduleKindEveryNTicks, ScheduleSpec: "5",
		Body: "low-priority",
	}))
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "paused", AgentID: agent.ID, Name: "paused", Priority: 100,
		ScheduleKind: ScheduleKindEveryNTicks, ScheduleSpec: "5",
		Body: "should not appear", Status: ScheduleStatusPaused,
	}))
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "wrong-tick", AgentID: agent.ID, Name: "wt", Priority: 30,
		ScheduleKind: ScheduleKindEveryNTicks, ScheduleSpec: "7",
		Body: "wrong cadence",
	}))

	due, err := s.GetDueSchedules(ctx, agent.ID, "", 10, now)
	if err != nil {
		t.Fatalf("GetDueSchedules: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected 2 due schedules at tick 10, got %d: %+v", len(due), due)
	}
	if due[0].ID != "p-hi" || due[1].ID != "p-lo" {
		t.Errorf("priority order wrong: %s then %s", due[0].ID, due[1].ID)
	}
}

// TestGetDueSchedules_SessionScope verifies session-scoped rows match only
// for that session, while session_id=NULL rows match all sessions.
func TestGetDueSchedules_SessionScope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-scope")
	now := time.Now().UTC()

	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "all-sessions", AgentID: agent.ID, Name: "global",
		ScheduleKind: ScheduleKindOneShot, Body: "global one-shot",
	}))
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "sess-A", AgentID: agent.ID, SessionID: "session-A",
		Name: "scoped", ScheduleKind: ScheduleKindOneShot, Body: "A-only",
	}))

	dueA, err := s.GetDueSchedules(ctx, agent.ID, "session-A", 1, now)
	if err != nil {
		t.Fatalf("GetDueSchedules A: %v", err)
	}
	if len(dueA) != 2 {
		t.Errorf("session A: expected 2 (global + A-only), got %d", len(dueA))
	}

	dueB, err := s.GetDueSchedules(ctx, agent.ID, "session-B", 1, now)
	if err != nil {
		t.Fatalf("GetDueSchedules B: %v", err)
	}
	if len(dueB) != 1 {
		t.Errorf("session B: expected 1 (global only), got %d", len(dueB))
	}
}

// TestGetDueSchedules_ExpiresAt excludes rows whose expires_at is in the
// past.
func TestGetDueSchedules_ExpiresAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sched-expires")
	now := time.Date(2026, 5, 20, 22, 51, 0, 0, time.UTC)

	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "live", AgentID: agent.ID, Name: "live",
		ScheduleKind: ScheduleKindOneShot, Body: "still good",
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
	}))
	must(t, s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "expired", AgentID: agent.ID, Name: "expired",
		ScheduleKind: ScheduleKindOneShot, Body: "stale",
		ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339),
	}))

	due, err := s.GetDueSchedules(ctx, agent.ID, "", 1, now)
	if err != nil {
		t.Fatalf("GetDueSchedules: %v", err)
	}
	if len(due) != 1 || due[0].ID != "live" {
		t.Errorf("expires_at filter wrong: %+v", due)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
