package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WorkflowRunRow is the persisted identity/status row for one built-in
// WorkflowEngine Run()/Resume() invocation (CW-20260813-0010).
type WorkflowRunRow struct {
	ID             string
	DefinitionName string
	Status         string
	InputJSON      string
	Error          string
	StartedAt      time.Time
	CompletedAt    time.Time
	UpdatedAt      time.Time

	// LoopRunID scopes this WorkflowRun to one loop_runs.id when it was
	// launched as one iteration of a Loop (docs/engineering/architecture/
	// 21-loops.md's schema-ledger line, migration 140,
	// TASKS/loops/05-workflow-runs-loop-scoping-columns.md). REFERENCES
	// loop_runs(id). nil for every ordinary, non-loop-launched run --
	// which is every run today: nothing in this codebase sets this field
	// yet (task 08's LoopEngine is the first intended writer, via
	// UpdateWorkflowRunLoopScope below, not via CreateWorkflowRun's normal
	// insert path -- see that function's doc comment for why).
	LoopRunID *string

	// LoopIteration is the iteration number within LoopRunID this run
	// represents. Only meaningful when LoopRunID is non-nil; nil
	// otherwise. A pointer (not a bare int) because 0 is a legitimate
	// iteration number and can't double as an "unset" sentinel the way an
	// empty string can for LoopRunID.
	LoopIteration *int
}

// WorkflowRunStepRow is the persisted per-step status/output row a later
// step's template resolution reads from directly — the typed inter-step
// data flow the design doc calls for, and the unit a crash/restart resumes
// from.
type WorkflowRunStepRow struct {
	ID            string
	WorkflowRunID string
	StepID        string
	Kind          string
	Status        string
	Output        string
	IsError       bool
	ToolCallsJSON string
	VerifyJSON    string
	Error         string
	GateInput     string // CW-20260814-0017: external input that resolves a gate step
	StartedAt     time.Time
	CompletedAt   time.Time
	UpdatedAt     time.Time

	// LoopRunID is set only for a StepKindLoop step (TASKS/loops/
	// 09-stepkindloop-executor-and-waiting-status.md): the loop_runs.id of
	// the contained LoopRun this step launched, recorded as soon as
	// LoopEngine.Run returns a loop_run_id (whether the step then resolves
	// immediately or parks itself waiting_on_loop). REFERENCES
	// loop_runs(id). nil for every non-loop step, and nil for a loop step
	// that hasn't launched yet. This is the reverse direction from
	// WorkflowRunRow.LoopRunID (migration 140: "given a WorkflowRun, what
	// loop is it in," set when a run itself IS one loop iteration) — this
	// field answers "given a WorkflowRun's own STEP, what loop did it
	// launch," and is what GetWorkflowRunStepByLoopRunID looks up by.
	LoopRunID *string
}

// ErrWorkflowRunNotFound signals an unknown workflow_runs.id.
var ErrWorkflowRunNotFound = errors.New("workflow_runs: not found")

// ErrWorkflowRunStepNotFound signals no workflow_run_steps row matched a
// lookup — e.g. GetWorkflowRunStepByLoopRunID for a loop_run_id no step
// references (the ordinary case for the overwhelming majority of
// LoopRuns, which are launched directly rather than from a StepKindLoop
// step — docs/engineering/architecture/21-loops.md's "Trigger surface").
var ErrWorkflowRunStepNotFound = errors.New("workflow_run_steps: not found")

const workflowRunColumns = `id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration`

