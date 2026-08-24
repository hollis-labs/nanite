package store

// TASKS/loops/04-loop-run-iterations-schema.md -- Go-side storage for the
// loop_run_iterations table migration 142_loop_run_iterations.sql adds. See
// docs/engineering/architecture/21-loops.md's per-iteration history section
// for the design this table encodes: "New, thin (loop_run_iterations) --
// same shape as Scheduling's illustrative schedule_runs: one row per
// firing." Each row records one iteration's actual WorkflowRun and the
// continuation-policy decision made against it.
//
// evaluation_json stores the per-iteration Verify rollup (remaining_delta,
// confidence, regressions per the design doc), NOT a duplicate of
// workflow_run_steps.verify_json itself -- workflow_run_id is the pointer
// back to the real per-step verify records. Per the design doc, "an
// iteration's IterationResult.progress is a rollup over that iteration's
// WorkflowRun step VerifyResults, not a second evaluator subsystem."
//
// Storage only: this file does not launch iterations, evaluate progress, or
// run the continuation-policy "decide" function -- TASKS/loops' later tasks
// (07/08) own that logic.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrLoopRunIterationNotFound is returned when a loop_run_iterations row
// cannot be located.
var ErrLoopRunIterationNotFound = errors.New("loop run iteration not found")

// loop_run_iterations.decision vocabulary -- migration 142's CHECK
// constraint, taken verbatim from 21-loops.md's continuation-policy decision
// enum (CONTINUE | RETRY | REPLAN | REARCHITECT | WAIT | ESCALATE | COMPLETE
// | FAIL).
const (
	LoopRunIterationDecisionContinue    = "continue"
	LoopRunIterationDecisionRetry       = "retry"
	LoopRunIterationDecisionReplan      = "replan"
	LoopRunIterationDecisionRearchitect = "rearchitect"
	LoopRunIterationDecisionWait        = "wait"
	LoopRunIterationDecisionEscalate    = "escalate"
	LoopRunIterationDecisionComplete    = "complete"
	LoopRunIterationDecisionFail        = "fail"
)

var validLoopRunIterationDecisions = map[string]bool{
	LoopRunIterationDecisionContinue:    true,
	LoopRunIterationDecisionRetry:       true,
	LoopRunIterationDecisionReplan:      true,
	LoopRunIterationDecisionRearchitect: true,
	LoopRunIterationDecisionWait:        true,
	LoopRunIterationDecisionEscalate:    true,
	LoopRunIterationDecisionComplete:    true,
	LoopRunIterationDecisionFail:        true,
}

// validateLoopRunIterationDecision checks decision against the enum
// migration 142's DB CHECK constraint also enforces -- same Go-layer-
// validation-over-a-DB-CHECK-constraint approach loop_runs.go's
// validateLoopRunStatus uses, giving a typed Go error instead of a raw
// sqlite CHECK-violation error.
func validateLoopRunIterationDecision(decision string) error {
	if !validLoopRunIterationDecisions[decision] {
		return fmt.Errorf("decision %q invalid: must be one of continue, retry, replan, rearchitect, wait, escalate, complete, fail", decision)
	}
	return nil
}

// loop_run_iterations.progress_state vocabulary -- migration 142's CHECK
// constraint, taken verbatim from 21-loops.md's IterationResult.progress
// rollup enum (PROGRESS | NO_PROGRESS | REGRESSION | BLOCKED | GOAL_MET).
const (
	LoopRunIterationProgressProgress   = "progress"
	LoopRunIterationProgressNoProgress = "no_progress"
	LoopRunIterationProgressRegression = "regression"
	LoopRunIterationProgressBlocked    = "blocked"
	LoopRunIterationProgressGoalMet    = "goal_met"
)

var validLoopRunIterationProgressStates = map[string]bool{
	LoopRunIterationProgressProgress:   true,
	LoopRunIterationProgressNoProgress: true,
	LoopRunIterationProgressRegression: true,
	LoopRunIterationProgressBlocked:    true,
	LoopRunIterationProgressGoalMet:    true,
}

// validateLoopRunIterationProgressState mirrors
// validateLoopRunIterationDecision for the progress_state enum.
func validateLoopRunIterationProgressState(progressState string) error {
	if !validLoopRunIterationProgressStates[progressState] {
		return fmt.Errorf("progress_state %q invalid: must be one of progress, no_progress, regression, blocked, goal_met", progressState)
	}
	return nil
}

// Evaluation is loop_run_iterations.evaluation_json's decoded shape --
// 21-loops.md's illustrative "remaining_delta, confidence, regressions"
// per-iteration Verify rollup (schema ledger section, evaluation_json's own
// comment). Not a duplicate of workflow_run_steps.verify_json -- this is the
// rolled-up summary the continuation policy's decide() function consumes,
// not the per-step detail (that's reached via WorkflowRunID).
type Evaluation struct {
	RemainingDelta string   `json:"remaining_delta"`
	Confidence     float64  `json:"confidence"`
	Regressions    []string `json:"regressions"`
}

