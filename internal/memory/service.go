// Package memory provides persistent memory storage, recall, and extraction
// backed by an embedded Vanta Conduit memory store. Memories survive session
// boundaries and are surfaced during context assembly via the MemorySource.
package memory

import (
	"context"
	"fmt"
	"log"

	conduitMemory "github.com/hollis-labs/vanta-conduit/memory"
)

// Memory represents a memory item to store or recalled from Vanta Conduit.
type Memory struct {
	Namespace  string   `json:"namespace"`
	MemoryKey  string   `json:"memory_key"`
	Summary    string   `json:"summary"`
	Body       string   `json:"body"`
	Origin     string   `json:"origin"`     // user, feedback, project, reference, observation
	Trigger    string   `json:"trigger"`    // explicit, post_compact, per_turn, promotion, manual
	Confidence float64  `json:"confidence"` // 0.0-1.0
	Tags       []string `json:"tags"`
	SessionID  string   `json:"session_id"`
	RevisionID string   `json:"revision_id,omitempty"` // set on recall
	Status     string   `json:"status,omitempty"`      // draft, reviewed, canonical, deprecated
}

// RecallOpts configures memory recall.
type RecallOpts struct {
	Namespaces    []string // Conduit-format namespaces (e.g. "user/x/memory")
	Ranking       string   // "activation" (default), "chronological", "similarity"
	Limit         int      // max results (default 20)
	MinConfidence float64  // minimum confidence threshold
	Origins       []string // filter by origin
	Tags          []string // filter by tags
}

// Service provides memory storage and recall via an embedded Vanta Conduit memory store.
type Service struct {
	store *conduitMemory.Store
}

// NewService creates a MemoryService backed by the given Conduit memory store.
func NewService(store *conduitMemory.Store) *Service {
	return &Service{store: store}
}

// Store writes a memory revision to the embedded Conduit memory store.
func (s *Service) Store(ctx context.Context, m Memory) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}

	status := conduitMemory.StatusDraft
	if m.Status != "" {
		status = conduitMemory.Status(m.Status)
	}

	in := conduitMemory.WriteInput{
		Namespace:  m.Namespace,
		MemoryKey:  m.MemoryKey,
		Status:     status,
		Author:     conduitMemory.Author{AgentID: "nanite", AgentVersion: "1.0"},
		Trigger:    mapTrigger(m.Trigger),
		SessionID:  m.SessionID,
		Origin:     mapOrigin(m.Origin),
		Confidence: m.Confidence,
		Tags:       m.Tags,
		Payload: conduitMemory.Payload{
			Summary: m.Summary,
			Body:    m.Body,
		},
	}

	// SessionID is required by Conduit; provide a fallback.
	if in.SessionID == "" {
		in.SessionID = "manual:nanite"
	}

	_, err := s.store.WriteRevision(ctx, in)
	if err != nil {
		return fmt.Errorf("memory_write: %w", err)
	}

	log.Printf("memory: stored %s/%s (origin=%s, trigger=%s, confidence=%.1f)",
		m.Namespace, m.MemoryKey, m.Origin, m.Trigger, m.Confidence)
	return nil
}