// CreateWorkflowRun persists a new run row. row.ID must already be set by
// the caller (the engine generates it up front so it can stamp
// WorkflowRunID onto every step request before the row exists).
//
// row.LoopRunID/row.LoopIteration are threaded straight through to the
// INSERT for a caller that already knows them, but as of this task nothing
// in this codebase actually sets them on the WorkflowRunRow passed here --
// BuiltinWorkflowEngine.Run (the only real caller,
// internal/service/workflow_engine.go) constructs its WorkflowRunRow from
// agentworkflow.WorkflowInput, a shared type every WorkflowEngine
// implementation (built-in and external) consumes, and does not carry a
// loop-scoping concept. Extending that shared, engine-agnostic type would
// be a much larger change than this task's scope. Task 08's LoopEngine is
// expected to call UpdateWorkflowRunLoopScope (below) right after
// WorkflowLauncher.Launch returns its WorkflowLaunchResult.RunID instead --
// see this task's Work Log for the full reasoning.
func (s *Store) CreateWorkflowRun(row *WorkflowRunRow) error {
	if row == nil {
		return errors.New("CreateWorkflowRun: nil row")
	}
	if row.ID == "" {
		return errors.New("CreateWorkflowRun: empty id")
	}
	if row.Status == "" {
		row.Status = "running"
	}
	if row.InputJSON == "" {
		row.InputJSON = "{}"
	}
	if row.StartedAt.IsZero() {
		row.StartedAt = time.Now().UTC()
	}
	row.UpdatedAt = row.StartedAt

	_, err := s.DB.Exec(
		`INSERT INTO workflow_runs (`+workflowRunColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.DefinitionName, row.Status, row.InputJSON, row.Error,
		formatTimeRFC3339Nano(row.StartedAt), formatTimeRFC3339NanoOrEmpty(row.CompletedAt),
		formatTimeRFC3339Nano(row.UpdatedAt),
		row.LoopRunID, row.LoopIteration,
	)
	if err != nil {
		return fmt.Errorf("create workflow_runs row %s: %w", row.ID, err)
	}
	return nil
}

// UpdateWorkflowRunLoopScope sets loop_run_id/loop_iteration on an existing
// workflow_runs row -- the follow-up narrow updater TASKS/loops/
// 05-workflow-runs-loop-scoping-columns.md's own "Done means" calls for,
// since WorkflowLaunchRequest/WorkflowLauncher.Launch has no natural
// pass-through for these two values (see CreateWorkflowRun's doc comment
// above for why). Intended caller: task 08's LoopEngine, immediately after
// WorkflowLauncher.Launch returns a WorkflowLaunchResult for one loop
// iteration's run. loopRunID must be non-empty (mirrors
// CreateWorkflowRun's own required-id check) -- this updater exists
// specifically to stamp a run as loop-scoped, so an empty loopRunID is
// always a caller bug, not a valid "clear the scope" request. Returns
// ErrWorkflowRunNotFound when id doesn't match any row, matching this
// file's SetWorkflowRunStatus convention.
func (s *Store) UpdateWorkflowRunLoopScope(id, loopRunID string, loopIteration int) error {
	if id == "" {
		return errors.New("UpdateWorkflowRunLoopScope: empty id")
	}
	if loopRunID == "" {
		return errors.New("UpdateWorkflowRunLoopScope: empty loop_run_id")
	}
	res, err := s.DB.Exec(
		`UPDATE workflow_runs SET loop_run_id = ?, loop_iteration = ?, updated_at = ? WHERE id = ?`,
		loopRunID, loopIteration, formatTimeRFC3339Nano(time.Now().UTC()), id,
	)
	if err != nil {
		return fmt.Errorf("update workflow_runs loop scope %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update workflow_runs loop scope %s: rows affected: %w", id, err)
	}
	if n == 0 {
		return ErrWorkflowRunNotFound
	}
	return nil
}

