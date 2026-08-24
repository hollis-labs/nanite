package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrAgentKnowledgeSeedNotFound is returned when an agent_knowledge_seed
// row cannot be located.
var ErrAgentKnowledgeSeedNotFound = errors.New("agent knowledge seed not found")

// AgentKnowledgeSeed is one row in the agent_knowledge_seed table — the
// manifest of memory_keys an agent wants seeded into Tesseract on first
// activation. applied_at is NULL until the consumer (FU-7f) writes the
// body into the target namespace.
type AgentKnowledgeSeed struct {
	AgentID   string `json:"agent_id"`
	SeedKey   string `json:"seed_key"`
	Namespace string `json:"namespace"`
	Body      string `json:"body"`
	TagsJSON  string `json:"tags_json"`
	AppliedAt string `json:"applied_at"` // empty until applied
	CreatedAt string `json:"created_at"`
}

const agentKnowledgeSeedColumns = `agent_id, seed_key, namespace, body, tags_json,
       COALESCE(applied_at,''), created_at`

func scanAgentKnowledgeSeed(scanner interface{ Scan(...any) error }, k *AgentKnowledgeSeed) error {
	return scanner.Scan(
		&k.AgentID, &k.SeedKey, &k.Namespace, &k.Body, &k.TagsJSON,
		&k.AppliedAt, &k.CreatedAt,
	)
}

// InsertAgentKnowledgeSeed upserts a manifest row. PK is (agent_id,
// seed_key). Empty tags_json is normalised to '[]' to match the column
// default.
func (s *Store) InsertAgentKnowledgeSeed(ctx context.Context, row AgentKnowledgeSeed) error {
	if row.AgentID == "" {
		return fmt.Errorf("insert agent_knowledge_seed: agent_id is required")
	}
	if row.SeedKey == "" {
		return fmt.Errorf("insert agent_knowledge_seed: seed_key is required")
	}
	if row.Namespace == "" {
		return fmt.Errorf("insert agent_knowledge_seed: namespace is required")
	}
	if row.TagsJSON == "" {
		row.TagsJSON = "[]"
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT OR REPLACE INTO agent_knowledge_seed
		    (agent_id, seed_key, namespace, body, tags_json, applied_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?,
		         COALESCE(NULLIF(?, ''), datetime('now')))`,
		row.AgentID, row.SeedKey, row.Namespace, row.Body, row.TagsJSON,
		nullIfEmpty(row.AppliedAt),
		row.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert agent_knowledge_seed: %w", err)
	}
	return nil
}

// ListAgentKnowledgeSeeds returns every seed row for an agent ordered by
// seed_key ASC.
func (s *Store) ListAgentKnowledgeSeeds(ctx context.Context, agentID string) ([]AgentKnowledgeSeed, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+agentKnowledgeSeedColumns+`
		 FROM agent_knowledge_seed
		 WHERE agent_id = ?
		 ORDER BY seed_key ASC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent_knowledge_seed: %w", err)
	}
	defer closeRows(rows)

	out := make([]AgentKnowledgeSeed, 0)
	for rows.Next() {
		var k AgentKnowledgeSeed
		if err := scanAgentKnowledgeSeed(rows, &k); err != nil {
			return nil, fmt.Errorf("scan agent_knowledge_seed: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetAgentKnowledgeSeed returns a single seed row by (agent_id, seed_key).
// Returns ErrAgentKnowledgeSeedNotFound when the row does not exist.
func (s *Store) GetAgentKnowledgeSeed(ctx context.Context, agentID, seedKey string) (*AgentKnowledgeSeed, error) {
	var k AgentKnowledgeSeed
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+agentKnowledgeSeedColumns+`
		 FROM agent_knowledge_seed
		 WHERE agent_id = ? AND seed_key = ?`,
		agentID, seedKey,
	)
	if err := scanAgentKnowledgeSeed(row, &k); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentKnowledgeSeedNotFound
		}
		return nil, fmt.Errorf("get agent_knowledge_seed: %w", err)
	}
	return &k, nil
}

// MarkAgentKnowledgeSeedApplied stamps applied_at = datetime('now') on a
// seed row to record that the body has been written to the target
// namespace by the FU-7f boot hook. Returns ErrAgentKnowledgeSeedNotFound
// if no row matched.
func (s *Store) MarkAgentKnowledgeSeedApplied(ctx context.Context, agentID, seedKey string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE agent_knowledge_seed
		    SET applied_at = datetime('now')
		  WHERE agent_id = ? AND seed_key = ?`,
		agentID, seedKey,
	)
	if err != nil {
		return fmt.Errorf("mark agent_knowledge_seed applied: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark agent_knowledge_seed applied rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentKnowledgeSeedNotFound
	}
	return nil
}

// DeleteAgentKnowledgeSeed removes a single seed row. Returns
// ErrAgentKnowledgeSeedNotFound if no row matched.
func (s *Store) DeleteAgentKnowledgeSeed(ctx context.Context, agentID, seedKey string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM agent_knowledge_seed WHERE agent_id = ? AND seed_key = ?`,
		agentID, seedKey,
	)
	if err != nil {
		return fmt.Errorf("delete agent_knowledge_seed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent_knowledge_seed rows affected: %w", err)
	}
	if n == 0 {
		return ErrAgentKnowledgeSeedNotFound
	}
	return nil
}
