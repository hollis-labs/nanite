package store

// TASKS/loops/02-goal-evidence-schema.md -- Go-side storage + evidence-walk
// support for the goal_evidence table migration 140_goal_evidence.sql adds.
// See docs/engineering/architecture/21-loops.md's Decision 2 for the design
// this table encodes: "Goal evidence is a thin pointer table, not a
// duplicate content store... It points into workflow_run_steps.verify_json,
// a gate's resolution record, or an event_log row -- the same
// 'derived/observability, not new persistent state' discipline Teams
// applied to routing provenance."
//
// This file is storage plus the evidence-walk *query* (EvaluateGoalEvidence/
// EvidenceSatisfiesGoal) -- computing the design doc's §7 formula
// (goal_met = acceptance_criteria_satisfied AND constraints_satisfied AND
// invariants_preserved AND required_evidence_present) by reading this
// table alongside the parent goals row's own acceptance_criteria_json/
// constraints_json/invariants_json columns. It is deliberately NOT the
// thing that *calls* this query as part of a live continuation decision --
// that is task 07's job (internal/loop's decide()); this task only builds
// the pure, directly-testable read.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrGoalEvidenceNotFound is returned when a goal_evidence row cannot be
// located by id.
var ErrGoalEvidenceNotFound = errors.New("goal evidence not found")

// goal_evidence.evidence_type vocabulary -- migration 140's CHECK
// constraint, taken verbatim from this task file's own illustrative DDL.
const (
	GoalEvidenceTypeTestSuite       = "test_suite"
	GoalEvidenceTypeVerifyResult    = "verify_result"
	GoalEvidenceTypeGateApproval    = "gate_approval"
	GoalEvidenceTypeHumanAcceptance = "human_acceptance"
	GoalEvidenceTypeArtifact        = "artifact"
)

// validGoalEvidenceTypes is the Go-side mirror of migration 140's CHECK
// constraint -- checked before insert so a caller gets a typed Go error
// instead of a raw sqlite CHECK-constraint-violation error, matching this
// package's existing enum-validation convention (team_authority.go's
// validTeamAuthorityVerbs, goals.go's validGoalStatuses).
var validGoalEvidenceTypes = map[string]bool{
	GoalEvidenceTypeTestSuite:       true,
	GoalEvidenceTypeVerifyResult:    true,
	GoalEvidenceTypeGateApproval:    true,
	GoalEvidenceTypeHumanAcceptance: true,
	GoalEvidenceTypeArtifact:        true,
}

// GoalEvidence is one row in the goal_evidence table -- a pointer into a
// structured evidence record (workflow_run_steps.verify_json, a gate
// resolution, or an event_log row), never a free-text-only claim. Mirrors
// the table's columns 1:1. Rows are append-only once recorded -- there is
// deliberately no UpdateGoalEvidence, matching the design doc's own
// loop_run_iterations "one row per firing" discipline (task 04).
type GoalEvidence struct {
	ID string `json:"id"`

	// GoalID is the goal this evidence bears on. Required, FK-enforced
	// (REFERENCES goals(id)).
	GoalID string `json:"goal_id"`

	// LoopRunID is the LoopRun this evidence was recorded during, if any.
	// Deliberately a plain nullable TEXT column with NO FK constraint --
	// see migration 140's own doc comment for why (order-independent
	// w.r.t. task 03's loop_runs table, not an oversight). Empty means "no
	// loop run" -- a goal-only evidence row is a real, valid shape (e.g. a
	// human acceptance note against a DEFINED goal with no loop launched
	// yet).
	LoopRunID string `json:"loop_run_id,omitempty"`

	// IterationNumber is the loop iteration this evidence was recorded
	// during, if any. Nil means unset -- always nil when LoopRunID is
	// empty in practice, though this type does not enforce that
	// coherence (validateGoalEvidence does not require it; not a real
	// need identified by any caller in this task's scope).
	IterationNumber *int64 `json:"iteration_number,omitempty"`

	// EvidenceType is one of the GoalEvidenceType* constants above.
	// Required, enum-validated by validateGoalEvidence and migration
	// 140's own CHECK constraint.
	EvidenceType string `json:"evidence_type"`

	// RefTable / RefID together point at the structured evidence record
	// this row is about (e.g. "workflow_run_steps", a step id; "event_log",
	// an event_log row id; a gate resolution record's own table/id). Both
	// required -- this is the "always structured, never free-text-only"
	// discipline this task's own Context section states as load-bearing.
	// Deliberately untyped/unvalidated against a real FK (goal_evidence
	// can point at more than one table, and some of those pointer targets
	// -- e.g. a gate resolution record -- are not necessarily their own
	// normalized table in every case), same "pointer, not enforced FK"
	// trade-off migration 132's team_authority_grants.to_slot='self'
	// sentinel documents for a different reason.
	RefTable string `json:"ref_table"`
	RefID    string `json:"ref_id"`

	// Result is a free-form outcome marker (e.g. "pass"/"fail") -- this
	// task's own scope does not lock a Result vocabulary (21-loops.md's
	// own "What this session did not decide" list does not mention it
	// either), so EvaluateGoalEvidence below treats a matching evidence
	// row as satisfying regardless of Result's value. A stricter
	// pass/fail-aware evaluation is a real follow-up once a real Result
	// vocabulary exists, not built speculatively here.
	Result string `json:"result,omitempty"`

	// Summary is free text describing what this evidence shows.
	// EvaluateGoalEvidence's evidence-to-criterion matching (below) reads
	// this field as the name of the specific acceptance-criterion/
	// constraint/invariant string (from the parent goal's own
	// acceptance_criteria_json/constraints_json/invariants_json lists)
	// this row addresses -- an exact string match. This is this task's
	// own design call for how to walk unstructured "which named item does
	// this evidence cover" linkage without adding a new column beyond
	// this table's fixed illustrative shape (see this file's
	// EvaluateGoalEvidence doc comment for the full reasoning).
	Summary string `json:"summary,omitempty"`

	RecordedAt string `json:"recorded_at"`
}

