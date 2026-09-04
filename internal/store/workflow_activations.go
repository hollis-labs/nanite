package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const WorkflowActivationSchedulePrefix = "workflow-activation:"

var (
	ErrWorkflowActivationScheduleNotFound = errors.New("workflow activation schedule not found")
	ErrWorkflowActivationFireNotFound     = errors.New("workflow activation fire not found")
)

// WorkflowActivationSchedule is the durable host projection of one Hadron
// activation into go-scheduler's one-shot Schedule contract. ActivationJSON is
// immutable: re-scheduling the same activation identity with different
// semantics is rejected instead of silently changing a pending activation.
type WorkflowActivationSchedule struct {
	ScheduleID     string
	ActivationID   string
	ActivationJSON string
	FireAt         string
	NextRun        string
	Status         string
	CreatedAt      string
	UpdatedAt      string
}

func WorkflowActivationScheduleID(activationID string) string {
	return WorkflowActivationSchedulePrefix + activationID
}

func IsWorkflowActivationScheduleID(scheduleID string) bool {
	return strings.HasPrefix(scheduleID, WorkflowActivationSchedulePrefix)
}

const workflowActivationScheduleColumns = `schedule_id, activation_id,
       activation_json, fire_at, COALESCE(next_run,''), status, created_at, updated_at`

func scanWorkflowActivationSchedule(scanner interface{ Scan(...any) error }, row *WorkflowActivationSchedule) error {
	return scanner.Scan(
		&row.ScheduleID, &row.ActivationID, &row.ActivationJSON, &row.FireAt,
		&row.NextRun, &row.Status, &row.CreatedAt, &row.UpdatedAt,
	)
}

// ScheduleWorkflowActivation durably creates one immutable one-shot schedule.
// Repeating the exact request is idempotent even after materialization or
// cancellation; reusing the activation identity for different content fails.
func (s *Store) ScheduleWorkflowActivation(ctx context.Context, row WorkflowActivationSchedule) error {
	if row.ScheduleID == "" || row.ActivationID == "" || row.ScheduleID != WorkflowActivationScheduleID(row.ActivationID) {
		return fmt.Errorf("schedule workflow activation: invalid schedule or activation identity")
	}
	if row.FireAt == "" || row.NextRun != row.FireAt {
		return fmt.Errorf("schedule workflow activation: one-shot fire_at and next_run must match")
	}
	if !json.Valid([]byte(row.ActivationJSON)) {
		return fmt.Errorf("schedule workflow activation: activation payload is not valid JSON")
	}
	if row.CreatedAt == "" {
		row.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	row.UpdatedAt = row.CreatedAt
	row.Status = "active"

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("schedule workflow activation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO workflow_activation_schedules(
    schedule_id, activation_id, activation_json, fire_at, next_run,
    status, created_at, updated_at
)
SELECT ?, ?, ?, ?, ?, ?, ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_activation_schedules WHERE activation_id = ?
)`, row.ScheduleID, row.ActivationID, row.ActivationJSON, row.FireAt, row.NextRun,
		row.Status, row.CreatedAt, row.UpdatedAt, row.ActivationID)
	if err != nil {
		return fmt.Errorf("schedule workflow activation: insert: %w", err)
	}
	var persisted WorkflowActivationSchedule
	if err := scanWorkflowActivationSchedule(tx.QueryRowContext(ctx,
		`SELECT `+workflowActivationScheduleColumns+` FROM workflow_activation_schedules WHERE activation_id = ?`,
		row.ActivationID), &persisted); err != nil {
		return fmt.Errorf("schedule workflow activation: read persisted row: %w", err)
	}
	if persisted.ScheduleID != row.ScheduleID || persisted.ActivationJSON != row.ActivationJSON || persisted.FireAt != row.FireAt {
		return fmt.Errorf("schedule workflow activation: activation %q conflicts with immutable material", row.ActivationID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("schedule workflow activation: commit: %w", err)
	}
	return nil
}

func (s *Store) GetWorkflowActivationSchedule(ctx context.Context, scheduleID string) (*WorkflowActivationSchedule, error) {
	var row WorkflowActivationSchedule
	err := scanWorkflowActivationSchedule(s.DB.QueryRowContext(ctx,
		`SELECT `+workflowActivationScheduleColumns+` FROM workflow_activation_schedules WHERE schedule_id = ?`,
		scheduleID), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkflowActivationScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow activation schedule: %w", err)
	}
	return &row, nil
}

func (s *Store) ListDueWorkflowActivationSchedules(ctx context.Context, now time.Time, limit int) ([]WorkflowActivationSchedule, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT `+workflowActivationScheduleColumns+`
FROM workflow_activation_schedules
WHERE status = 'active' AND next_run IS NOT NULL
  AND julianday(next_run) <= julianday(?)
ORDER BY julianday(next_run), schedule_id
LIMIT ?`, formatFireTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list due workflow activation schedules: %w", err)
	}
	defer closeRows(rows)
	result := make([]WorkflowActivationSchedule, 0)
	for rows.Next() {
		var row WorkflowActivationSchedule
		if err := scanWorkflowActivationSchedule(rows, &row); err != nil {
			return nil, fmt.Errorf("list due workflow activation schedules: scan: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due workflow activation schedules: iterate: %w", err)
	}
	return result, nil
}

