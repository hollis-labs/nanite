package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func workflowActivationScheduleFixture(id string, at time.Time) WorkflowActivationSchedule {
	return WorkflowActivationSchedule{
		ScheduleID:     WorkflowActivationScheduleID(id),
		ActivationID:   id,
		ActivationJSON: `{"id":"` + id + `","kind":"wait_wake"}`,
		FireAt:         at.UTC().Format(time.RFC3339Nano),
		NextRun:        at.UTC().Format(time.RFC3339Nano),
		CreatedAt:      at.Add(-time.Minute).UTC().Format(time.RFC3339Nano),
	}
}

func workflowActivationFireFixture(schedule WorkflowActivationSchedule, fireID string, at time.Time) ScheduleFireCreation {
	return ScheduleFireCreation{
		ScheduleID:   schedule.ScheduleID,
		ExpectedNext: at,
		Fire: ScheduleFire{
			ID: fireID, RunID: fireID, ScheduleID: schedule.ScheduleID,
			ScheduledAt: at.UTC().Format(time.RFC3339Nano),
			Status:      ScheduleFireStatusPending, NextAttemptAt: at.UTC().Format(time.RFC3339Nano),
			RetryMaxAttempts: 3, RetryBackoffStrategy: "exponential",
			RetryInitialDelayNanos: int64(time.Second), RetryMaximumDelayNanos: int64(30 * time.Second),
			JobType: "workflow_activation", JobPayload: schedule.ActivationJSON,
		},
	}
}

func TestWorkflowActivationScheduleIsImmutableIdempotentAndOneShot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.September, 4, 14, 0, 0, 123, time.UTC)
	row := workflowActivationScheduleFixture("activation-one", at)

	if err := s.ScheduleWorkflowActivation(ctx, row); err != nil {
		t.Fatalf("ScheduleWorkflowActivation: %v", err)
	}
	if err := s.ScheduleWorkflowActivation(ctx, row); err != nil {
		t.Fatalf("exact schedule replay: %v", err)
	}
	conflict := row
	conflict.ActivationJSON = `{"id":"activation-one","kind":"node_retry"}`
	if err := s.ScheduleWorkflowActivation(ctx, conflict); err == nil {
		t.Fatal("conflicting activation replay unexpectedly succeeded")
	}
	due, err := s.ListDueWorkflowActivationSchedules(ctx, at, 10)
	if err != nil || len(due) != 1 || due[0].ScheduleID != row.ScheduleID {
		t.Fatalf("due activations = %+v, %v", due, err)
	}

	creation := workflowActivationFireFixture(row, "workflow-fire-one", at)
	created, err := s.CreateWorkflowActivationFire(ctx, creation)
	if err != nil || !created {
		t.Fatalf("CreateWorkflowActivationFire = (%v, %v)", created, err)
	}
	created, err = s.CreateWorkflowActivationFire(ctx, creation)
	if err != nil || created {
		t.Fatalf("duplicate CreateWorkflowActivationFire = (%v, %v)", created, err)
	}
	if disableErr := s.DisableWorkflowActivationSchedule(ctx, row.ScheduleID, at); disableErr != nil {
		t.Fatalf("DisableWorkflowActivationSchedule: %v", disableErr)
	}
	persisted, err := s.GetWorkflowActivationSchedule(ctx, row.ScheduleID)
	if err != nil || persisted.Status != "materialized" || persisted.NextRun != "" {
		t.Fatalf("materialized schedule = %+v, %v", persisted, err)
	}
	due, err = s.ListDueWorkflowActivationSchedules(ctx, at.Add(time.Hour), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("one-shot schedule rematerialized = %+v, %v", due, err)
	}
}

