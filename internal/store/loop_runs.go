package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrLoopRunNotFound is returned when a loop_runs row cannot be located.
var ErrLoopRunNotFound = errors.New("loop run not found")

// LoopRun status constants -- the CHECK constraint on loop_runs.status
// (migration 138) keeps DB rows aligned with these values; mismatches
// surface as INSERT/UPDATE errors, and validateLoopRunStatus (below)
// rejects them before they ever reach the DB. Per docs/engineering/
// architecture/21-loops.md's Decision 1, a LoopRun is a peer entity to
// WorkflowRun, not a WorkflowRun itself -- these six values are this
// table's own lifecycle, not borrowed from workflow_runs.status.
const (
	LoopRunStatusRunning             = "running"
	LoopRunStatusCompleted           = "completed"
	LoopRunStatusFailed              = "failed"
	LoopRunStatusCancelled           = "cancelled"
	LoopRunStatusWaitingOnGate       = "waiting_on_gate"
	LoopRunStatusWaitingOnEscalation = "waiting_on_escalation"
)

// LoopRunActiveStatuses is the "still in play" status subset
// TASKS/loops/03-loop-runs-schema.md's Context section names as the one-
// active-LoopRun-per-goal check's filter set: task 10's launcher is
// expected to call
// ListLoopRuns(ctx, LoopRunFilter{GoalID: goalID, Statuses: LoopRunActiveStatuses})
// before launching a new LoopRun against a goal already carrying one of
// these. Exported here (rather than left for task 10 to redeclare) so both
// that check and any other future caller share one definition of "active."
// Not itself an enforcement mechanism -- see this file's package doc
// comment on ListLoopRuns for why this is an application-level check, not a
// DB constraint.
var LoopRunActiveStatuses = []string{
	LoopRunStatusRunning,
	LoopRunStatusWaitingOnGate,
	LoopRunStatusWaitingOnEscalation,
}

// validLoopRunStatuses is the enum validateLoopRunStatus checks against --
// same Go-layer-validation-over-a-DB-CHECK-constraint approach goals.go's
// validateGoalStatus and agent_schedules.go's UpdateAgentScheduleStatus
// already use.
var validLoopRunStatuses = map[string]bool{
	LoopRunStatusRunning:             true,
	LoopRunStatusCompleted:           true,
	LoopRunStatusFailed:              true,
	LoopRunStatusCancelled:           true,
	LoopRunStatusWaitingOnGate:       true,
	LoopRunStatusWaitingOnEscalation: true,
}

// validateLoopRunStatus checks status against the enum the DB CHECK
// constraint (migration 138) also enforces. A bare enum-membership check is
// deliberately all this does -- real transition-legality enforcement is a
// later task's job (the continuation policy engine, 21-loops.md's "decide"
// function) once something is actually driving transitions, not this
// storage layer.
func validateLoopRunStatus(status string) error {
	if !validLoopRunStatuses[status] {
		return fmt.Errorf("status %q invalid: must be one of running, completed, failed, cancelled, waiting_on_gate, waiting_on_escalation", status)
	}
	return nil
}

// on_exhausted policy constants -- Budget.OnExhausted's enum,
// validateBudget enforces. Narrower than Scheduling's retry/disable/notify
// vocabulary deliberately: retry doesn't apply once the *total* loop budget
// is exhausted (there's nothing left to retry into), and disable/notify are
// Schedule-specific concepts with no LoopRun equivalent. See
// TASKS/loops/03-loop-runs-schema.md's Context section for the full
// reasoning and the "default escalate" call.
const (
	LoopRunOnExhaustedEscalate = "escalate"
	LoopRunOnExhaustedFail     = "fail"
)

// Budget is loop_runs.budget_json's decoded shape --
// docs/engineering/architecture/21-loops.md's illustrative
// `{ max_iterations, max_failures, max_no_progress_iterations }` (plus
// max_runtime, named in that doc's schema ledger section as
// max_runtime_seconds here), with OnExhausted folded in as one more policy
// knob in the same blob rather than split into its own column -- see
// migration 138's own doc comment for why.
type Budget struct {
	MaxIterations           int    `json:"max_iterations"`
	MaxFailures             int    `json:"max_failures"`
	MaxRuntimeSeconds       int    `json:"max_runtime_seconds"`
	MaxNoProgressIterations int    `json:"max_no_progress_iterations"`
	OnExhausted             string `json:"on_exhausted"`
}

