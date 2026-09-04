package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrScheduleFireNotFound is returned when a durable fire cannot be located.
var ErrScheduleFireNotFound = errors.New("schedule fire not found")

const (
	ScheduleFireStatusPending   = "pending"
	ScheduleFireStatusClaimed   = "claimed"
	ScheduleFireStatusRetrying  = "retrying"
	ScheduleFireStatusSucceeded = "succeeded"
	ScheduleFireStatusSkipped   = "skipped"
	ScheduleFireStatusExhausted = "exhausted"
)

// ScheduleFire is Nanite's persisted representation of a go-scheduler Fire.
// RunID is the stable library Fire.ID; ID remains Nanite's internal row key.
type ScheduleFire struct {
	ID                     string
	ScheduleID             string
	RunID                  string
	ScheduledAt            string
	FiredAt                string
	ClaimExpiresAt         string
	DispatchAcceptedAt     string
	Status                 string
	AttemptCount           int64
	LastError              string
	NextAttemptAt          string
	RetryMaxAttempts       int64
	RetryBackoffStrategy   string
	RetryInitialDelayNanos int64
	RetryMaximumDelayNanos int64
	JobType                string
	JobPayload             string
}

type ScheduleFireCreation struct {
	ScheduleID   string
	ExpectedNext time.Time
	NextRun      time.Time
	Fire         ScheduleFire
}

type ScheduleFireClaim struct {
	FireID          string
	ExpectedStatus  string
	ExpectedAttempt int64
	ExpectedFiredAt time.Time
	ClaimedAt       time.Time
	ClaimExpiresAt  time.Time
}

type ScheduleFireTransition struct {
	FireID        string
	Attempt       int64
	From          string
	ClaimedAt     time.Time
	To            string
	NextAttemptAt time.Time
	LastError     string
}

const scheduleFireColumns = `id, schedule_id, run_id, scheduled_at, fired_at,
       COALESCE(claim_expires_at,''), COALESCE(dispatch_accepted_at,''), status, attempt_count,
       COALESCE(last_error,''), COALESCE(next_attempt_at,''),
       retry_max_attempts, retry_backoff_strategy, retry_initial_delay_ns,
       retry_max_delay_ns, job_type, job_payload`

func scanScheduleFire(scanner interface{ Scan(...any) error }, fire *ScheduleFire) error {
	return scanner.Scan(
		&fire.ID, &fire.ScheduleID, &fire.RunID, &fire.ScheduledAt, &fire.FiredAt,
		&fire.ClaimExpiresAt, &fire.DispatchAcceptedAt, &fire.Status, &fire.AttemptCount,
		&fire.LastError, &fire.NextAttemptAt,
		&fire.RetryMaxAttempts, &fire.RetryBackoffStrategy,
		&fire.RetryInitialDelayNanos, &fire.RetryMaximumDelayNanos,
		&fire.JobType, &fire.JobPayload,
	)
}

