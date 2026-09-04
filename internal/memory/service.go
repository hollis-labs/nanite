// Package memory provides Nanite's application-facing memory service. Storage,
// ranking, paging, projection, and reinforcement are delegated to Tesseract's
// public v0.9 API; this package only maps Nanite's wire types onto that API.
package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	tesseractMemory "github.com/hollis-labs/tesseract/memory"
)

// Public option types and constants re-export Tesseract's typed v0.9
// contract so Nanite call sites do not depend on magic string conversions.
type (
	Ranking     = tesseractMemory.Ranking
	SearchMode  = tesseractMemory.SearchMode
	PayloadMode = tesseractMemory.PayloadMode
)

const (
	RankingActivation    = tesseractMemory.RankingActivation
	RankingChronological = tesseractMemory.RankingChronological
	RankingSimilarity    = tesseractMemory.RankingSimilarity
	RankingRelevance     = tesseractMemory.RankingRelevance

	SearchModeHybrid   = tesseractMemory.SearchModeHybrid
	SearchModeLexical  = tesseractMemory.SearchModeLexical
	SearchModeSemantic = tesseractMemory.SearchModeSemantic

	PayloadModeKeys    = tesseractMemory.PayloadModeKeys
	PayloadModeSummary = tesseractMemory.PayloadModeSummary
	PayloadModeFull    = tesseractMemory.PayloadModeFull
)

