package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ErrScheduleRunNotFound is returned when a schedule_runs row cannot be
// located (GetOpenScheduleRun/GetLatestScheduleRun) or when
// RecordScheduleRunAttempt's WHERE id=? matches nothing.
var ErrScheduleRunNotFound = errors.New("schedule run not found")

// schedule_runs.status vocabulary -- migration 127
// (TASKS/scheduling/01-schema-schedule-kind-collapse-and-retry-columns.md),
// taken verbatim from that migration's CHECK constraint. 'pending'/'failed'
// are the two non-terminal ("open," still eligible for a further retry
// attempt) statuses; 'succeeded'/'exhausted' are terminal.
const (
	ScheduleRunStatusPending   = "pending"
	ScheduleRunStatusSucceeded = "succeeded"
	ScheduleRunStatusFailed    = "failed"
	ScheduleRunStatusExhausted = "exhausted"
)

// ScheduleRun is one row in the schedule_runs table -- retry-attempt
// bookkeeping for a single schedule firing, read/written by
// internal/scheduler's RetryingRunner
// (TASKS/scheduling/04-retry-backoff-on-fail-policy.md).
type ScheduleRun struct {
	ID         string `json:"id"`
	ScheduleID string `json:"schedule_id"`
	// RunID is go-scheduler's Job.RunID for the attempt that created this
	// row. NOTE: RunID is NOT a stable identifier across retry attempts of
	// the "same" logical firing -- see RetryingRunner's package doc comment
	// in internal/scheduler for why (libs/go-scheduler/engine.go's tick()
	// generates a fresh RunID every tick). This field records the first
	// attempt's RunID and is not updated on subsequent retries of the same
	// row; correlation across retries is done by ScheduleID + open status,
	// not by RunID.
	RunID string `json:"run_id"`
	// FiredAt is RFC3339, the first attempt's Job.FiredAt (or the row's
	// creation time, if FiredAt was zero).
	FiredAt       string `json:"fired_at"`
	Status        string `json:"status"`
	AttemptCount  int64  `json:"attempt_count"`
	LastError     string `json:"last_error"`
	NextAttemptAt string `json:"next_attempt_at"` // RFC3339; empty = no backoff window active
}

const scheduleRunColumns = `id, schedule_id, run_id, fired_at, status, attempt_count,
       COALESCE(last_error,''), COALESCE(next_attempt_at,'')`

func scanScheduleRun(scanner interface{ Scan(...any) error }, r *ScheduleRun) error {
	return scanner.Scan(
		&r.ID, &r.ScheduleID, &r.RunID, &r.FiredAt, &r.Status, &r.AttemptCount,
		&r.LastError, &r.NextAttemptAt,
	)
}

// GetOpenScheduleRun returns the most recent non-terminal (pending/failed)
// schedule_runs row for scheduleID -- the row representing whichever
// firing's retry sequence is currently in progress, or
// ErrScheduleRunNotFound if none is open.
//
// Correlated by schedule_id + open status, deliberately NOT by
// go-scheduler's Job.RunID (the architecture doc's own illustrative "one
// row per firing, keyed by run_id" language) -- see internal/scheduler's
// RetryingRunner doc comment for the full finding: reading
// libs/go-scheduler/engine.go's tick() directly shows Job.RunID
// (fmt.Sprintf("sched-%s-%d", sch.ID, now.Unix())) is generated fresh on
// every tick, not stable across a firing's retry attempts, so keying
// lookups strictly by run_id would silently create a brand-new row (and
// reset the retry budget to zero) on every single retry attempt in real
// production use -- defeating the entire point of max_retries. schedule_id
// is the value that stays stable across retries of the same firing (the
// engine keeps resetting agent_schedules.next_run back to the same
// expectedNext on every failed Enqueue until this layer returns nil), so
// that is the real correlation key.
func (s *Store) GetOpenScheduleRun(ctx context.Context, scheduleID string) (*ScheduleRun, error) {
	var out ScheduleRun
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+scheduleRunColumns+` FROM schedule_runs
		 WHERE schedule_id = ? AND status IN (?, ?)
		 ORDER BY fired_at DESC, rowid DESC
		 LIMIT 1`,
		scheduleID, ScheduleRunStatusPending, ScheduleRunStatusFailed,
	)
	if err := scanScheduleRun(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrScheduleRunNotFound
		}
		return nil, fmt.Errorf("get open schedule_runs row: %w", err)
	}
	return &out, nil
}