// Recall fetches memories from the embedded Conduit memory store.
func (s *Service) Recall(ctx context.Context, opts RecallOpts) ([]Memory, error) {
	if s.store == nil {
		return nil, fmt.Errorf("memory service: no memory store configured")
	}

	ranking := conduitMemory.RankingActivation
	switch opts.Ranking {
	case "chronological":
		ranking = conduitMemory.RankingChronological
	case "similarity":
		ranking = conduitMemory.RankingSimilarity
	case "activation", "":
		ranking = conduitMemory.RankingActivation
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	// Build filters.
	var filters conduitMemory.RecallFilters
	if opts.MinConfidence > 0 {
		filters.ConfidenceMin = opts.MinConfidence
	}
	if len(opts.Origins) > 0 {
		for _, o := range opts.Origins {
			filters.Origins = append(filters.Origins, conduitMemory.Origin(o))
		}
	}
	if len(opts.Tags) > 0 {
		filters.Tags = opts.Tags
	}
	// Only include statuses that are active.
	filters.Statuses = []conduitMemory.Status{
		conduitMemory.StatusDraft,
		conduitMemory.StatusReviewed,
		conduitMemory.StatusCanonical,
	}

	in := conduitMemory.RecallInput{
		Namespaces: opts.Namespaces,
		Ranking:    ranking,
		Limit:      limit,
		Filters:    filters,
	}

	results, err := s.store.Recall(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("memory_recall: %w", err)
	}

	memories := make([]Memory, 0, len(results))
	for _, r := range results {
		memories = append(memories, revisionToMemory(r.Revision))
	}
	return memories, nil
}

// Get fetches a single memory by namespace and key.
func (s *Service) Get(ctx context.Context, namespace, memoryKey string) (*Memory, error) {
	if s.store == nil {
		return nil, fmt.Errorf("memory service: no memory store configured")
	}

	rev, err := s.store.GetCurrent(ctx, namespace, memoryKey)
	if err != nil {
		return nil, fmt.Errorf("memory_get: %w", err)
	}

	m := revisionToMemory(rev)
	return &m, nil
}

// Promote moves a memory to a broader scope.
func (s *Service) Promote(ctx context.Context, revisionID, targetNamespace string) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}

	// Look up the revision to find its source memory ID and namespace.
	rev, err := s.store.GetRevisionByID(ctx, revisionID)
	if err != nil {
		return fmt.Errorf("memory_promote: lookup revision: %w", err)
	}

	_, err = s.store.Promote(ctx, conduitMemory.PromoteInput{
		SourceNamespace: rev.Namespace,
		SourceMemoryID:  rev.MemoryID,
		TargetNamespace: targetNamespace,
		ActorAgentID:    "nanite",
		ActorVersion:    "1.0",
	})
	if err != nil {
		return fmt.Errorf("memory_promote: %w", err)
	}

	log.Printf("memory: promoted revision %s to %s", revisionID, targetNamespace)
	return nil
}

// Deprecate marks a memory revision as deprecated.
func (s *Service) Deprecate(ctx context.Context, revisionID string) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}

	if err := s.store.Deprecate(ctx, revisionID); err != nil {
		return fmt.Errorf("memory_deprecate: %w", err)
	}

	log.Printf("memory: deprecated revision %s", revisionID)
	return nil
}

// SessionNamespace returns the Conduit-format memory namespace for a session.
func SessionNamespace(sessionID string) string {
	return "user/default/session/" + sessionID + "/memory"
}

// ProjectNamespace returns the Conduit-format memory namespace for a project.
func ProjectNamespace(projectID string) string {
	return "user/default/project/" + projectID + "/memory"
}

// UserNamespace returns the Conduit-format memory namespace for a user.
func UserNamespace(userID string) string {
	return "user/" + userID + "/memory"
}

// AllNaniteNamespaces returns namespaces that cover all Nanite memory scopes.
// Since Conduit doesn't support globs in Recall, we return the broadest
// user-scope namespace; callers that need session/project should specify explicitly.
func AllNaniteNamespaces() []string {
	return []string{"user/default/memory"}
}

// revisionToMemory converts a Conduit Revision to a Nanite Memory.
func revisionToMemory(rev conduitMemory.Revision) Memory {
	return Memory{
		Namespace:  rev.Namespace,
		MemoryKey:  rev.MemoryKey,
		Summary:    rev.Payload.Summary,
		Body:       rev.Payload.Body,
		Origin:     string(rev.Origin),
		Trigger:    string(rev.Trigger),
		Confidence: rev.Confidence,
		Tags:       rev.Tags,
		SessionID:  rev.SessionID,
		RevisionID: rev.RevisionID,
		Status:     string(rev.Status),
	}
}

// mapOrigin converts a string origin to the Conduit Origin type.
func mapOrigin(s string) conduitMemory.Origin {
	switch s {
	case "user":
		return conduitMemory.OriginUser
	case "feedback":
		return conduitMemory.OriginFeedback
	case "project":
		return conduitMemory.OriginProject
	case "reference":
		return conduitMemory.OriginReference
	case "observation":
		return conduitMemory.OriginObservation
	default:
		if s == "" {
			return conduitMemory.OriginObservation
		}
		return conduitMemory.Origin(s)
	}
}

// mapTrigger converts a string trigger to the Conduit Trigger type.
func mapTrigger(s string) conduitMemory.Trigger {
	switch s {
	case "explicit":
		return conduitMemory.TriggerExplicit
	case "post_compact":
		return conduitMemory.TriggerPostCompact
	case "per_turn":
		return conduitMemory.TriggerPerTurn
	case "promotion":
		return conduitMemory.TriggerPromotion
	case "manual":
		return conduitMemory.TriggerManual
	default:
		if s == "" {
			return conduitMemory.TriggerManual
		}
		return conduitMemory.Trigger(s)
	}
}
