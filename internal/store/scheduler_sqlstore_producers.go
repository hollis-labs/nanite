package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	gosched "github.com/hollis-labs/go-scheduler"
)

func parseSchedulerHostTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, raw)
}

// InsertAgentSchedule writes host metadata and authoritative lifecycle in one
// transaction. It never REPLACEs a foreign-key parent. Wake payload resolution
// remains a due-time operation; the stored payload is the host's original JSON.
func (s *SchedulerSQLStore) InsertAgentSchedule(ctx context.Context, row AgentSchedule) error {
	_, err := s.insertAgentSchedule(ctx, row, false)
	return err
}

func (s *SchedulerSQLStore) InsertAgentScheduleIfNameMissing(ctx context.Context, row AgentSchedule) (bool, error) {
	return s.insertAgentSchedule(ctx, row, true)
}

func (s *SchedulerSQLStore) insertAgentSchedule(ctx context.Context, row AgentSchedule, ifMissing bool) (bool, error) {
	row, err := prepareAgentSchedule(row)
	if err != nil {
		return false, err
	}
	next, err := parseSchedulerHostTime(row.NextRun)
	if err != nil {
		return false, fmt.Errorf("schedule %s next_run: %w", row.ID, err)
	}
	last, err := parseSchedulerHostTime(row.LastFiredAt)
	if err != nil {
		return false, fmt.Errorf("schedule %s last_fired_at: %w", row.ID, err)
	}
	sch := gosched.Schedule{ID: row.ID, LastRun: last, NextRun: next, Enabled: row.Status == ScheduleStatusActive, JobType: row.JobType, Payload: []byte(row.JobPayload),
		Retry: gosched.RetryPolicy{MaxAttempts: int(row.MaxRetries), Backoff: gosched.BackoffPolicy{Strategy: gosched.BackoffExponential, InitialDelay: 30 * time.Second, MaxDelay: 5 * time.Minute}}}
	if row.ScheduleKind == ScheduleKindCron {
		sch.CronExpr = row.ScheduleSpec
	}
	tx, err := s.shared.DB().BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Acquire the writer before conditional reads. A no-op write neither creates
	// rows nor advances any schedule; it prevents a deferred read/write upgrade.
	if _, writeErr := tx.ExecContext(ctx, `UPDATE scheduler_storage_authority SET authority=authority WHERE singleton=1`); writeErr != nil {
		return false, writeErr
	}
	if ifMissing {
		var exists bool
		if writeErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_schedules WHERE (agent_id=? AND name=?) OR id=?)`, row.AgentID, row.Name, row.ID).Scan(&exists); writeErr != nil {
			return false, writeErr
		}
		if exists {
			return false, tx.Commit()
		}
	}
	if writeErr := writeSharedSchedule(ctx, tx, sch); writeErr != nil {
		return false, writeErr
	}
	if writeErr := writeScheduleIdentity(ctx, tx, row.ID, "agent", row.ID); writeErr != nil {
		return false, writeErr
	}
	// Metadata follows enabled updates so a pause is not projected as expiry.
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_schedules
 (id,agent_id,session_id,name,schedule_kind,schedule_spec,body,priority,status,expires_at,
 fired_count,last_fired_at,created_at,created_by,max_retries,on_fail,next_run,job_type,job_payload)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,COALESCE(NULLIF(?,''),datetime('now')),?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET agent_id=excluded.agent_id,session_id=excluded.session_id,name=excluded.name,
 schedule_kind=excluded.schedule_kind,schedule_spec=excluded.schedule_spec,body=excluded.body,priority=excluded.priority,
 status=excluded.status,expires_at=excluded.expires_at,created_by=excluded.created_by,
 max_retries=excluded.max_retries,on_fail=excluded.on_fail,job_type=excluded.job_type,job_payload=excluded.job_payload`, agentScheduleInsertArgs(row)...)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// GetAgentSchedule joins times from the authoritative lifecycle, leaving old
