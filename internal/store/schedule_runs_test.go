package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func makeTestSchedule(t *testing.T, s *Store, agentID, id string, next time.Time) string {
	t.Helper()
	if err := s.InsertAgentSchedule(context.Background(), AgentSchedule{
		ID: id, AgentID: agentID, Name: "test-" + id, Body: "body",
		ScheduleKind: ScheduleKindCron, ScheduleSpec: "* * * * *",
		NextRun: next.UTC().Format(time.RFC3339Nano),
		JobType: ScheduleJobTypeCommandRun, JobPayload: `{"command":"noop"}`,
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}
	return id
}

func testFireCreation(scheduleID, fireID string, at time.Time) ScheduleFireCreation {
	return ScheduleFireCreation{
		ScheduleID: scheduleID, ExpectedNext: at, NextRun: at.Add(time.Minute),
		Fire: ScheduleFire{
			ID: fireID, RunID: fireID, ScheduleID: scheduleID,
			ScheduledAt:      at.UTC().Format(time.RFC3339Nano),
			Status:           ScheduleFireStatusPending,
			NextAttemptAt:    at.UTC().Format(time.RFC3339Nano),
			RetryMaxAttempts: 3, RetryBackoffStrategy: "exponential",
			RetryInitialDelayNanos: int64(30 * time.Second),
			RetryMaximumDelayNanos: int64(5 * time.Minute),
			JobType:                ScheduleJobTypeCommandRun, JobPayload: `{"command":"noop"}`,
		},
	}
}

func TestScheduleFireCreateIsAtomicAndIdentityIsImmutable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "fire-create")
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	scheduleID := makeTestSchedule(t, s, agent.ID, "fire-create", at)
	creation := testFireCreation(scheduleID, "fire-stable", at)

	created, err := s.CreateScheduleFire(ctx, creation)
	if err != nil || !created {
		t.Fatalf("CreateScheduleFire = (%v, %v), want (true, nil)", created, err)
	}
	created, err = s.CreateScheduleFire(ctx, creation)
	if err != nil || created {
		t.Fatalf("duplicate CreateScheduleFire = (%v, %v), want (false, nil)", created, err)
	}
	row, err := s.GetScheduleFire(ctx, "fire-stable")
	if err != nil {
		t.Fatalf("GetScheduleFire: %v", err)
	}
	if row.RunID != "fire-stable" || row.Status != ScheduleFireStatusPending || row.AttemptCount != 0 {
		t.Fatalf("unexpected fire: %+v", row)
	}
	schedule, err := s.GetAgentSchedule(ctx, scheduleID)
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if schedule.NextRun != creation.NextRun.Format(time.RFC3339Nano) || schedule.LastFiredAt != creation.Fire.ScheduledAt {
		t.Fatalf("schedule was not atomically advanced: %+v", schedule)
	}
	if schedule.FiredCount != 1 {
		t.Fatalf("fired_count = %d, want 1 after one materialization", schedule.FiredCount)
	}
	if _, err := s.GetScheduleFire(ctx, "missing"); !errors.Is(err, ErrScheduleFireNotFound) {
		t.Fatalf("missing fire error = %v", err)
	}
}

