package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrGoalNotFound is returned when a goals row cannot be located.
var ErrGoalNotFound = errors.New("goal not found")

// Goal status constants -- the CHECK constraint on goals.status (migration
// 135) keeps DB rows aligned with these values; mismatches surface as
// INSERT/UPDATE errors, and validateGoalStatus (below) rejects them before
// they ever reach the DB. Full lifecycle per docs/engineering/architecture/
// 21-loops.md's Decision 2 and GLOSSARY.md's Goal entry: draft/defined are
// pre-launch authoring states, active is the one running state, and
// blocked/satisfied/failed/cancelled/superseded are the terminal-or-paused
// outcomes. Real transition-legality enforcement (e.g. rejecting draft ->
// satisfied directly) is a later task's job (21-loops.md's continuation
// policy engine) -- this package only enforces enum membership.
const (
	GoalStatusDraft      = "draft"
	GoalStatusDefined    = "defined"
	GoalStatusActive     = "active"
	GoalStatusBlocked    = "blocked"
	GoalStatusSatisfied  = "satisfied"
	GoalStatusFailed     = "failed"
	GoalStatusCancelled  = "cancelled"
	GoalStatusSuperseded = "superseded"
)

// goalTerminalStatuses is the subset of the lifecycle UpdateGoalStatus
// treats as "completed" -- it sets completed_at on transition into any of
// these. blocked is deliberately excluded: it is a paused, not terminal,
// state (a blocked Goal can still resume toward active).
var goalTerminalStatuses = map[string]bool{
	GoalStatusSatisfied:  true,
	GoalStatusFailed:     true,
	GoalStatusCancelled:  true,
	GoalStatusSuperseded: true,
}

// validGoalStatuses is the enum validateGoalStatus checks against -- same
// Go-layer-validation-over-a-DB-CHECK-constraint approach agent_schedules.go's
// UpdateAgentScheduleStatus already uses for schedule_kind/status.
var validGoalStatuses = map[string]bool{
	GoalStatusDraft:      true,
	GoalStatusDefined:    true,
	GoalStatusActive:     true,
	GoalStatusBlocked:    true,
	GoalStatusSatisfied:  true,
	GoalStatusFailed:     true,
	GoalStatusCancelled:  true,
	GoalStatusSuperseded: true,
}

// validateGoalStatus checks status against the enum the DB CHECK constraint
// (migration 135) also enforces. A bare enum-membership check is
// deliberately all this does -- real transition-legality enforcement (e.g.
// rejecting draft -> satisfied directly) is task 08's job once the loop
// engine, not this storage layer, is the thing driving transitions
// (TASKS/loops/01-goals-schema.md's "What to do" §2, last bullet).
func validateGoalStatus(status string) error {
	if !validGoalStatuses[status] {
		return fmt.Errorf("status %q invalid: must be one of draft, defined, active, blocked, satisfied, failed, cancelled, superseded", status)
	}
	return nil
}

// Goal is one row in the goals table -- a first-class, persisted target
// state with its own lifecycle, independent of any one execution. See
// docs/engineering/architecture/21-loops.md's Decision 2 and GLOSSARY.md's
// Goal entry for the full design intent this type implements.
//
// Storage only: this type and its CRUD below don't launch a Loop, walk
// parent_goal_id decomposition, or compute goal_met from goal_evidence --
// TASKS/loops' later tasks (07/08/10) own that logic. A LoopLaunchRequest
// can accept either an existing goal_id or an inline goal spec that gets
// upserted into this table first (task 10) -- not this task's job to build,
// just to keep this schema's shape compatible with every field the design
// doc's illustrative `goal:` YAML names.
type Goal struct {
	ID string `json:"id"`

	// ParentGoalID supports 21-loops.md's §16 decomposition (one goal
	// recomputing its own subgoals as barriers are discovered) -- a
	// self-referencing FK into this same table. Empty means "no parent"
	// (root goal). No engine walks this yet.
	ParentGoalID string `json:"parent_goal_id,omitempty"`

	// Intent is the goal's human-readable statement of what it's for
	// (21-loops.md's illustrative `intent: "Implement durable loop
	// execution in Nanite"`). Required.
	Intent string `json:"intent"`

	// DesiredStateJSON / ConstraintsJSON / AcceptanceCriteriaJSON /
	// InvariantsJSON are JSON-encoded []string, matching 21-loops.md's
	// illustrative shape (plain string lists for all four). Use the typed
	// accessor pairs below (DesiredState/SetDesiredState, etc.) instead of
	// handling the raw JSON at call sites -- mirrors teams.go's
	// SlotsJSON/Slots()/SetSlots() convention.
	DesiredStateJSON       string `json:"desired_state_json"`
	ConstraintsJSON        string `json:"constraints_json"`
	AcceptanceCriteriaJSON string `json:"acceptance_criteria_json"`
	InvariantsJSON         string `json:"invariants_json"`

	// Priority and Scope are free-text placeholders -- 21-loops.md's
	// illustrative schema names both columns but never fixes their vocabulary
	// (see that doc's "What this session did not decide"). No CHECK/enum
	// here; a later task can narrow these once a real consumer needs to.
	Priority string `json:"priority,omitempty"`
	Scope    string `json:"scope,omitempty"`

	// Status is the goal's lifecycle state -- see the GoalStatus* constants
	// above. CreateGoal defaults this to GoalStatusDraft when unset. Mutated
	// going forward via UpdateGoalStatus (the narrow updater), not UpdateGoal
	// (the full mutable-column replacer) -- see each method's own doc
	// comment for why the two are split.
	Status string `json:"status"`

	// Owner and Source are free-text provenance fields -- who/what this
	// Goal is for (an operator handle, an agent id) and where it came from
	// (a planning session, an architect agent, a LoopLaunchRequest inline
	// spec). Neither is validated against any enum or FK here.
	Owner  string `json:"owner,omitempty"`
	Source string `json:"source,omitempty"`

	CreatedAt string `json:"created_at"`

	// ActivatedAt is set (once) when Status first transitions to
	// GoalStatusActive via UpdateGoalStatus. Empty until then.
	ActivatedAt string `json:"activated_at,omitempty"`

	// CompletedAt is set (once) when Status first transitions into any
	// terminal status (satisfied/failed/cancelled/superseded -- NOT
	// blocked, which is a paused, not terminal, state) via
	// UpdateGoalStatus. Empty until then.
	CompletedAt string `json:"completed_at,omitempty"`
}

