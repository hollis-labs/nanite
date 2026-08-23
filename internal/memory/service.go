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
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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
	if opts.Search != "" || opts.Offset != 0 {
		page, err := s.recallFilteredPage(ctx, opts)
		if err != nil {
			return nil, err
		}
		return page.Memories, nil
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

// RecallPage recalls a page and computes a true filtered total independently
// of the page length. Existing semantic-recall callers continue using Recall.
func (s *Service) RecallPage(ctx context.Context, opts RecallOpts) (RecallPage, error) {
	if s.store == nil {
		return RecallPage{}, fmt.Errorf("memory service: no memory store configured")
	}
	return s.recallFilteredPage(ctx, opts)
}

type rankedMemory struct {
	memory         Memory
	createdAt      time.Time
	activation     float64
	lastAccessedAt *time.Time
	score          float64
}

// recallFilteredPage is the uncapped list-query path. Tesseract Recall is
// intentionally capped at 500 for semantic/context recall, so it cannot back
// an offset-based API: filtering after that cap can hide later matches. This
// query reads every metadata-filtered current revision, applies the one shared
// Unicode-aware text predicate, preserves Tesseract's activation or
// chronological ordering, and only then computes Total and the requested page.
func (s *Service) recallFilteredPage(ctx context.Context, opts RecallOpts) (RecallPage, error) {
	if opts.Query != "" || (opts.Ranking != "" && opts.Ranking != "activation" && opts.Ranking != "chronological") {
		return RecallPage{}, fmt.Errorf("memory_recall page: only activation or chronological list ranking is supported")
	}
	if len(opts.Namespaces) == 0 {
		return RecallPage{}, fmt.Errorf("memory_recall page: at least one namespace is required")
	}
	for _, namespace := range opts.Namespaces {
		if strings.TrimSpace(namespace) == "" {
			return RecallPage{}, fmt.Errorf("memory_recall page: namespace entries must be non-empty")
		}
	}

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
	if len(opts.Tags) > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(r.tags) WHERE value IN ("+queryPlaceholders(len(opts.Tags))+"))")
		for _, tag := range opts.Tags {
			args = append(args, tag)
		}
	}
	where = append(where, "(r.expires_at IS NULL OR r.expires_at > ?)")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano))

	query := `SELECT r.namespace, COALESCE(r.memory_key, ''),
		       COALESCE(r.payload_summary, ''), COALESCE(r.payload_body, ''),
		       r.origin, r."trigger", r.confidence, r.tags, r.session_id,
		       r.revision_id, r.status, r.created_at,
		       s.activation, s.last_accessed_at
		FROM memory_revisions r
		INNER JOIN memory_state s ON s.current_revision = r.revision_id
		WHERE ` + strings.Join(where, " AND ")
	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return RecallPage{}, fmt.Errorf("memory_recall page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	search := strings.ToLower(opts.Search)
	var ranked []rankedMemory
	for rows.Next() {
		var candidate rankedMemory
		var tagsJSON, createdAt string
		var lastAccessed sql.NullString
		if err := rows.Scan(
			&candidate.memory.Namespace, &candidate.memory.MemoryKey,
			&candidate.memory.Summary, &candidate.memory.Body,
			&candidate.memory.Origin, &candidate.memory.Trigger,
			&candidate.memory.Confidence, &tagsJSON, &candidate.memory.SessionID,
			&candidate.memory.RevisionID, &candidate.memory.Status, &createdAt,
			&candidate.activation, &lastAccessed,
		); err != nil {
			return RecallPage{}, fmt.Errorf("memory_recall page scan: %w", err)
		}
		if err := json.Unmarshal([]byte(tagsJSON), &candidate.memory.Tags); err != nil {
			return RecallPage{}, fmt.Errorf("memory_recall page tags: %w", err)
		}
		candidate.createdAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if lastAccessed.Valid {
			parsed, _ := time.Parse(time.RFC3339Nano, lastAccessed.String)
			candidate.lastAccessedAt = &parsed
		}
		if !memoryMatchesSearch(candidate.memory, search) {
			continue
		}
		ranked = append(ranked, candidate)
	}
	if err := rows.Err(); err != nil {
		return RecallPage{}, fmt.Errorf("memory_recall page rows: %w", err)
	}

	now := time.Now().UTC()
	for i := range ranked {
		if opts.Ranking == "chronological" {
			ranked[i].score = float64(ranked[i].createdAt.UnixNano())
		} else {
			ranked[i].score = memoryActivationScore(ranked[i], now)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].createdAt.After(ranked[j].createdAt)
		}
		return ranked[i].score > ranked[j].score
	})

	total := len(ranked)
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 500 {
		// Preserve Tesseract Recall's per-page maximum without using it as a
		// finite prefilter candidate window.
		limit = 500
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return RecallPage{Memories: []Memory{}, Total: total}, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	memories := make([]Memory, end-offset)
	for i, candidate := range ranked[offset:end] {
		memories[i] = candidate.memory
	}
	return RecallPage{Memories: memories, Total: total}, nil
}

func queryPlaceholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func memoryMatchesSearch(memory Memory, foldedSearch string) bool {
	if foldedSearch == "" {
		return true
	}
	return strings.Contains(strings.ToLower(memory.Summary), foldedSearch) ||
		strings.Contains(strings.ToLower(memory.Body), foldedSearch)
}

// memoryActivationScore mirrors the pinned Tesseract activation ranking
// for the uncapped list path above. Semantic/context callers still use
// Tesseract Recall directly; this copy exists only because its 500-result cap
// makes correct offset pagination impossible.
func memoryActivationScore(candidate rankedMemory, now time.Time) float64 {
	statusWeight := memoryStatusWeight(candidate.memory.Status)
	originWeight := memoryOriginWeight(candidate.memory.Origin)
	recency := 0.75
	if candidate.lastAccessedAt != nil {
		days := now.Sub(*candidate.lastAccessedAt).Hours() / 24
		switch {
		case days <= 0:
			recency = 1
		case days >= 30:
			recency = 0.5
		default:
			recency = 1 - 0.5*(days/30)
		}
	}
	return candidate.activation * statusWeight * candidate.memory.Confidence * originWeight * recency
}

func memoryStatusWeight(status string) float64 {
	switch status {
	case "canonical":
		return 1
	case "reviewed":
		return 0.9
	case "draft":
		return 0.6
	case "deprecated":
		return 0.1
	default:
		return 0
	}
}

func memoryOriginWeight(origin string) float64 {
	switch origin {
	case "feedback":
		return 1.3
	case "user":
		return 1.1
	case "project":
		return 1
	case "reference":
		return 0.9
	case "observation":
		return 0.8
	default:
		return 0
	}
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