func decodeLoopRunIterationEvaluation(raw string) (Evaluation, error) {
	if raw == "" || raw == "{}" {
		return Evaluation{}, nil
	}
	var e Evaluation
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return Evaluation{}, fmt.Errorf("decode loop_run_iteration evaluation: %w", err)
	}
	return e, nil
}

func encodeLoopRunIterationEvaluation(e Evaluation) (string, error) {
	encoded, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("encode loop_run_iteration evaluation: %w", err)
	}
	return string(encoded), nil
}

// LoopRunIteration is one row in the loop_run_iterations table -- one
// firing of a LoopRun's iteration loop. Mirrors the table's columns 1:1.
//
// decision, progress_state, and workflow_run_id are all left empty on an
// in-flight row -- 21-loops.md's own illustrative example shows this
// exactly ("decision: null # in flight"). Per this task's own "What to do"
// §1: task 08 creates a row when an iteration *starts* (workflow_run_id may
// not exist yet at that instant if the WorkflowRun is launched in a
// transaction-adjacent step) via CreateLoopRunIteration, then fills in
// decision/progress_state/workflow_run_id/completed_at once that
// iteration's evaluation finishes, via CompleteLoopRunIteration -- the one
// mutator this file provides.
type LoopRunIteration struct {
	ID string `json:"id"`

	// LoopRunID is the parent loop_runs row this iteration belongs to.
	// Required, FK-enforced (REFERENCES loop_runs(id)).
	LoopRunID string `json:"loop_run_id"`

	// IterationNumber is this iteration's 1-based sequence number within
	// LoopRunID -- 21-loops.md's illustrative `iteration_number: 1`.
	// Unique together with LoopRunID (migration 142's
	// idx_loop_run_iterations_seq unique index).
	IterationNumber int `json:"iteration_number"`

	// WorkflowRunID is the actual executed WorkflowRun for this iteration --
	// FK-enforced (REFERENCES workflow_runs(id)) but nullable: empty on an
	// in-flight row created before its WorkflowRun exists yet.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`

	// Decision is the continuation-policy outcome for this iteration -- one
	// of the LoopRunIterationDecision* constants above. Empty on an
	// in-flight row; set by CompleteLoopRunIteration.
	Decision string `json:"decision,omitempty"`

	// ProgressState is this iteration's Verify-rollup progress classification
	// -- one of the LoopRunIterationProgress* constants above. Empty on an
	// in-flight row; set by CompleteLoopRunIteration.
	ProgressState string `json:"progress_state,omitempty"`

	// EvaluationJSON is Evaluation's encoded form -- use the
	// Evaluation()/SetEvaluation() typed accessor pair below rather than
	// handling the raw JSON at call sites, mirrors loop_runs.go's
	// BudgetJSON/Budget()/SetBudget() convention.
	EvaluationJSON string `json:"evaluation_json"`

	StartedAt string `json:"started_at"`

	// CompletedAt is set by CompleteLoopRunIteration. Empty on an in-flight
	// row.
	CompletedAt string `json:"completed_at,omitempty"`
}

// Evaluation decodes LoopRunIteration.EvaluationJSON into Evaluation.
// Returns a zero-value Evaluation for an empty/unset EvaluationJSON.
func (li *LoopRunIteration) Evaluation() (Evaluation, error) {
	return decodeLoopRunIterationEvaluation(li.EvaluationJSON)
}

// SetEvaluation encodes e into LoopRunIteration.EvaluationJSON. There is no
// enum to validate here (unlike loop_runs.go's SetBudget) -- Evaluation has
// no field with a locked vocabulary.
func (li *LoopRunIteration) SetEvaluation(e Evaluation) error {
	encoded, err := encodeLoopRunIterationEvaluation(e)
	if err != nil {
		return err
	}
	li.EvaluationJSON = encoded
	return nil
}

const loopRunIterationColumns = `id, loop_run_id, iteration_number,
       COALESCE(workflow_run_id,''), COALESCE(decision,''),
       COALESCE(progress_state,''), evaluation_json,
       started_at, COALESCE(completed_at,'')`

func scanLoopRunIteration(scanner interface{ Scan(...any) error }, li *LoopRunIteration) error {
	return scanner.Scan(
		&li.ID, &li.LoopRunID, &li.IterationNumber,
		&li.WorkflowRunID, &li.Decision, &li.ProgressState, &li.EvaluationJSON,
		&li.StartedAt, &li.CompletedAt,
	)
}

