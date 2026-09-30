package api

import "github.com/hollis-labs/nanite/internal/store"

// GoalView, GoalEvidenceView, LoopRunView and LoopRunIterationView are the
// API-owned wire shapes of the Loops records. Their keys match what the store
// rows used to emit directly — including exactly which keys are omitempty —
// so the wire did not change when the rows stopped riding onto it. A new
// column reaches the wire only once it is added here and to its translator.
// TestLoopViewJSONKeys pins the keys at zero and fully populated values.
//
// The list translators always return a non-nil slice: the store lists
// return an empty slice, never nil.

// GoalView is the wire shape of one goals row.
type GoalView struct {
	ID                     string `json:"id"`
	ParentGoalID           string `json:"parent_goal_id,omitempty"`
	Intent                 string `json:"intent"`
	DesiredStateJSON       string `json:"desired_state_json"`
	ConstraintsJSON        string `json:"constraints_json"`
	AcceptanceCriteriaJSON string `json:"acceptance_criteria_json"`
	InvariantsJSON         string `json:"invariants_json"`
	Priority               string `json:"priority,omitempty"`
	Scope                  string `json:"scope,omitempty"`
	Status                 string `json:"status"`
	Owner                  string `json:"owner,omitempty"`
	Source                 string `json:"source,omitempty"`
	CreatedAt              string `json:"created_at"`
	ActivatedAt            string `json:"activated_at,omitempty"`
	CompletedAt            string `json:"completed_at,omitempty"`
}

func goalToView(g *store.Goal) GoalView {
	return GoalView{
		ID:                     g.ID,
		ParentGoalID:           g.ParentGoalID,
		Intent:                 g.Intent,
		DesiredStateJSON:       g.DesiredStateJSON,
		ConstraintsJSON:        g.ConstraintsJSON,
		AcceptanceCriteriaJSON: g.AcceptanceCriteriaJSON,
		InvariantsJSON:         g.InvariantsJSON,
		Priority:               g.Priority,
		Scope:                  g.Scope,
		Status:                 g.Status,
		Owner:                  g.Owner,
		Source:                 g.Source,
		CreatedAt:              g.CreatedAt,
		ActivatedAt:            g.ActivatedAt,
		CompletedAt:            g.CompletedAt,
	}
}

func goalsToView(rows []store.Goal) []GoalView {
	out := make([]GoalView, 0, len(rows))
	for i := range rows {
		out = append(out, goalToView(&rows[i]))
	}
	return out
}

// GoalEvidenceView is the wire shape of one goal_evidence row.
type GoalEvidenceView struct {
	ID              string `json:"id"`
	GoalID          string `json:"goal_id"`
	LoopRunID       string `json:"loop_run_id,omitempty"`
	IterationNumber *int64 `json:"iteration_number,omitempty"`
	EvidenceType    string `json:"evidence_type"`
	RefTable        string `json:"ref_table"`
	RefID           string `json:"ref_id"`
	Result          string `json:"result,omitempty"`
	Summary         string `json:"summary,omitempty"`
	RecordedAt      string `json:"recorded_at"`
}

func goalEvidenceToView(rows []store.GoalEvidence) []GoalEvidenceView {
	out := make([]GoalEvidenceView, 0, len(rows))
	for i := range rows {
		e := &rows[i]
		out = append(out, GoalEvidenceView{
			ID:              e.ID,
			GoalID:          e.GoalID,
			LoopRunID:       e.LoopRunID,
			IterationNumber: e.IterationNumber,
			EvidenceType:    e.EvidenceType,
			RefTable:        e.RefTable,
			RefID:           e.RefID,
			Result:          e.Result,
			Summary:         e.Summary,
			RecordedAt:      e.RecordedAt,
		})
	}
	return out
}

// LoopRunView is the wire shape of one loop_runs row.
type LoopRunView struct {
	ID                     string `json:"id"`
	GoalID                 string `json:"goal_id"`
	DefinitionName         string `json:"definition_name"`
	Status                 string `json:"status"`
	CurrentIteration       int    `json:"current_iteration"`
	BudgetJSON             string `json:"budget_json"`
	ContinuationPolicyJSON string `json:"continuation_policy_json"`
	NoProgressStreak       int    `json:"no_progress_streak"`
	StartedAt              string `json:"started_at"`
	UpdatedAt              string `json:"updated_at"`
	CompletedAt            string `json:"completed_at,omitempty"`
}

func loopRunToView(lr *store.LoopRun) LoopRunView {
	return LoopRunView{
		ID:                     lr.ID,
		GoalID:                 lr.GoalID,
		DefinitionName:         lr.DefinitionName,
		Status:                 lr.Status,
		CurrentIteration:       lr.CurrentIteration,
		BudgetJSON:             lr.BudgetJSON,
		ContinuationPolicyJSON: lr.ContinuationPolicyJSON,
		NoProgressStreak:       lr.NoProgressStreak,
		StartedAt:              lr.StartedAt,
		UpdatedAt:              lr.UpdatedAt,
		CompletedAt:            lr.CompletedAt,
	}
}

func loopRunsToView(rows []store.LoopRun) []LoopRunView {
	out := make([]LoopRunView, 0, len(rows))
	for i := range rows {
		out = append(out, loopRunToView(&rows[i]))
	}
	return out
}

// LoopRunIterationView is the wire shape of one loop_run_iterations row.
type LoopRunIterationView struct {
	ID              string `json:"id"`
	LoopRunID       string `json:"loop_run_id"`
	IterationNumber int    `json:"iteration_number"`
	WorkflowRunID   string `json:"workflow_run_id,omitempty"`
	Decision        string `json:"decision,omitempty"`
	ProgressState   string `json:"progress_state,omitempty"`
	EvaluationJSON  string `json:"evaluation_json"`
	StartedAt       string `json:"started_at"`
	CompletedAt     string `json:"completed_at,omitempty"`
}

func loopRunIterationsToView(rows []store.LoopRunIteration) []LoopRunIterationView {
	out := make([]LoopRunIterationView, 0, len(rows))
	for i := range rows {
		it := &rows[i]
		out = append(out, LoopRunIterationView{
			ID:              it.ID,
			LoopRunID:       it.LoopRunID,
			IterationNumber: it.IterationNumber,
			WorkflowRunID:   it.WorkflowRunID,
			Decision:        it.Decision,
			ProgressState:   it.ProgressState,
			EvaluationJSON:  it.EvaluationJSON,
			StartedAt:       it.StartedAt,
			CompletedAt:     it.CompletedAt,
		})
	}
	return out
}