// GetLatestScheduleRun returns the most recent schedule_runs row for
// scheduleID regardless of status, or ErrScheduleRunNotFound if none
// exists. Used by RetryingRunner to detect the "caller kept calling
// Enqueue with an unchanged Job after this layer already terminated that
// firing" case (a fixed-Job test harness simulating repeated ticking, or a
// go-scheduler tick landing before agent_schedules.next_run has actually
// advanced) without re-creating a fresh retry budget for a firing that has
// already concluded.
func (s *Store) GetLatestScheduleRun(ctx context.Context, scheduleID string) (*ScheduleRun, error) {
	var out ScheduleRun
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+scheduleRunColumns+` FROM schedule_runs
		 WHERE schedule_id = ?
		 ORDER BY fired_at DESC, rowid DESC
		 LIMIT 1`,
		scheduleID,
	)
	if err := scanScheduleRun(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrScheduleRunNotFound
		}
		return nil, fmt.Errorf("get latest schedule_runs row: %w", err)
	}
	return &out, nil
}

// CreateScheduleRun inserts a new schedule_runs row representing the first
// attempt of a new firing -- attempt_count/last_error/next_attempt_at
// always start at their zero values regardless of what row.AttemptCount/
// row.LastError/row.NextAttemptAt hold on the way in (a genuinely new
// firing has made zero real attempts yet, by definition).
func (s *Store) CreateScheduleRun(ctx context.Context, row ScheduleRun) (*ScheduleRun, error) {
	if row.ScheduleID == "" {
		return nil, fmt.Errorf("create schedule_runs: schedule_id is required")
	}
	if row.RunID == "" {
		return nil, fmt.Errorf("create schedule_runs: run_id is required")
	}
	if row.ID == "" {
		row.ID = "sr-" + ulid.Make().String()
	}
	if row.Status == "" {
		row.Status = ScheduleRunStatusPending
	}
	if row.FiredAt == "" {
		row.FiredAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO schedule_runs
		    (id, schedule_id, run_id, fired_at, status, attempt_count, last_error, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, 0, NULL, NULL)`,
		row.ID, row.ScheduleID, row.RunID, row.FiredAt, row.Status,
	)
	if err != nil {
		return nil, fmt.Errorf("create schedule_runs: %w", err)
	}
	row.AttemptCount = 0
	row.LastError = ""
	row.NextAttemptAt = ""
	return &row, nil
}

// RecordScheduleRunAttempt is the single write path after every real
// dispatch attempt (success, retriable failure, or exhaustion): it
// unconditionally increments attempt_count and sets status/last_error/
// next_attempt_at to the outcome the caller already decided. nextAttemptAt
// nil (or zero) clears the backoff window (NULL) -- the correct value on
// success and on exhaustion, since neither state has a further attempt to
// wait for.
func (s *Store) RecordScheduleRunAttempt(ctx context.Context, id, status, lastError string, nextAttemptAt *time.Time) error {
	switch status {
	case ScheduleRunStatusPending, ScheduleRunStatusSucceeded, ScheduleRunStatusFailed, ScheduleRunStatusExhausted:
	default:
		return fmt.Errorf("record schedule_runs attempt: invalid status %q", status)
	}
	var nextAttemptVal any
	if nextAttemptAt != nil && !nextAttemptAt.IsZero() {
		nextAttemptVal = nextAttemptAt.UTC().Format(time.RFC3339)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE schedule_runs
		    SET attempt_count = attempt_count + 1,
		        status = ?,
		        last_error = ?,
		        next_attempt_at = ?
		  WHERE id = ?`,
		status, nullIfEmpty(lastError), nextAttemptVal, id,
	)
	if err != nil {
		return fmt.Errorf("record schedule_runs attempt: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("record schedule_runs attempt rows affected: %w", err)
	}
	if n == 0 {
		return ErrScheduleRunNotFound
	}
	return nil
}
