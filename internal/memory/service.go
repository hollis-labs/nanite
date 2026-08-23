// Package memory provides persistent memory storage, recall, and extraction
// backed by an embedded Tesseract memory store. Memories survive session
// boundaries and are surfaced during context assembly via the MemorySource.
//
// Tesseract is the current name of the store formerly published as
// github.com/hollis-labs/vanta-conduit (renamed at module v0.7.0). Its
// Go package is still `package conduit` / `package memory`, so the
// `conduitMemory` import alias and "Conduit-format" terminology below
// remain accurate.
package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	conduitMemory "github.com/hollis-labs/tesseract/memory"
)

// Memory represents a memory item to store or recalled from Tesseract.
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
	Namespaces []string // Conduit-format namespaces (e.g. "user/x/memory")
	// Ranking selects the recall mode. Accepts: "activation", "chronological",
	// "similarity", "relevance", or "" (empty). Empty triggers Conduit's smart
	// default: "relevance" when Query is non-empty, else "activation".
	Ranking       string
	Query         string   // raw query text — required for "similarity" / "relevance"
	Limit         int      // max results (default 20)
	MinConfidence float64  // minimum confidence threshold
	Origins       []string // filter by origin
	Tags          []string // filter by tags
	Statuses      []string // exact lifecycle statuses; empty uses active statuses
	Search        string   // case-insensitive substring match on summary/body
	Offset        int      // applied after all filters, before Limit
}

// RecallPage is a filtered recall page plus the total number of matches before
// Offset/Limit are applied.
type RecallPage struct {
	Memories []Memory
	Total    int
}

// Service provides memory storage and recall via an embedded Tesseract memory store.
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

	slog.Info("memory: stored",
		"namespace", m.Namespace, "memory_key", m.MemoryKey,
		"origin", m.Origin, "trigger", m.Trigger, "confidence", m.Confidence)
	return nil
}

// Recall fetches memories from the embedded Conduit memory store.
func (s *Service) Recall(ctx context.Context, opts RecallOpts) ([]Memory, error) {
	if s.store == nil {
		return nil, fmt.Errorf("memory service: no memory store configured")
	}

	// Map caller string → Conduit Ranking. Empty passes through empty so
	// Conduit's smart default (relevance-when-query, else activation) fires.
	var ranking conduitMemory.Ranking
	switch opts.Ranking {
	case "activation":
		ranking = conduitMemory.RankingActivation
	case "chronological":
		ranking = conduitMemory.RankingChronological
	case "similarity":
		ranking = conduitMemory.RankingSimilarity
	case "relevance":
		// Tesseract (through v0.7.0) ships RankingRelevance in
		// internal/memory but does not re-export the constant in the
		// public memory package. The string literal is the stable wire
		// value and the store's internal switch compares by string.
		// Replace with conduitMemory.RankingRelevance once Tesseract
		// publishes the export (BLG-worthy patch release).
		ranking = conduitMemory.Ranking("relevance")
	case "":
		// leave as "" — Conduit resolves to relevance (when Query != "") or activation.
	default:
		slog.Warn("memory: unknown ranking; falling through to Conduit smart default",
			"requested", opts.Ranking)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	fetchLimit := limit + offset
	if opts.Search != "" {
		// Tesseract currently caps recall at 500. Fetch its full supported
		// candidate window so text filtering happens before this caller's page.
		fetchLimit = 500
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
	if len(opts.Statuses) > 0 {
		for _, status := range opts.Statuses {
			filters.Statuses = append(filters.Statuses, conduitMemory.Status(status))
		}
	} else {
		// Only include statuses that are active.
		filters.Statuses = []conduitMemory.Status{
			conduitMemory.StatusDraft,
			conduitMemory.StatusReviewed,
			conduitMemory.StatusCanonical,
		}
	}

	if (ranking == conduitMemory.RankingSimilarity || ranking == conduitMemory.Ranking("relevance")) && opts.Query == "" {
		slog.Warn("memory: query-required ranking without query text — results will be degenerate",
			"ranking", string(ranking),
			"namespaces", opts.Namespaces)
	}

	in := conduitMemory.RecallInput{
		Namespaces: opts.Namespaces,
		Ranking:    ranking,
		Query:      opts.Query,
		Limit:      fetchLimit,
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
	if search := strings.ToLower(opts.Search); search != "" {
		filtered := memories[:0]
		for _, m := range memories {
			if strings.Contains(strings.ToLower(m.Summary), search) || strings.Contains(strings.ToLower(m.Body), search) {
				filtered = append(filtered, m)
			}
		}
		memories = filtered
	}
	if offset >= len(memories) {
		return []Memory{}, nil
	}
	if offset > 0 {
		memories = memories[offset:]
	}
	if len(memories) > limit {
		memories = memories[:limit]
	}
	return memories, nil
}

// RecallPage recalls a page and computes a true filtered total independently
// of the page length. Existing semantic-recall callers continue using Recall.
func (s *Service) RecallPage(ctx context.Context, opts RecallOpts) (RecallPage, error) {
	memories, err := s.Recall(ctx, opts)
	if err != nil {
		return RecallPage{}, err
	}
	total, err := s.countRecallMatches(ctx, opts)
	if err != nil {
		return RecallPage{}, err
	}
	return RecallPage{Memories: memories, Total: total}, nil
}

func (s *Service) countRecallMatches(ctx context.Context, opts RecallOpts) (int, error) {
	var where []string
	var args []any
	where = append(where, "r.namespace IN ("+queryPlaceholders(len(opts.Namespaces))+")")
	for _, namespace := range opts.Namespaces {
		args = append(args, namespace)
	}
	statuses := opts.Statuses
	if len(statuses) == 0 {
		statuses = []string{"draft", "reviewed", "canonical"}
	}
	where = append(where, "r.status IN ("+queryPlaceholders(len(statuses))+")")
	for _, status := range statuses {
		args = append(args, status)
	}
	if len(opts.Origins) > 0 {
		where = append(where, "r.origin IN ("+queryPlaceholders(len(opts.Origins))+")")
		for _, origin := range opts.Origins {
			args = append(args, origin)
		}
	}
	if opts.MinConfidence > 0 {
		where = append(where, "r.confidence >= ?")
		args = append(args, opts.MinConfidence)
	}
	if opts.Search != "" {
		pattern := "%" + escapeLike(strings.ToLower(opts.Search)) + "%"
		where = append(where, "(LOWER(COALESCE(r.payload_summary, '')) LIKE ? ESCAPE '\\' OR LOWER(COALESCE(r.payload_body, '')) LIKE ? ESCAPE '\\')")
		args = append(args, pattern, pattern)
	}
	if len(opts.Tags) > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(r.tags) WHERE value IN ("+queryPlaceholders(len(opts.Tags))+"))")
		for _, tag := range opts.Tags {
			args = append(args, tag)
		}
	}
	where = append(where, "(r.expires_at IS NULL OR r.expires_at > ?)")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano))

	query := `SELECT COUNT(*)
		FROM memory_revisions r
		INNER JOIN memory_state s ON s.current_revision = r.revision_id
		WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := s.store.DB().QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("memory_recall count: %w", err)
	}
	return total, nil
}

func queryPlaceholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
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

	slog.Info("memory: promoted revision", "revision_id", revisionID, "target_namespace", targetNamespace)
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

	slog.Info("memory: deprecated revision", "revision_id", revisionID)
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
