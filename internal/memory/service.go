// Package memory provides Nanite's application-facing memory service. Storage,
// ranking, paging, projection, and reinforcement are delegated to Tesseract's
// public v0.11 API; this package only maps Nanite's wire types onto that API.
package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	tesseractMemory "github.com/hollis-labs/tesseract/memory"
)

// Public option types and constants re-export Tesseract's typed v0.11
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
	Namespace string `json:"namespace"`
	Scope     string `json:"scope,omitempty"`
	MemoryKey string `json:"memory_key"`
	Summary   string `json:"summary,omitempty"`
	Body      string `json:"body,omitempty"`
	Origin    string `json:"origin,omitempty"`
	Trigger   string `json:"trigger,omitempty"`
	// Confidence deliberately has no omitempty. Zero is a valid confidence,
	// not evidence that a projected field was absent.
	Confidence float64  `json:"confidence"`
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

// RecallPage is Tesseract's projected recall document plus its matching
// manifest. Results is kept in the exact upstream shape: []RecallResult for
// full mode and []ProjectedResult for keys/summary mode. Keeping that pair
// intact is what makes bytes_returned, tokens_estimate, budgets, cursors, and
// estimate-only describe the document a caller actually receives.
type RecallPage struct {
	Results  any                       `json:"results,omitempty"`
	Manifest *tesseractMemory.Manifest `json:"manifest,omitempty"`
}

// Service provides storage and recall through Tesseract's public memory API.
type Service struct {
	store   *tesseractMemory.Store
	appUser string
}

func NewService(store *tesseractMemory.Store) *Service { return &Service{store: store} }

// Store writes a memory-domain revision. Tesseract v0.11 validates that the
// namespace is typed (for example user/alice/memory/notes).
func (s *Service) Store(ctx context.Context, m Memory) error {
	return s.write(ctx, m, false)
}

