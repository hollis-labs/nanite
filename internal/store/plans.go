package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Plan represents an internal plan scoped to workspace, project, or session.
type Plan struct {
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

// PlanStep represents a single step within a plan.
type PlanStep struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"` // pending, in_progress, done, skipped
	TodoID     string   `json:"todo_id,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
	Acceptance string   `json:"acceptance,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

// PlanFilter controls which plans to retrieve.
type PlanFilter struct {
	Scope   string
	ScopeID string
	Status  string
}

// ParsePlanSteps parses the JSON steps string into a slice of PlanStep.
func (p *Plan) ParsePlanSteps() ([]PlanStep, error) {
	var steps []PlanStep
	if p.Steps == "" || p.Steps == "[]" {
		return steps, nil
	}
	if err := json.Unmarshal([]byte(p.Steps), &steps); err != nil {
		return nil, fmt.Errorf("parse plan steps: %w", err)
	}
	return steps, nil
}

// SetPlanSteps serializes the steps slice back to JSON.
func (p *Plan) SetPlanSteps(steps []PlanStep) error {
	data, err := json.Marshal(steps)
	if err != nil {
		return fmt.Errorf("marshal plan steps: %w", err)
	}
	p.Steps = string(data)
	return nil
}

// CreatePlan inserts a new plan, auto-generating the ID if empty.
func (s *Store) CreatePlan(p *Plan) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if p.Status == "" {
		p.Status = "proposed"
	}
	if p.Steps == "" {
		p.Steps = "[]"
	}
	if p.Metadata == "" {
		p.Metadata = "{}"
	}
	if p.CreatedBy == "" {
		p.CreatedBy = "user"
	}
	p.CreatedAt = now
	p.UpdatedAt = now

	_, err := s.DB.Exec(
		`INSERT INTO plans (id, scope, scope_id, title, description, status, steps, metadata, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Scope, p.ScopeID, p.Title, p.Description, p.Status,
		p.Steps, p.Metadata, p.CreatedBy, now, now,
	)
	if err != nil {
		return fmt.Errorf("create plan: %w", err)
	}
	return nil
}

// GetPlan returns a single plan by ID.
func (s *Store) GetPlan(id string) (*Plan, error) {
	var p Plan
	err := s.DB.QueryRow(
		`SELECT id, scope, scope_id, title, description, status, steps, metadata,
		        created_by, created_at, updated_at
		 FROM plans WHERE id = ?`, id,
	).Scan(
		&p.ID, &p.Scope, &p.ScopeID, &p.Title, &p.Description, &p.Status,
		&p.Steps, &p.Metadata, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get plan %s: %w", id, err)
	}
	return &p, nil
}

// ListPlans returns plans matching the given filter.
func (s *Store) ListPlans(f PlanFilter) ([]Plan, error) {
	query := `SELECT id, scope, scope_id, title, description, status, steps, metadata,
	                 created_by, created_at, updated_at
	          FROM plans WHERE 1=1`
	var args []any

	if f.Scope != "" {
		query += ` AND scope = ?`
		args = append(args, f.Scope)
	}
	if f.ScopeID != "" {
		query += ` AND scope_id = ?`
		args = append(args, f.ScopeID)
	}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}

	query += ` ORDER BY created_at DESC`

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()

	out := make([]Plan, 0)
	for rows.Next() {
		var p Plan
		if err := rows.Scan(
			&p.ID, &p.Scope, &p.ScopeID, &p.Title, &p.Description, &p.Status,
			&p.Steps, &p.Metadata, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan plan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdatePlan updates mutable fields on a plan.
func (s *Store) UpdatePlan(p *Plan) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE plans SET title = ?, description = ?, status = ?, steps = ?,
		        metadata = ?, updated_at = ?
		 WHERE id = ?`,
		p.Title, p.Description, p.Status, p.Steps,
		p.Metadata, now, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update plan %s: %w", p.ID, err)
	}
	p.UpdatedAt = now
	return nil
}

// UpdatePlanStep updates a single step within a plan by step ID.
func (s *Store) UpdatePlanStep(planID, stepID string, updates PlanStep) error {
	p, err := s.GetPlan(planID)
	if err != nil {
		return err
	}

	steps, err := p.ParsePlanSteps()
	if err != nil {
		return err
	}

	found := false
	for i, step := range steps {
		if step.ID == stepID {
			if updates.Title != "" {
				steps[i].Title = updates.Title
			}
			if updates.Status != "" {
				steps[i].Status = updates.Status
			}
			if updates.TodoID != "" {
				steps[i].TodoID = updates.TodoID
			}
			if updates.Notes != "" {
				steps[i].Notes = updates.Notes
			}
			if updates.Acceptance != "" {
				steps[i].Acceptance = updates.Acceptance
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("step %s not found in plan %s", stepID, planID)
	}

	if err := p.SetPlanSteps(steps); err != nil {
		return err
	}
	return s.UpdatePlan(p)
}

// DeletePlan removes a plan by ID.
func (s *Store) DeletePlan(id string) error {
	_, err := s.DB.Exec(`DELETE FROM plans WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete plan %s: %w", id, err)
	}
	return nil
}