// Memory represents a memory item to store or one returned by Tesseract.
type Memory struct {
	Namespace  string   `json:"namespace"`
	MemoryKey  string   `json:"memory_key"`
	Summary    string   `json:"summary,omitempty"`
	Body       string   `json:"body,omitempty"`
	Origin     string   `json:"origin,omitempty"`
	Trigger    string   `json:"trigger,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	SessionID  string   `json:"session_id,omitempty"`
	RevisionID string   `json:"revision_id,omitempty"`
	Status     string   `json:"status,omitempty"`
	// Score is nil when the selected ordering has no numeric score. A real
	// zero or negative semantic score therefore remains distinguishable.
	Score *float64 `json:"score,omitempty"`
	// PayloadMode is present for projected results. An omitted body under
	// keys/summary is not an empty stored body; hydrate by RevisionID.
	PayloadMode string `json:"payload_mode,omitempty"`
}

// RecallOpts configures memory recall.
type RecallOpts struct {
	// Namespaces accept typed namespaces for exact reads and flat memory
	// prefixes for cross-type recall. Writes always require typed namespaces.
	Namespaces    []string
	Ranking       Ranking
	SearchMode    SearchMode
	Query         string
	Limit         int
	MinConfidence float64
	Origins       []string
	Tags          []string
	Statuses      []string
	// Search is the legacy Nanite list-search spelling. It preserves the UI's
	// case-insensitive Unicode substring filter over Tesseract-owned pages.
	Search       string
	Offset       int
	Cursor       string
	PayloadMode  PayloadMode
	BudgetBytes  int
	BudgetTokens int
	EstimateOnly bool
}

// RecallPage is a projected recall page. Manifest is populated for cursor and
// budget-aware reads; Total is retained for the existing offset-based API.
type RecallPage struct {
	Memories []Memory                  `json:"memories"`
	Total    int                       `json:"total"`
	Manifest *tesseractMemory.Manifest `json:"manifest,omitempty"`
}

// Service provides storage and recall through Tesseract's public memory API.
type Service struct {
	store *tesseractMemory.Store
}

func NewService(store *tesseractMemory.Store) *Service { return &Service{store: store} }

// Store writes a memory-domain revision. Tesseract v0.9 validates that the
// namespace is typed (for example user/alice/memory/notes).
func (s *Service) Store(ctx context.Context, m Memory) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}
	status := tesseractMemory.StatusDraft
	if m.Status != "" {
		status = tesseractMemory.Status(m.Status)
	}
	in := tesseractMemory.WriteInput{
		Domain:     tesseractMemory.DomainMemory,
		Namespace:  m.Namespace,
		MemoryKey:  m.MemoryKey,
		Status:     status,
		Author:     tesseractMemory.Author{AgentID: "nanite", AgentVersion: "1.0"},
		Trigger:    mapTrigger(m.Trigger),
		SessionID:  m.SessionID,
		Origin:     mapOrigin(m.Origin),
		Confidence: m.Confidence,
		Tags:       m.Tags,
		Payload: tesseractMemory.Payload{
			Summary: m.Summary,
			Body:    m.Body,
		},
	}
	if in.SessionID == "" {
		in.SessionID = "manual:nanite"
	}
	if _, err := s.store.WriteRevision(ctx, in); err != nil {
		return fmt.Errorf("memory_write: %w", err)
	}
	slog.Info("memory: stored", "namespace", m.Namespace, "memory_key", m.MemoryKey,
		"origin", m.Origin, "trigger", m.Trigger, "confidence", m.Confidence)
	return nil
}

// Recall fetches a single page. Summary projection is the default.
func (s *Service) Recall(ctx context.Context, opts RecallOpts) ([]Memory, error) {
	page, err := s.RecallPage(ctx, opts)
	if err != nil {
		return nil, err
	}
	return page.Memories, nil
}

// RecallPage delegates candidate selection, ordering, filtering, paging,
// projection accounting, and cursor validation to Tesseract v0.9.
func (s *Service) RecallPage(ctx context.Context, opts RecallOpts) (RecallPage, error) {
	if s.store == nil {
		return RecallPage{}, fmt.Errorf("memory service: no memory store configured")
	}
	in, err := recallInput(opts)
	if err != nil {
		return RecallPage{}, err
	}
	mode, err := payloadMode(opts.PayloadMode)
	if err != nil {
		return RecallPage{}, err
	}
	if opts.Search != "" {
		return s.recallLegacySearch(ctx, opts, mode)
	}

	// The retained offset API uses Tesseract's uncapped public RecallPage
	// primitive. Cursor and budget behavior belongs to RecallPaged below.
	if opts.Offset != 0 {
		if opts.Offset < 0 {
			return RecallPage{}, fmt.Errorf("tesseract_recall: offset must not be negative")
		}
		if opts.Cursor != "" || opts.BudgetBytes != 0 || opts.BudgetTokens != 0 || opts.EstimateOnly {
			return RecallPage{}, fmt.Errorf("tesseract_recall: offset cannot be combined with cursor, budgets, or estimate_only")
		}
		if mode == tesseractMemory.PayloadModeFull && in.Limit > tesseractMemory.MaxRecallLimitFull {
			in.Limit = tesseractMemory.MaxRecallLimitFull
		}
		in.Offset = opts.Offset
		result, recallErr := s.store.RecallPage(ctx, in)
		if recallErr != nil {
			return RecallPage{}, fmt.Errorf("tesseract_recall: %w", recallErr)
		}
		memories := projectMemories(result.Results, mode)
		if opts.EstimateOnly {
			memories = []Memory{}
		}
		return RecallPage{Memories: memories, Total: result.Total}, nil
	}

	paged, err := s.store.RecallPaged(ctx, in, tesseractMemory.PageRequest{
		Cursor: opts.Cursor,
		Budget: tesseractMemory.Budget{
			Bytes:  opts.BudgetBytes,
			Tokens: opts.BudgetTokens,
		},
		PayloadMode:  mode,
		Limit:        opts.Limit,
		EstimateOnly: opts.EstimateOnly,
	})
	if err != nil {
		return RecallPage{}, fmt.Errorf("tesseract_recall: %w", err)
	}
	memories := projectMemories(paged.Kept, mode)
	if opts.EstimateOnly {
		memories = []Memory{}
	}
	manifest := paged.Manifest
	return RecallPage{Memories: memories, Total: manifest.ResultsTotal, Manifest: &manifest}, nil
}

func recallInput(opts RecallOpts) (tesseractMemory.RecallInput, error) {
	if len(opts.Namespaces) == 0 {
		return tesseractMemory.RecallInput{}, fmt.Errorf("tesseract_recall: at least one namespace is required")
	}
	for _, namespace := range opts.Namespaces {
		if strings.TrimSpace(namespace) == "" {
			return tesseractMemory.RecallInput{}, fmt.Errorf("tesseract_recall: namespace entries must be non-empty")
		}
	}

	ranking := opts.Ranking
	query := opts.Query
	searchMode := opts.SearchMode

	filters := tesseractMemory.RecallFilters{
		Tags:    opts.Tags,
		Domains: []tesseractMemory.Domain{tesseractMemory.DomainMemory},
	}
	if opts.MinConfidence > 0 {
		filters.ConfidenceMin = opts.MinConfidence
	}
	for _, origin := range opts.Origins {
		filters.Origins = append(filters.Origins, tesseractMemory.Origin(origin))
	}
	if len(opts.Statuses) == 0 {
		filters.Statuses = []tesseractMemory.Status{
			tesseractMemory.StatusDraft,
			tesseractMemory.StatusReviewed,
			tesseractMemory.StatusCanonical,
		}
	} else {
		for _, status := range opts.Statuses {
			filters.Statuses = append(filters.Statuses, tesseractMemory.Status(status))
		}
	}

	return tesseractMemory.RecallInput{
		Namespaces: opts.Namespaces,
		Ranking:    ranking,
		Query:      query,
		SearchMode: searchMode,
		Filters:    filters,
		Limit:      opts.Limit,
	}, nil
}

// recallLegacySearch preserves Nanite's UI substring filter while delegating
// every database page, filter, and ordering operation to Tesseract. Tesseract's
// lexical arm intentionally rejects non-ASCII tokens and is not a substring
// query, so it cannot implement this pre-existing Unicode list-search contract.
func (s *Service) recallLegacySearch(ctx context.Context, opts RecallOpts, mode tesseractMemory.PayloadMode) (RecallPage, error) {
	if opts.Cursor != "" || opts.BudgetBytes != 0 || opts.BudgetTokens != 0 || opts.EstimateOnly {
		return RecallPage{}, fmt.Errorf("tesseract_recall: legacy search cannot be combined with cursor, budgets, or estimate_only")
	}
	base := opts
	base.Search = ""
	base.Offset = 0
	base.Limit = tesseractMemory.MaxRecallLimit
	in, err := recallInput(base)
	if err != nil {
		return RecallPage{}, err
	}

	folded := strings.ToLower(opts.Search)
	var matches []tesseractMemory.RecallResult
	for offset := 0; ; offset += tesseractMemory.MaxRecallLimit {
		in.Offset = offset
		page, err := s.store.RecallPage(ctx, in)
		if err != nil {
			return RecallPage{}, fmt.Errorf("tesseract_recall: %w", err)
		}
		for _, result := range page.Results {
			haystack := strings.ToLower(result.Revision.Payload.Summary + "\n" + result.Revision.Payload.Body)
			if strings.Contains(haystack, folded) {
				matches = append(matches, result)
			}
		}
		if offset+len(page.Results) >= page.Total || len(page.Results) == 0 {
			break
		}
	}

	total := len(matches)
	offset := opts.Offset
	if offset < 0 {
		return RecallPage{}, fmt.Errorf("tesseract_recall: offset must not be negative")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = tesseractMemory.DefaultRecallLimit
	}
	ceiling := tesseractMemory.MaxRecallLimit
	if mode == tesseractMemory.PayloadModeFull {
		ceiling = tesseractMemory.MaxRecallLimitFull
	}
	if limit > ceiling {
		limit = ceiling
	}
	if offset >= total {
		return RecallPage{Memories: []Memory{}, Total: total}, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	memories := projectMemories(matches[offset:end], mode)
	if opts.EstimateOnly {
		memories = []Memory{}
	}
	return RecallPage{Memories: memories, Total: total}, nil
}

func payloadMode(raw PayloadMode) (tesseractMemory.PayloadMode, error) {
	if raw == "" {
		return tesseractMemory.DefaultPayloadMode, nil
	}
	mode := raw
	if !mode.Valid() {
		return "", fmt.Errorf("tesseract_recall: payload_mode must be one of keys|summary|full, got %q", raw)
	}
	return mode, nil
}

func projectMemories(results []tesseractMemory.RecallResult, mode tesseractMemory.PayloadMode) []Memory {
	memories := make([]Memory, 0, len(results))
	for _, result := range results {
		m := revisionToMemory(result.Revision)
		m.Score = result.Score
		if mode != tesseractMemory.PayloadModeFull {
			m.PayloadMode = string(mode)
			m.Body = ""
		}
		if mode == tesseractMemory.PayloadModeKeys {
			m.Summary = ""
			m.Origin = ""
			m.Trigger = ""
			m.Confidence = 0
			m.Tags = nil
			m.SessionID = ""
			m.Status = ""
		}
		memories = append(memories, m)
	}
	return memories
}

// Get resolves the current revision and reinforces its activation.
func (s *Service) Get(ctx context.Context, namespace, memoryKey string) (*Memory, error) {
	if s.store == nil {
		return nil, fmt.Errorf("memory service: no memory store configured")
	}
	rev, err := s.store.GetCurrentReinforced(ctx, namespace, memoryKey)
	if err != nil {
		return nil, fmt.Errorf("tesseract_get: %w", err)
	}
	m := revisionToMemory(rev)
	return &m, nil
}

// GetRevision hydrates one projected result and reinforces its activation.
func (s *Service) GetRevision(ctx context.Context, revisionID string) (*Memory, error) {
	if s.store == nil {
		return nil, fmt.Errorf("memory service: no memory store configured")
	}
	rev, err := s.store.GetRevisionByIDReinforced(ctx, revisionID)
	if err != nil {
		return nil, fmt.Errorf("tesseract_get_revision: %w", err)
	}
	m := revisionToMemory(rev)
	return &m, nil
}

// Touch records that the caller selected these recall results for actual use.
func (s *Service) Touch(ctx context.Context, revisionIDs []string) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}
	if _, err := s.store.TouchRevisions(ctx, revisionIDs); err != nil {
		return fmt.Errorf("tesseract_touch: %w", err)
	}
	return nil
}

func IsInvalidCursor(err error) bool { return errors.Is(err, tesseractMemory.ErrInvalidCursor) }

func (s *Service) Promote(ctx context.Context, revisionID, targetNamespace string) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}
	rev, err := s.store.GetRevisionByID(ctx, revisionID)
	if err != nil {
		return fmt.Errorf("memory_promote: lookup revision: %w", err)
	}
	if _, err = s.store.Promote(ctx, tesseractMemory.PromoteInput{
		SourceNamespace: rev.Namespace,
		SourceMemoryID:  rev.MemoryID,
		TargetNamespace: targetNamespace,
		ActorAgentID:    "nanite",
		ActorVersion:    "1.0",
	}); err != nil {
		return fmt.Errorf("memory_promote: %w", err)
	}
	slog.Info("memory: promoted revision", "revision_id", revisionID, "target_namespace", targetNamespace)
	return nil
}

func (s *Service) Deprecate(ctx context.Context, revisionID string) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}
	if err := s.store.Deprecate(ctx, revisionID); err != nil {
		return fmt.Errorf("tesseract_deprecate: %w", err)
	}
	slog.Info("memory: deprecated revision", "revision_id", revisionID)
	return nil
}

// SessionNamespace returns a writable typed session namespace. It defaults to
// notes for compatibility with existing one-argument call sites.
func SessionNamespace(sessionID string, memoryType ...string) string {
	return "user/default/session/" + sessionID + "/memory/" + resolveMemoryType(memoryType)
}

// ProjectNamespace returns a writable typed project namespace. It defaults to
// notes for compatibility with existing one-argument call sites.
func ProjectNamespace(projectID string, memoryType ...string) string {
	return "user/default/project/" + projectID + "/memory/" + resolveMemoryType(memoryType)
}

// UserNamespace returns a writable typed user namespace. It defaults to notes
// for compatibility with existing one-argument call sites.
func UserNamespace(userID string, memoryType ...string) string {
	return "user/" + userID + "/memory/" + resolveMemoryType(memoryType)
}

// SessionMemoryPrefix spans all typed memory namespaces in one session.
func SessionMemoryPrefix(sessionID string) string {
	return "user/default/session/" + sessionID + "/memory"
}

// ProjectMemoryPrefix spans all typed memory namespaces in one project.
func ProjectMemoryPrefix(projectID string) string {
	return "user/default/project/" + projectID + "/memory"
}

// UserMemoryPrefix spans all typed user-level memory namespaces.
func UserMemoryPrefix(userID string) string { return "user/" + userID + "/memory" }

func resolveMemoryType(memoryType []string) string {
	if len(memoryType) > 0 && strings.TrimSpace(memoryType[0]) != "" {
		return strings.TrimSpace(memoryType[0])
	}
	return "notes"
}

// AllNaniteNamespaces is the legacy name for the default user's cross-type
// user-scope read prefix. Project/session scopes require their own prefixes.
func AllNaniteNamespaces() []string { return []string{UserMemoryPrefix("default")} }

func revisionToMemory(rev tesseractMemory.Revision) Memory {
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

func mapOrigin(value string) tesseractMemory.Origin {
	switch value {
	case "user":
		return tesseractMemory.OriginUser
	case "feedback":
		return tesseractMemory.OriginFeedback
	case "project":
		return tesseractMemory.OriginProject
	case "reference":
		return tesseractMemory.OriginReference
	case "observation", "":
		return tesseractMemory.OriginObservation
	default:
		return tesseractMemory.Origin(value)
	}
}

func mapTrigger(value string) tesseractMemory.Trigger {
	switch value {
	case "explicit":
		return tesseractMemory.TriggerExplicit
	case "post_compact":
		return tesseractMemory.TriggerPostCompact
	case "per_turn":
		return tesseractMemory.TriggerPerTurn
	case "promotion":
		return tesseractMemory.TriggerPromotion
	case "manual", "":
		return tesseractMemory.TriggerManual
	default:
		return tesseractMemory.Trigger(value)
	}
}
