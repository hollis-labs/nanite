package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrAgentScheduleNotFound is returned when an agent_schedules row cannot
// be located.
var ErrAgentScheduleNotFound = errors.New("agent schedule not found")

// Schedule kind constants. The CHECK constraint on agent_schedules.schedule_kind
// keeps DB rows aligned with these values; mismatches surface as INSERT errors.
//
// TASKS/scheduling/01-schema-schedule-kind-collapse-and-retry-columns.md
// (migration 127) collapsed this from five values down to these two --
// every_n_ticks/on_tick/on_event never had a real production firing
// mechanism (the FU-27 composer that would have interpreted them was never
// built) and don't map onto go-scheduler's time-based-only model
// (docs/engineering/architecture/12-scheduling.md, "Full replace, not
// dual-run"). Do not reintroduce them without a corresponding schema
// migration widening the CHECK back out.
const (
	ScheduleKindCron    = "cron"
	ScheduleKindOneShot = "one_shot"
)

// Schedule status constants. CHECK constraint enforces.
const (
	ScheduleStatusActive  = "active"
	ScheduleStatusPaused  = "paused"
	ScheduleStatusExpired = "expired"
)

// on_fail policy constants. CHECK constraint enforces. See migration 127's
// doc comment and TASKS/scheduling/01-schema-schedule-kind-collapse-and-
// retry-columns.md's Work Log for how 'retry' (the column default) is
// meant to resolve once max_retries is actually exhausted -- it is not a
// fourth do-nothing terminal state.
const (
	ScheduleOnFailRetry   = "retry"
	ScheduleOnFailDisable = "disable"
	ScheduleOnFailNotify  = "notify"
)

// Job type constants -- docs/engineering/architecture/12-scheduling.md's
// "The Runner adapter and job taxonomy" table. durable_agent_wake is the
// only value with a live dispatch path today; the other three are schema-
// ready for TASKS/scheduling/03-runner-adapter-and-job-taxonomy.md.
const (
	ScheduleJobTypeDurableAgentWake = "durable_agent_wake"
	ScheduleJobTypeAgentWorkflowRun = "agent_workflow_run"
	ScheduleJobTypeCommandRun       = "command_run"
	ScheduleJobTypeReflexDispatch   = "reflex_dispatch"
)

// AgentSchedule is one row in the agent_schedules table -- a per-agent
// directive that fires on a schedule and dispatches a job. Historically
// (pre-migration-127) described as feeding an "FU-27 composer" that was
// never built; the live mechanism today is durable_wake.go's RunDue for
// job_type=durable_agent_wake, and the go-scheduler-backed engine
// (TASKS/scheduling/02..05) is what next_run/max_retries/on_fail/job_type/
// job_payload exist to support going forward.
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

	// MaxRetries, OnFail, NextRun, JobType, JobPayload were added by
	// migration 127 (TASKS/scheduling/01-schema-schedule-kind-collapse-
	// and-retry-columns.md).
	MaxRetries int64  `json:"max_retries"`
	OnFail     string `json:"on_fail"`
	// NextRun is empty when unscheduled (DB NULL), matching go-scheduler's
	// own "zero NextRun is skipped" convention. When set, it is RFC3339 --
	// the same format LastFiredAt/ExpiresAt already use in this table, and
	// what the Store adapter (TASKS/scheduling/02-store-adapter.md) parses.
	NextRun    string `json:"next_run"`
	JobType    string `json:"job_type"`
	JobPayload string `json:"job_payload"`
}

const agentScheduleColumns = `id, agent_id, COALESCE(session_id,''), name, schedule_kind,
       schedule_spec, body, priority, status, COALESCE(expires_at,''),
       fired_count, COALESCE(last_fired_at,''), created_at, created_by,
       max_retries, on_fail, COALESCE(next_run,''), job_type, job_payload`

