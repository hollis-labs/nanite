package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TriggerRule defines a binding between an event type and a connector.
// When the event fires, the connector is called with the templated payload.
type TriggerRule struct {
	ID              string `json:"id"`
	PluginID        string `json:"plugin_id"`
	EventType       string `json:"event_type"`
	ConnectorName   string `json:"connector_name"`
	PayloadTemplate string `json:"payload_template"` // JSON template with {{.Field}} placeholders
	FilterExpr      string `json:"filter_expr"`      // optional: simple field=value filter expression
	Enabled         bool   `json:"enabled"`
	Description     string `json:"description"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// CreateTriggerRule inserts a new trigger rule.
func (s *Store) CreateTriggerRule(rule *TriggerRule) error {
	if rule.ID == "" {
		rule.ID = uuid.NewString()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(`INSERT INTO trigger_rules
		(id, plugin_id, event_type, connector_name, payload_template, filter_expr, enabled, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.PluginID, rule.EventType, rule.ConnectorName,
		rule.PayloadTemplate, rule.FilterExpr, rule.Enabled, rule.Description,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("create trigger rule: %w", err)
	}
	rule.CreatedAt = now
	rule.UpdatedAt = now
	return nil
}

// GetTriggerRule retrieves a trigger rule by ID.
func (s *Store) GetTriggerRule(id string) (*TriggerRule, error) {
	var r TriggerRule
	err := s.DB.QueryRow(`SELECT id, plugin_id, event_type, connector_name,
		payload_template, filter_expr, enabled, description, created_at, updated_at
		FROM trigger_rules WHERE id = ?`, id).Scan(
		&r.ID, &r.PluginID, &r.EventType, &r.ConnectorName,
		&r.PayloadTemplate, &r.FilterExpr, &r.Enabled, &r.Description,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("trigger rule %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get trigger rule: %w", err)
	}
	return &r, nil
}

// UpdateTriggerRule updates a trigger rule by ID.
func (s *Store) UpdateTriggerRule(rule *TriggerRule) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE trigger_rules SET
		event_type = ?, connector_name = ?, payload_template = ?,
		filter_expr = ?, enabled = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		rule.EventType, rule.ConnectorName, rule.PayloadTemplate,
		rule.FilterExpr, rule.Enabled, rule.Description, now,
		rule.ID,
	)
	if err != nil {
		return fmt.Errorf("update trigger rule: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("trigger rule %q not found", rule.ID)
	}
	rule.UpdatedAt = now
	return nil
}

// DeleteTriggerRule removes a trigger rule by ID.
func (s *Store) DeleteTriggerRule(id string) error {
	res, err := s.DB.Exec(`DELETE FROM trigger_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete trigger rule: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("trigger rule %q not found", id)
	}
	return nil
}

// ListTriggerRules returns all trigger rules, optionally filtered by plugin ID.
func (s *Store) ListTriggerRules(pluginID string) ([]TriggerRule, error) {
	query := `SELECT id, plugin_id, event_type, connector_name,
		payload_template, filter_expr, enabled, description, created_at, updated_at
		FROM trigger_rules`
	var args []any
	if pluginID != "" {
		query += ` WHERE plugin_id = ?`
		args = append(args, pluginID)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trigger rules: %w", err)
	}
	defer rows.Close()

	out := make([]TriggerRule, 0)
	for rows.Next() {
		var r TriggerRule
		if err := rows.Scan(
			&r.ID, &r.PluginID, &r.EventType, &r.ConnectorName,
			&r.PayloadTemplate, &r.FilterExpr, &r.Enabled, &r.Description,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan trigger rule: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// ListTriggerRulesByEvent returns all enabled trigger rules for a given event type.
func (s *Store) ListTriggerRulesByEvent(eventType string) ([]TriggerRule, error) {
	rows, err := s.DB.Query(`SELECT id, plugin_id, event_type, connector_name,
		payload_template, filter_expr, enabled, description, created_at, updated_at
		FROM trigger_rules
		WHERE event_type = ? AND enabled = 1
		ORDER BY created_at ASC`, eventType)
	if err != nil {
		return nil, fmt.Errorf("list trigger rules by event: %w", err)
	}
	defer rows.Close()

	out := make([]TriggerRule, 0)
	for rows.Next() {
		var r TriggerRule
		if err := rows.Scan(
			&r.ID, &r.PluginID, &r.EventType, &r.ConnectorName,
			&r.PayloadTemplate, &r.FilterExpr, &r.Enabled, &r.Description,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan trigger rule: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// DeleteTriggerRulesByPlugin removes all trigger rules for a given plugin.
// Used during plugin uninstall.
func (s *Store) DeleteTriggerRulesByPlugin(pluginID string) error {
	_, err := s.DB.Exec(`DELETE FROM trigger_rules WHERE plugin_id = ?`, pluginID)
	if err != nil {
		return fmt.Errorf("delete trigger rules by plugin: %w", err)
	}
	return nil
}
