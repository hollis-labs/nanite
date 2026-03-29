package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CustomAction is a user-defined automation binding that can be triggered
// by a keybinding, slash command, or auto-trigger (event).
type CustomAction struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Keybinding   string `json:"keybinding"`    // binding string: "mod+shift+e"
	Command      string `json:"command"`       // text to inject or action ref
	SlashCommand string `json:"slash_command"` // auto-register as /name (empty = no slash cmd)
	AutoTriggers string `json:"auto_triggers"` // JSON array: ["on_new_session", "on_agent_switch"]
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Known auto-trigger values. Actions with these triggers fire automatically
// when the corresponding event occurs.
const (
	AutoTriggerNewSession  = "on_new_session"
	AutoTriggerAgentSwitch = "on_agent_switch"
	AutoTriggerModeChange  = "on_mode_change"
)

// CreateCustomAction inserts a new custom action.
func (s *Store) CreateCustomAction(action *CustomAction) error {
	if action.ID == "" {
		action.ID = uuid.NewString()
	}
	if action.AutoTriggers == "" {
		action.AutoTriggers = "[]"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(`INSERT INTO custom_actions
		(id, name, description, keybinding, command, slash_command, auto_triggers, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.ID, action.Name, action.Description, action.Keybinding,
		action.Command, action.SlashCommand, action.AutoTriggers,
		action.Enabled, now, now,
	)
	if err != nil {
		return fmt.Errorf("create custom action: %w", err)
	}
	action.CreatedAt = now
	action.UpdatedAt = now
	return nil
}

// GetCustomAction retrieves a custom action by ID.
func (s *Store) GetCustomAction(id string) (*CustomAction, error) {
	var a CustomAction
	err := s.DB.QueryRow(`SELECT id, name, description, keybinding, command,
		slash_command, auto_triggers, enabled, created_at, updated_at
		FROM custom_actions WHERE id = ?`, id).Scan(
		&a.ID, &a.Name, &a.Description, &a.Keybinding, &a.Command,
		&a.SlashCommand, &a.AutoTriggers, &a.Enabled, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("custom action %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get custom action: %w", err)
	}
	return &a, nil
}

// UpdateCustomAction updates a custom action by ID.
func (s *Store) UpdateCustomAction(action *CustomAction) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE custom_actions SET
		name = ?, description = ?, keybinding = ?, command = ?,
		slash_command = ?, auto_triggers = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		action.Name, action.Description, action.Keybinding, action.Command,
		action.SlashCommand, action.AutoTriggers, action.Enabled, now,
		action.ID,
	)
	if err != nil {
		return fmt.Errorf("update custom action: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("custom action %q not found", action.ID)
	}
	action.UpdatedAt = now
	return nil
}

// DeleteCustomAction removes a custom action by ID.
func (s *Store) DeleteCustomAction(id string) error {
	res, err := s.DB.Exec(`DELETE FROM custom_actions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete custom action: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("custom action %q not found", id)
	}
	return nil
}

// ListCustomActions returns all custom actions.
func (s *Store) ListCustomActions() ([]CustomAction, error) {
	rows, err := s.DB.Query(`SELECT id, name, description, keybinding, command,
		slash_command, auto_triggers, enabled, created_at, updated_at
		FROM custom_actions ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("list custom actions: %w", err)
	}
	defer rows.Close()

	out := make([]CustomAction, 0)
	for rows.Next() {
		var a CustomAction
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Description, &a.Keybinding, &a.Command,
			&a.SlashCommand, &a.AutoTriggers, &a.Enabled, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan custom action: %w", err)
		}
		out = append(out, a)
	}
	return out, nil
}

// ListCustomActionsByTrigger returns all enabled custom actions that have
// the given auto-trigger in their auto_triggers JSON array.
func (s *Store) ListCustomActionsByTrigger(trigger string) ([]CustomAction, error) {
	// SQLite JSON: json_each expands the array, we match against the value.
	rows, err := s.DB.Query(`SELECT ca.id, ca.name, ca.description, ca.keybinding,
		ca.command, ca.slash_command, ca.auto_triggers, ca.enabled, ca.created_at, ca.updated_at
		FROM custom_actions ca, json_each(ca.auto_triggers) je
		WHERE je.value = ? AND ca.enabled = 1
		ORDER BY ca.name ASC`, trigger)
	if err != nil {
		return nil, fmt.Errorf("list custom actions by trigger: %w", err)
	}
	defer rows.Close()

	out := make([]CustomAction, 0)
	for rows.Next() {
		var a CustomAction
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Description, &a.Keybinding, &a.Command,
			&a.SlashCommand, &a.AutoTriggers, &a.Enabled, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan custom action: %w", err)
		}
		out = append(out, a)
	}
	return out, nil
}
