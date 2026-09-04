package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestMigration149MapsExistingOpenRunsIntoRecoverableDurableFires(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	migrationsDir, subErr := fs.Sub(migrationsFS, "migrations")
	if subErr != nil {
		t.Fatalf("fs.Sub migrations: %v", subErr)
	}
	provider, providerErr := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if providerErr != nil {
		t.Fatalf("goose.NewProvider: %v", providerErr)
	}
	if _, err := provider.DownTo(ctx, 148); err != nil {
		t.Fatalf("DownTo(148): %v", err)
	}

	agent := makeTestAgent(t, s, "migration-149")
	instance := &DurableAgentInstance{Name: "migration instance", Slug: "migration-149-inst", ProfileID: agent.ID}
	if err := s.CreateDurableAgentInstance(ctx, instance); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if err := s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "migration-149-wake", AgentID: agent.ID, Name: "wake", Body: "hello",
		ScheduleKind: ScheduleKindCron, ScheduleSpec: "* * * * *",
		MaxRetries: 4, OnFail: ScheduleOnFailNotify,
		JobType: ScheduleJobTypeDurableAgentWake, JobPayload: `{}`,
		NextRun: "2026-09-04T10:00:00Z",
	}); err != nil {
		t.Fatalf("InsertAgentSchedule wake: %v", err)
	}
	if err := s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "migration-149-command", AgentID: agent.ID, Name: "command", Body: "run",
		ScheduleKind: ScheduleKindCron, ScheduleSpec: "* * * * *",
		MaxRetries: 2, OnFail: ScheduleOnFailDisable,
		JobType: ScheduleJobTypeCommandRun, JobPayload: `{"command":"lint"}`,
		NextRun: "2026-09-04T11:00:00Z",
	}); err != nil {
		t.Fatalf("InsertAgentSchedule command: %v", err)
	}
	if err := s.InsertAgentSchedule(ctx, AgentSchedule{
		ID: "migration-149-once", AgentID: agent.ID, Name: "once", Body: "run once",
		ScheduleKind: ScheduleKindOneShot,
		MaxRetries:   2, OnFail: ScheduleOnFailNotify,
		JobType: ScheduleJobTypeCommandRun, JobPayload: `{"command":"once"}`,
		NextRun: "2026-09-04T09:00:00Z",
	}); err != nil {
		t.Fatalf("InsertAgentSchedule one-shot: %v", err)
	}
	_, insertErr := s.DB.ExecContext(ctx, `
		INSERT INTO schedule_runs
		    (id, schedule_id, run_id, fired_at, status, attempt_count, last_error, next_attempt_at)
		VALUES
		    ('legacy-pending', 'migration-149-wake', 'legacy-fire-pending', '2026-09-04T10:00:00Z', 'pending', 0, NULL, NULL),
		    ('legacy-failed', 'migration-149-command', 'legacy-fire-failed', '2026-09-04T11:00:00Z', 'failed', 1, 'boom', '2026-09-04T11:00:30Z'),
		    ('legacy-once', 'migration-149-once', 'legacy-fire-once', '2026-09-04T09:00:00Z', 'pending', 0, NULL, NULL)`)
	if insertErr != nil {
		t.Fatalf("insert legacy schedule runs: %v", insertErr)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up migration 149: %v", err)
	}

	pending, err := s.GetScheduleFire(ctx, "legacy-fire-pending")
	if err != nil {
		t.Fatalf("get migrated pending: %v", err)
	}
	if pending.ID != "legacy-pending" || pending.Status != ScheduleFireStatusPending ||
		pending.AttemptCount != 0 || pending.ClaimExpiresAt != "" || pending.ScheduledAt != pending.FiredAt ||
		pending.RetryMaxAttempts != 4 || pending.JobType != ScheduleJobTypeDurableAgentWake {
		t.Fatalf("migrated pending = %+v", pending)
	}
	var wakePayload struct {
		InstanceID string `json:"instance_id"`
		Prompt     string `json:"prompt"`
	}
	if decodeErr := json.Unmarshal([]byte(pending.JobPayload), &wakePayload); decodeErr != nil {
		t.Fatalf("decode migrated wake payload: %v", decodeErr)
	}
	if wakePayload.InstanceID != instance.ID || wakePayload.Prompt != "hello" {
		t.Fatalf("migrated wake payload = %+v", wakePayload)
	}
	wakeSchedule, err := s.GetAgentSchedule(ctx, "migration-149-wake")
	if err != nil {
		t.Fatalf("get migrated wake schedule: %v", err)
	}
	if wakeSchedule.NextRun != "" || wakeSchedule.FiredCount != 1 {
		t.Fatalf("migrated wake schedule before backfill = %+v", wakeSchedule)
	}

	failed, err := s.GetScheduleFire(ctx, "legacy-fire-failed")
	if err != nil {
		t.Fatalf("get migrated failed: %v", err)
	}
	if failed.Status != ScheduleFireStatusRetrying || failed.AttemptCount != 1 ||
		failed.LastError != "boom" || failed.NextAttemptAt != "2026-09-04T11:00:30Z" ||
		failed.RetryMaxAttempts != 2 || failed.RetryBackoffStrategy != "exponential" ||
		time.Duration(failed.RetryInitialDelayNanos) != 30*time.Second ||
		time.Duration(failed.RetryMaximumDelayNanos) != 5*time.Minute ||
		failed.JobPayload != `{"command":"lint"}` {
		t.Fatalf("migrated failed = %+v", failed)
	}
	commandSchedule, err := s.GetAgentSchedule(ctx, "migration-149-command")
	if err != nil {
		t.Fatalf("get migrated command schedule: %v", err)
	}
	if commandSchedule.NextRun != "" || commandSchedule.FiredCount != 1 {
		t.Fatalf("migrated command schedule before backfill = %+v", commandSchedule)
	}
	oneShot, err := s.GetAgentSchedule(ctx, "migration-149-once")
	if err != nil {
		t.Fatalf("get migrated one-shot schedule: %v", err)
	}
	if oneShot.Status != ScheduleStatusExpired || oneShot.FiredCount != 1 {
		t.Fatalf("migrated one-shot schedule = %+v", oneShot)
	}
	if oneShotFire, fireErr := s.GetScheduleFire(ctx, "legacy-fire-once"); fireErr != nil || oneShotFire.Status != ScheduleFireStatusPending {
		t.Fatalf("migrated one-shot fire = (%+v, %v)", oneShotFire, fireErr)
	}
	backfillAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	if backfillErr := s.backfillScheduleNextRun(ctx, backfillAt); backfillErr != nil {
		t.Fatalf("post-migration backfill: %v", backfillErr)
	}
	for _, id := range []string{"migration-149-wake", "migration-149-command"} {
		schedule, getErr := s.GetAgentSchedule(ctx, id)
		if getErr != nil {
			t.Fatalf("get backfilled schedule %s: %v", id, getErr)
		}
		next, parseErr := time.Parse(time.RFC3339Nano, schedule.NextRun)
		if parseErr != nil || !next.After(backfillAt) {
			t.Fatalf("schedule %s rematerializes legacy occurrence: next=%q err=%v", id, schedule.NextRun, parseErr)
		}
	}
	assertGooseHasNothingPending(t, s)
}
