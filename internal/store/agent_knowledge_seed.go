package store

import (
	"context"
	"errors"
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
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

// ListAgentKnowledgeSeeds returns every seed row for an agent ordered by
// seed_key ASC.
func (s *Store) ListAgentKnowledgeSeeds(ctx context.Context, agentID string) ([]AgentKnowledgeSeed, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// GetAgentKnowledgeSeed returns a single seed row by (agent_id, seed_key).
// Returns ErrAgentKnowledgeSeedNotFound when the row does not exist.
func (s *Store) GetAgentKnowledgeSeed(ctx context.Context, agentID, seedKey string) (*AgentKnowledgeSeed, error) {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return nil, ErrImmutableAgentProfile
}

// MarkAgentKnowledgeSeedApplied stamps applied_at = datetime('now') on a
// seed row to record that the body has been written to the target
// namespace by the FU-7f boot hook. Returns ErrAgentKnowledgeSeedNotFound
// if no row matched.
func (s *Store) MarkAgentKnowledgeSeedApplied(ctx context.Context, agentID, seedKey string) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}

// DeleteAgentKnowledgeSeed removes a single seed row. Returns
// ErrAgentKnowledgeSeedNotFound if no row matched.
func (s *Store) DeleteAgentKnowledgeSeed(ctx context.Context, agentID, seedKey string) error {
	// Intrinsic content is authored and pinned; mutable profile behavior is retired.
	return ErrImmutableAgentProfile
}