// validateBudget enforces Budget's two invariants: OnExhausted must be one
// of the two LoopRunOnExhausted* values, and every max_* field must be
// non-negative (a negative budget has no meaningful interpretation).
// Invoked from SetBudget (the typed accessor), not from CreateLoopRun --
// mirrors goals.go's split between validateGoalStatus (a top-level column)
// and the JSON sub-structure accessors (which validate shape/encodability
// but, for Goal, have no enum to check). Budget is the first JSON
// sub-structure in this package with a real enum inside it, so its
// accessor is the one that owns the check.
func validateBudget(b Budget) error {
	if b.OnExhausted != LoopRunOnExhaustedEscalate && b.OnExhausted != LoopRunOnExhaustedFail {
		return fmt.Errorf("budget on_exhausted %q invalid: must be %q or %q", b.OnExhausted, LoopRunOnExhaustedEscalate, LoopRunOnExhaustedFail)
	}
	if b.MaxIterations < 0 || b.MaxFailures < 0 || b.MaxRuntimeSeconds < 0 || b.MaxNoProgressIterations < 0 {
		return fmt.Errorf("budget: max_iterations, max_failures, max_runtime_seconds, and max_no_progress_iterations must all be non-negative")
	}
	return nil
}

// LoopRun is one row in the loop_runs table -- a Loop's launched execution
// instance. Per docs/engineering/architecture/21-loops.md's Decision 1,
// this is a new PEER entity to WorkflowRun, not a WorkflowRun itself: its
// id is its own, never keyed by or aliased to workflow_runs.id. LoopRun
// orchestrates a *sequence* of ordinary WorkflowRuns (task 04's
// loop_run_iterations records each one) rather than owning a second
// execution engine.
//
// Storage only: this type and its CRUD below don't launch iterations,
// evaluate progress, or run the continuation-policy "decide" function --
// TASKS/loops' later tasks (07/08/10) own that logic.
type LoopRun struct {
	ID string `json:"id"`

	// GoalID is the LoopRun's one required target-state pointer --
	// "a loop_runs row always has exactly one goal_id" (21-loops.md's
	// Decision 2). REFERENCES goals(id), FK-enforced (migration 138).
	GoalID string `json:"goal_id"`

	// DefinitionName is the initial iteration's WorkflowDefinition name (or
	// a preset name, e.g. "ralph") -- 21-loops.md's illustrative
	// `definition_name: implementation_iteration`. A REPLAN/REARCHITECT
	// decision can launch a later iteration under a *different* definition;
	// this column records only the LoopRun's starting point, not every
	// iteration's actual definition (that's loop_run_iterations' job,
	// task 04).
	DefinitionName string `json:"definition_name"`

	// Status is the LoopRun's lifecycle state -- see the LoopRunStatus*
	// constants above. CreateLoopRun defaults this to LoopRunStatusRunning
	// when unset. Mutated going forward via UpdateLoopRunStatus (the narrow
	// updater), not a full-row replacer -- there isn't one; see this file's
	// "What to do" scope note at the top of the CRUD section below.
	Status string `json:"status"`

	// CurrentIteration is the 0-based count of iterations launched so far --
	// 21-loops.md's illustrative `current_iteration: 3`. Mutated via
	// BumpLoopRunIteration (narrow, increments by 1), not this full struct.
	CurrentIteration int `json:"current_iteration"`

	// BudgetJSON is Budget's encoded form -- use the Budget()/SetBudget()
	// typed accessor pair below rather than handling the raw JSON at call
	// sites, mirrors goals.go's SlotsJSON/Slots()/SetSlots()-style
	// convention.
	BudgetJSON string `json:"budget_json"`

	// ContinuationPolicyJSON is left an untyped JSON-blob placeholder here
	// -- task 07 defines and consumes its real shape without this table
	// needing to be revisited, the same "don't guess a shape another task
	// owns" discipline TASKS/teams/01 applied to authority_json/
	// routing_json.
	ContinuationPolicyJSON string `json:"continuation_policy_json"`

	// NoProgressStreak is the consecutive-no-progress iteration counter the
	// continuation policy's `no_progress_streak >= budget.
	// max_no_progress_iterations` check (21-loops.md, "Continuation
	// policy") reads. Mutated via UpdateLoopRunNoProgressStreak (narrow,
	// sets the value -- task 08 calls this after every iteration's
	// evaluation with the freshly computed streak, not an increment).
	NoProgressStreak int `json:"no_progress_streak"`

	StartedAt string `json:"started_at"`
	UpdatedAt string `json:"updated_at"`

	// CompletedAt is set when this LoopRun reaches a terminal status
	// (completed/failed/cancelled -- NOT waiting_on_gate/
	// waiting_on_escalation, which are paused, not terminal, states) via
	// UpdateLoopRunStatus's completedAt parameter. Empty until then.
	CompletedAt string `json:"completed_at,omitempty"`
}

