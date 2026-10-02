package service

import (
	"context"
	"errors"
	"time"

	svcerr "github.com/hollis-labs/go-svcerr"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/nanite/internal/store"
)

// ScheduleService owns operator CRUD on agent_schedules rows. It depends only
// on the store: internal/scheduler imports this package, so the scheduler
// engine that fires these rows (Container.Engine) is never imported here.
//
// Create and Patch keep next_run coherent with the row: a row whose next_run
// is NULL is never picked up by the engine's due-schedule query, so it would
// silently never fire.
type ScheduleService struct {
	store *store.Store
}

func NewScheduleService(st *store.Store) *ScheduleService {
	return &ScheduleService{store: st}
}

// ScheduleWriteError wraps a caller-correctable rejection: a row that fails
// store.ValidateAgentSchedule, or an empty status or on_fail in a patch.
// Database write failures use a typed internal error instead.
type ScheduleWriteError struct {
	Err error
}

func (e *ScheduleWriteError) Error() string { return e.Err.Error() }
func (e *ScheduleWriteError) Unwrap() error { return e.Err }

// SchedulePatch is a partial update; a nil field leaves the column alone.
//
// agent_id, schedule_kind and job_type are deliberately not patchable:
// changing a schedule's owner is a delete-and-recreate; cron vs one_shot
// changes how next_run is interpreted and would have to change together with
// schedule_spec; and job_payload's shape is determined by job_type, so
// changing the type alone would produce a row that validates but fails at
// dispatch. job_payload is patchable on its own, since a same-type payload
// edit keeps the dispatch contract. next_run, id, fired_count,
// last_fired_at, created_at and created_by are engine- or bookkeeping-owned.
type SchedulePatch struct {
	Name         *string
	SessionID    *string
	ScheduleSpec *string
	Body         *string
	Priority     *int64
	Status       *string
	ExpiresAt    *string
	MaxRetries   *int64
	OnFail       *string
	JobPayload   *string
}

// List returns one agent's schedules, or every schedule when agentID is "".
func (s *ScheduleService) List(ctx context.Context, agentID string) ([]store.AgentSchedule, error) {
	if agentID != "" {
		return s.store.ListAgentSchedules(ctx, agentID)
	}
	return s.store.ListAllAgentSchedules(ctx)
}

// Get returns store.ErrAgentScheduleNotFound when absent.
func (s *ScheduleService) Get(ctx context.Context, id string) (*store.AgentSchedule, error) {
	return s.store.GetAgentSchedule(ctx, id)
}

// Create assigns row a new id, validates it, computes its first next_run and
// inserts it. Validation rejections are *ScheduleWriteError; the
// store applies its usual defaults to unset optional columns.
func (s *ScheduleService) Create(ctx context.Context, row store.AgentSchedule) (*store.AgentSchedule, error) {
	row.ID = "sched-" + ulid.Make().String()
	if err := store.ValidateAgentSchedule(row); err != nil {
		return nil, &ScheduleWriteError{Err: err}
	}
	if next := store.ComputeAgentScheduleNextRun(row.ScheduleKind, row.ScheduleSpec, time.Now()); !next.IsZero() {
		row.NextRun = next.UTC().Format(time.RFC3339)
	}
	if err := s.store.InsertAgentSchedule(ctx, row); err != nil {
		return nil, svcerr.Wrap(err, svcerr.CodeInternal, "failed to create schedule")
	}
	return s.store.GetAgentSchedule(ctx, row.ID)
}

// Patch copies the current row, applies p, validates the result and writes
// it back (InsertAgentSchedule is an upsert keyed on id, so untouched columns
// round-trip). next_run is recomputed when schedule_spec changes, or when
// status moves into active from anything else — a reactivated row with a
// stale or NULL next_run would otherwise never fire or misfire at once. It
// returns store.ErrAgentScheduleNotFound when absent.
func (s *ScheduleService) Patch(ctx context.Context, id string, p SchedulePatch) (*store.AgentSchedule, error) {
	current, err := s.store.GetAgentSchedule(ctx, id)
	if err != nil {
		return nil, err
	}
	updated := *current
	recomputeNextRun := false

	if p.Name != nil {
		updated.Name = *p.Name
	}
	if p.SessionID != nil {
		updated.SessionID = *p.SessionID
	}
	if p.ScheduleSpec != nil {
		updated.ScheduleSpec = *p.ScheduleSpec
		recomputeNextRun = true
	}
	if p.Body != nil {
		updated.Body = *p.Body
	}
	if p.Priority != nil {
		updated.Priority = *p.Priority
	}
	if p.Status != nil {
		if *p.Status == "" {
			return nil, &ScheduleWriteError{Err: errors.New("status must not be empty")}
		}
		if *p.Status == store.ScheduleStatusActive && current.Status != store.ScheduleStatusActive {
			recomputeNextRun = true
		}
		updated.Status = *p.Status
	}
	if p.ExpiresAt != nil {
		updated.ExpiresAt = *p.ExpiresAt
	}
	if p.MaxRetries != nil {
		updated.MaxRetries = *p.MaxRetries
	}
	if p.OnFail != nil {
		if *p.OnFail == "" {
			return nil, &ScheduleWriteError{Err: errors.New("on_fail must not be empty")}
		}
		updated.OnFail = *p.OnFail
	}
	if p.JobPayload != nil {
		updated.JobPayload = *p.JobPayload
	}
	if err := store.ValidateAgentSchedule(updated); err != nil {
		return nil, &ScheduleWriteError{Err: err}
	}

	if recomputeNextRun {
		if next := store.ComputeAgentScheduleNextRun(updated.ScheduleKind, updated.ScheduleSpec, time.Now()); !next.IsZero() {
			updated.NextRun = next.UTC().Format(time.RFC3339)
		}
	}

	if err := s.store.InsertAgentSchedule(ctx, updated); err != nil {
		return nil, svcerr.Wrap(err, svcerr.CodeInternal, "failed to update schedule")
	}
	return s.store.GetAgentSchedule(ctx, id)
}

// Delete returns store.ErrAgentScheduleNotFound when absent.
func (s *ScheduleService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteAgentSchedule(ctx, id)
}

// InsertPrepared preserves the self-tool's identity, retry policy and computed
// next_run. Unlike operator Create, it does not replace the caller-assigned ID.
func (s *ScheduleService) InsertPrepared(ctx context.Context, row store.AgentSchedule) error {
	return s.store.InsertAgentSchedule(ctx, row)
}