// CancelWorkflowActivation is idempotent. Pending/retrying fires are closed
// immediately. A claimed fire is left fenced to its current owner: if cancel
// raced the handler, that handler's Hadron CAS decides the semantic winner;
// after a crash the expired claim is still recoverable and observes the now
// terminal workflow state before becoming skipped.
func (s *Store) CancelWorkflowActivation(ctx context.Context, activationID string, at time.Time) error {
	if strings.TrimSpace(activationID) == "" {
		return fmt.Errorf("cancel workflow activation: activation id is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cancel workflow activation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
UPDATE workflow_activation_schedules
SET status = 'canceled', next_run = NULL, updated_at = ?
WHERE activation_id = ? AND status != 'canceled'`, formatFireTime(at), activationID); err != nil {
		return fmt.Errorf("cancel workflow activation: update schedule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE workflow_activation_fires
SET status = ?, claim_expires_at = NULL, next_attempt_at = NULL,
    last_error = COALESCE(last_error, 'workflow activation canceled')
WHERE schedule_id = ? AND status IN (?, ?)`,
		ScheduleFireStatusSkipped, WorkflowActivationScheduleID(activationID),
		ScheduleFireStatusPending, ScheduleFireStatusRetrying); err != nil {
		return fmt.Errorf("cancel workflow activation: close unclaimed fires: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cancel workflow activation: commit: %w", err)
	}
	return nil
}

func (s *Store) DisableWorkflowActivationSchedule(ctx context.Context, scheduleID string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res, err := s.DB.ExecContext(ctx, `
UPDATE workflow_activation_schedules
SET status = CASE WHEN status = 'active' THEN 'materialized' ELSE status END,
    updated_at = ?
WHERE schedule_id = ?`, formatFireTime(at), scheduleID)
	if err != nil {
		return fmt.Errorf("disable workflow activation schedule: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("disable workflow activation schedule rows affected: %w", err)
	}
	if count == 0 {
		return ErrWorkflowActivationScheduleNotFound
	}
	return nil
}

const workflowActivationFireColumns = `fire_id, schedule_id, fire_id,
       scheduled_at, fired_at, COALESCE(claim_expires_at,''),
       COALESCE(dispatch_accepted_at,''), status, attempt_count,
       COALESCE(last_error,''), COALESCE(next_attempt_at,''),
       retry_max_attempts, retry_backoff_strategy, retry_initial_delay_ns,
       retry_max_delay_ns, job_type, job_payload`

func (s *Store) CreateWorkflowActivationFire(ctx context.Context, creation ScheduleFireCreation) (bool, error) {
	if creation.ScheduleID == "" || creation.Fire.RunID == "" {
		return false, fmt.Errorf("create workflow activation fire: schedule and fire identity are required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("create workflow activation fire: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if queryErr := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM workflow_activation_fires WHERE fire_id = ?)`,
		creation.Fire.RunID).Scan(&exists); queryErr != nil {
		return false, fmt.Errorf("create workflow activation fire: check identity: %w", queryErr)
	}
	if exists != 0 {
		return false, nil
	}
	updated, err := tx.ExecContext(ctx, `
UPDATE workflow_activation_schedules
SET next_run = ?, updated_at = ?
WHERE schedule_id = ? AND status = 'active' AND next_run = ?`,
		nullableFireTime(creation.NextRun), creation.Fire.ScheduledAt,
		creation.ScheduleID, formatFireTime(creation.ExpectedNext))
	if err != nil {
		return false, fmt.Errorf("create workflow activation fire: advance schedule: %w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("create workflow activation fire: advance rows affected: %w", err)
	}
	if count == 0 {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO workflow_activation_fires(
    fire_id, schedule_id, scheduled_at, fired_at, claim_expires_at,
    dispatch_accepted_at, status, attempt_count, last_error,
    next_attempt_at, retry_max_attempts, retry_backoff_strategy,
    retry_initial_delay_ns, retry_max_delay_ns, job_type, job_payload
)
SELECT ?, ?, ?, ?, NULL, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`,
		creation.Fire.RunID, creation.ScheduleID, creation.Fire.ScheduledAt,
		creation.Fire.FiredAt, creation.Fire.Status, creation.Fire.AttemptCount,
		nullIfEmpty(creation.Fire.LastError), nullIfEmpty(creation.Fire.NextAttemptAt),
		creation.Fire.RetryMaxAttempts, creation.Fire.RetryBackoffStrategy,
		creation.Fire.RetryInitialDelayNanos, creation.Fire.RetryMaximumDelayNanos,
		creation.Fire.JobType, creation.Fire.JobPayload)
	if err != nil {
		return false, fmt.Errorf("create workflow activation fire: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("create workflow activation fire: commit: %w", err)
	}
	return true, nil
}

func (s *Store) ListDueWorkflowActivationFires(ctx context.Context, now time.Time, limit int) ([]ScheduleFire, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT `+workflowActivationFireColumns+`
FROM workflow_activation_fires
WHERE (status IN (?, ?) AND julianday(COALESCE(next_attempt_at, scheduled_at)) <= julianday(?))
   OR (status = ? AND (claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday(?)))
ORDER BY julianday(COALESCE(next_attempt_at, scheduled_at)), fire_id
LIMIT ?`, ScheduleFireStatusPending, ScheduleFireStatusRetrying, formatFireTime(now),
		ScheduleFireStatusClaimed, formatFireTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list due workflow activation fires: %w", err)
	}
	defer closeRows(rows)
	result := make([]ScheduleFire, 0)
	for rows.Next() {
		var fire ScheduleFire
		if err := scanScheduleFire(rows, &fire); err != nil {
			return nil, fmt.Errorf("list due workflow activation fires: scan: %w", err)
		}
		result = append(result, fire)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due workflow activation fires: iterate: %w", err)
	}
	return result, nil
}

func (s *Store) GetWorkflowActivationFire(ctx context.Context, fireID string) (*ScheduleFire, error) {
	var fire ScheduleFire
	err := scanScheduleFire(s.DB.QueryRowContext(ctx,
		`SELECT `+workflowActivationFireColumns+` FROM workflow_activation_fires WHERE fire_id = ?`,
		fireID), &fire)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkflowActivationFireNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow activation fire: %w", err)
	}
	return &fire, nil
}

func (s *Store) ClaimWorkflowActivationFire(ctx context.Context, claim ScheduleFireClaim) (ScheduleFire, bool, error) {
	newAttempt := claim.ExpectedAttempt + 1
	recoveryClause := ""
	args := []any{
		newAttempt, formatFireTime(claim.ClaimedAt), formatFireTime(claim.ClaimExpiresAt),
		claim.FireID, claim.ExpectedStatus, claim.ExpectedAttempt, formatFireTime(claim.ExpectedFiredAt),
	}
	if claim.ExpectedStatus == ScheduleFireStatusClaimed {
		newAttempt = claim.ExpectedAttempt
		args[0] = newAttempt
		recoveryClause = ` AND (claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday(?))`
		args = append(args, formatFireTime(claim.ClaimedAt))
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim workflow activation fire: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.ExecContext(ctx, `
UPDATE workflow_activation_fires
SET status = ?, attempt_count = ?, fired_at = ?, claim_expires_at = ?, next_attempt_at = NULL
WHERE fire_id = ? AND status = ? AND attempt_count = ? AND fired_at = ?`+recoveryClause,
		append([]any{ScheduleFireStatusClaimed}, args...)...)
	if err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim workflow activation fire: update: %w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim workflow activation fire rows affected: %w", err)
	}
	if count == 0 {
		return ScheduleFire{}, false, nil
	}
	var fire ScheduleFire
	if err := scanScheduleFire(tx.QueryRowContext(ctx,
		`SELECT `+workflowActivationFireColumns+` FROM workflow_activation_fires WHERE fire_id = ?`,
		claim.FireID), &fire); err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim workflow activation fire: read result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim workflow activation fire: commit: %w", err)
	}
	return fire, true, nil
}

func (s *Store) TransitionWorkflowActivationFire(ctx context.Context, transition ScheduleFireTransition) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `
UPDATE workflow_activation_fires
SET status = ?, claim_expires_at = NULL, next_attempt_at = ?, last_error = ?
WHERE fire_id = ? AND status = ? AND attempt_count = ? AND fired_at = ?`,
		transition.To, nullableFireTime(transition.NextAttemptAt), nullIfEmpty(transition.LastError),
		transition.FireID, transition.From, transition.Attempt, formatFireTime(transition.ClaimedAt))
	if err != nil {
		return false, fmt.Errorf("transition workflow activation fire: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("transition workflow activation fire rows affected: %w", err)
	}
	return count == 1, nil
}

func (s *Store) IsWorkflowActivationDispatchAccepted(ctx context.Context, fireID string) (bool, error) {
	var accepted int
	err := s.DB.QueryRowContext(ctx, `
SELECT dispatch_accepted_at IS NOT NULL
FROM workflow_activation_fires
WHERE fire_id = ?`, fireID).Scan(&accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrWorkflowActivationFireNotFound
	}
	if err != nil {
		return false, fmt.Errorf("check workflow activation dispatch receipt: %w", err)
	}
	return accepted != 0, nil
}

func (s *Store) MarkWorkflowActivationDispatchAccepted(
	ctx context.Context,
	fireID string,
	attempt int,
	claimedAt, acceptedAt time.Time,
) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `
UPDATE workflow_activation_fires
SET dispatch_accepted_at = COALESCE(dispatch_accepted_at, ?)
WHERE fire_id = ? AND status = ? AND attempt_count = ? AND fired_at = ?`,
		formatFireTime(acceptedAt), fireID, ScheduleFireStatusClaimed, attempt, formatFireTime(claimedAt))
	if err != nil {
		return false, fmt.Errorf("mark workflow activation dispatch accepted: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark workflow activation dispatch accepted rows affected: %w", err)
	}
	return count == 1, nil
}

type WorkflowWaitMaterialization struct {
	WaitID    string
	Kind      string
	ResumeURL string
	ExpiresAt string
	Status    string
	CreatedAt string
	UpdatedAt string
}

const workflowWaitMaterializationColumns = `wait_id, kind, resume_url,
       COALESCE(expires_at,''), status, created_at, updated_at`

func scanWorkflowWaitMaterialization(scanner interface{ Scan(...any) error }, row *WorkflowWaitMaterialization) error {
	return scanner.Scan(
		&row.WaitID, &row.Kind, &row.ResumeURL, &row.ExpiresAt,
		&row.Status, &row.CreatedAt, &row.UpdatedAt,
	)
}

// MaterializeWorkflowWait records the application-facing projection of a
// durable Hadron wait. The row contains no raw resume token and may be safely
// reconstructed after restart from the authoritative workflow wait snapshot.
func (s *Store) MaterializeWorkflowWait(ctx context.Context, row WorkflowWaitMaterialization) error {
	return s.writeWorkflowWaitMaterialization(ctx, row, "open")
}

// ResolveWorkflowWait closes the materialized projection idempotently. It may
// create an already-resolved row when an earlier post-commit materialization
// failed, ensuring cleanup never depends on the success of that prior adapter
// call.
func (s *Store) ResolveWorkflowWait(ctx context.Context, row WorkflowWaitMaterialization) error {
	return s.writeWorkflowWaitMaterialization(ctx, row, "resolved")
}

func (s *Store) writeWorkflowWaitMaterialization(ctx context.Context, row WorkflowWaitMaterialization, target string) error {
	if strings.TrimSpace(row.WaitID) == "" || strings.TrimSpace(row.Kind) == "" {
		return fmt.Errorf("materialize workflow wait: wait id and kind are required")
	}
	if target != "open" && target != "resolved" {
		return fmt.Errorf("materialize workflow wait: invalid target status %q", target)
	}
	if row.CreatedAt == "" {
		row.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	row.UpdatedAt = row.CreatedAt
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("materialize workflow wait: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
INSERT INTO workflow_wait_materializations(
    wait_id, kind, resume_url, expires_at, status, created_at, updated_at
)
SELECT ?, ?, ?, ?, ?, ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM workflow_wait_materializations WHERE wait_id = ?
)`, row.WaitID, row.Kind, row.ResumeURL, nullIfEmpty(row.ExpiresAt), target,
		row.CreatedAt, row.UpdatedAt, row.WaitID)
	if err != nil {
		return fmt.Errorf("materialize workflow wait: insert: %w", err)
	}
	var persisted WorkflowWaitMaterialization
	if err := scanWorkflowWaitMaterialization(tx.QueryRowContext(ctx,
		`SELECT `+workflowWaitMaterializationColumns+` FROM workflow_wait_materializations WHERE wait_id = ?`,
		row.WaitID), &persisted); err != nil {
		return fmt.Errorf("materialize workflow wait: read persisted row: %w", err)
	}
	if persisted.Kind != row.Kind || persisted.ResumeURL != row.ResumeURL || persisted.ExpiresAt != row.ExpiresAt {
		return fmt.Errorf("materialize workflow wait: wait %q conflicts with immutable material", row.WaitID)
	}
	if target == "resolved" && persisted.Status != "resolved" {
		if _, err := tx.ExecContext(ctx, `
UPDATE workflow_wait_materializations
SET status = 'resolved', updated_at = ?
WHERE wait_id = ? AND status = 'open'`, row.UpdatedAt, row.WaitID); err != nil {
			return fmt.Errorf("resolve workflow wait materialization: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("materialize workflow wait: commit: %w", err)
	}
	return nil
}

func (s *Store) GetWorkflowWaitMaterialization(ctx context.Context, waitID string) (*WorkflowWaitMaterialization, error) {
	var row WorkflowWaitMaterialization
	err := scanWorkflowWaitMaterialization(s.DB.QueryRowContext(ctx,
		`SELECT `+workflowWaitMaterializationColumns+` FROM workflow_wait_materializations WHERE wait_id = ?`,
		waitID), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workflow wait materialization not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow wait materialization: %w", err)
	}
	return &row, nil
}
