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

// AppendPlanSteps appends one or more steps to an existing plan without
// re-ordering or mutating any existing steps. New steps with empty IDs are
// auto-assigned (`s<n>` where n is one past the highest existing numeric
// suffix, or a UUID fallback when no numeric suffix exists). New step IDs
// that collide with an existing step ID return an error rather than
// silently overwriting.
//
// CW-20260430-0001 (SP1) — closes the c120 workaround where the agent
// deleted and recreated a plan to add a single step.
func (s *Store) AppendPlanSteps(planID string, newSteps []PlanStep) ([]PlanStep, error) {
	if len(newSteps) == 0 {
		return nil, fmt.Errorf("at least one step is required")
	}
	p, err := s.GetPlan(planID)
	if err != nil {
		return nil, err
	}

	existing, err := p.ParsePlanSteps()
	if err != nil {
		return nil, err
	}

	existingIDs := make(map[string]bool, len(existing))
	for _, st := range existing {
		existingIDs[st.ID] = true
	}

	appended := make([]PlanStep, 0, len(newSteps))
	for i, ns := range newSteps {
		if ns.Title == "" {
			return nil, fmt.Errorf("step %d: title is required", i)
		}
		if ns.ID == "" {
			ns.ID = nextStepID(existing, appended)
		}
		if existingIDs[ns.ID] {
			return nil, fmt.Errorf("step %d: id %q collides with an existing step", i, ns.ID)
		}
		// Also dedupe within the appended batch.
		for _, prior := range appended {
			if prior.ID == ns.ID {
				return nil, fmt.Errorf("step %d: id %q collides with another new step in the same call", i, ns.ID)
			}
		}
		if ns.Status == "" {
			ns.Status = "pending"
		}
		appended = append(appended, ns)
	}

	combined := make([]PlanStep, 0, len(existing)+len(appended))
	combined = append(combined, existing...)
	combined = append(combined, appended...)

	if err := p.SetPlanSteps(combined); err != nil {
		return nil, err
	}
	if err := s.UpdatePlan(p); err != nil {
		return nil, err
	}
	return appended, nil
}

// nextStepID returns a deterministic `s<n>` ID one past the highest
// numeric-suffixed ID seen across existing+pending steps, falling back to
// a UUID when no suffixed IDs are detected. Designed so plans authored
// with the conventional s1/s2/s3 scheme keep producing s4/s5/... when the
// agent appends without supplying explicit IDs.
func nextStepID(existing, pending []PlanStep) string {
	maxN := 0
	saw := false
	scan := func(steps []PlanStep) {
		for _, st := range steps {
			if len(st.ID) < 2 || st.ID[0] != 's' {
				continue
			}
			n := 0
			for i := 1; i < len(st.ID); i++ {
				c := st.ID[i]
				if c < '0' || c > '9' {
					n = 0
					break
				}
				n = n*10 + int(c-'0')
			}
			if n > 0 {
				saw = true
				if n > maxN {
					maxN = n
				}
			}
		}
	}
	scan(existing)
	scan(pending)
	if saw {
		return fmt.Sprintf("s%d", maxN+1)
	}
	return uuid.New().String()
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
