package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrAgentScheduleNotFound is returned when an agent_schedules row cannot
// be located.
var ErrAgentScheduleNotFound = errors.New("agent schedule not found")

// Schedule kind constants. The CHECK constraint on agent_schedules.schedule_kind
// keeps DB rows aligned with these values; mismatches surface as INSERT errors.
const (
	ScheduleKindEveryNTicks = "every_n_ticks"
	ScheduleKindOnTick      = "on_tick"
	ScheduleKindCron        = "cron"
	ScheduleKindOneShot     = "one_shot"
	ScheduleKindOnEvent     = "on_event"
)

// Schedule status constants. CHECK constraint enforces.
const (
	ScheduleStatusActive  = "active"
	ScheduleStatusPaused  = "paused"
	ScheduleStatusExpired = "expired"
)

// AgentSchedule is one row in the agent_schedules table — a per-agent
// directive that the composer (FU-27) folds into the per-tick procedure
// body when its firing criteria match the tick.
type AgentSchedule struct {
	ID           string `json:"id"`
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id"` // empty = applies to all sessions of this agent
	Name         string `json:"name"`
	ScheduleKind string `json:"schedule_kind"`
	ScheduleSpec string `json:"schedule_spec"`
	Body         string `json:"body"`
	Priority     int64  `json:"priority"`
	Status       string `json:"status"`
	ExpiresAt    string `json:"expires_at"`
	FiredCount   int64  `json:"fired_count"`
	LastFiredAt  string `json:"last_fired_at"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by"`
}

const agentScheduleColumns = `id, agent_id, COALESCE(session_id,''), name, schedule_kind,
       schedule_spec, body, priority, status, COALESCE(expires_at,''),
       fired_count, COALESCE(last_fired_at,''), created_at, created_by`

func scanAgentSchedule(scanner interface{ Scan(...any) error }, s *AgentSchedule) error {
	return scanner.Scan(
		&s.ID, &s.AgentID, &s.SessionID, &s.Name, &s.ScheduleKind,
		&s.ScheduleSpec, &s.Body, &s.Priority, &s.Status, &s.ExpiresAt,
		&s.FiredCount, &s.LastFiredAt, &s.CreatedAt, &s.CreatedBy,
	)
}

