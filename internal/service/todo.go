package service

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// TodoService manages internal todos and plans.
type TodoService interface {
	// Todos
	CreateTodo(ctx context.Context, t *store.Todo) error
	GetTodo(ctx context.Context, id string) (*store.Todo, error)
	ListTodos(ctx context.Context, f store.TodoFilter) ([]store.Todo, error)
	UpdateTodo(ctx context.Context, id string, updates TodoUpdates) (*store.Todo, error)
	UpdateTodoScope(ctx context.Context, id, scope, scopeID, projectID string) (*store.Todo, error)
	DeleteTodo(ctx context.Context, id string) error
	ListTodoChildren(ctx context.Context, parentID string) ([]store.Todo, error)

	// Plans
	CreatePlan(ctx context.Context, p *store.Plan) error
	GetPlan(ctx context.Context, id string) (*store.Plan, error)
	ListPlans(ctx context.Context, f store.PlanFilter) ([]store.Plan, error)
	UpdatePlan(ctx context.Context, id string, updates PlanUpdates) (*store.Plan, error)
	UpdatePlanStep(ctx context.Context, planID, stepID string, updates store.PlanStep) error
	DeletePlan(ctx context.Context, id string) error
	ApprovePlan(ctx context.Context, id string, createTodos bool) (*store.Plan, error)
}

// TodoUpdates holds optional fields for partial todo updates.
type TodoUpdates struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Priority    *string `json:"priority"`
	Labels      *string `json:"labels"`
	Metadata    *string `json:"metadata"`
}

// PlanUpdates holds optional fields for partial plan updates.
type PlanUpdates struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Steps       *string `json:"steps"`
	Metadata    *string `json:"metadata"`
}

// TodoServiceConfig holds dependencies for the todo service.
type TodoServiceConfig struct {
	Todos TodoStore
	Plans PlanStore
}

type todoServiceImpl struct {
	todos TodoStore
	plans PlanStore
}

// NewTodoService creates a new TodoService.
func NewTodoService(cfg TodoServiceConfig) TodoService {
	return &todoServiceImpl{
		todos: cfg.Todos,
		plans: cfg.Plans,
	}
}

// --- Todo operations ---

func (s *todoServiceImpl) CreateTodo(_ context.Context, t *store.Todo) error {
	if t.Title == "" {
		return fmt.Errorf("title is required")
	}
	if t.Scope == "" {
		t.Scope = store.TodoScopeSession
	}
	if !validScope(t.Scope) {
		return fmt.Errorf("invalid scope %q: must be turn, session, or project", t.Scope)
	}
	if t.ScopeID == "" {
		return fmt.Errorf("scope_id is required for scope %q", t.Scope)
	}
	if t.Scope == store.TodoScopeProject && t.ProjectID == "" {
		t.ProjectID = t.ScopeID
	}
	// Validate parent exists if specified.
	if t.ParentID != "" {
		parent, err := s.todos.GetTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, t.ParentID)
		if err != nil {
			return fmt.Errorf("parent_id %q not found: %w", t.ParentID, err)
		}
		// Ensure parent is in the same scope.
		if parent.Scope != t.Scope || parent.ScopeID != t.ScopeID {
			return fmt.Errorf("parent todo must be in the same scope")
		}
	}
	return s.todos.CreateTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, t)
}

func (s *todoServiceImpl) GetTodo(_ context.Context, id string) (*store.Todo, error) {
	return s.todos.GetTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
}

func (s *todoServiceImpl) ListTodos(_ context.Context, f store.TodoFilter) ([]store.Todo, error) {
	return s.todos.ListTodos(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, f)
}