// DesiredState decodes Goal.DesiredStateJSON into []string. Returns (nil,
// nil) for an empty/unset DesiredStateJSON -- mirrors teams.go's Slots()
// "absent means legitimately empty" convention.
func (g *Goal) DesiredState() ([]string, error) {
	return decodeGoalStringList(g.DesiredStateJSON)
}

// SetDesiredState encodes items into Goal.DesiredStateJSON. A nil slice
// encodes to "[]", matching the column's own DEFAULT.
func (g *Goal) SetDesiredState(items []string) error {
	encoded, err := encodeGoalStringList(items)
	if err != nil {
		return err
	}
	g.DesiredStateJSON = encoded
	return nil
}

// Constraints decodes Goal.ConstraintsJSON into []string. Returns (nil,
// nil) for an empty/unset ConstraintsJSON.
func (g *Goal) Constraints() ([]string, error) {
	return decodeGoalStringList(g.ConstraintsJSON)
}

// SetConstraints encodes items into Goal.ConstraintsJSON. A nil slice
// encodes to "[]", matching the column's own DEFAULT.
func (g *Goal) SetConstraints(items []string) error {
	encoded, err := encodeGoalStringList(items)
	if err != nil {
		return err
	}
	g.ConstraintsJSON = encoded
	return nil
}

// AcceptanceCriteria decodes Goal.AcceptanceCriteriaJSON into []string.
// Returns (nil, nil) for an empty/unset AcceptanceCriteriaJSON.
func (g *Goal) AcceptanceCriteria() ([]string, error) {
	return decodeGoalStringList(g.AcceptanceCriteriaJSON)
}

// SetAcceptanceCriteria encodes items into Goal.AcceptanceCriteriaJSON. A
// nil slice encodes to "[]", matching the column's own DEFAULT.
func (g *Goal) SetAcceptanceCriteria(items []string) error {
	encoded, err := encodeGoalStringList(items)
	if err != nil {
		return err
	}
	g.AcceptanceCriteriaJSON = encoded
	return nil
}

// Invariants decodes Goal.InvariantsJSON into []string. Returns (nil, nil)
// for an empty/unset InvariantsJSON.
func (g *Goal) Invariants() ([]string, error) {
	return decodeGoalStringList(g.InvariantsJSON)
}

// SetInvariants encodes items into Goal.InvariantsJSON. A nil slice encodes
// to "[]", matching the column's own DEFAULT.
func (g *Goal) SetInvariants(items []string) error {
	encoded, err := encodeGoalStringList(items)
	if err != nil {
		return err
	}
	g.InvariantsJSON = encoded
	return nil
}

// decodeGoalStringList / encodeGoalStringList are the shared decode/encode
// helpers all four Goal JSON sub-structure accessor pairs above use --
// factored out since all four are the identical []string shape (21-loops.md's
// "Illustrative shape" shows plain string lists for desired_state,
// constraints, acceptance_criteria, and -- by the same shape -- invariants).
func decodeGoalStringList(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode goal string list: %w", err)
	}
	return out, nil
}