// lifecycle columns inert. Status/counters remain host policy projections.
func (s *SchedulerSQLStore) GetAgentSchedule(ctx context.Context, id string) (*AgentSchedule, error) {
	var row AgentSchedule
	// A single statement observes metadata and lifecycle from one SQLite snapshot.
	err := scanAgentSchedule(s.shared.DB().QueryRowContext(ctx, `SELECT a.id,a.agent_id,COALESCE(a.session_id,''),a.name,a.schedule_kind,
 a.schedule_spec,a.body,a.priority,a.status,COALESCE(a.expires_at,''),a.fired_count,g.last_run,
 a.created_at,a.created_by,a.max_retries,a.on_fail,g.next_run,a.job_type,a.job_payload
 FROM agent_schedules a JOIN scheduler_schedule_identity i ON i.family='agent' AND i.source_id=a.id
 JOIN gosched_schedules g ON g.id=i.schedule_id WHERE a.id=?`, id), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentScheduleNotFound
	}
	if err != nil {
		return nil, err
	}
	next, err := time.Parse(SchedulerTimeLayout, row.NextRun)
	if err != nil {
		return nil, err
	}
	last, err := time.Parse(SchedulerTimeLayout, row.LastFiredAt)
	if err != nil {
		return nil, err
	}
	row.NextRun = hostSchedulerTime(next)
	row.LastFiredAt = hostSchedulerTime(last)
	return &row, nil
}

func hostSchedulerTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *SchedulerSQLStore) UpdateAgentScheduleStatus(ctx context.Context, id, status string) error {
	switch status {
	case ScheduleStatusActive, ScheduleStatusPaused, ScheduleStatusExpired:
	default:
		return fmt.Errorf("invalid schedule status %q", status)
	}
	tx, err := s.shared.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE gosched_schedules SET enabled=? WHERE id IN
 (SELECT schedule_id FROM scheduler_schedule_identity WHERE family='agent' AND source_id=?)`, status == ScheduleStatusActive, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAgentScheduleNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_schedules SET status=? WHERE id=?`, status, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAgentSchedule preserves Nanite's restriction against deleting history,
// including new shared fires. Receipts and fire identities are never purged.
func (s *SchedulerSQLStore) DeleteAgentSchedule(ctx context.Context, id string) error {
	tx, err := s.shared.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `DELETE FROM scheduler_schedule_identity WHERE family='agent' AND source_id=?
 AND NOT EXISTS(SELECT 1 FROM gosched_fires WHERE schedule_id=scheduler_schedule_identity.schedule_id)`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var exists bool
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_schedules WHERE id=?)`, id).Scan(&exists); queryErr != nil {
			return queryErr
		}
		if !exists {
			return ErrAgentScheduleNotFound
		}
		return fmt.Errorf("delete schedule %s: missing identity or retained fire history", id)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gosched_schedules WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_schedules WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SchedulerSQLStore) ScheduleWorkflowActivation(ctx context.Context, row WorkflowActivationSchedule) error {
	if row.ActivationID == "" || row.ScheduleID != WorkflowActivationScheduleID(row.ActivationID) {
		return fmt.Errorf("invalid activation identity")
	}
	if row.FireAt == "" || row.NextRun != row.FireAt || !json.Valid([]byte(row.ActivationJSON)) {
		return fmt.Errorf("invalid one-shot activation material")
	}
	fireAt, err := parseSchedulerHostTime(row.FireAt)
	if err != nil {
		return err
	}
	if row.CreatedAt == "" {
		row.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	row.UpdatedAt = row.CreatedAt
	row.Status = "active"
	tx, err := s.shared.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_activation_schedules(schedule_id,activation_id,activation_json,fire_at,next_run,status,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(activation_id) DO NOTHING`, row.ScheduleID, row.ActivationID, row.ActivationJSON, row.FireAt, row.NextRun, row.Status, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return err
	}
	var persisted WorkflowActivationSchedule
	if err := scanWorkflowActivationSchedule(tx.QueryRowContext(ctx, `SELECT `+workflowActivationScheduleColumns+` FROM workflow_activation_schedules WHERE activation_id=?`, row.ActivationID), &persisted); err != nil {
		return err
	}
	if persisted.ScheduleID != row.ScheduleID || persisted.ActivationJSON != row.ActivationJSON || persisted.FireAt != row.FireAt {
		return fmt.Errorf("activation %s conflicts with immutable material", row.ActivationID)
	}
	// Existing activation replays must never re-enable/retime materialized work.
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gosched_schedules WHERE id=?)`, row.ScheduleID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		var family, source string
		err := tx.QueryRowContext(ctx, `SELECT family,source_id FROM scheduler_schedule_identity WHERE schedule_id=?`, row.ScheduleID).Scan(&family, &source)
		if err != nil {
			return err
		}
		if family != "workflow" || source != row.ScheduleID {
			return fmt.Errorf("activation shared identity conflicts with %s/%s", family, source)
		}
	} else {
		if persisted.Status != "active" {
			return fmt.Errorf("activation %s lacks authoritative lifecycle", row.ActivationID)
		}
		sch := gosched.Schedule{ID: row.ScheduleID, NextRun: fireAt, Enabled: true, JobType: "workflow_activation", Payload: []byte(row.ActivationJSON), Retry: gosched.RetryPolicy{MaxAttempts: 3, Backoff: gosched.BackoffPolicy{Strategy: gosched.BackoffExponential, InitialDelay: time.Second, MaxDelay: 30 * time.Second}}}
		if err := writeSharedSchedule(ctx, tx, sch); err != nil {
			return err
		}
		if err := writeScheduleIdentity(ctx, tx, row.ScheduleID, "workflow", row.ScheduleID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SchedulerSQLStore) CancelWorkflowActivation(ctx context.Context, activationID string, at time.Time) error {
	if strings.TrimSpace(activationID) == "" {
		return fmt.Errorf("activation id required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.shared.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Mark cancellation first; disable projection must preserve this status.
	_, err = tx.ExecContext(ctx, `UPDATE workflow_activation_schedules SET status='canceled',updated_at=? WHERE activation_id=? AND status!='canceled'`, schedulerTime(at), activationID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE gosched_schedules SET enabled=0,next_run=? WHERE id IN
 (SELECT schedule_id FROM scheduler_schedule_identity WHERE family='workflow' AND source_id=?)`, schedulerTime(time.Time{}), WorkflowActivationScheduleID(activationID))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE gosched_fires SET status='skipped',claim_expires_at=?,next_attempt_at=?,
 last_error=CASE WHEN last_error='' THEN 'workflow activation canceled' ELSE last_error END
 WHERE schedule_id=? AND status IN ('pending','retrying')`, schedulerTime(time.Time{}), schedulerTime(time.Time{}), WorkflowActivationScheduleID(activationID))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// BumpAgentScheduleFireCount preserves the manual RunDue behavior. It does not
// create a durable fire or receipt and is not an automatic engine claim.
func (s *SchedulerSQLStore) BumpAgentScheduleFireCount(ctx context.Context, id string, at time.Time) error {
	tx, err := s.shared.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE gosched_schedules SET last_run=? WHERE id IN
 (SELECT schedule_id FROM scheduler_schedule_identity WHERE family='agent' AND source_id=?)`, schedulerTime(at), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAgentScheduleNotFound
	}
	result, err = tx.ExecContext(ctx, `UPDATE agent_schedules SET fired_count=fired_count+1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAgentScheduleNotFound
	}
	return tx.Commit()
}

// ScheduleFamily uses the identity map, never a prefix heuristic. Neutral
// library fixtures have no host identity; production producers always do.
func (s *SchedulerSQLStore) ScheduleFamily(ctx context.Context, id string) (string, error) {
	var family string
	err := s.shared.DB().QueryRowContext(ctx, `SELECT family FROM scheduler_schedule_identity WHERE schedule_id=?`, id).Scan(&family)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return family, err
}