// SetWorkflowRunStatus transitions a run to a new status (typically
// terminal-for-this-call: completed/failed/cancelled/waiting_on_gate).
// completedAt may be the zero time when the run isn't yet finished. Returns
// ErrWorkflowRunNotFound when id doesn't match any row — matching this
// codebase's dominant store convention of surfacing a no-op update as an
// explicit error rather than succeeding silently, so a caller (the engine)
// can never mistake "nothing updated" for "run finalized."
func (s *Store) SetWorkflowRunStatus(id, status, errMsg string, completedAt time.Time) error {
	res, err := s.DB.Exec(
		`UPDATE workflow_runs SET status = ?, error = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		status, errMsg, formatTimeRFC3339NanoOrEmpty(completedAt), formatTimeRFC3339Nano(time.Now().UTC()), id,
	)
	if err != nil {
		return fmt.Errorf("set workflow_runs status %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set workflow_runs status %s: rows affected: %w", id, err)
	}
	if n == 0 {
		return ErrWorkflowRunNotFound
	}
	return nil
}

// GetWorkflowRun loads a run row by id. Returns ErrWorkflowRunNotFound when
// unknown.
func (s *Store) GetWorkflowRun(id string) (*WorkflowRunRow, error) {
	row := s.DB.QueryRow(`SELECT `+workflowRunColumns+` FROM workflow_runs WHERE id = ?`, id)
	r, err := scanWorkflowRunRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkflowRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow_runs %s: %w", id, err)
	}
	return r, nil
}

func scanWorkflowRunRow(scanner interface{ Scan(...any) error }) (*WorkflowRunRow, error) {
	r := &WorkflowRunRow{}
	var startedAt, completedAt, updatedAt string
	var loopRunID sql.NullString
	var loopIteration sql.NullInt64
	err := scanner.Scan(
		&r.ID, &r.DefinitionName, &r.Status, &r.InputJSON, &r.Error, &startedAt, &completedAt, &updatedAt,
		&loopRunID, &loopIteration,
	)
	if err != nil {
		return nil, err
	}
	r.StartedAt = parseTimeRFC3339Nano(startedAt)
	r.CompletedAt = parseTimeRFC3339Nano(completedAt)
	r.UpdatedAt = parseTimeRFC3339Nano(updatedAt)
	if loopRunID.Valid {
		v := loopRunID.String
		r.LoopRunID = &v
	}
	if loopIteration.Valid {
		v := int(loopIteration.Int64)
		r.LoopIteration = &v
	}
	return r, nil
}

const workflowRunStepColumns = `id, workflow_run_id, step_id, kind, status, output, is_error, tool_calls_json, verify_json, error, gate_input, started_at, completed_at, updated_at, loop_run_id`

// workflowRunStepID builds the deterministic synthetic PK for a
// (workflow_run_id, step_id) pair — stable and collision-free without
// needing a generated id, and it doubles as the natural key the unique
// index on (workflow_run_id, step_id) already enforces.
func workflowRunStepID(runID, stepID string) string {
	return runID + ":" + stepID
}

// UpsertWorkflowRunStep inserts or updates one step's row. The engine calls
// this on every status transition (pending -> running -> terminal) so a
// crash at any point leaves the store reflecting the step's last known
// state — the crash-durability requirement.
func (s *Store) UpsertWorkflowRunStep(row *WorkflowRunStepRow) error {
	if row == nil {
		return errors.New("UpsertWorkflowRunStep: nil row")
	}
	if row.WorkflowRunID == "" || row.StepID == "" {
		return errors.New("UpsertWorkflowRunStep: workflow_run_id and step_id are required")
	}
	if row.ID == "" {
		row.ID = workflowRunStepID(row.WorkflowRunID, row.StepID)
	}
	if row.Status == "" {
		row.Status = "pending"
	}
	if row.ToolCallsJSON == "" {
		row.ToolCallsJSON = "[]"
	}
	row.UpdatedAt = time.Now().UTC()

	_, err := s.DB.Exec(
		`INSERT INTO workflow_run_steps (`+workflowRunStepColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		     kind = excluded.kind,
		     status = excluded.status,
		     output = excluded.output,
		     is_error = excluded.is_error,
		     tool_calls_json = excluded.tool_calls_json,
		     verify_json = excluded.verify_json,
		     error = excluded.error,
		     gate_input = excluded.gate_input,
		     started_at = CASE WHEN excluded.started_at = '' THEN workflow_run_steps.started_at ELSE excluded.started_at END,
		     completed_at = excluded.completed_at,
		     updated_at = excluded.updated_at,
		     loop_run_id = excluded.loop_run_id`,
		row.ID, row.WorkflowRunID, row.StepID, row.Kind, row.Status, row.Output, row.IsError,
		row.ToolCallsJSON, row.VerifyJSON, row.Error, row.GateInput,
		formatTimeRFC3339NanoOrEmpty(row.StartedAt), formatTimeRFC3339NanoOrEmpty(row.CompletedAt),
		formatTimeRFC3339Nano(row.UpdatedAt), row.LoopRunID,
	)
	if err != nil {
		return fmt.Errorf("upsert workflow_run_steps %s: %w", row.ID, err)
	}
	return nil
}

// ListWorkflowRunSteps returns every step row for a run, in insertion
// (rowid) order — not started_at, which is empty for steps that never
// progressed past pending and so can't be used to order the full set.
// Resume uses this to rebuild in-memory step results from persisted state
// before continuing execution.
func (s *Store) ListWorkflowRunSteps(runID string) ([]*WorkflowRunStepRow, error) {
	rows, err := s.DB.Query(
		`SELECT `+workflowRunStepColumns+` FROM workflow_run_steps WHERE workflow_run_id = ? ORDER BY rowid`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("list workflow_run_steps %s: %w", runID, err)
	}
	defer rows.Close()

	var out []*WorkflowRunStepRow
	for rows.Next() {
		r, err := scanWorkflowRunStepRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow_run_steps %s: %w", runID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanWorkflowRunStepRow(scanner interface{ Scan(...any) error }) (*WorkflowRunStepRow, error) {
	r := &WorkflowRunStepRow{}
	var startedAt, completedAt, updatedAt string
	var loopRunID sql.NullString
	err := scanner.Scan(
		&r.ID, &r.WorkflowRunID, &r.StepID, &r.Kind, &r.Status, &r.Output, &r.IsError,
		&r.ToolCallsJSON, &r.VerifyJSON, &r.Error, &r.GateInput, &startedAt, &completedAt, &updatedAt,
		&loopRunID,
	)
	if err != nil {
		return nil, err
	}
	r.StartedAt = parseTimeRFC3339Nano(startedAt)
	r.CompletedAt = parseTimeRFC3339Nano(completedAt)
	r.UpdatedAt = parseTimeRFC3339Nano(updatedAt)
	if loopRunID.Valid {
		v := loopRunID.String
		r.LoopRunID = &v
	}
	return r, nil
}

// GetWorkflowRunStepByLoopRunID finds the (at most one) workflow_run_steps
// row whose loop_run_id column matches loopRunID — the outer StepKindLoop
// step waiting on a given contained LoopRun, if this LoopRun was ever
// launched from one (TASKS/loops/09-stepkindloop-executor-and-waiting-
// status.md). Returns ErrWorkflowRunStepNotFound when no such row exists —
// the ordinary case for the overwhelming majority of LoopRuns, which are
// launched directly (Manual/API launch), not from a StepKindLoop step at
// all (docs/engineering/architecture/21-loops.md's "Trigger surface").
// Intended caller: service.LoopResumeNotifier.NotifyLoopRunTerminal, the
// real push a terminal LoopRun uses to find and resume its specific outer
// WorkflowRun.
func (s *Store) GetWorkflowRunStepByLoopRunID(loopRunID string) (*WorkflowRunStepRow, error) {
	if loopRunID == "" {
		return nil, errors.New("GetWorkflowRunStepByLoopRunID: empty loop_run_id")
	}
	row := s.DB.QueryRow(
		`SELECT `+workflowRunStepColumns+` FROM workflow_run_steps WHERE loop_run_id = ? LIMIT 1`,
		loopRunID,
	)
	r, err := scanWorkflowRunStepRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkflowRunStepNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow_run_steps by loop_run_id %s: %w", loopRunID, err)
	}
	return r, nil
}

func formatTimeRFC3339Nano(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func formatTimeRFC3339NanoOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTimeRFC3339Nano(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ResolveGate updates a waiting gate step with the provided input and marks it
// completed. This is called when external input (e.g., from A2A task input)
// resolves a paused gate, allowing the workflow to resume.
// CW-20260814-0017: A2A gate ↔ input-required mapping.
func (s *Store) ResolveGate(runID, stepID, input string) error {
	if runID == "" || stepID == "" {
		return errors.New("ResolveGate: runID and stepID are required")
	}

	id := workflowRunStepID(runID, stepID)
	now := time.Now().UTC()

	result, err := s.DB.Exec(
		`UPDATE workflow_run_steps
		 SET gate_input = ?,
		     status = 'completed',
		     output = ?,
		     completed_at = ?,
		     updated_at = ?
		 WHERE id = ? AND kind = 'gate' AND status = 'waiting_on_gate'`,
		input,
		"Gate resolved: "+input, // Store input in output for visibility
		formatTimeRFC3339Nano(now),
		formatTimeRFC3339Nano(now),
		id,
	)
	if err != nil {
		return fmt.Errorf("resolve gate %s/%s: %w", runID, stepID, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolve gate %s/%s: check rows: %w", runID, stepID, err)
	}
	if rows == 0 {
		return fmt.Errorf("resolve gate %s/%s: no waiting gate found", runID, stepID)
	}

	return nil
}

// GetWaitingGates returns all gate steps in waiting_on_gate status for a run.
// CW-20260814-0017: used to surface gate context in A2A Task state.
func (s *Store) GetWaitingGates(runID string) ([]*WorkflowRunStepRow, error) {
	rows, err := s.DB.Query(
		`SELECT `+workflowRunStepColumns+` FROM workflow_run_steps
		 WHERE workflow_run_id = ? AND kind = 'gate' AND status = 'waiting_on_gate'
		 ORDER BY rowid`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("get waiting gates %s: %w", runID, err)
	}
	defer rows.Close()

	var out []*WorkflowRunStepRow
	for rows.Next() {
		r, err := scanWorkflowRunStepRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan waiting gate %s: %w", runID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