func (s *Service) write(ctx context.Context, m Memory, appOwned bool) error {
	if s.store == nil {
		return fmt.Errorf("memory service: no memory store configured")
	}
	status := tesseractMemory.StatusDraft
	if m.Status != "" {
		status = tesseractMemory.Status(m.Status)
	}
	in := tesseractMemory.WriteInput{
		Domain:      tesseractMemory.DomainMemory,
		Namespace:   m.Namespace,
		MemoryKey:   m.MemoryKey,
		Status:      status,
		Author:      tesseractMemory.Author{AgentID: "nanite", AgentVersion: "1.0"},
		Trigger:     mapTrigger(m.Trigger),
		SessionID:   m.SessionID,
		DerivedFrom: mapDerivedFrom(m.Origin),
		Confidence:  m.Confidence,
		Tags:        m.Tags,
		Summary:     m.Summary,
		Body:        m.Body,
	}
	if appOwned {
		if !AppMemoryAccessible(s.appUser, m.Namespace, m.MemoryKey) || !strings.HasPrefix(m.Namespace, AppMemoryPrefix()+"/") {
			return fmt.Errorf("app memory write requires the host-owned scope and user key")
		}
		in.Actor, in.ClientID = "app:nanite", "nanite"
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
	if opts.Search != "" || opts.Offset != 0 || readsAppMemory(opts) {
		memories, _, err := s.List(ctx, opts)
		return memories, err
	}
	page, err := s.RecallPage(ctx, opts)
	if err != nil {
		return nil, err
	}
	return memoriesFromProjectedResults(page.Results)
}

// RecallPage delegates candidate selection, ordering, filtering, paging,
// projection accounting, and cursor validation to Tesseract v0.11. It does not
// flatten Tesseract's projected result document into Nanite's legacy Memory
// shape because doing so would invalidate the upstream manifest and budgets.
func (s *Service) RecallPage(ctx context.Context, opts RecallOpts) (RecallPage, error) {
	if readsAppMemory(opts) {
		return RecallPage{}, fmt.Errorf("app memory requires user-filtered Recall/List; upstream cursor manifests cannot express this host filter")
	}
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
	if opts.Search != "" || opts.Offset != 0 {
		return RecallPage{}, fmt.Errorf("tesseract_recall: search and offset belong to the legacy List surface, not cursor paging")
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
	results := paged.Results
	if opts.EstimateOnly {
		// Tesseract computes the same projected rows in estimate mode so its
		// manifest is exact; the caller-facing surface withholds those rows.
		results = nil
	}
	manifest := paged.Manifest
	return RecallPage{Results: results, Manifest: &manifest}, nil
}

// List retains Nanite's offset-based HTTP/UI list contract. It deliberately
// has no manifest or budget knobs: callers that need those use RecallPage and
// receive Tesseract's exact projected document.
func (s *Service) List(ctx context.Context, opts RecallOpts) ([]Memory, int, error) {
	if s.store == nil {
		return nil, 0, fmt.Errorf("memory service: no memory store configured")
	}
	if opts.Cursor != "" || opts.BudgetBytes != 0 || opts.BudgetTokens != 0 || opts.EstimateOnly {
		return nil, 0, fmt.Errorf("tesseract_recall: list cannot be combined with cursor, budgets, or estimate_only")
	}
	if opts.Offset < 0 {
		return nil, 0, fmt.Errorf("tesseract_recall: offset must not be negative")
	}
	mode, err := payloadMode(opts.PayloadMode)
	if err != nil {
		return nil, 0, err
	}
	if opts.Search != "" || readsAppMemory(opts) {
		return s.recallLegacySearch(ctx, opts, mode)
	}
	in, err := recallInput(opts)
	if err != nil {
		return nil, 0, err
	}
	if mode == tesseractMemory.PayloadModeFull && in.Limit > tesseractMemory.MaxRecallLimitFull {
		in.Limit = tesseractMemory.MaxRecallLimitFull
	}
	in.Offset = opts.Offset
	result, err := s.store.RecallPage(ctx, in)
	if err != nil {
		return nil, 0, fmt.Errorf("tesseract_recall: %w", err)
	}
	projected := tesseractMemory.ProjectResults(result.Results, mode)
	memories, err := memoriesFromProjectedResults(projected)
	if err != nil {
		return nil, 0, err
	}
	return memories, result.Total, nil
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
		filters.DerivedFrom = append(filters.DerivedFrom, tesseractMemory.DerivedFrom(origin))
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
		Namespaces: retainedReadNamespaces(opts.Namespaces),
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
func (s *Service) recallLegacySearch(ctx context.Context, opts RecallOpts, mode tesseractMemory.PayloadMode) ([]Memory, int, error) {
	if opts.Cursor != "" || opts.BudgetBytes != 0 || opts.BudgetTokens != 0 || opts.EstimateOnly {
		return nil, 0, fmt.Errorf("tesseract_recall: legacy search cannot be combined with cursor, budgets, or estimate_only")
	}
	base := opts
	base.Search = ""
	base.Offset = 0
	base.Limit = tesseractMemory.MaxRecallLimit
	in, err := recallInput(base)
	if err != nil {
		return nil, 0, err
	}

	folded := strings.ToLower(opts.Search)
	var matches []tesseractMemory.RecallResult
	for offset := 0; ; offset += tesseractMemory.MaxRecallLimit {
		in.Offset = offset
		page, recallErr := s.store.RecallPage(ctx, in)
		if recallErr != nil {
			return nil, 0, fmt.Errorf("tesseract_recall: %w", recallErr)
		}
		for _, result := range page.Results {
			haystack := strings.ToLower(result.Revision.Payload.Summary + "\n" + result.Revision.Payload.Body)
			if AppMemoryAccessible(s.appUser, result.Revision.Namespace, result.Revision.MemoryKey) && strings.Contains(haystack, folded) {
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
		return nil, 0, fmt.Errorf("tesseract_recall: offset must not be negative")
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
		return []Memory{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	projected := tesseractMemory.ProjectResults(matches[offset:end], mode)
	memories, err := memoriesFromProjectedResults(projected)
	if err != nil {
		return nil, 0, err
	}
	return memories, total, nil
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

func memoriesFromProjectedResults(results any) ([]Memory, error) {
	switch rows := results.(type) {
	case nil:
		return []Memory{}, nil
	case []tesseractMemory.RecallResult:
		memories := make([]Memory, 0, len(rows))
		for _, result := range rows {
			m := revisionToMemory(result.Revision)
			m.Score = result.Score
			memories = append(memories, m)
		}
		return memories, nil
	case []tesseractMemory.ProjectedResult:
		memories := make([]Memory, 0, len(rows))
		for _, result := range rows {
			revision := result.Revision
			m := Memory{
				Namespace: revision.Namespace, Scope: strings.Split(revision.Namespace, "/")[0], MemoryKey: revision.MemoryKey,
				RevisionID: revision.RevisionID, Status: string(revision.Status),
				Tags: revision.Tags, Score: result.Score, PayloadMode: string(result.PayloadMode),
			}
			if revision.Confidence != nil {
				m.Confidence = *revision.Confidence
			}
			if revision.Payload != nil {
				m.Summary = revision.Payload.Summary
			}
			memories = append(memories, m)
		}
		return memories, nil
	default:
		return nil, fmt.Errorf("tesseract_recall: unexpected projected results type %T", results)
	}
}

// Get resolves the current revision and reinforces its activation.
func (s *Service) Get(ctx context.Context, namespace, memoryKey string) (*Memory, error) {
	if !AppMemoryAccessible(s.appUser, namespace, memoryKey) {
		return nil, fmt.Errorf("app memory is not visible to this user")
	}
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
	before, err := s.store.GetRevisionByID(ctx, revisionID)
	if err != nil {
		return nil, fmt.Errorf("tesseract_get_revision: %w", err)
	}
	if !AppMemoryAccessible(s.appUser, before.Namespace, before.MemoryKey) {
		return nil, fmt.Errorf("app memory is not visible to this user")
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
	for _, id := range revisionIDs {
		revision, err := s.store.GetRevisionByID(ctx, id)
		if err != nil {
			return fmt.Errorf("tesseract_touch: lookup: %w", err)
		}
		if !AppMemoryAccessible(s.appUser, revision.Namespace, revision.MemoryKey) {
			return fmt.Errorf("app memory is not visible to this user")
		}
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
	if !AppMemoryAccessible(s.appUser, rev.Namespace, rev.MemoryKey) {
		return fmt.Errorf("app memory is not visible to this user")
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
	revision, err := s.store.GetRevisionByID(ctx, revisionID)
	if err != nil {
		return fmt.Errorf("tesseract_deprecate: lookup: %w", err)
	}
	if strings.HasPrefix(revision.Namespace, "user/") || !AppMemoryAccessible(s.appUser, revision.Namespace, revision.MemoryKey) {
		return fmt.Errorf("tesseract_deprecate: protected or foreign memory is read-only")
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
	return "session/" + sessionID + "/memory/" + resolveMemoryType(memoryType)
}

// ProjectNamespace returns a writable typed project namespace. It defaults to
// notes for compatibility with existing one-argument call sites.
func ProjectNamespace(projectID string, memoryType ...string) string {
	return "project/" + projectID + "/memory/" + resolveMemoryType(memoryType)
}

// UserNamespace returns a writable typed user namespace. It defaults to notes
// for compatibility with existing one-argument call sites.
func UserNamespace(userID string, memoryType ...string) string {
	return "user/" + userID + "/memory/" + resolveMemoryType(memoryType)
}

// SessionMemoryPrefix spans all typed memory namespaces in one session.
func SessionMemoryPrefix(sessionID string) string {
	return "session/" + sessionID + "/memory"
}

// ProjectMemoryPrefix spans all typed memory namespaces in one project.
func ProjectMemoryPrefix(projectID string) string {
	return "project/" + projectID + "/memory"
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
func AllNaniteNamespaces() []string { return []string{UserMemoryPrefix("default"), AppMemoryPrefix()} }

func revisionToMemory(rev tesseractMemory.Revision) Memory {
	return Memory{
		Namespace:  rev.Namespace,
		Scope:      strings.Split(rev.Namespace, "/")[0],
		MemoryKey:  rev.MemoryKey,
		Summary:    rev.Payload.Summary,
		Body:       rev.Payload.Body,
		Origin:     string(rev.DerivedFrom),
		Trigger:    string(rev.Trigger),
		Confidence: rev.Confidence,
		Tags:       rev.Tags,
		SessionID:  rev.SessionID,
		RevisionID: rev.RevisionID,
		Status:     string(rev.Status),
	}
}

func mapDerivedFrom(value string) tesseractMemory.DerivedFrom {
	switch value {
	case "user":
		return tesseractMemory.DerivedFromUser
	case "feedback":
		return tesseractMemory.DerivedFromFeedback
	case "project":
		return tesseractMemory.DerivedFromProject
	case "reference":
		return tesseractMemory.DerivedFromReference
	case "observation", "":
		return tesseractMemory.DerivedFromObservation
	default:
		return tesseractMemory.DerivedFrom(value)
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

// retainedReadNamespaces keeps the default user's existing project/session
// records discoverable after new writes use the scope-rooted grammar. It never
// rewrites records, changes exact-key lookup, or supplies write authority.
func retainedReadNamespaces(namespaces []string) []string {
	out := make([]string, 0, len(namespaces)*2)
	seen := make(map[string]bool)
	add := func(value string) {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	for _, namespace := range namespaces {
		add(namespace)
		scoped := strings.TrimPrefix(namespace, "user/default/")
		parts := strings.Split(scoped, "/")
		if len(parts) >= 3 && (parts[0] == "project" || parts[0] == "session") && parts[1] != "" && parts[2] == "memory" {
			add(scoped)
			add("user/default/" + scoped)
		}
	}
	return out
}