const goalEvidenceColumns = `id, goal_id, COALESCE(loop_run_id,''), iteration_number,
       evidence_type, ref_table, ref_id, COALESCE(result,''), COALESCE(summary,''),
       recorded_at`

func scanGoalEvidence(scanner interface{ Scan(...any) error }, e *GoalEvidence) error {
	var iterationNumber sql.NullInt64
	if err := scanner.Scan(
		&e.ID, &e.GoalID, &e.LoopRunID, &iterationNumber,
		&e.EvidenceType, &e.RefTable, &e.RefID, &e.Result, &e.Summary,
		&e.RecordedAt,
	); err != nil {
		return err
	}
	e.IterationNumber = nil
	if iterationNumber.Valid {
		v := iterationNumber.Int64
		e.IterationNumber = &v
	}
	return nil
}

// validateGoalEvidence enforces the "always structured, never free-text-only"
// decision this task's Context section states as load-bearing: evidence_type
// must be a real enum member, and ref_table/ref_id must both be non-empty
// (never a bare, unpointed evidence row). goal_id must also be non-empty --
// a goal_evidence row about no goal is meaningless regardless of the DB's
// own NOT NULL/FK enforcement, and this gives a typed Go error instead of a
// raw constraint-violation error for the same missing input.
func validateGoalEvidence(e *GoalEvidence) error {
	if e.GoalID == "" {
		return fmt.Errorf("goal_id is required")
	}
	if !validGoalEvidenceTypes[e.EvidenceType] {
		return fmt.Errorf("evidence_type %q invalid: must be one of test_suite, verify_result, gate_approval, human_acceptance, artifact", e.EvidenceType)
	}
	if e.RefTable == "" {
		return fmt.Errorf("ref_table is required (evidence must point at something structured)")
	}
	if e.RefID == "" {
		return fmt.Errorf("ref_id is required (evidence must point at something structured)")
	}
	return nil
}

// nullIfNilInt64Ptr mirrors agent_reflexes.go's nullIfNilInt64 (binds SQL
// NULL for a nil *int64, otherwise the dereferenced value) -- named
// distinctly here since agent_reflexes.go already owns that exact name in
// this package and this file's field is a plain field, not a struct
// setter's argument.
func nullIfNilInt64Ptr(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

// RecordGoalEvidence inserts a new goal_evidence row. Generates e.ID via
// uuid.New().String() if left empty, and sets e.RecordedAt to the insert
// time if left empty (mirrors goals.go's CreateGoal convention of computing
// the timestamp in Go rather than relying on the column DEFAULT, since the
// value is always passed explicitly in this INSERT). Validated via
// validateGoalEvidence before the write. There is deliberately no
// UpdateGoalEvidence -- evidence rows are immutable once recorded, matching
// the design doc's own "one row per firing" append-only discipline.
func (s *Store) RecordGoalEvidence(ctx context.Context, e *GoalEvidence) error {
	if err := validateGoalEvidence(e); err != nil {
		return fmt.Errorf("record goal evidence: %w", err)
	}
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if e.RecordedAt == "" {
		e.RecordedAt = time.Now().UTC().Format(time.RFC3339)
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO goal_evidence
		    (id, goal_id, loop_run_id, iteration_number, evidence_type,
		     ref_table, ref_id, result, summary, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.GoalID, nullIfEmpty(e.LoopRunID), nullIfNilInt64Ptr(e.IterationNumber),
		e.EvidenceType, e.RefTable, e.RefID, nullIfEmpty(e.Result), nullIfEmpty(e.Summary),
		e.RecordedAt,
	)
	if err != nil {
		return fmt.Errorf("record goal evidence: %w", err)
	}
	return nil
}

// GetGoalEvidence returns a goal_evidence row by id, or
// ErrGoalEvidenceNotFound.
func (s *Store) GetGoalEvidence(ctx context.Context, id string) (*GoalEvidence, error) {
	var e GoalEvidence
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+goalEvidenceColumns+` FROM goal_evidence WHERE id = ?`, id,
	)
	if err := scanGoalEvidence(row, &e); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGoalEvidenceNotFound
		}
		return nil, fmt.Errorf("get goal evidence: %w", err)
	}
	return &e, nil
}

