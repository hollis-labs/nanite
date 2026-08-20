package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// makeTestSchedule inserts a minimal agent_schedules row (the FK target for
// schedule_runs.schedule_id) and returns its ID.
func makeTestSchedule(t *testing.T, s *Store, agentID, id string) string {
	t.Helper()
	if err := s.InsertAgentSchedule(context.Background(), AgentSchedule{
		ID:           id,
		AgentID:      agentID,
		Name:         "test-schedule-" + id,
		ScheduleKind: ScheduleKindCron,
		ScheduleSpec: "0 9 * * *",
		Body:         "test body",
	}); err != nil {
		t.Fatalf("InsertAgentSchedule: %v", err)
	}
	return id
}

func TestScheduleRun_CreateAndGetOpen(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sr-create")
	schedID := makeTestSchedule(t, s, agent.ID, "sched-sr-create")

	if _, err := s.GetOpenScheduleRun(ctx, schedID); !errors.Is(err, ErrScheduleRunNotFound) {
		t.Fatalf("GetOpenScheduleRun before create: got err=%v, want ErrScheduleRunNotFound", err)
	}

	created, err := s.CreateScheduleRun(ctx, ScheduleRun{
		ScheduleID: schedID,
		RunID:      "run-1",
	})
	if err != nil {
		t.Fatalf("CreateScheduleRun: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateScheduleRun: expected generated ID")
	}
	if created.Status != ScheduleRunStatusPending || created.AttemptCount != 0 {
		t.Fatalf("CreateScheduleRun: unexpected defaults: %+v", created)
	}

	open, err := s.GetOpenScheduleRun(ctx, schedID)
	if err != nil {
		t.Fatalf("GetOpenScheduleRun after create: %v", err)
	}
	if open.ID != created.ID || open.RunID != "run-1" {
		t.Fatalf("GetOpenScheduleRun: unexpected row: %+v", open)
	}
}

func TestScheduleRun_RecordAttempt_FailureThenSuccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sr-record")
	schedID := makeTestSchedule(t, s, agent.ID, "sched-sr-record")

	row, err := s.CreateScheduleRun(ctx, ScheduleRun{ScheduleID: schedID, RunID: "run-1"})
	if err != nil {
		t.Fatalf("CreateScheduleRun: %v", err)
	}

	nextAt := time.Now().UTC().Add(30 * time.Second)
	if err := s.RecordScheduleRunAttempt(ctx, row.ID, ScheduleRunStatusFailed, "boom", &nextAt); err != nil {
		t.Fatalf("RecordScheduleRunAttempt (failed): %v", err)
	}

	open, err := s.GetOpenScheduleRun(ctx, schedID)
	if err != nil {
		t.Fatalf("GetOpenScheduleRun after failure: %v", err)
	}
	if open.AttemptCount != 1 {
		t.Fatalf("AttemptCount: got %d, want 1", open.AttemptCount)
	}
	if open.Status != ScheduleRunStatusFailed {
		t.Fatalf("Status: got %q, want failed", open.Status)
	}
	if open.LastError != "boom" {
		t.Fatalf("LastError: got %q, want boom", open.LastError)
	}
	if open.NextAttemptAt == "" {
		t.Fatalf("NextAttemptAt: expected non-empty")
	}

	if err := s.RecordScheduleRunAttempt(ctx, row.ID, ScheduleRunStatusSucceeded, "", nil); err != nil {
		t.Fatalf("RecordScheduleRunAttempt (succeeded): %v", err)
	}

	if _, err := s.GetOpenScheduleRun(ctx, schedID); !errors.Is(err, ErrScheduleRunNotFound) {
		t.Fatalf("GetOpenScheduleRun after success: got err=%v, want ErrScheduleRunNotFound (succeeded is terminal)", err)
	}

	latest, err := s.GetLatestScheduleRun(ctx, schedID)
	if err != nil {
		t.Fatalf("GetLatestScheduleRun: %v", err)
	}
	if latest.Status != ScheduleRunStatusSucceeded || latest.AttemptCount != 2 {
		t.Fatalf("unexpected latest row: %+v", latest)
	}
	if latest.NextAttemptAt != "" {
		t.Fatalf("NextAttemptAt should clear on success, got %q", latest.NextAttemptAt)
	}
}

func TestScheduleRun_RecordAttempt_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.RecordScheduleRunAttempt(ctx, "does-not-exist", ScheduleRunStatusFailed, "x", nil); !errors.Is(err, ErrScheduleRunNotFound) {
		t.Fatalf("RecordScheduleRunAttempt: got err=%v, want ErrScheduleRunNotFound", err)
	}
}

func TestScheduleRun_GetLatest_MultipleFirings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "sr-multi")
	schedID := makeTestSchedule(t, s, agent.ID, "sched-sr-multi")

	first, err := s.CreateScheduleRun(ctx, ScheduleRun{ScheduleID: schedID, RunID: "run-1", FiredAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("CreateScheduleRun first: %v", err)
	}
	if err := s.RecordScheduleRunAttempt(ctx, first.ID, ScheduleRunStatusSucceeded, "", nil); err != nil {
		t.Fatalf("RecordScheduleRunAttempt first: %v", err)
	}

	second, err := s.CreateScheduleRun(ctx, ScheduleRun{ScheduleID: schedID, RunID: "run-2", FiredAt: "2026-01-02T00:00:00Z"})
	if err != nil {
		t.Fatalf("CreateScheduleRun second: %v", err)
	}

	latest, err := s.GetLatestScheduleRun(ctx, schedID)
	if err != nil {
		t.Fatalf("GetLatestScheduleRun: %v", err)
	}
	if latest.ID != second.ID {
		t.Fatalf("GetLatestScheduleRun: got %+v, want second row %+v", latest, second)
	}

	open, err := s.GetOpenScheduleRun(ctx, schedID)
	if err != nil {
		t.Fatalf("GetOpenScheduleRun: %v", err)
	}
	if open.ID != second.ID {
		t.Fatalf("GetOpenScheduleRun: got %+v, want second (only open) row %+v", open, second)
	}
}

func TestScheduleRun_CreateValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateScheduleRun(ctx, ScheduleRun{RunID: "x"}); err == nil {
		t.Fatalf("expected error for missing schedule_id")
	}
	if _, err := s.CreateScheduleRun(ctx, ScheduleRun{ScheduleID: "x"}); err == nil {
		t.Fatalf("expected error for missing run_id")
	}
}