// Budget decodes LoopRun.BudgetJSON into Budget. Returns a zero-value
// Budget (all zero ints, empty OnExhausted) for an empty/unset BudgetJSON
// -- mirrors goals.go's "absent means legitimately empty" convention, with
// no enum default applied at decode time (SetBudget, not this getter, is
// where the "default escalate" convention lives).
func (lr *LoopRun) Budget() (Budget, error) {
	return decodeLoopRunBudget(lr.BudgetJSON)
}

// SetBudget validates b (defaulting an empty OnExhausted to
// LoopRunOnExhaustedEscalate first, per this design's "default escalate"
// call -- TASKS/loops/03-loop-runs-schema.md's Context section) via
// validateBudget, then encodes it into LoopRun.BudgetJSON. Returns the
// validation error, unencoded, on failure -- LoopRun.BudgetJSON is left
// untouched.
func (lr *LoopRun) SetBudget(b Budget) error {
	if b.OnExhausted == "" {
		b.OnExhausted = LoopRunOnExhaustedEscalate
	}
	if err := validateBudget(b); err != nil {
		return err
	}
	encoded, err := encodeLoopRunBudget(b)
	if err != nil {
		return err
	}
	lr.BudgetJSON = encoded
	return nil
}

func decodeLoopRunBudget(raw string) (Budget, error) {
	if raw == "" || raw == "{}" {
		return Budget{}, nil
	}
	var b Budget
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return Budget{}, fmt.Errorf("decode loop_run budget: %w", err)
	}
	return b, nil
}

func encodeLoopRunBudget(b Budget) (string, error) {
	encoded, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("encode loop_run budget: %w", err)
	}
	return string(encoded), nil
}

const loopRunColumns = `id, goal_id, definition_name, status, current_iteration,
       budget_json, continuation_policy_json, no_progress_streak,
       started_at, updated_at, COALESCE(completed_at,'')`

func scanLoopRun(scanner interface{ Scan(...any) error }, lr *LoopRun) error {
	return scanner.Scan(
		&lr.ID, &lr.GoalID, &lr.DefinitionName, &lr.Status, &lr.CurrentIteration,
		&lr.BudgetJSON, &lr.ContinuationPolicyJSON, &lr.NoProgressStreak,
		&lr.StartedAt, &lr.UpdatedAt, &lr.CompletedAt,
	)
}

