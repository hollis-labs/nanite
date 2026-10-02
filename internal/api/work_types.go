package api

import "github.com/hollis-labs/nanite/internal/store"

// TodoView is the API-owned shape for todo.
type TodoView struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`    // turn, session, project
	ScopeID     string `json:"scope_id"` // session_id (turn/session) or project_id (project)
	ProjectID   string `json:"project_id,omitempty"`
	ParentID    string `json:"parent_id"` // optional parent todo for nesting
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`   // pending, in_progress, done, blocked
	Priority    string `json:"priority"` // low, medium, high, critical
	Labels      string `json:"labels"`   // JSON array of strings
	Metadata    string `json:"metadata"` // JSON object
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func todoToView(r *store.Todo) *TodoView {
	if r == nil {
		return nil
	}
	return &TodoView{
		ID:          r.ID,
		Scope:       r.Scope,
		ScopeID:     r.ScopeID,
		ProjectID:   r.ProjectID,
		ParentID:    r.ParentID,
		Title:       r.Title,
		Description: r.Description,
		Status:      r.Status,
		Priority:    r.Priority,
		Labels:      r.Labels,
		Metadata:    r.Metadata,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}
func todoToViews(rows []store.Todo) []TodoView {
	if rows == nil {
		return nil
	}
	out := make([]TodoView, len(rows))
	for i := range rows {
		out[i] = *todoToView(&rows[i])
	}
	return out
}
func (r TodoView) toStore() store.Todo {
	return store.Todo{
		ID:          r.ID,
		Scope:       r.Scope,
		ScopeID:     r.ScopeID,
		ProjectID:   r.ProjectID,
		ParentID:    r.ParentID,
		Title:       r.Title,
		Description: r.Description,
		Status:      r.Status,
		Priority:    r.Priority,
		Labels:      r.Labels,
		Metadata:    r.Metadata,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// PlanView is the API-owned shape for plan.
type PlanView struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`    // workspace, project, session
	ScopeID     string `json:"scope_id"` // empty for workspace, project_id or session_id
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"` // proposed, approved, in_progress, complete, abandoned
	Steps       string `json:"steps"`  // JSON array of PlanStep
	Metadata    string `json:"metadata"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func planToView(r *store.Plan) *PlanView {
	if r == nil {
		return nil
	}
	return &PlanView{
		ID:          r.ID,
		Scope:       r.Scope,
		ScopeID:     r.ScopeID,
		Title:       r.Title,
		Description: r.Description,
		Status:      r.Status,
		Steps:       r.Steps,
		Metadata:    r.Metadata,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}
func planToViews(rows []store.Plan) []PlanView {
	if rows == nil {
		return nil
	}
	out := make([]PlanView, len(rows))
	for i := range rows {
		out[i] = *planToView(&rows[i])
	}
	return out
}
func (r PlanView) toStore() store.Plan {
	return store.Plan{
		ID:          r.ID,
		Scope:       r.Scope,
		ScopeID:     r.ScopeID,
		Title:       r.Title,
		Description: r.Description,
		Status:      r.Status,
		Steps:       r.Steps,
		Metadata:    r.Metadata,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// PlanStepView is the API-owned shape for planstep.
type PlanStepView struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"` // pending, in_progress, done, skipped
	TodoID     string   `json:"todo_id,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
	Acceptance string   `json:"acceptance,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

func (r PlanStepView) toStore() store.PlanStep {
	return store.PlanStep{
		ID:         r.ID,
		Title:      r.Title,
		Status:     r.Status,
		TodoID:     r.TodoID,
		DependsOn:  r.DependsOn,
		Acceptance: r.Acceptance,
		Notes:      r.Notes,
	}
}