// GoalEvidenceFilter narrows ListGoalEvidence -- an empty field means "no
// filter on this column." This task's own "What to do" §2 requires at
// minimum a filter by loop_run_id and a filter by evidence_type; both are
// here.
type GoalEvidenceFilter struct {
	LoopRunID    string
	EvidenceType string
}

// ListGoalEvidence returns every goal_evidence row for goalID matching
// filter, ordered by recorded_at ascending (append-only, so this is also
// insertion order). An empty GoalEvidenceFilter returns every evidence row
// for goalID. Returns an empty (non-nil) slice, not an error, for a goal
// with no recorded evidence yet -- a real, legitimate starting shape (a
// freshly-DEFINED goal has none), not an error condition.
func (s *Store) ListGoalEvidence(ctx context.Context, goalID string, filter GoalEvidenceFilter) ([]GoalEvidence, error) {
	query := `SELECT ` + goalEvidenceColumns + ` FROM goal_evidence WHERE goal_id = ?`
	args := []any{goalID}
	if filter.LoopRunID != "" {
		query += ` AND loop_run_id = ?`
		args = append(args, filter.LoopRunID)
	}
	if filter.EvidenceType != "" {
		query += ` AND evidence_type = ?`
		args = append(args, filter.EvidenceType)
	}
	query += ` ORDER BY recorded_at ASC`

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list goal evidence: %w", err)
	}
	defer rows.Close()
	out := make([]GoalEvidence, 0)
	for rows.Next() {
		var e GoalEvidence
		if err := scanGoalEvidence(rows, &e); err != nil {
			return nil, fmt.Errorf("scan goal evidence: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteGoalEvidence removes a goal_evidence row by id. Returns
// ErrGoalEvidenceNotFound if no row matched. Rarely used in practice --
// evidence is append-only by convention -- kept only for symmetry with
// every other CRUD set in this store package, per this task's own "What to
// do" §2.
func (s *Store) DeleteGoalEvidence(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM goal_evidence WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete goal evidence: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete goal evidence rows affected: %w", err)
	}
	if n == 0 {
		return ErrGoalEvidenceNotFound
	}
	return nil
}

// GoalEvidenceEvaluation is the four-clause breakdown behind
// EvidenceSatisfiesGoal's single bool -- docs/engineering/architecture/
// 21-loops.md's §7 formula: goal_met = acceptance_criteria_satisfied AND
// constraints_satisfied AND invariants_preserved AND
// required_evidence_present. Exposed as its own type (rather than only ever
// collapsed to a bool) so a real caller (task 07's continuation-policy
// decide(), or an operator-facing "why isn't this goal met yet" view) can
// report which specific clause is blocking, not just that the goal isn't
// met.
type GoalEvidenceEvaluation struct {
	GoalMet bool

	AcceptanceCriteriaSatisfied bool
	ConstraintsSatisfied        bool
	InvariantsPreserved         bool
	RequiredEvidencePresent     bool

	// MissingAcceptanceCriteria / MissingConstraints / MissingInvariants
	// list the specific named items (verbatim strings from the goal's own
	// AcceptanceCriteria()/Constraints()/Invariants() lists) that have no
	// matching evidence row yet -- empty when the corresponding
	// *Satisfied/*Preserved field above is true.
	MissingAcceptanceCriteria []string
	MissingConstraints        []string
	MissingInvariants         []string
}

// EvaluateGoalEvidence walks goal_evidence for goalID and computes the
// design doc's §7 goal_met formula against goalID's own goals row --
// this task's real deliverable ("a real, callable evidence-walk helper
// belongs here, not deferred to task 07"). Returns ErrGoalNotFound if
// goalID does not resolve to a real goals row.
//
// Evidence-to-criterion matching -- this task's own design call, since
// neither 21-loops.md nor this table's fixed illustrative shape names a
// dedicated "which criterion does this evidence address" column: a
// GoalEvidence row is considered to address a specific named
// acceptance-criterion/constraint/invariant string (from the parent goal's
// own JSON list columns) when that row's Summary field exactly matches the
// string. This keeps the "thin pointer, no duplicate content" table shape
// exactly as specified while still letting the walk distinguish "some
// criteria covered" from "all criteria covered" per this task's own "Done
// means" requirement ("partial evidence covering only some acceptance
// criteria" must evaluate not-satisfied). A goal whose given list (desired_
// state is not part of the §7 formula and is intentionally not evaluated
// here) is empty is vacuously satisfied for that clause -- an empty
// acceptance_criteria list has nothing left uncovered.
//
// RequiredEvidencePresent is a separate, fourth clause from the three
// per-list coverage checks above -- true iff at least one goal_evidence row
// exists for this goal at all, regardless of what it covers. This keeps
// the fourth clause meaningful even for a goal whose three JSON lists are
// all empty (which would otherwise vacuously satisfy the other three
// clauses with zero evidence ever recorded) -- a goal cannot be reported
// goal_met with no evidence trail whatsoever.
//
// Result's value is deliberately not consulted -- see GoalEvidence.Result's
// own doc comment for why: no Result vocabulary is locked by this task's
// scope, so any matching evidence row (regardless of Result) counts as
// coverage. A stricter pass/fail-aware evaluation is a real follow-up once
// a real Result vocabulary exists.
func (s *Store) EvaluateGoalEvidence(ctx context.Context, goalID string) (GoalEvidenceEvaluation, []GoalEvidence, error) {
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return GoalEvidenceEvaluation{}, nil, err
	}

	evidence, err := s.ListGoalEvidence(ctx, goalID, GoalEvidenceFilter{})
	if err != nil {
		return GoalEvidenceEvaluation{}, nil, fmt.Errorf("evaluate goal evidence: %w", err)
	}

	covered := make(map[string]bool, len(evidence))
	for _, e := range evidence {
		if e.Summary != "" {
			covered[e.Summary] = true
		}
	}

	acceptanceCriteria, err := goal.AcceptanceCriteria()
	if err != nil {
		return GoalEvidenceEvaluation{}, nil, fmt.Errorf("evaluate goal evidence: %w", err)
	}
	constraints, err := goal.Constraints()
	if err != nil {
		return GoalEvidenceEvaluation{}, nil, fmt.Errorf("evaluate goal evidence: %w", err)
	}
	invariants, err := goal.Invariants()
	if err != nil {
		return GoalEvidenceEvaluation{}, nil, fmt.Errorf("evaluate goal evidence: %w", err)
	}

	missingAcceptance := missingFromCoverage(acceptanceCriteria, covered)
	missingConstraints := missingFromCoverage(constraints, covered)
	missingInvariants := missingFromCoverage(invariants, covered)

	eval := GoalEvidenceEvaluation{
		AcceptanceCriteriaSatisfied: len(missingAcceptance) == 0,
		ConstraintsSatisfied:        len(missingConstraints) == 0,
		InvariantsPreserved:         len(missingInvariants) == 0,
		RequiredEvidencePresent:     len(evidence) > 0,
		MissingAcceptanceCriteria:   missingAcceptance,
		MissingConstraints:          missingConstraints,
		MissingInvariants:           missingInvariants,
	}
	eval.GoalMet = eval.AcceptanceCriteriaSatisfied && eval.ConstraintsSatisfied &&
		eval.InvariantsPreserved && eval.RequiredEvidencePresent

	return eval, evidence, nil
}

// missingFromCoverage returns the subset of items not present as a key in
// covered, preserving items' original order. A nil/empty items list
// returns an empty (non-nil) slice -- vacuously nothing missing.
func missingFromCoverage(items []string, covered map[string]bool) []string {
	missing := make([]string, 0)
	for _, item := range items {
		if !covered[item] {
			missing = append(missing, item)
		}
	}
	return missing
}

// EvidenceSatisfiesGoal is the narrow bool-plus-evidence-trail form of
// EvaluateGoalEvidence -- the shape this task file's own "What to do" §3
// names directly (`EvidenceSatisfiesGoal(ctx, goalID string) (bool,
// []GoalEvidence, error)`). task 07's decide() is the real consumer;
// EvaluateGoalEvidence (above) is available directly when the four-clause
// breakdown is useful (e.g. an operator-facing "why not met yet" view).
func (s *Store) EvidenceSatisfiesGoal(ctx context.Context, goalID string) (bool, []GoalEvidence, error) {
	eval, evidence, err := s.EvaluateGoalEvidence(ctx, goalID)
	if err != nil {
		return false, nil, err
	}
	return eval.GoalMet, evidence, nil
}
