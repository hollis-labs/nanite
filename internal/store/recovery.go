package store

import (
	"fmt"
	"time"
)

// RecoveryBreadcrumb is the persistence shape the in-process recovery
// broker writes per failure observation. The broker package
// (internal/recovery/broker) owns the typed enum values; this
// row stores their lower-case string forms via the broker's Stringers.
//
// One row per broker-handled FailureEvent. Postmortem queries pivot
// off (session_id, outcome) — see migration 054 for the indexes.
type RecoveryBreadcrumb struct {
	Timestamp    time.Time
	SessionID    string
	Class        string
	Cause        string
	Remediation  string
	Action       string
	Outcome      string
	AttemptCount int
	DurationMs   int64
	Reason       string
}

// WriteRecoveryBreadcrumb persists a breadcrumb row. Errors propagate
// up to the broker, which logs but does not escalate — telemetry must
// not crash recovery flow.
func (s *Store) WriteRecoveryBreadcrumb(b *RecoveryBreadcrumb) error {
	if b == nil {
		return fmt.Errorf("WriteRecoveryBreadcrumb: nil breadcrumb")
	}
	if b.SessionID == "" {
		return fmt.Errorf("WriteRecoveryBreadcrumb: empty session_id")
	}
	ts := b.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.DB.Exec(
		`INSERT INTO nanite_recovery_breadcrumbs
		 (timestamp, session_id, class, cause, remediation, action, outcome, attempt_count, duration_ms, reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.UTC().Format(time.RFC3339Nano),
		b.SessionID,
		b.Class,
		b.Cause,
		b.Remediation,
		b.Action,
		b.Outcome,
		b.AttemptCount,
		b.DurationMs,
		b.Reason,
	)
	if err != nil {
		return fmt.Errorf("write recovery breadcrumb: %w", err)
	}
	return nil
}

// MarkAgentRuntimeRelaunching transitions the runtime row from its
// current state to state="launching" with failure_reason capturing the
// broker's relaunch reason ("broker retry attempt N"). Distinct from
// MarkAgentRuntimeFailed — the broker uses this to signal "we're
// retrying" before the agent.Boot replacement-dispatch fires.
//
// No-op on unknown id (mirrors MarkAgentRuntimeFailed's tolerance).
func (s *Store) MarkAgentRuntimeRelaunching(id, reason string) error {
	_, err := s.DB.Exec(
		`UPDATE agent_runtime
		 SET state = 'launching', failure_reason = ?, updated_at = ?
		 WHERE id = ?`,
		reason, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("mark agent_runtime relaunching %s: %w", id, err)
	}
	return nil
}

// ListRecoveryBreadcrumbsForSession returns all breadcrumbs for a
// session_id, oldest-first. Used by postmortem CLI / future inspector
// surfaces. Limit caps the row count; pass 0 for "no limit".
func (s *Store) ListRecoveryBreadcrumbsForSession(sessionID string, limit int) ([]*RecoveryBreadcrumb, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("ListRecoveryBreadcrumbsForSession: empty session_id")
	}
	q := `SELECT timestamp, session_id, class, cause, remediation, action, outcome, attempt_count, duration_ms, reason
	      FROM nanite_recovery_breadcrumbs
	      WHERE session_id = ?
	      ORDER BY timestamp ASC`
	args := []any{sessionID}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list recovery breadcrumbs: %w", err)
	}
	defer rows.Close()

	var out []*RecoveryBreadcrumb
	for rows.Next() {
		b := &RecoveryBreadcrumb{}
		var ts string
		if err := rows.Scan(
			&ts, &b.SessionID, &b.Class, &b.Cause, &b.Remediation,
			&b.Action, &b.Outcome, &b.AttemptCount, &b.DurationMs, &b.Reason,
		); err != nil {
			return nil, fmt.Errorf("scan recovery breadcrumb: %w", err)
		}
		if t, perr := time.Parse(time.RFC3339Nano, ts); perr == nil {
			b.Timestamp = t
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