func scanAgentSchedule(scanner interface{ Scan(...any) error }, s *AgentSchedule) error {
	return scanner.Scan(
		&s.ID, &s.AgentID, &s.SessionID, &s.Name, &s.ScheduleKind,
		&s.ScheduleSpec, &s.Body, &s.Priority, &s.Status, &s.ExpiresAt,
		&s.FiredCount, &s.LastFiredAt, &s.CreatedAt, &s.CreatedBy,
		&s.MaxRetries, &s.OnFail, &s.NextRun, &s.JobType, &s.JobPayload,
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
	// MaxRetries/OnFail/JobType/JobPayload default from their zero Go
	// value, mirroring Status/CreatedBy above and matching the DDL's own
	// column defaults (migration 127) -- since every value is passed
	// explicitly in this INSERT, SQLite's column DEFAULT never actually
	// applies, so it has to be replicated here. Trade-off, documented: a
	// genuine "zero retries, fail immediately" policy isn't independently
	// expressible via MaxRetries==0 today (it collapses to the default of
	// 3) -- callers wanting fail-fast use on_fail alone with a small
	// MaxRetries (e.g. 1), not MaxRetries==0. NextRun has no default: an
	// empty value is a real, meaningful "unscheduled" (NULL), not a gap to
	// fill in.
	if row.MaxRetries <= 0 {
		row.MaxRetries = 3
	}
	if row.OnFail == "" {
		row.OnFail = ScheduleOnFailRetry
	}
	if row.JobType == "" {
		row.JobType = ScheduleJobTypeDurableAgentWake
	}
	if row.JobPayload == "" {
		row.JobPayload = "{}"
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO agent_schedules
		    (id, agent_id, session_id, name, schedule_kind, schedule_spec,
		     body, priority, status, expires_at, fired_count, last_fired_at,
		     created_at, created_by, max_retries, on_fail, next_run,
		     job_type, job_payload)
		 VALUES (?, ?, ?, ?, ?, ?,
		         ?, ?, ?, ?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')),
		         ?, ?, ?, ?, ?, ?)`,
		row.ID, row.AgentID, nullIfEmpty(row.SessionID), row.Name,
		row.ScheduleKind, row.ScheduleSpec,
		row.Body, row.Priority, row.Status, nullIfEmpty(row.ExpiresAt),
		row.FiredCount, nullIfEmpty(row.LastFiredAt),
		row.CreatedAt,
		row.CreatedBy, row.MaxRetries, row.OnFail, nullIfEmpty(row.NextRun),
		row.JobType, row.JobPayload,
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

// ListAllAgentSchedules returns every agent_schedules row across every
// agent, ordered the same way ListAgentSchedules orders its per-agent
// results (priority DESC, created_at ASC). Backs the unfiltered case of
// GET /api/schedules (TASKS/scheduling/09-operator-http-api.md) -- the
// operator HTTP surface is the first caller that needs a cross-agent view;
// every other existing caller of this table (durable_wake.go, the
// go-scheduler StoreAdapter, the reflex hook, managed_durable_configs.go)
// is agent-scoped by construction and has no need for it. Deliberately not
// added to the AgentStateStore interface above: that interface exists to
// let a future per-agent-file backend swap in for the per-agent state
// tables, and "list every agent's schedules in one call" is not a
// per-agent-state concept that backend would need to reason about --
// it's a plain operator-surface convenience specific to the central DB.
func (s *Store) ListAllAgentSchedules(ctx context.Context) ([]AgentSchedule, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentScheduleColumns+`
		 FROM agent_schedules
		 ORDER BY priority DESC, created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all agent_schedules: %w", err)
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

// ListDueAgentSchedules returns up to limit active agent_schedules rows
// whose next_run is set (NOT NULL) and at or before now, ordered by
// next_run ascending (earliest-due first). This is the low-level query
// backing TASKS/scheduling/02-store-adapter.md's gosched.Store.
// ListDueSchedules -- the neutral-type conversion (including per-job-type
// Payload construction) lives in internal/scheduler's StoreAdapter, not
// here; this method only knows about agent_schedules' own row shape.
//
// A NULL next_run (an "unscheduled" row -- see AgentSchedule.NextRun's own
// doc comment) is never due by construction and is excluded here rather
// than coerced to any placeholder, matching go-scheduler's own "zero
// NextRun is skipped" convention (libs/go-scheduler/scheduler.go:28).
func (s *Store) ListDueAgentSchedules(ctx context.Context, now time.Time, limit int) ([]AgentSchedule, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentScheduleColumns+`
		 FROM agent_schedules
		 WHERE status = ? AND next_run IS NOT NULL AND next_run <= ?
		 ORDER BY next_run ASC
		 LIMIT ?`,
		ScheduleStatusActive, now.UTC().Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due agent_schedules: %w", err)
	}
	defer rows.Close()
	out := make([]AgentSchedule, 0)
	for rows.Next() {
		var sch AgentSchedule
		if err := scanAgentSchedule(rows, &sch); err != nil {
			return nil, fmt.Errorf("scan due agent_schedules: %w", err)
		}
		out = append(out, sch)
	}
	return out, rows.Err()
}

// ClaimAgentScheduleRun is the compare-and-set the go-scheduler engine's
// duplicate-dispatch safety rests on: it advances last_fired_at/next_run
// only if the row's *current* next_run still equals expectedNext, and
// reports whether the claim succeeded (RowsAffected()==1). A losing
// concurrent caller gets (false, nil) -- not an error, and no row is
// touched.
//
// Safe by construction under a single-writer connection pool (store.go's
// sqlitekit.OpenSingle, WAL + busy_timeout(5s) + _txlock=immediate on every
// connection) without any additional locking -- see
// docs/engineering/architecture/12-scheduling.md's "The Store adapter"
// section for the full argument. last_fired_at is the pre-existing column
// migration 127 repurposed for this (not a new "last_run" column) per that
// migration's own Work Log.
func (s *Store) ClaimAgentScheduleRun(ctx context.Context, id string, expectedNext, lastRun, nextRun time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_schedules
		    SET last_fired_at = ?, next_run = ?
		  WHERE id = ? AND next_run = ?`,
		lastRun.UTC().Format(time.RFC3339), nextRun.UTC().Format(time.RFC3339),
		id, expectedNext.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("claim agent_schedules run %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim agent_schedules run %s rows affected: %w", id, err)
	}
	return n == 1, nil
}

// SetAgentScheduleNextRun resets next_run unconditionally -- no CAS check,
// unlike ClaimAgentScheduleRun. go-scheduler's engine calls the matching
// Store method to roll a schedule back to its pre-claim next_run after a
// failed Runner.Enqueue, so the same firing becomes due again on the next
// tick (libs/go-scheduler/engine.go's tick()).
func (s *Store) SetAgentScheduleNextRun(ctx context.Context, id string, nextRun time.Time) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_schedules SET next_run = ? WHERE id = ?`,
		nextRun.UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("set agent_schedules next_run %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set agent_schedules next_run %s rows affected: %w", id, err)
	}
	if n == 0 {
		return ErrAgentScheduleNotFound
	}
	return nil
}

// backfillScheduleNextRun computes agent_schedules.next_run for every
// active cron/one_shot row that doesn't already have one -- covers rows
// that pre-date migration 127's next_run column (which the migration's
// own SQL deliberately leaves NULL for every row; see 127's Up doc
// comment for why). Called once per process, from New() immediately after
// migrate() succeeds -- see store.go.
//
// Idempotent by construction: only rows with next_run IS NULL are
// touched, so this is a safe no-op on every boot after the one that
// actually needed to backfill something. TASKS/scheduling/02-store-
// adapter.md's Store adapter (ClaimAndUpdateScheduleRun/SetScheduleNextRun)
// keeps next_run populated going forward once a schedule has fired at
// least once under the new engine, so this function's job is strictly
// "cover the gap between migration 127 landing and the new engine's first
// real tick," not an ongoing recomputation path.
//
// cron rows: computed via cron.ParseStandard(schedule_spec).Next(now) --
// the exact same parsing go-scheduler.NextRun itself wraps
// (libs/go-scheduler/scheduler.go:85-92). Deliberately hand-called here
// via the already-present robfig/cron/v3 dependency rather than importing
// go-scheduler directly -- introducing that dependency to Nanite's go.mod
// is 02-store-adapter.md's job, not this migration's. A malformed
// schedule_spec (should not happen for the one real production row, which
// is a plain `0 3 * * *`, but defensively for any other DB this migration
// might run against) falls back to "due now" rather than leaving next_run
// NULL -- NULL means "unscheduled, permanently skipped" to go-scheduler,
// which would silently and permanently disable the row; "due now" costs
// at most one off-schedule immediate fire, which is recoverable, versus a
// silent, permanent loss of due-ness, which is what this whole backfill
// exists to prevent.
//
// one_shot rows: confirmed by reading wakeScheduleDue (internal/service/
// durable_wake.go) before writing this -- a one_shot row today has no
// independent target-time encoding in schedule_spec at all. It's
// evaluated as due immediately and continuously until FiredCount stops
// being 0; there is no "wait until time X" semantics to preserve.
// Backfilling next_run=now matches that real, existing "due now" behavior
// rather than inventing a delayed target time the current data model
// never had.
func (s *Store) backfillScheduleNextRun(ctx context.Context, now time.Time) error {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, schedule_kind, schedule_spec FROM agent_schedules
		 WHERE status = 'active' AND next_run IS NULL
		   AND schedule_kind IN ('cron', 'one_shot')`,
	)
	if err != nil {
		return fmt.Errorf("backfill agent_schedules next_run: query: %w", err)
	}
	type candidate struct {
		id, kind, spec string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.kind, &c.spec); err != nil {
			rows.Close()
			return fmt.Errorf("backfill agent_schedules next_run: scan: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backfill agent_schedules next_run: rows: %w", err)
	}
	rows.Close()

	for _, c := range candidates {
		next := ComputeAgentScheduleNextRun(c.kind, c.spec, now)
		if next.IsZero() {
			continue
		}
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE agent_schedules SET next_run = ? WHERE id = ? AND next_run IS NULL`,
			next.Format(time.RFC3339), c.id,
		); err != nil {
			return fmt.Errorf("backfill agent_schedules next_run: update %s: %w", c.id, err)
		}
	}
	return nil
}