// CreateLoopRun inserts a new loop_runs row. Generates an ID via uuid.New()
// if lr.ID is empty, defaults Status to LoopRunStatusRunning if unset
// (validated via validateLoopRunStatus before insert), and defaults
// BudgetJSON/ContinuationPolicyJSON to "{}" if left empty (matching the
// column DEFAULTs migration 138 sets, replicated here because every value
// is passed explicitly in this INSERT so SQLite's column DEFAULT never
// actually applies -- same trade-off goals.go's CreateGoal documents for
// its own JSON defaults). GoalID and DefinitionName are required.
func (s *Store) CreateLoopRun(ctx context.Context, lr *LoopRun) error {
	if lr.GoalID == "" {
		return fmt.Errorf("create loop_run: goal_id is required")
	}
	if lr.DefinitionName == "" {
		return fmt.Errorf("create loop_run: definition_name is required")
	}
	if lr.ID == "" {
		lr.ID = uuid.New().String()
	}
	if lr.Status == "" {
		lr.Status = LoopRunStatusRunning
	}
	if err := validateLoopRunStatus(lr.Status); err != nil {
		return fmt.Errorf("create loop_run: %w", err)
	}
	if lr.BudgetJSON == "" {
		lr.BudgetJSON = "{}"
	}
	if lr.ContinuationPolicyJSON == "" {
		lr.ContinuationPolicyJSON = "{}"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO loop_runs
		    (id, goal_id, definition_name, status, current_iteration,
		     budget_json, continuation_policy_json, no_progress_streak,
		     started_at, updated_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lr.ID, lr.GoalID, lr.DefinitionName, lr.Status, lr.CurrentIteration,
		lr.BudgetJSON, lr.ContinuationPolicyJSON, lr.NoProgressStreak,
		now, now, nullIfEmpty(lr.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("create loop_run: %w", err)
	}
	lr.StartedAt = now
	lr.UpdatedAt = now
	return nil
}

// GetLoopRun returns a loop_runs row by id, or ErrLoopRunNotFound.
func (s *Store) GetLoopRun(ctx context.Context, id string) (*LoopRun, error) {
	var lr LoopRun
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+loopRunColumns+` FROM loop_runs WHERE id = ?`, id,
	)
	if err := scanLoopRun(row, &lr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLoopRunNotFound
		}
		return nil, fmt.Errorf("get loop_run: %w", err)
	}
	return &lr, nil
}

// LoopRunFilter narrows ListLoopRuns -- an empty field means "no filter on
// this column." GoalID filters to one goal; Statuses (when non-empty)
// filters to status IN (...) -- task 10's one-active-run-per-goal check is
// the intended caller of the combination
// LoopRunFilter{GoalID: goalID, Statuses: LoopRunActiveStatuses}, per
// TASKS/loops/03-loop-runs-schema.md's Context section. One active LoopRun
// per goal_id is enforced by that later task's launcher reading this
// filter's result, not by anything in this file -- a partial unique index
// on SQLite would need WHERE status IN (...), valid SQLite but brittle
// against future status additions; a Go-layer check is simpler.
type LoopRunFilter struct {
	GoalID   string
	Statuses []string
}

