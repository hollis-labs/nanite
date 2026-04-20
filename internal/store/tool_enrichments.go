package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrToolEnrichmentNotFound is returned by GetToolEnrichment when the named tool has no record.
var ErrToolEnrichmentNotFound = errors.New("tool enrichment not found")

// ToolEnrichment is the row in the tool_enrichments table. HintsJSON is the serialized
// internal/tool/enrichment.Hints struct; callers should marshal/unmarshal via that package.
type ToolEnrichment struct {
	ToolName  string
	HintsJSON string
	UpdatedAt time.Time
}

// GetToolEnrichment loads the enrichment record for a tool by name.
// Returns ErrToolEnrichmentNotFound if no row exists.
func (s *Store) GetToolEnrichment(toolName string) (ToolEnrichment, error) {
	var rec ToolEnrichment
	var updatedAtStr string
	err := s.DB.QueryRow(
		`SELECT tool_name, hints_json, updated_at FROM tool_enrichments WHERE tool_name = ?`,
		toolName,
	).Scan(&rec.ToolName, &rec.HintsJSON, &updatedAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolEnrichment{}, ErrToolEnrichmentNotFound
	}
	if err != nil {
		return ToolEnrichment{}, fmt.Errorf("get tool enrichment: %w", err)
	}
	t, err := time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return ToolEnrichment{}, fmt.Errorf("parse updated_at: %w", err)
	}
	rec.UpdatedAt = t
	return rec, nil
}

// UpsertToolEnrichment inserts or replaces the enrichment record for a tool.
func (s *Store) UpsertToolEnrichment(rec ToolEnrichment) error {
	if rec.ToolName == "" {
		return fmt.Errorf("upsert tool enrichment: tool_name is required")
	}
	if rec.HintsJSON == "" {
		rec.HintsJSON = "{}"
	}
	updatedAt := rec.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := s.DB.Exec(
		`INSERT INTO tool_enrichments (tool_name, hints_json, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(tool_name) DO UPDATE SET hints_json = excluded.hints_json, updated_at = excluded.updated_at`,
		rec.ToolName, rec.HintsJSON, updatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert tool enrichment: %w", err)
	}
	return nil
}

// ListToolEnrichments returns all enrichment records ordered by updated_at DESC.
func (s *Store) ListToolEnrichments() ([]ToolEnrichment, error) {
	rows, err := s.DB.Query(
		`SELECT tool_name, hints_json, updated_at FROM tool_enrichments ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list tool enrichments: %w", err)
	}
	defer rows.Close()

	var out []ToolEnrichment
	for rows.Next() {
		var rec ToolEnrichment
		var updatedAtStr string
		if err := rows.Scan(&rec.ToolName, &rec.HintsJSON, &updatedAtStr); err != nil {
			return nil, fmt.Errorf("scan tool enrichment: %w", err)
		}
		t, err := time.Parse(time.RFC3339, updatedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at for %s: %w", rec.ToolName, err)
		}
		rec.UpdatedAt = t
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteToolEnrichment removes the enrichment record for a tool. No error if not present.
func (s *Store) DeleteToolEnrichment(toolName string) error {
	_, err := s.DB.Exec(`DELETE FROM tool_enrichments WHERE tool_name = ?`, toolName)
	if err != nil {
		return fmt.Errorf("delete tool enrichment: %w", err)
	}
	return nil
}
