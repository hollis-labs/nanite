package api

import "github.com/hollis-labs/nanite/internal/store"

// AgentReflexView and PendingReflexView are the API-owned wire shapes of a
// reflex definition and a pending reflex. Their keys match what the store
// rows used to emit directly, so the wire did not change when the rows
// stopped riding onto it; a new column reaches the wire only once it is added
// here and to its translator. TestReflexViewJSONKeys pins the key sets.
//
// RecurrenceOverrideSeconds stays a pointer: an unset override has always
// serialized as null. The list translators always return a non-nil slice:
// the store lists return an empty slice, never nil.

// AgentReflexView is the wire shape of one agent_reflexes row.
type AgentReflexView struct {
	ID                        string `json:"id"`
	AgentID                   string `json:"agent_id"`
	ClassTag                  string `json:"class_tag"`
	Name                      string `json:"name"`
	TriggerKind               string `json:"trigger_kind"`
	TriggerSpec               string `json:"trigger_spec"`
	ActionKind                string `json:"action_kind"`
	ActionSpec                string `json:"action_spec"`
	Status                    string `json:"status"`
	Priority                  int64  `json:"priority"`
	FiredCount                int64  `json:"fired_count"`
	LastFiredAt               string `json:"last_fired_at"`
	CreatedAt                 string `json:"created_at"`
	CreatedBy                 string `json:"created_by"`
	OptOutAllowed             bool   `json:"opt_out_allowed"`
	ProvenanceTier            string `json:"provenance_tier"`
	RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds"`
	WorkflowRunID             string `json:"workflow_run_id"`
}

func agentReflexToView(r *store.AgentReflex) AgentReflexView {
	return AgentReflexView{
		ID:                        r.ID,
		AgentID:                   r.AgentID,
		ClassTag:                  r.ClassTag,
		Name:                      r.Name,
		TriggerKind:               r.TriggerKind,
		TriggerSpec:               r.TriggerSpec,
		ActionKind:                r.ActionKind,
		ActionSpec:                r.ActionSpec,
		Status:                    r.Status,
		Priority:                  r.Priority,
		FiredCount:                r.FiredCount,
		LastFiredAt:               r.LastFiredAt,
		CreatedAt:                 r.CreatedAt,
		CreatedBy:                 r.CreatedBy,
		OptOutAllowed:             r.OptOutAllowed,
		ProvenanceTier:            r.ProvenanceTier,
		RecurrenceOverrideSeconds: r.RecurrenceOverrideSeconds,
		WorkflowRunID:             r.WorkflowRunID,
	}
}

func agentReflexesToView(rows []store.AgentReflex) []AgentReflexView {
	out := make([]AgentReflexView, 0, len(rows))
	for i := range rows {
		out = append(out, agentReflexToView(&rows[i]))
	}
	return out
}

// PendingReflexView is the wire shape of one pending_reflexes row.
type PendingReflexView struct {
	ID            string `json:"id"`
	ProposedBy    string `json:"proposed_by"`
	ProposedAt    string `json:"proposed_at"`
	TargetAgentID string `json:"target_agent_id"`
	Name          string `json:"name"`
	TriggerKind   string `json:"trigger_kind"`
	TriggerSpec   string `json:"trigger_spec"`
	ActionKind    string `json:"action_kind"`
	ActionSpec    string `json:"action_spec"`
	Rationale     string `json:"rationale"`
	Status        string `json:"status"`
	ReviewedAt    string `json:"reviewed_at"`
	ReviewedBy    string `json:"reviewed_by"`
}

func pendingReflexesToView(rows []store.PendingReflex) []PendingReflexView {
	out := make([]PendingReflexView, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, PendingReflexView{
			ID:            r.ID,
			ProposedBy:    r.ProposedBy,
			ProposedAt:    r.ProposedAt,
			TargetAgentID: r.TargetAgentID,
			Name:          r.Name,
			TriggerKind:   r.TriggerKind,
			TriggerSpec:   r.TriggerSpec,
			ActionKind:    r.ActionKind,
			ActionSpec:    r.ActionSpec,
			Rationale:     r.Rationale,
			Status:        r.Status,
			ReviewedAt:    r.ReviewedAt,
			ReviewedBy:    r.ReviewedBy,
		})
	}
	return out
}