// ComputeAgentScheduleNextRun computes the next-fire time for a cron/
// one_shot schedule given its kind/spec, as of now. Factored out of
// backfillScheduleNextRun (above) so a schedule producer that inserts a
// genuinely new row mid-process (managed_durable_configs.go's
// syncManagedDurableAgentSchedule is the one real caller today) can compute
// a usable next_run at insert time, instead of leaving it NULL until the
// next process restart's backfillScheduleNextRun pass — see that function's
// call site for the full finding (TASKS/scheduling/
// 05-engine-wiring-and-full-replace.md's Work Log) on why a NULL next_run
// on a freshly-synced row is a real, not hypothetical, gap: backfillScheduleNextRun
// only runs once, at Store.New() boot time, strictly before
// SyncManagedDurableAgentConfigs (container.go) ever gets a chance to
// upsert a schedule row for the first time.
//
// one_shot rows: no independent target-time encoding exists in spec today
// (see backfillScheduleNextRun's own doc comment) — "now" matches the
// existing due-immediately-and-continuously-until-fired semantics.
//
// cron rows: cron.ParseStandard(spec).Next(now) — the exact parsing
// go-scheduler.NextRun itself wraps (libs/go-scheduler/scheduler.go:85-92).
// A malformed spec falls back to "due now" rather than returning the zero
// time, matching backfillScheduleNextRun's own defensive rationale: NULL/
// zero next_run means "permanently unscheduled" to go-scheduler, a worse
// failure mode than one off-schedule immediate fire.
//
// An unrecognized kind returns the zero time — the caller's cue to leave
// next_run unset (NULL) rather than inventing a due time for a kind this
// function doesn't understand.
func ComputeAgentScheduleNextRun(kind, spec string, now time.Time) time.Time {
	nowUTC := now.UTC()
	switch kind {
	case ScheduleKindOneShot:
		return nowUTC
	case ScheduleKindCron:
		parsed, err := cron.ParseStandard(strings.TrimSpace(spec))
		if err != nil {
			return nowUTC
		}
		return parsed.Next(nowUTC)
	default:
		return time.Time{}
	}
}
