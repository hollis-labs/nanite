package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrAgentBootPlanNotFound = errors.New("agent boot plan not found")

const AgentBootPlanSchemaVersion1 = 1

type AgentBootPlanDocument struct {
	AgentID       string               `json:"agent_id"`
	SchemaVersion int                  `json:"schema_version"`
	PlantItems    []AgentBootPlantItem `json:"plant_items"`
	Callbacks     []AgentBootCallback  `json:"callbacks"`
	CreatedAt     string               `json:"created_at"`
	UpdatedAt     string               `json:"updated_at"`
}

type AgentBootPlantItem struct {
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	SourceKind      string                  `json:"source_kind"`
	SourcePath      string                  `json:"source_path,omitempty"`
	Content         string                  `json:"content,omitempty"`
	Generator       *AgentBootGeneratorSpec `json:"generator,omitempty"`
	TargetRelPath   string                  `json:"target_rel_path"`
	EntryKind       string                  `json:"entry_kind"`
	Timing          []string                `json:"timing"`
	Secret          bool                    `json:"secret"`
	OverwritePolicy string                  `json:"overwrite_policy"`
	FailurePolicy   string                  `json:"failure_policy"`
	Enabled         bool                    `json:"enabled"`
	Metadata        map[string]string       `json:"metadata,omitempty"`
}

type AgentBootGeneratorSpec struct {
	Kind   string            `json:"kind"`
	Params map[string]string `json:"params,omitempty"`
}

type AgentBootCallback struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Timing         string                `json:"timing"`
	CallbackType   string                `json:"callback_type"`
	Command        *AgentBootCommandSpec `json:"command,omitempty"`
	ToolName       string                `json:"tool_name,omitempty"`
	ToolInput      map[string]any        `json:"tool_input,omitempty"`
	Message        string                `json:"message,omitempty"`
	Request        *AgentBootRequestSpec `json:"request,omitempty"`
	Permissions    map[string]any        `json:"permissions,omitempty"`
	TimeoutSeconds int                   `json:"timeout_seconds"`
	Env            map[string]string     `json:"env,omitempty"`
	FailurePolicy  string                `json:"failure_policy"`
	Enabled        bool                  `json:"enabled"`
}

type AgentBootCommandSpec struct {
	Argv    []string `json:"argv"`
	Workdir string   `json:"workdir,omitempty"`
}

type AgentBootRequestSpec struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type agentBootPlanRow struct {
	AgentID        string
	SchemaVersion  int
	PlantItemsJSON string
	CallbacksJSON  string
	CreatedAt      string
	UpdatedAt      string
}

func EmptyAgentBootPlan(agentID string) *AgentBootPlanDocument {
	return &AgentBootPlanDocument{
		AgentID:       agentID,
		SchemaVersion: AgentBootPlanSchemaVersion1,
		PlantItems:    []AgentBootPlantItem{},
		Callbacks:     []AgentBootCallback{},
	}
}

func (s *Store) GetAgentBootPlan(ctx context.Context, agentID string) (*AgentBootPlanDocument, error) {
	var row agentBootPlanRow
	err := s.DB.QueryRowContext(ctx, `
		SELECT agent_id, schema_version, plant_items_json, callbacks_json, created_at, updated_at
		FROM agent_boot_plans
		WHERE agent_id = ?`, agentID).
		Scan(&row.AgentID, &row.SchemaVersion, &row.PlantItemsJSON, &row.CallbacksJSON, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentBootPlanNotFound
		}
		return nil, fmt.Errorf("get agent boot plan %s: %w", agentID, err)
	}
	doc, err := decodeAgentBootPlanRow(row)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Store) PutAgentBootPlan(ctx context.Context, doc AgentBootPlanDocument) (*AgentBootPlanDocument, error) {
	if doc.AgentID == "" {
		return nil, errors.New("agent_id is required")
	}
	plantItemsJSON, err := json.Marshal(doc.PlantItems)
	if err != nil {
		return nil, fmt.Errorf("marshal plant_items: %w", err)
	}
	callbacksJSON, err := json.Marshal(doc.Callbacks)
	if err != nil {
		return nil, fmt.Errorf("marshal callbacks: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := now
	var existingCreated string
	err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM agent_boot_plans WHERE agent_id = ?`, doc.AgentID).Scan(&existingCreated)
	if err == nil && existingCreated != "" {
		createdAt = existingCreated
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup agent boot plan created_at %s: %w", doc.AgentID, err)
	}
	if _, err := s.DB.ExecContext(ctx, `
		INSERT INTO agent_boot_plans (
			agent_id, schema_version, plant_items_json, callbacks_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			schema_version = excluded.schema_version,
			plant_items_json = excluded.plant_items_json,
			callbacks_json = excluded.callbacks_json,
			updated_at = excluded.updated_at
	`, doc.AgentID, doc.SchemaVersion, string(plantItemsJSON), string(callbacksJSON), createdAt, now); err != nil {
		return nil, fmt.Errorf("upsert agent boot plan %s: %w", doc.AgentID, err)
	}
	return s.GetAgentBootPlan(ctx, doc.AgentID)
}

func (s *Store) DeleteAgentBootPlan(ctx context.Context, agentID string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM agent_boot_plans WHERE agent_id = ?`, agentID)
	if err != nil {
		return fmt.Errorf("delete agent boot plan %s: %w", agentID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent boot plan rows affected %s: %w", agentID, err)
	}
	if n == 0 {
		return ErrAgentBootPlanNotFound
	}
	return nil
}

func decodeAgentBootPlanRow(row agentBootPlanRow) (*AgentBootPlanDocument, error) {
	doc := &AgentBootPlanDocument{
		AgentID:       row.AgentID,
		SchemaVersion: row.SchemaVersion,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if err := json.Unmarshal([]byte(row.PlantItemsJSON), &doc.PlantItems); err != nil {
		return nil, fmt.Errorf("decode agent boot plan plant_items for %s: %w", row.AgentID, err)
	}
	if err := json.Unmarshal([]byte(row.CallbacksJSON), &doc.Callbacks); err != nil {
		return nil, fmt.Errorf("decode agent boot plan callbacks for %s: %w", row.AgentID, err)
	}
	if doc.PlantItems == nil {
		doc.PlantItems = []AgentBootPlantItem{}
	}
	if doc.Callbacks == nil {
		doc.Callbacks = []AgentBootCallback{}
	}
	return doc, nil
}