// ListLoopRuns returns loop_runs rows matching filter, ordered by
// started_at ascending. An empty LoopRunFilter returns every row.
func (s *Store) ListLoopRuns(ctx context.Context, filter LoopRunFilter) ([]LoopRun, error) {
	query := `SELECT ` + loopRunColumns + ` FROM loop_runs WHERE 1=1`
	var args []any
	if filter.GoalID != "" {
		query += ` AND goal_id = ?`
		args = append(args, filter.GoalID)
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, st := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, st)
		}
		query += ` AND status IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY started_at ASC`

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list loop_runs: %w", err)
	}
	defer rows.Close()
	out := make([]LoopRun, 0)
	for rows.Next() {
		var lr LoopRun
		if err := scanLoopRun(rows, &lr); err != nil {
			return nil, fmt.Errorf("scan loop_run: %w", err)
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}

// UpdateLoopRunStatus sets the status column (validated via
// validateLoopRunStatus) and bumps updated_at to now. Narrow updater,
// mirrors agent_schedules.go's UpdateAgentScheduleStatus. completedAt is
// optional (nil means "leave completed_at untouched") -- unlike goals.go's
// UpdateGoalStatus, which computes whether to set completed_at itself from
// a fixed terminal-status set, this caller (the continuation policy engine,
// task 08) is the thing that already knows whether the transition is
// terminal, so it passes completedAt explicitly rather than this storage
// layer re-deriving it. Returns ErrLoopRunNotFound if no row matched.
func (s *Store) UpdateLoopRunStatus(ctx context.Context, id, status string, completedAt *time.Time) error {
	if err := validateLoopRunStatus(status); err != nil {
		return fmt.Errorf("update loop_run status: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var (
		query string
		args  []any
	)
	if completedAt != nil {
		query = `UPDATE loop_runs SET status = ?, updated_at = ?, completed_at = ? WHERE id = ?`
		args = []any{status, now, completedAt.UTC().Format(time.RFC3339), id}
	} else {
		query = `UPDATE loop_runs SET status = ?, updated_at = ? WHERE id = ?`
		args = []any{status, now, id}
	}

	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update loop_run status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update loop_run status rows affected: %w", err)
	}
	if n == 0 {
		return ErrLoopRunNotFound
	}
	return nil
}

// BumpLoopRunIteration increments current_iteration by 1 and bumps
// updated_at to now. Narrow updater, mirrors agent_schedules.go's
// BumpAgentScheduleFireCount. Returns ErrLoopRunNotFound if no row matched.
func (s *Store) BumpLoopRunIteration(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE loop_runs
		    SET current_iteration = current_iteration + 1,
		        updated_at = ?
		  WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("bump loop_run iteration: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("bump loop_run iteration rows affected: %w", err)
	}
	if n == 0 {
		return ErrLoopRunNotFound
	}
	return nil
}

// UpdateLoopRunNoProgressStreak sets no_progress_streak to streak (a
// direct set, not an increment -- task 08 calls this after every
// iteration's evaluation with the freshly computed streak value) and bumps
// updated_at to now. Narrow updater. Rejects a negative streak before
// issuing the UPDATE. Returns ErrLoopRunNotFound if no row matched.
func (s *Store) UpdateLoopRunNoProgressStreak(ctx context.Context, id string, streak int) error {
	if streak < 0 {
		return fmt.Errorf("update loop_run no_progress_streak: streak must be non-negative, got %d", streak)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE loop_runs SET no_progress_streak = ?, updated_at = ? WHERE id = ?`,
		streak, now, id,
	)
	if err != nil {
		return fmt.Errorf("update loop_run no_progress_streak: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update loop_run no_progress_streak rows affected: %w", err)
	}
	if n == 0 {
		return ErrLoopRunNotFound
	}
	return nil
}

// UpdateLoopRunDefinitionName sets the definition_name column and bumps
// updated_at to now. Narrow updater, mirrors this file's other narrow
// updaters (BumpLoopRunIteration, UpdateLoopRunNoProgressStreak). Added by
// TASKS/loops/10-loop-launcher-and-api.md for its escalation-resolution
// endpoint's "supply a replan" override (docs/engineering/architecture/
// 21-loops.md's "Human resolution of waiting_on_escalation" trigger
// surface): an operator resolving a waiting_on_escalation LoopRun can
// supply a revised definition_name for every subsequent iteration --
// LoopEngine.Resume (engine.go) re-fetches this row and passes
// lr.DefinitionName straight into driveIterations, so persisting the new
// value here is sufficient; no other engine change is needed to honor it.
// Returns ErrLoopRunNotFound if no row matched.
func (s *Store) UpdateLoopRunDefinitionName(ctx context.Context, id, definitionName string) error {
	if definitionName == "" {
		return fmt.Errorf("update loop_run definition_name: definition_name is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE loop_runs SET definition_name = ?, updated_at = ? WHERE id = ?`,
		definitionName, now, id,
	)
	if err != nil {
		return fmt.Errorf("update loop_run definition_name: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update loop_run definition_name rows affected: %w", err)
	}
	if n == 0 {
		return ErrLoopRunNotFound
	}
	return nil
}

// DeleteLoopRun removes a loop_runs row by id. Returns ErrLoopRunNotFound
// if no row matched. Storage-only: this does not check for or cascade into
// any loop_run_iterations row referencing this LoopRun (loop_run_iterations
// is task 04's table, not added here).
func (s *Store) DeleteLoopRun(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM loop_runs WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete loop_run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete loop_run rows affected: %w", err)
	}
	if n == 0 {
		return ErrLoopRunNotFound
	}
	return nil
}