func (s *todoServiceImpl) UpdateTodo(_ context.Context, id string, updates TodoUpdates) (*store.Todo, error) {
	existing, err := s.todos.GetTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
	if err != nil {
		return nil, fmt.Errorf("todo not found: %w", err)
	}
	if updates.Title != nil {
		existing.Title = *updates.Title
	}
	if updates.Description != nil {
		existing.Description = *updates.Description
	}
	if updates.Status != nil {
		if !validTodoStatus(*updates.Status) {
			return nil, fmt.Errorf("invalid status %q", *updates.Status)
		}
		existing.Status = *updates.Status
	}
	if updates.Priority != nil {
		if !validPriority(*updates.Priority) {
			return nil, fmt.Errorf("invalid priority %q", *updates.Priority)
		}
		existing.Priority = *updates.Priority
	}
	if updates.Labels != nil {
		existing.Labels = *updates.Labels
	}
	if updates.Metadata != nil {
		existing.Metadata = *updates.Metadata
	}
	if err := s.todos.UpdateTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// UpdateTodoScope promotes/demotes a todo between session and project scope.
// Powers the "Promote to project" / "Demote to session" actions in D2.
func (s *todoServiceImpl) UpdateTodoScope(_ context.Context, id, scope, scopeID, projectID string) (*store.Todo, error) {
	if !validScope(scope) {
		return nil, fmt.Errorf("invalid scope %q: must be turn, session, or project", scope)
	}
	if scope == store.TodoScopeProject && projectID == "" {
		return nil, fmt.Errorf("project_id is required for scope=project")
	}
	if scope != store.TodoScopeProject && scopeID == "" {
		return nil, fmt.Errorf("scope_id is required for scope=%q", scope)
	}
	if scope == store.TodoScopeProject && scopeID == "" {
		scopeID = projectID
	}
	if err := s.todos.UpdateTodoScope(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id, scope, scopeID, projectID); err != nil {
		return nil, err
	}
	return s.todos.GetTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
}

func (s *todoServiceImpl) DeleteTodo(_ context.Context, id string) error {
	return s.todos.DeleteTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
}

func (s *todoServiceImpl) ListTodoChildren(_ context.Context, parentID string) ([]store.Todo, error) {
	return s.todos.ListTodoChildren(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, parentID)
}

// --- Plan operations ---

func (s *todoServiceImpl) CreatePlan(_ context.Context, p *store.Plan) error {
	if p.Title == "" {
		return fmt.Errorf("title is required")
	}
	if p.Scope == "" {
		return fmt.Errorf("scope is required")
	}
	if !validPlanScope(p.Scope) {
		return fmt.Errorf("invalid scope %q: must be workspace, project, or session", p.Scope)
	}
	if p.Scope != "workspace" && p.ScopeID == "" {
		return fmt.Errorf("scope_id is required for scope %q", p.Scope)
	}
	return s.plans.CreatePlan(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, p)
}

func (s *todoServiceImpl) GetPlan(_ context.Context, id string) (*store.Plan, error) {
	return s.plans.GetPlan(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
}

func (s *todoServiceImpl) ListPlans(_ context.Context, f store.PlanFilter) ([]store.Plan, error) {
	return s.plans.ListPlans(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, f)
}

func (s *todoServiceImpl) UpdatePlan(_ context.Context, id string, updates PlanUpdates) (*store.Plan, error) {
	existing, err := s.plans.GetPlan(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %w", err)
	}
	if updates.Title != nil {
		existing.Title = *updates.Title
	}
	if updates.Description != nil {
		existing.Description = *updates.Description
	}
	if updates.Status != nil {
		if !validPlanStatus(*updates.Status) {
			return nil, fmt.Errorf("invalid status %q", *updates.Status)
		}
		existing.Status = *updates.Status
	}
	if updates.Steps != nil {
		existing.Steps = *updates.Steps
	}
	if updates.Metadata != nil {
		existing.Metadata = *updates.Metadata
	}
	if err := s.plans.UpdatePlan(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *todoServiceImpl) UpdatePlanStep(_ context.Context, planID, stepID string, updates store.PlanStep) error {
	return s.plans.UpdatePlanStep(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, planID, stepID, updates)
}

func (s *todoServiceImpl) DeletePlan(_ context.Context, id string) error {
	return s.plans.DeletePlan(context.

		// ApprovePlan transitions a plan from proposed to approved and optionally creates todos from steps.
		TODO(), id)
}

func (s *todoServiceImpl) ApprovePlan(_ context.Context, id string, createTodos bool) (*store.Plan, error) {
	plan, err := s.plans.GetPlan(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %w", err)
	}
	if plan.Status != "proposed" {
		return nil, fmt.Errorf("plan must be in 'proposed' status to approve, current: %q", plan.Status)
	}

	plan.Status = "approved"

	if createTodos {
		steps, err := plan.ParsePlanSteps()
		if err != nil {
			return nil, fmt.Errorf("parse steps: %w", err)
		}
		for i, step := range steps {
			if step.Title == "" {
				continue
			}
			todo := &store.Todo{
				Scope:       plan.Scope,
				ScopeID:     plan.ScopeID,
				Title:       step.Title,
				Description: step.Acceptance,
				CreatedBy:   plan.CreatedBy,
			}
			if err := s.todos.CreateTodo(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, todo); err != nil {
				return nil, fmt.Errorf("create todo for step %s: %w", step.ID, err)
			}
			steps[i].TodoID = todo.ID
		}
		if err := plan.SetPlanSteps(steps); err != nil {
			return nil, err
		}
	}

	if err := s.plans.UpdatePlan(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// --- helpers ---

// validScope returns true for the D1 todo scope set (turn|session|project).
// Workspace scope was retired in migration 043.
func validScope(s string) bool {
	return s == store.TodoScopeTurn || s == store.TodoScopeSession || s == store.TodoScopeProject
}

// validPlanScope retains the legacy plan scope set (workspace|project|session).
// Plans were intentionally left out of D1's scope refactor (Out of scope:
// "Backfill logic" — plans aren't on the D1 spec).
func validPlanScope(s string) bool {
	return s == "workspace" || s == "project" || s == "session"
}

func validTodoStatus(s string) bool {
	return s == "pending" || s == "in_progress" || s == "done" || s == "blocked"
}

func validPriority(s string) bool {
	return s == "low" || s == "medium" || s == "high" || s == "critical"
}

func validPlanStatus(s string) bool {
	return s == "proposed" || s == "approved" || s == "in_progress" || s == "complete" || s == "abandoned"
}