func TestScheduleFireClaimRecoveryAndFencedTransition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "fire-claim")
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	scheduleID := makeTestSchedule(t, s, agent.ID, "fire-claim", at)
	creation := testFireCreation(scheduleID, "fire-claim-id", at)
	if ok, err := s.CreateScheduleFire(ctx, creation); err != nil || !ok {
		t.Fatalf("CreateScheduleFire = (%v, %v)", ok, err)
	}

	firstAt := at.Add(time.Second)
	first, won, err := s.ClaimScheduleFire(ctx, ScheduleFireClaim{
		FireID: "fire-claim-id", ExpectedStatus: ScheduleFireStatusPending,
		ExpectedAttempt: 0, ClaimedAt: firstAt, ClaimExpiresAt: firstAt.Add(time.Minute),
	})
	if err != nil || !won || first.AttemptCount != 1 || first.Status != ScheduleFireStatusClaimed {
		t.Fatalf("first claim = (%+v, %v, %v)", first, won, err)
	}
	if _, earlyWon, earlyErr := s.ClaimScheduleFire(ctx, ScheduleFireClaim{
		FireID: first.RunID, ExpectedStatus: ScheduleFireStatusClaimed,
		ExpectedAttempt: 1, ExpectedFiredAt: firstAt,
		ClaimedAt: firstAt.Add(30 * time.Second), ClaimExpiresAt: firstAt.Add(2 * time.Minute),
	}); earlyErr != nil || earlyWon {
		t.Fatalf("unexpired recovery = (%v, %v), want false,nil", earlyWon, earlyErr)
	}

	recoveredAt := firstAt.Add(time.Minute)
	recovered, won, err := s.ClaimScheduleFire(ctx, ScheduleFireClaim{
		FireID: first.RunID, ExpectedStatus: ScheduleFireStatusClaimed,
		ExpectedAttempt: 1, ExpectedFiredAt: firstAt,
		ClaimedAt: recoveredAt, ClaimExpiresAt: recoveredAt.Add(time.Minute),
	})
	if err != nil || !won || recovered.AttemptCount != 1 {
		t.Fatalf("expired recovery = (%+v, %v, %v)", recovered, won, err)
	}
	if won, err := s.TransitionScheduleFire(ctx, ScheduleFireTransition{
		FireID: first.RunID, Attempt: 1, From: ScheduleFireStatusClaimed,
		ClaimedAt: firstAt, To: ScheduleFireStatusSucceeded,
	}); err != nil || won {
		t.Fatalf("stale transition = (%v, %v), want false,nil", won, err)
	}
	if won, err := s.TransitionScheduleFire(ctx, ScheduleFireTransition{
		FireID: recovered.RunID, Attempt: 1, From: ScheduleFireStatusClaimed,
		ClaimedAt: recoveredAt, To: ScheduleFireStatusRetrying,
		NextAttemptAt: recoveredAt.Add(30 * time.Second), LastError: "boom",
	}); err != nil || !won {
		t.Fatalf("winning transition = (%v, %v), want true,nil", won, err)
	}
	row, _ := s.GetScheduleFire(ctx, recovered.RunID)
	if row.Status != ScheduleFireStatusRetrying || row.AttemptCount != 1 || row.LastError != "boom" || row.ClaimExpiresAt != "" {
		t.Fatalf("unexpected transitioned fire: %+v", row)
	}
}

func TestListDueScheduleFiresHonorsBackoffLeaseAndTerminalStates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "fire-due")
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"due", "future", "claimed", "terminal"} {
		at := now.Add(time.Duration(i) * time.Second)
		scheduleID := makeTestSchedule(t, s, agent.ID, "sched-"+id, at)
		creation := testFireCreation(scheduleID, "fire-"+id, at)
		if ok, err := s.CreateScheduleFire(ctx, creation); err != nil || !ok {
			t.Fatalf("create %s = (%v,%v)", id, ok, err)
		}
	}
	if won, err := s.TransitionScheduleFire(ctx, ScheduleFireTransition{
		FireID: "fire-terminal", Attempt: 0, From: ScheduleFireStatusPending,
		To: ScheduleFireStatusSucceeded,
	}); err != nil || !won {
		t.Fatalf("terminal transition = (%v,%v)", won, err)
	}
	claimedAt := now.Add(-2 * time.Minute)
	if _, won, err := s.ClaimScheduleFire(ctx, ScheduleFireClaim{
		FireID: "fire-claimed", ExpectedStatus: ScheduleFireStatusPending,
		ExpectedAttempt: 0, ClaimedAt: claimedAt, ClaimExpiresAt: now.Add(-time.Minute),
	}); err != nil || !won {
		t.Fatalf("expired claim = (%v,%v)", won, err)
	}
	due, err := s.ListDueScheduleFires(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListDueScheduleFires: %v", err)
	}
	if len(due) != 2 || due[0].RunID != "fire-claimed" && due[1].RunID != "fire-claimed" {
		t.Fatalf("due fires = %+v, want pending due + expired claim", due)
	}
}
