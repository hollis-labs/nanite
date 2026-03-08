package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Template represents a user-defined output template.
type Template struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Template  string `json:"template"`
	IsBuiltin bool   `json:"is_builtin"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// BuiltinTemplates are seeded on startup.
var BuiltinTemplates = []Template{
	{
		Name: "task-summary",
		Template: `## Task Summary

**Task:** {{.Title}}
**Status:** {{.Status}}

### Key Points
{{.Content}}

### Next Steps
- [ ] Review the above
- [ ] Follow up as needed
`,
	},
	{
		Name: "code-review",
		Template: `## Code Review

**File(s):** {{.Title}}

### Changes
{{.Content}}

### Feedback
| Area | Rating | Notes |
|------|--------|-------|
| Correctness | — | |
| Readability | — | |
| Performance | — | |

### Summary
_Review pending._
`,
	},
	{
		Name: "standup",
		Template: `## Standup Update

**Date:** {{.Date}}

### Yesterday
{{.Content}}

### Today
- [ ] _Plan your day_

### Blockers
- None
`,
	},
}

// SeedBuiltinTemplates inserts built-in templates if they don't exist.
func (s *Store) SeedBuiltinTemplates() error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, t := range BuiltinTemplates {
		_, err := s.DB.Exec(
			`INSERT OR IGNORE INTO templates (id, name, template, is_builtin, created_at, updated_at)
			 VALUES (?, ?, ?, 1, ?, ?)`,
			uuid.New().String(), t.Name, t.Template, now, now,
		)
		if err != nil {
			return fmt.Errorf("seed template %s: %w", t.Name, err)
		}
	}
	return nil
}

// ListTemplates returns all templates ordered by name.
func (s *Store) ListTemplates() ([]Template, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, template, is_builtin, created_at, updated_at
		 FROM templates ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()

	out := make([]Template, 0)
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Template, &t.IsBuiltin, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate returns a template by name.
func (s *Store) GetTemplate(name string) (*Template, error) {
	var t Template
	err := s.DB.QueryRow(
		`SELECT id, name, template, is_builtin, created_at, updated_at
		 FROM templates WHERE name = ?`, name,
	).Scan(&t.ID, &t.Name, &t.Template, &t.IsBuiltin, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get template %s: %w", name, err)
	}
	return &t, nil
}

// CreateTemplate inserts a new user-defined template.
func (s *Store) CreateTemplate(t *Template) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	t.CreatedAt = now
	t.UpdatedAt = now

	_, err := s.DB.Exec(
		`INSERT INTO templates (id, name, template, is_builtin, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?)`,
		t.ID, t.Name, t.Template, now, now,
	)
	if err != nil {
		return fmt.Errorf("create template: %w", err)
	}
	return nil
}

// UpdateTemplate updates an existing template's text.
func (s *Store) UpdateTemplate(name, templateText string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE templates SET template = ?, updated_at = ? WHERE name = ?`,
		templateText, now, name,
	)
	if err != nil {
		return fmt.Errorf("update template %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("template %q not found", name)
	}
	return nil
}

// DeleteTemplate removes a template by name (only non-builtin).
func (s *Store) DeleteTemplate(name string) error {
	res, err := s.DB.Exec(
		`DELETE FROM templates WHERE name = ? AND is_builtin = 0`, name,
	)
	if err != nil {
		return fmt.Errorf("delete template %s: %w", name, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("template %q not found or is built-in", name)
	}
	return nil
}