// CreateScheduleFire atomically advances agent_schedules.next_run and inserts
// one immutable fire snapshot. A stale schedule CAS or existing fire ID is a
// clean loss (false, nil) and changes neither record.
func (s *Store) CreateScheduleFire(ctx context.Context, creation ScheduleFireCreation) (bool, error) {
	if creation.ScheduleID == "" || creation.Fire.RunID == "" {
		return false, fmt.Errorf("create schedule fire: schedule_id and fire id are required")
	}
	if creation.Fire.ID == "" {
		creation.Fire.ID = creation.Fire.RunID
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("create schedule fire: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schedule_runs WHERE run_id = ?)`, creation.Fire.RunID).Scan(&exists); queryErr != nil {
		return false, fmt.Errorf("create schedule fire: check identity: %w", queryErr)
	}
	if exists != 0 {
		return false, nil
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE agent_schedules
		    SET last_fired_at = ?, next_run = ?, fired_count = fired_count + 1
		  WHERE id = ? AND status = ? AND next_run = ?`,
		creation.Fire.ScheduledAt, nullableFireTime(creation.NextRun),
		creation.ScheduleID, ScheduleStatusActive, formatFireTime(creation.ExpectedNext),
	)
	if err != nil {
		return false, fmt.Errorf("create schedule fire: advance schedule: %w", err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("create schedule fire: advance rows affected: %w", err)
	}
	if updated == 0 {
		return false, nil
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO schedule_runs (
		    id, schedule_id, run_id, scheduled_at, fired_at, claim_expires_at, dispatch_accepted_at,
		    status, attempt_count, last_error, next_attempt_at,
		    retry_max_attempts, retry_backoff_strategy, retry_initial_delay_ns,
		    retry_max_delay_ns, job_type, job_payload
		 ) SELECT ?, ?, ?, ?, ?, NULL, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`,
		creation.Fire.ID, creation.ScheduleID, creation.Fire.RunID,
		creation.Fire.ScheduledAt, creation.Fire.FiredAt,
		creation.Fire.Status, creation.Fire.AttemptCount,
		nullIfEmpty(creation.Fire.LastError), nullIfEmpty(creation.Fire.NextAttemptAt),
		creation.Fire.RetryMaxAttempts, creation.Fire.RetryBackoffStrategy,
		creation.Fire.RetryInitialDelayNanos, creation.Fire.RetryMaximumDelayNanos,
		creation.Fire.JobType, creation.Fire.JobPayload,
	)
	if err != nil {
		return false, fmt.Errorf("create schedule fire: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("create schedule fire: commit: %w", err)
	}
	return true, nil
}

// ListDueScheduleFires returns claimable fires, including claimed fires whose
// lease is missing (legacy migration) or expired.
func (s *Store) ListDueScheduleFires(ctx context.Context, now time.Time, limit int) ([]ScheduleFire, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+scheduleFireColumns+` FROM schedule_runs
		  WHERE (
		      status IN (?, ?) AND julianday(COALESCE(next_attempt_at, scheduled_at)) <= julianday(?)
		  ) OR (
		      status = ? AND (claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday(?))
		  )
		 ORDER BY julianday(COALESCE(next_attempt_at, scheduled_at)), run_id
		 LIMIT ?`,
		ScheduleFireStatusPending, ScheduleFireStatusRetrying, formatFireTime(now),
		ScheduleFireStatusClaimed, formatFireTime(now), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due schedule fires: %w", err)
	}
	defer closeRows(rows)
	out := make([]ScheduleFire, 0)
	for rows.Next() {
		var fire ScheduleFire
		if err := scanScheduleFire(rows, &fire); err != nil {
			return nil, fmt.Errorf("list due schedule fires: scan: %w", err)
		}
		out = append(out, fire)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due schedule fires: iterate: %w", err)
	}
	return out, nil
}

// ClaimScheduleFire claims a pending/retrying attempt or recovers an expired
// claim. Recovery preserves its attempt while replacing the claim timestamp,
// which fences the prior owner from transitioning it later.
func (s *Store) ClaimScheduleFire(ctx context.Context, claim ScheduleFireClaim) (ScheduleFire, bool, error) {
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
		return ScheduleFire{}, false, fmt.Errorf("claim schedule fire: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE schedule_runs
		    SET status = ?, attempt_count = ?, fired_at = ?, claim_expires_at = ?, next_attempt_at = NULL
		  WHERE run_id = ? AND status = ? AND attempt_count = ? AND fired_at = ?`+recoveryClause,
		append([]any{ScheduleFireStatusClaimed}, args...)...,
	)
	if err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim schedule fire: update: %w", err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim schedule fire: rows affected: %w", err)
	}
	if updated == 0 {
		return ScheduleFire{}, false, nil
	}
	var fire ScheduleFire
	if err := scanScheduleFire(tx.QueryRowContext(ctx,
		`SELECT `+scheduleFireColumns+` FROM schedule_runs WHERE run_id = ?`, claim.FireID), &fire); err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim schedule fire: read result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ScheduleFire{}, false, fmt.Errorf("claim schedule fire: commit: %w", err)
	}
	return fire, true, nil
}

// TransitionScheduleFire applies an attempt result with a fenced CAS.
func (s *Store) TransitionScheduleFire(ctx context.Context, transition ScheduleFireTransition) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE schedule_runs
		    SET status = ?, claim_expires_at = NULL, next_attempt_at = ?, last_error = ?
		  WHERE run_id = ? AND status = ? AND attempt_count = ? AND fired_at = ?`,
		transition.To, nullableFireTime(transition.NextAttemptAt), nullIfEmpty(transition.LastError),
		transition.FireID, transition.From, transition.Attempt, formatFireTime(transition.ClaimedAt),
	)
	if err != nil {
		return false, fmt.Errorf("transition schedule fire: %w", err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("transition schedule fire: rows affected: %w", err)
	}
	return updated == 1, nil
}

func (s *Store) GetScheduleFire(ctx context.Context, fireID string) (*ScheduleFire, error) {
	var fire ScheduleFire
	err := scanScheduleFire(s.DB.QueryRowContext(ctx,
		`SELECT `+scheduleFireColumns+` FROM schedule_runs WHERE run_id = ?`, fireID), &fire)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScheduleFireNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get schedule fire: %w", err)
	}
	return &fire, nil
}

// IsScheduleFireDispatchAccepted reports whether RunnerAdapter already
// completed the application dispatch for this stable Fire ID.
func (s *Store) IsScheduleFireDispatchAccepted(ctx context.Context, fireID string) (bool, error) {
	var accepted int
	err := s.DB.QueryRowContext(ctx,
		`SELECT dispatch_accepted_at IS NOT NULL FROM schedule_runs WHERE run_id = ?`, fireID,
	).Scan(&accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return s.IsWorkflowActivationDispatchAccepted(ctx, fireID)
	}
	if err != nil {
		return false, fmt.Errorf("check schedule fire dispatch receipt: %w", err)
	}
	return accepted != 0, nil
}

// MarkScheduleFireDispatchAccepted persists the application dispatch receipt
// while the caller still owns the exact claimed attempt. The claim timestamp
// fences a worker whose lease was recovered while it was dispatching.
func (s *Store) MarkScheduleFireDispatchAccepted(
	ctx context.Context,
	fireID string,
	attempt int,
	claimedAt, acceptedAt time.Time,
) (bool, error) {
	if _, err := s.GetWorkflowActivationFire(ctx, fireID); err == nil {
		return s.MarkWorkflowActivationDispatchAccepted(ctx, fireID, attempt, claimedAt, acceptedAt)
	} else if !errors.Is(err, ErrWorkflowActivationFireNotFound) {
		return false, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE schedule_runs
		    SET dispatch_accepted_at = COALESCE(dispatch_accepted_at, ?)
		  WHERE run_id = ? AND status = ? AND attempt_count = ? AND fired_at = ?`,
		formatFireTime(acceptedAt), fireID, ScheduleFireStatusClaimed, attempt, formatFireTime(claimedAt),
	)
	if err != nil {
		return false, fmt.Errorf("mark schedule fire dispatch accepted: %w", err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark schedule fire dispatch accepted rows affected: %w", err)
	}
	return updated == 1, nil
}

func formatFireTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableFireTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatFireTime(t)
}