func encodeGoalStringList(items []string) (string, error) {
	if items == nil {
		items = []string{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("encode goal string list: %w", err)
	}
	return string(b), nil
}

const goalColumns = `id, COALESCE(parent_goal_id,''), intent, desired_state_json,
       constraints_json, acceptance_criteria_json, invariants_json,
       COALESCE(priority,''), COALESCE(scope,''), status, COALESCE(owner,''),
       COALESCE(source,''), created_at, COALESCE(activated_at,''), COALESCE(completed_at,'')`

func scanGoal(scanner interface{ Scan(...any) error }, g *Goal) error {
	return scanner.Scan(
		&g.ID, &g.ParentGoalID, &g.Intent, &g.DesiredStateJSON,
		&g.ConstraintsJSON, &g.AcceptanceCriteriaJSON, &g.InvariantsJSON,
		&g.Priority, &g.Scope, &g.Status, &g.Owner,
		&g.Source, &g.CreatedAt, &g.ActivatedAt, &g.CompletedAt,
	)
}

// CreateGoal inserts a new goals row. Generates an ID via uuid.New() if
// g.ID is empty, defaults every JSON sub-structure column to "[]" if left
// empty (matching the column DEFAULTs migration 135 sets, replicated here
// because every value is passed explicitly in this INSERT so SQLite's
// column DEFAULT never actually applies -- same trade-off teams.go's
// CreateTeam documents for its own JSON defaults), and defaults Status to
// GoalStatusDraft if unset. Status is validated via validateGoalStatus
// before insert.
func (s *Store) CreateGoal(ctx context.Context, g *Goal) error {
	if g.Intent == "" {
		return fmt.Errorf("create goal: intent is required")
	}
	if g.ID == "" {
		g.ID = uuid.New().String()
	}
	if g.DesiredStateJSON == "" {
		g.DesiredStateJSON = "[]"
	}
	if g.ConstraintsJSON == "" {
		g.ConstraintsJSON = "[]"
	}
	if g.AcceptanceCriteriaJSON == "" {
		g.AcceptanceCriteriaJSON = "[]"
	}
	if g.InvariantsJSON == "" {
		g.InvariantsJSON = "[]"
	}
	if g.Status == "" {
		g.Status = GoalStatusDraft
	}
	if err := validateGoalStatus(g.Status); err != nil {
		return fmt.Errorf("create goal: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO goals
		    (id, parent_goal_id, intent, desired_state_json, constraints_json,
		     acceptance_criteria_json, invariants_json, priority, scope,
		     status, owner, source, created_at, activated_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, nullIfEmpty(g.ParentGoalID), g.Intent, g.DesiredStateJSON,
		g.ConstraintsJSON, g.AcceptanceCriteriaJSON, g.InvariantsJSON,
		nullIfEmpty(g.Priority), nullIfEmpty(g.Scope), g.Status,
		nullIfEmpty(g.Owner), nullIfEmpty(g.Source), now,
		nullIfEmpty(g.ActivatedAt), nullIfEmpty(g.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("create goal: %w", err)
	}
	g.CreatedAt = now
	return nil
}

// GetGoal returns a goal by id, or ErrGoalNotFound.
func (s *Store) GetGoal(ctx context.Context, id string) (*Goal, error) {
	var g Goal
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+goalColumns+` FROM goals WHERE id = ?`, id,
	)
	if err := scanGoal(row, &g); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGoalNotFound
		}
		return nil, fmt.Errorf("get goal: %w", err)
	}
	return &g, nil
}

// GoalFilter narrows ListGoals -- an empty field means "no filter on this
// column." TASKS/loops/01-goals-schema.md's "What to do" §2 requires at
// minimum a Status filter and a ParentGoalID filter; both are here. Neither
// distinguishes "unset" from "filter for NULL/root goals" -- a caller
// wanting root-only goals (parent_goal_id IS NULL) has no way to express
// that via this filter yet; not needed by any real caller in this task's
// scope (storage-only, no engine wired).
type GoalFilter struct {
	Status       string
	ParentGoalID string
}

// ListGoals returns goals matching filter, ordered by created_at ascending.
// An empty GoalFilter returns every goal.
func (s *Store) ListGoals(ctx context.Context, filter GoalFilter) ([]Goal, error) {
	query := `SELECT ` + goalColumns + ` FROM goals WHERE 1=1`
	var args []any
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	if filter.ParentGoalID != "" {
		query += ` AND parent_goal_id = ?`
		args = append(args, filter.ParentGoalID)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()
	out := make([]Goal, 0)
	for rows.Next() {
		var g Goal
		if err := scanGoal(rows, &g); err != nil {
			return nil, fmt.Errorf("scan goal: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateGoal updates every mutable "definition" column (parent_goal_id,
// intent, all four JSON sub-structure columns, priority, scope, owner,
// source) by id. Deliberately does NOT touch status, activated_at, or
// completed_at -- those are goals' independently-evolving runtime columns,
// mutated only through UpdateGoalStatus (the narrow updater), mirroring
// agent_schedules.go's UpdateAgentScheduleStatus split from its own row's
// other mutable fields. CreatedAt is immutable after insert. Returns
// ErrGoalNotFound if no row matched.
func (s *Store) UpdateGoal(ctx context.Context, g *Goal) error {
	if g.ID == "" {
		return fmt.Errorf("update goal: id is required")
	}
	if g.Intent == "" {
		return fmt.Errorf("update goal: intent is required")
	}
	if g.DesiredStateJSON == "" {
		g.DesiredStateJSON = "[]"
	}
	if g.ConstraintsJSON == "" {
		g.ConstraintsJSON = "[]"
	}
	if g.AcceptanceCriteriaJSON == "" {
		g.AcceptanceCriteriaJSON = "[]"
	}
	if g.InvariantsJSON == "" {
		g.InvariantsJSON = "[]"
	}

	res, err := s.DB.ExecContext(ctx,
		`UPDATE goals
		    SET parent_goal_id = ?, intent = ?, desired_state_json = ?,
		        constraints_json = ?, acceptance_criteria_json = ?,
		        invariants_json = ?, priority = ?, scope = ?, owner = ?,
		        source = ?
		  WHERE id = ?`,
		nullIfEmpty(g.ParentGoalID), g.Intent, g.DesiredStateJSON,
		g.ConstraintsJSON, g.AcceptanceCriteriaJSON, g.InvariantsJSON,
		nullIfEmpty(g.Priority), nullIfEmpty(g.Scope), nullIfEmpty(g.Owner),
		nullIfEmpty(g.Source), g.ID,
	)
	if err != nil {
		return fmt.Errorf("update goal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update goal rows affected: %w", err)
	}
	if n == 0 {
		return ErrGoalNotFound
	}
	return nil
}

// UpdateGoalStatus sets the status column, validated via validateGoalStatus.
// Narrow updater, mirrors agent_schedules.go's UpdateAgentScheduleStatus.
// Sets activated_at (once -- COALESCE, never overwritten by a later call)
// when transitioning into GoalStatusActive, and completed_at (once) when
// transitioning into any terminal status (satisfied/failed/cancelled/
// superseded -- see goalTerminalStatuses; blocked is a paused, not
// terminal, state and does not set completed_at). Returns ErrGoalNotFound
// if no row matched.
//
// This is a bare status write, not a transition-legality guard -- calling
// this with, say, GoalStatusSatisfied on a GoalStatusDraft row succeeds
// (and sets completed_at) exactly as calling it on a GoalStatusActive row
// would. Real transition-legality enforcement (e.g. rejecting draft ->
// satisfied directly) is task 08's job once the loop engine is the thing
// driving transitions, not this storage layer's -- see
// TASKS/loops/01-goals-schema.md's "What to do" §2, last bullet.
func (s *Store) UpdateGoalStatus(ctx context.Context, id, status string) error {
	if err := validateGoalStatus(status); err != nil {
		return fmt.Errorf("update goal status: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var (
		query string
		args  []any
	)
	switch {
	case status == GoalStatusActive:
		query = `UPDATE goals SET status = ?, activated_at = COALESCE(activated_at, ?) WHERE id = ?`
		args = []any{status, now, id}
	case goalTerminalStatuses[status]:
		query = `UPDATE goals SET status = ?, completed_at = COALESCE(completed_at, ?) WHERE id = ?`
		args = []any{status, now, id}
	default:
		query = `UPDATE goals SET status = ? WHERE id = ?`
		args = []any{status, id}
	}

	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update goal status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update goal status rows affected: %w", err)
	}
	if n == 0 {
		return ErrGoalNotFound
	}
	return nil
}

// DeleteGoal removes a goal by id. Returns ErrGoalNotFound if no row
// matched. Storage-only: this does not check for or cascade into any
// loop_runs row launched against this goal (loop_runs is a later task in
// this batch) or any child goal referencing this row via parent_goal_id --
// migration 135's parent_goal_id FK has no ON DELETE clause, so SQLite's
// default (NO ACTION) applies: deleting a goal that is still some other
// row's parent fails at the DB layer rather than silently orphaning or
// cascading. That failure surfaces here as a wrapped error, not
// ErrGoalNotFound.
func (s *Store) DeleteGoal(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM goals WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete goal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete goal rows affected: %w", err)
	}
	if n == 0 {
		return ErrGoalNotFound
	}
	return nil
}
