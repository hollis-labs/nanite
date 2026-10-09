package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	gosched "github.com/hollis-labs/libs/util/scheduler"
	"github.com/hollis-labs/libs/util/scheduler/sqlstore"
)

// SchedulerTimeLayout is the released SQLStore's nanosecond UTC encoding.
// Claim fences must use this exact representation, including zero times.
const SchedulerTimeLayout = "2006-01-02T15:04:05.000000000Z"

func schedulerTime(t time.Time) string { return t.UTC().Format(SchedulerTimeLayout) }

// SchedulerSQLStore adds host producers and fenced dispatch receipts to the
// released lifecycle store on the same database. Construction applies no DDL
// and changes no authority. The reference store is private so callers cannot
// bypass host producer identities or deletion restrictions.
type SchedulerSQLStore struct {
	shared *sqlstore.Store
}

func NewSchedulerSQLStore(host *Store) (*SchedulerSQLStore, error) {
	if host == nil {
		return nil, fmt.Errorf("scheduler sqlstore: nil host")
	}
	shared, err := sqlstore.New(host.DB)
	if err != nil {
		return nil, err
	}
	return &SchedulerSQLStore{shared: shared}, nil
}

func (s *SchedulerSQLStore) IsScheduleFireDispatchAccepted(ctx context.Context, id string) (bool, error) {
	var accepted bool
	err := s.shared.DB().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduler_dispatch_receipts WHERE fire_id = ?)`, id).Scan(&accepted)
	return accepted, err
}

// MarkScheduleFireDispatchAccepted serializes acceptance with claim recovery
// using one write statement. Repeated acceptance preserves the first instant;
// stale owners cannot create OR update a receipt. Cancellation policy belongs
// to the caller (RunnerAdapter uses WithoutCancel after target acceptance).
func (s *SchedulerSQLStore) MarkScheduleFireDispatchAccepted(ctx context.Context, id string, attempt int, claimedAt, acceptedAt time.Time) (bool, error) {
	result, err := s.shared.DB().ExecContext(ctx, `INSERT INTO scheduler_dispatch_receipts(fire_id, accepted_at)
 SELECT id, ? FROM gosched_fires WHERE id = ? AND status = 'claimed' AND attempt = ? AND fired_at = ?
 ON CONFLICT(fire_id) DO UPDATE SET accepted_at = scheduler_dispatch_receipts.accepted_at`, schedulerTime(acceptedAt), id, attempt, schedulerTime(claimedAt))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// InstallSchedulerProjections must be called inside the cutover transaction,
// after every historical row and identity has been copied and validated. These
// triggers participate in SQLStore's own fire transaction: duplicate/stale
// creates roll back their counters and identity alongside the lifecycle CAS.
func InstallSchedulerProjections(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TRIGGER scheduler_fire_projection AFTER INSERT ON gosched_fires
BEGIN
 INSERT INTO scheduler_fire_identity(fire_id, schedule_id, family, legacy_row_id)
 SELECT NEW.id, NEW.schedule_id, family, NEW.id
 FROM scheduler_schedule_identity WHERE schedule_id = NEW.schedule_id;
 UPDATE agent_schedules SET fired_count = fired_count + 1
 WHERE id IN (SELECT source_id FROM scheduler_schedule_identity WHERE schedule_id = NEW.schedule_id AND family = 'agent');
 UPDATE workflow_activation_schedules SET updated_at = NEW.scheduled_at
 WHERE schedule_id IN (SELECT source_id FROM scheduler_schedule_identity WHERE schedule_id = NEW.schedule_id AND family = 'workflow');
END;
CREATE TRIGGER scheduler_disable_projection AFTER UPDATE OF enabled ON gosched_schedules
WHEN NEW.enabled = 0 AND OLD.enabled = 1
BEGIN
 UPDATE agent_schedules SET status = 'expired'
 WHERE id IN (SELECT source_id FROM scheduler_schedule_identity WHERE schedule_id = NEW.id AND family = 'agent');
 UPDATE workflow_activation_schedules
 SET status = 'materialized', updated_at = NEW.last_run
 WHERE status = 'active' AND schedule_id IN (SELECT source_id FROM scheduler_schedule_identity WHERE schedule_id = NEW.id AND family = 'workflow');
END;`)
	return err
}