// InsertAgentSchedule upserts an agent_schedules row by ID.
func (s *Store) InsertAgentSchedule(ctx context.Context, row AgentSchedule) error {
	if row.ID == "" {
		return fmt.Errorf("insert agent_schedules: id is required")
	}
	if row.AgentID == "" {
		return fmt.Errorf("insert agent_schedules: agent_id is required")
	}
	if row.Name == "" {
		return fmt.Errorf("insert agent_schedules: name is required")
	}
	if row.ScheduleKind == "" {
		return fmt.Errorf("insert agent_schedules: schedule_kind is required")
	}
	if row.Body == "" {
		return fmt.Errorf("insert agent_schedules: body is required")
	}
	if row.Status == "" {
		row.Status = ScheduleStatusActive
	}
	if row.CreatedBy == "" {
		row.CreatedBy = "operator"
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO agent_schedules
		    (id, agent_id, session_id, name, schedule_kind, schedule_spec,
		     body, priority, status, expires_at, fired_count, last_fired_at,
		     created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?,
		         ?, ?, ?, ?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?)`,
		row.ID, row.AgentID, nullIfEmpty(row.SessionID), row.Name,
		row.ScheduleKind, row.ScheduleSpec,
		row.Body, row.Priority, row.Status, nullIfEmpty(row.ExpiresAt),
		row.FiredCount, nullIfEmpty(row.LastFiredAt),
		row.CreatedAt,
		row.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert agent_schedules: %w", err)
	}
	return nil
}

// GetAgentSchedule returns a row by id, or ErrAgentScheduleNotFound.
func (s *Store) GetAgentSchedule(ctx context.Context, id string) (*AgentSchedule, error) {
	var out AgentSchedule
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentScheduleColumns+` FROM agent_schedules WHERE id = ?`, id,
	)
	if err := scanAgentSchedule(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentScheduleNotFound
		}
		return nil, fmt.Errorf("get agent_schedules: %w", err)
	}
	return &out, nil
}

// ListAgentSchedules returns all rows for an agent, ordered by priority
// DESC, created_at ASC. Includes paused and expired rows; filter at the
// call site if you only want active.
func (s *Store) ListAgentSchedules(ctx context.Context, agentID string) ([]AgentSchedule, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentScheduleColumns+`
		 FROM agent_schedules
		 WHERE agent_id = ?
		 ORDER BY priority DESC, created_at ASC`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_schedules: %w", err)
	}
	defer rows.Close()
	out := make([]AgentSchedule, 0)
	for rows.Next() {
		var sch AgentSchedule
		if err := scanAgentSchedule(rows, &sch); err != nil {
			return nil, fmt.Errorf("scan agent_schedules: %w", err)
		}
		out = append(out, sch)
	}
	return out, rows.Err()
}

// DeleteAgentSchedule removes a row by id. Returns ErrAgentScheduleNotFound
// if no row matched.
func (s *Store) DeleteAgentSchedule(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_schedules WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete agent_schedules: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent_schedules rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentScheduleNotFound
	}
	return nil
}

// UpdateAgentScheduleStatus sets the status column (active|paused|expired).
func (s *Store) UpdateAgentScheduleStatus(ctx context.Context, id, status string) error {
	switch status {
	case ScheduleStatusActive, ScheduleStatusPaused, ScheduleStatusExpired:
	default:
		return fmt.Errorf("update agent_schedules status: invalid status %q", status)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_schedules SET status = ? WHERE id = ?`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("update agent_schedules status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent_schedules status rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentScheduleNotFound
	}
	return nil
}

// BumpAgentScheduleFireCount increments fired_count and updates
// last_fired_at to now. Used by the composer after the schedule has been
// folded into a tick's procedure body.
func (s *Store) BumpAgentScheduleFireCount(ctx context.Context, id string, now time.Time) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_schedules
		    SET fired_count = fired_count + 1,
		        last_fired_at = ?
		  WHERE id = ?`,
		now.UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("bump agent_schedules fired_count: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("bump agent_schedules rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentScheduleNotFound
	}
	return nil
}

// GetDueSchedules returns the active schedules for a given session+tick
// whose firing criteria match the current tick context. Results are
// ordered by priority DESC, created_at ASC so the composer can render
// them top-to-bottom.
//
// Matching rules:
//   - status must be 'active'
//   - session_id matches the provided sessionID, OR session_id IS NULL
//     (the latter applies to all sessions of the agent)
//   - expires_at, if set, must be in the future relative to now
//   - schedule_kind firing semantics defined in the table comment
//
// agentID is required to scope the lookup. sessionID may be empty to
// look up only NULL-session schedules.
func (s *Store) GetDueSchedules(ctx context.Context, agentID, sessionID string, tickN int, now time.Time) ([]AgentSchedule, error) {
	if agentID == "" {
		return nil, fmt.Errorf("get due schedules: agent_id is required")
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentScheduleColumns+`
		 FROM agent_schedules
		 WHERE agent_id = ?
		   AND status = 'active'
		   AND (session_id IS NULL OR session_id = ?)
		   AND (expires_at IS NULL OR expires_at > ?)
		 ORDER BY priority DESC, created_at ASC`,
		agentID, sessionID, now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("get due schedules: %w", err)
	}
	defer rows.Close()

	candidates := make([]AgentSchedule, 0)
	for rows.Next() {
		var sch AgentSchedule
		if err := scanAgentSchedule(rows, &sch); err != nil {
			return nil, fmt.Errorf("scan agent_schedules: %w", err)
		}
		candidates = append(candidates, sch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]AgentSchedule, 0, len(candidates))
	for _, sch := range candidates {
		fires, err := scheduleFires(sch, tickN, now)
		if err != nil {
			return nil, fmt.Errorf("schedule %s (%s): %w", sch.ID, sch.ScheduleKind, err)
		}
		if fires {
			out = append(out, sch)
		}
	}
	return out, nil
}

// scheduleFires evaluates whether a schedule's firing criteria match the
// given tick context. Pure function for unit testability.
func scheduleFires(sch AgentSchedule, tickN int, now time.Time) (bool, error) {
	switch sch.ScheduleKind {
	case ScheduleKindEveryNTicks:
		n, err := strconv.Atoi(sch.ScheduleSpec)
		if err != nil {
			return false, fmt.Errorf("every_n_ticks spec %q is not an integer", sch.ScheduleSpec)
		}
		if n <= 0 {
			return false, fmt.Errorf("every_n_ticks spec must be positive, got %d", n)
		}
		return tickN > 0 && tickN%n == 0, nil

	case ScheduleKindOnTick:
		target, err := strconv.Atoi(sch.ScheduleSpec)
		if err != nil {
			return false, fmt.Errorf("on_tick spec %q is not an integer", sch.ScheduleSpec)
		}
		return tickN == target, nil

	case ScheduleKindCron:
		// Standard 5-field cron (no seconds). Use ParseStandard so the
		// spec format matches operator intuition (`0 9 * * *`).
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		schedule, err := parser.Parse(sch.ScheduleSpec)
		if err != nil {
			return false, fmt.Errorf("cron spec %q: %w", sch.ScheduleSpec, err)
		}
		// Fires when the schedule's next-fire instant relative to a
		// reference one tick ago is at or before now. We approximate
		// "one tick ago" as 15 minutes (the Supervisor cadence). This
		// is the spike-default; callers wanting tighter precision will
		// pass a more recent reference once the composer threads tick
		// timing properly.
		ref := now.Add(-15 * time.Minute)
		next := schedule.Next(ref)
		return !next.After(now), nil

	case ScheduleKindOneShot:
		// Fires on the next tick after creation. Composer is expected to
		// flip the row to status='expired' after firing.
		return sch.FiredCount == 0, nil

	case ScheduleKindOnEvent:
		// Event detection happens outside this function. The composer
		// passes an event-resolved schedule kind by flipping a
		// transient flag, or by changing the row's kind temporarily.
		// For Phase A (this commit), on_event is a no-op — it never
		// fires from scheduleFires. Phase B will introduce an event
		// resolver.
		return false, nil

	default:
		return false, fmt.Errorf("unknown schedule_kind %q", sch.ScheduleKind)
	}
}