func TestWorkflowActivationFireRecoveryReceiptsAndFencing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.September, 4, 15, 0, 0, 0, time.UTC)
	row := workflowActivationScheduleFixture("activation-recover", at)
	if err := s.ScheduleWorkflowActivation(ctx, row); err != nil {
		t.Fatal(err)
	}
	if created, err := s.CreateWorkflowActivationFire(ctx, workflowActivationFireFixture(row, "workflow-fire-recover", at)); err != nil || !created {
		t.Fatalf("CreateWorkflowActivationFire = (%v, %v)", created, err)
	}

	firstAt := at.Add(time.Second)
	first, won, err := s.ClaimWorkflowActivationFire(ctx, ScheduleFireClaim{
		FireID: "workflow-fire-recover", ExpectedStatus: ScheduleFireStatusPending,
		ExpectedAttempt: 0, ClaimedAt: firstAt, ClaimExpiresAt: firstAt.Add(time.Minute),
	})
	if err != nil || !won || first.AttemptCount != 1 {
		t.Fatalf("first claim = (%+v, %v, %v)", first, won, err)
	}
	accepted, err := s.MarkWorkflowActivationDispatchAccepted(ctx, first.RunID, int(first.AttemptCount), firstAt, firstAt.Add(time.Second))
	if err != nil || !accepted {
		t.Fatalf("MarkWorkflowActivationDispatchAccepted = (%v, %v)", accepted, err)
	}
	if got, checkErr := s.IsWorkflowActivationDispatchAccepted(ctx, first.RunID); checkErr != nil || !got {
		t.Fatalf("IsWorkflowActivationDispatchAccepted = (%v, %v)", got, checkErr)
	}
	if _, early, earlyErr := s.ClaimWorkflowActivationFire(ctx, ScheduleFireClaim{
		FireID: first.RunID, ExpectedStatus: ScheduleFireStatusClaimed,
		ExpectedAttempt: first.AttemptCount, ExpectedFiredAt: firstAt,
		ClaimedAt: firstAt.Add(30 * time.Second), ClaimExpiresAt: firstAt.Add(2 * time.Minute),
	}); earlyErr != nil || early {
		t.Fatalf("unexpired recovery = (%v, %v), want false, nil", early, earlyErr)
	}

	recoveredAt := firstAt.Add(time.Minute)
	recovered, won, err := s.ClaimWorkflowActivationFire(ctx, ScheduleFireClaim{
		FireID: first.RunID, ExpectedStatus: ScheduleFireStatusClaimed,
		ExpectedAttempt: first.AttemptCount, ExpectedFiredAt: firstAt,
		ClaimedAt: recoveredAt, ClaimExpiresAt: recoveredAt.Add(time.Minute),
	})
	if err != nil || !won || recovered.AttemptCount != first.AttemptCount {
		t.Fatalf("expired recovery = (%+v, %v, %v)", recovered, won, err)
	}
	if marked, markErr := s.MarkWorkflowActivationDispatchAccepted(ctx, recovered.RunID, int(recovered.AttemptCount), firstAt, recoveredAt); markErr != nil || marked {
		t.Fatalf("stale receipt fence = (%v, %v), want false, nil", marked, markErr)
	}
	if transitioned, transitionErr := s.TransitionWorkflowActivationFire(ctx, ScheduleFireTransition{
		FireID: recovered.RunID, Attempt: recovered.AttemptCount, From: ScheduleFireStatusClaimed,
		ClaimedAt: firstAt, To: ScheduleFireStatusSucceeded,
	}); transitionErr != nil || transitioned {
		t.Fatalf("stale transition fence = (%v, %v), want false, nil", transitioned, transitionErr)
	}
	if transitioned, transitionErr := s.TransitionWorkflowActivationFire(ctx, ScheduleFireTransition{
		FireID: recovered.RunID, Attempt: recovered.AttemptCount, From: ScheduleFireStatusClaimed,
		ClaimedAt: recoveredAt, To: ScheduleFireStatusSucceeded,
	}); transitionErr != nil || !transitioned {
		t.Fatalf("winning transition = (%v, %v), want true, nil", transitioned, transitionErr)
	}
}

func TestCancelWorkflowActivationClosesUnclaimedAndPreservesClaimFence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.September, 4, 16, 0, 0, 0, time.UTC)

	pendingSchedule := workflowActivationScheduleFixture("activation-pending", at)
	if err := s.ScheduleWorkflowActivation(ctx, pendingSchedule); err != nil {
		t.Fatal(err)
	}
	if created, err := s.CreateWorkflowActivationFire(ctx, workflowActivationFireFixture(pendingSchedule, "workflow-fire-pending", at)); err != nil || !created {
		t.Fatalf("create pending fire = (%v, %v)", created, err)
	}
	if err := s.CancelWorkflowActivation(ctx, pendingSchedule.ActivationID, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelWorkflowActivation(ctx, pendingSchedule.ActivationID, at.Add(2*time.Second)); err != nil {
		t.Fatalf("idempotent cancel: %v", err)
	}
	pending, err := s.GetWorkflowActivationFire(ctx, "workflow-fire-pending")
	if err != nil || pending.Status != ScheduleFireStatusSkipped {
		t.Fatalf("canceled pending fire = %+v, %v", pending, err)
	}

	claimedSchedule := workflowActivationScheduleFixture("activation-claimed", at)
	if scheduleErr := s.ScheduleWorkflowActivation(ctx, claimedSchedule); scheduleErr != nil {
		t.Fatal(scheduleErr)
	}
	if created, createErr := s.CreateWorkflowActivationFire(ctx, workflowActivationFireFixture(claimedSchedule, "workflow-fire-claimed", at)); createErr != nil || !created {
		t.Fatalf("create claimed fire = (%v, %v)", created, createErr)
	}
	claimedAt := at.Add(time.Second)
	claimed, won, err := s.ClaimWorkflowActivationFire(ctx, ScheduleFireClaim{
		FireID: "workflow-fire-claimed", ExpectedStatus: ScheduleFireStatusPending,
		ExpectedAttempt: 0, ClaimedAt: claimedAt, ClaimExpiresAt: claimedAt.Add(time.Minute),
	})
	if err != nil || !won {
		t.Fatalf("claim = (%+v, %v, %v)", claimed, won, err)
	}
	if cancelErr := s.CancelWorkflowActivation(ctx, claimedSchedule.ActivationID, claimedAt.Add(time.Second)); cancelErr != nil {
		t.Fatal(cancelErr)
	}
	persisted, err := s.GetWorkflowActivationFire(ctx, claimed.RunID)
	if err != nil || persisted.Status != ScheduleFireStatusClaimed {
		t.Fatalf("claimed fire lost fence after cancel = %+v, %v", persisted, err)
	}
	if due, dueErr := s.ListDueWorkflowActivationFires(ctx, claimedAt.Add(30*time.Second), 10); dueErr != nil || len(due) != 0 {
		t.Fatalf("unexpired canceled claim due = %+v, %v", due, dueErr)
	}
	if due, dueErr := s.ListDueWorkflowActivationFires(ctx, claimedAt.Add(time.Minute), 10); dueErr != nil || len(due) != 1 || due[0].RunID != claimed.RunID {
		t.Fatalf("expired canceled claim recovery = %+v, %v", due, dueErr)
	}
	if _, err := s.GetWorkflowActivationSchedule(ctx, "missing"); !errors.Is(err, ErrWorkflowActivationScheduleNotFound) {
		t.Fatalf("missing activation schedule error = %v", err)
	}
}