// CreateLoopRunIteration inserts a new loop_run_iterations row -- ordinarily
// the in-flight shape (WorkflowRunID/Decision/ProgressState left empty),
// per this file's package doc comment, though a caller that already knows
// all three fields up front (e.g. a backfill or a synchronous launcher that
// resolves the WorkflowRun before creating the row) may set them directly;
// CreateLoopRunIteration validates whichever of Decision/ProgressState are
// non-empty rather than requiring the in-flight shape. Generates an ID via
// uuid.New() if li.ID is empty, and defaults EvaluationJSON to "{}" if left
// empty (matching migration 142's column DEFAULT, replicated here because
// every value is passed explicitly in this INSERT so SQLite's column
// DEFAULT never actually applies -- same trade-off loop_runs.go's
// CreateLoopRun documents for its own JSON defaults). LoopRunID and a real
// IterationNumber are required.
func (s *Store) CreateLoopRunIteration(ctx context.Context, li *LoopRunIteration) error {
	if li.LoopRunID == "" {
		return fmt.Errorf("create loop_run_iteration: loop_run_id is required")
	}
	if li.Decision != "" {
		if err := validateLoopRunIterationDecision(li.Decision); err != nil {
			return fmt.Errorf("create loop_run_iteration: %w", err)
		}
	}
	if li.ProgressState != "" {
		if err := validateLoopRunIterationProgressState(li.ProgressState); err != nil {
			return fmt.Errorf("create loop_run_iteration: %w", err)
		}
	}
	if li.ID == "" {
		li.ID = uuid.New().String()
	}
	if li.EvaluationJSON == "" {
		li.EvaluationJSON = "{}"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO loop_run_iterations
		    (id, loop_run_id, iteration_number, workflow_run_id, decision,
		     progress_state, evaluation_json, started_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		li.ID, li.LoopRunID, li.IterationNumber, nullIfEmpty(li.WorkflowRunID),
		nullIfEmpty(li.Decision), nullIfEmpty(li.ProgressState), li.EvaluationJSON,
		now, nullIfEmpty(li.CompletedAt),
	)
	if err != nil {
		return fmt.Errorf("create loop_run_iteration: %w", err)
	}
	li.StartedAt = now
	return nil
}

// GetLoopRunIteration returns the loop_run_iterations row for
// (loopRunID, iterationNumber), or ErrLoopRunIterationNotFound.
func (s *Store) GetLoopRunIteration(ctx context.Context, loopRunID string, iterationNumber int) (*LoopRunIteration, error) {
	var li LoopRunIteration
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+loopRunIterationColumns+`
		   FROM loop_run_iterations
		  WHERE loop_run_id = ? AND iteration_number = ?`,
		loopRunID, iterationNumber,
	)
	if err := scanLoopRunIteration(row, &li); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLoopRunIterationNotFound
		}
		return nil, fmt.Errorf("get loop_run_iteration: %w", err)
	}
	return &li, nil
}

// ListLoopRunIterations returns every loop_run_iterations row for
// loopRunID, ordered by iteration_number ascending. Returns an empty
// (non-nil) slice, not an error, for a LoopRun with no iterations yet.
func (s *Store) ListLoopRunIterations(ctx context.Context, loopRunID string) ([]LoopRunIteration, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+loopRunIterationColumns+`
		   FROM loop_run_iterations
		  WHERE loop_run_id = ?
		  ORDER BY iteration_number ASC`,
		loopRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("list loop_run_iterations: %w", err)
	}
	defer closeRows(rows)
	out := make([]LoopRunIteration, 0)
	for rows.Next() {
		var li LoopRunIteration
		if err := scanLoopRunIteration(rows, &li); err != nil {
			return nil, fmt.Errorf("scan loop_run_iteration: %w", err)
		}
		out = append(out, li)
	}
	return out, rows.Err()
}

// CompleteLoopRunIteration is the one mutator this file provides -- fills
// in the fields an in-flight row (per CreateLoopRunIteration's usual shape)
// left null: workflow_run_id, decision, progress_state, evaluation_json,
// and completed_at (set to now). id is the loop_run_iterations row's own
// id (from a prior CreateLoopRunIteration call), not (loop_run_id,
// iteration_number) -- matching this task's own "What to do" §2 signature.
// workflowRunID, decision, and progressState are all required (a completed
// iteration has a real executed WorkflowRun and a real continuation-policy
// verdict by definition); decision/progressState are validated against
// their enums before the write. Returns ErrLoopRunIterationNotFound if no
// row matched id.
func (s *Store) CompleteLoopRunIteration(ctx context.Context, id, workflowRunID, decision, progressState string, evaluation Evaluation) error {
	if workflowRunID == "" {
		return fmt.Errorf("complete loop_run_iteration: workflow_run_id is required")
	}
	if err := validateLoopRunIterationDecision(decision); err != nil {
		return fmt.Errorf("complete loop_run_iteration: %w", err)
	}
	if err := validateLoopRunIterationProgressState(progressState); err != nil {
		return fmt.Errorf("complete loop_run_iteration: %w", err)
	}
	encodedEvaluation, err := encodeLoopRunIterationEvaluation(evaluation)
	if err != nil {
		return fmt.Errorf("complete loop_run_iteration: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE loop_run_iterations
		    SET workflow_run_id = ?, decision = ?, progress_state = ?,
		        evaluation_json = ?, completed_at = ?
		  WHERE id = ?`,
		workflowRunID, decision, progressState, encodedEvaluation, now, id,
	)
	if err != nil {
		return fmt.Errorf("complete loop_run_iteration: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete loop_run_iteration rows affected: %w", err)
	}
	if n == 0 {
		return ErrLoopRunIterationNotFound
	}
	return nil
}