// ListDueFires keeps Nanite's next-attempt/scheduled-time ordering, including
// recovered claims. Filtering and LIMIT happen in one authoritative query.
func (s *SchedulerSQLStore) ListDueFires(ctx context.Context, now time.Time, limit int) ([]gosched.Fire, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.shared.DB().QueryContext(ctx, `SELECT id, schedule_id, scheduled_at, fired_at, claim_expires_at,
 attempt, status, next_attempt_at, last_error, retry_json, job_type, payload FROM gosched_fires
 WHERE (status IN ('pending','retrying') AND next_attempt_at <= ?) OR (status = 'claimed' AND claim_expires_at <= ?)
 ORDER BY CASE WHEN next_attempt_at = ? THEN scheduled_at ELSE next_attempt_at END, id LIMIT ?`,
		schedulerTime(now), schedulerTime(now), schedulerTime(time.Time{}), limit)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []gosched.Fire
	for rows.Next() {
		var f gosched.Fire
		var scheduled, fired, expires, next, retry string
		if err := rows.Scan(&f.ID, &f.ScheduleID, &scheduled, &fired, &expires, &f.Attempt, &f.Status, &next, &f.LastError, &retry, &f.JobType, &f.Payload); err != nil {
			return nil, err
		}
		for _, field := range []struct {
			raw  string
			dest *time.Time
		}{{scheduled, &f.ScheduledAt}, {fired, &f.FiredAt}, {expires, &f.ClaimExpiresAt}, {next, &f.NextAttemptAt}} {
			parsed, err := time.Parse(SchedulerTimeLayout, field.raw)
			if err != nil {
				return nil, fmt.Errorf("scheduler fire %s: %w", f.ID, err)
			}
			*field.dest = parsed
		}
		if err := json.Unmarshal([]byte(retry), &f.Retry); err != nil {
			return nil, fmt.Errorf("scheduler fire %s retry: %w", f.ID, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func writeSharedSchedule(ctx context.Context, tx *sql.Tx, sch gosched.Schedule) error {
	retry, err := json.Marshal(sch.Retry)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gosched_schedules(id, cron_expr, last_run, next_run, enabled, job_type, payload, retry_json)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
 cron_expr=excluded.cron_expr, last_run=excluded.last_run, next_run=excluded.next_run,
 enabled=excluded.enabled, job_type=excluded.job_type, payload=excluded.payload, retry_json=excluded.retry_json`,
		sch.ID, sch.CronExpr, schedulerTime(sch.LastRun), schedulerTime(sch.NextRun), sch.Enabled, sch.JobType, sch.Payload, string(retry))
	return err
}

func writeScheduleIdentity(ctx context.Context, tx *sql.Tx, id, family, sourceID string) error {
	// A write before reading takes SQLite's writer lock. An ID in another family
	// is an error, never an upsert that retargets historical fire identities.
	_, err := tx.ExecContext(ctx, `INSERT INTO scheduler_schedule_identity(schedule_id,family,source_id)
 VALUES(?,?,?) ON CONFLICT(schedule_id) DO NOTHING`, id, family, sourceID)
	if err != nil {
		return err
	}
	var gotFamily, gotSource string
	if err := tx.QueryRowContext(ctx, `SELECT family, source_id FROM scheduler_schedule_identity WHERE schedule_id=?`, id).Scan(&gotFamily, &gotSource); err != nil {
		return err
	}
	if gotFamily != family || gotSource != sourceID {
		return fmt.Errorf("scheduler identity %q conflicts with %s/%s", id, gotFamily, gotSource)
	}
	return nil
}

// ListDueSchedules reads neutral lifecycle; the scheduler adapter adds dynamic
// host payload resolution. Raw schedule creation/deletion is not exposed.
func (s *SchedulerSQLStore) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]gosched.Schedule, error) {
	return s.shared.ListDueSchedules(ctx, now, limit)
}
func (s *SchedulerSQLStore) GetSchedule(ctx context.Context, id string) (gosched.Schedule, bool, error) {
	return s.shared.GetSchedule(ctx, id)
}
func (s *SchedulerSQLStore) GetFire(ctx context.Context, id string) (gosched.Fire, bool, error) {
	return s.shared.GetFire(ctx, id)
}
func (s *SchedulerSQLStore) ListSchedules(ctx context.Context) ([]gosched.Schedule, error) {
	return s.shared.ListSchedules(ctx)
}
func (s *SchedulerSQLStore) CreateFire(ctx context.Context, c gosched.FireCreation) (bool, error) {
	return s.shared.CreateFire(ctx, c)
}
func (s *SchedulerSQLStore) ClaimFire(ctx context.Context, c gosched.FireClaim) (gosched.Fire, bool, error) {
	return s.shared.ClaimFire(ctx, c)
}
func (s *SchedulerSQLStore) TransitionFire(ctx context.Context, c gosched.FireTransition) (bool, error) {
	return s.shared.TransitionFire(ctx, c)
}

// DisableSchedule is the engine's required surface; host metadata is projected
// inside the same statement by the installed cutover triggers.
func (s *SchedulerSQLStore) DisableSchedule(ctx context.Context, id string) error {
	return s.shared.DisableSchedule(ctx, id)
}
