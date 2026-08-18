package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrToolEnrichmentNotFound is returned by GetToolEnrichment when the named tool has no record.
var ErrToolEnrichmentNotFound = errors.New("tool enrichment not found")

// ToolEnrichment is the row in the tool_enrichments table. HintsJSON is the
// serialized broker.Hints struct (from github.com/hollis-labs/go-toolbroker);
// callers should marshal/unmarshal via broker.MarshalHints / broker.UnmarshalHints.
type ToolEnrichment struct {
	ToolName  string    `json:"tool_name"`
	HintsJSON string    `json:"hints_json"`
	UpdatedAt time.Time `json:"updated_at"`
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
	t, err := time.Parse(time.RFC3339Nano, updatedAtStr)
	if err != nil {
		return ToolEnrichment{}, fmt.Errorf("parse updated_at: %w", err)
	}
	rec.UpdatedAt = t
	return rec, nil
}

// Write-side CRUD (UpsertToolEnrichment/DeleteToolEnrichment/ListToolEnrichments)
// was cut in 18a-cut-dead-storage-and-config: zero callers anywhere in the
// codebase ever populated or managed tool_enrichments rows, so the table can
// never hold real data through this app. GetToolEnrichment above stays — it's
// wired into the tool broker's enrichment lookup (internal/toolclient/enricher.go)
// and is live in the hot path of every tool-catalog assembly.
