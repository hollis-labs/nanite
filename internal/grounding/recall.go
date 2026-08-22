package grounding

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/memory"
)

// RecallTimeout is the maximum wall-clock budget for the Vanta recall call.
// On timeout the step settles for an empty result, never an error.
const RecallTimeout = 1500 * time.Millisecond

// GroundingNamespace returns the Conduit namespace used for the grounding
// recall query. Convention mirrors ToolPatternsNamespace in internal/toolclient.
//
// The caller passes user (empty → "default"). In v1 there is no per-agent or
// per-workspace scoping; that is a deferred follow-up per the ticket's scope
// fences.
func GroundingNamespace(user string) string {
	if user == "" {
		user = "default"
	}
	return fmt.Sprintf("user/%s/project/nanite/memory", user)
}

// Recaller wraps the memory.Service to provide the grounding-specific recall
// API. It is a thin adapter — the grounding package does not know about Conduit
// internals; it calls memory.Service.Recall with grounding-specific options.
type Recaller struct {
	svc   *memory.Service
	limit int
}

// NewRecaller returns a Recaller backed by the given memory.Service. A nil
// service is accepted and yields a no-op: Recall returns an empty
// GroundingResult with Enabled=false.
//
// limit controls how many memories are requested from Vanta per turn.
// <= 0 defaults to DefaultRecallLimit.
func NewRecaller(svc *memory.Service, limit int) *Recaller {
	if limit <= 0 {
		limit = DefaultRecallLimit
	}
	return &Recaller{svc: svc, limit: limit}
}

// IsGroundingEnabled returns true when NANITE_GROUNDING_ENABLED=true is set in
// the environment. When false, Recall is a no-op.
//
// The ticket specifies default OFF, matching UAT-time stance. A settings flag
// (memory.grounding.enabled) is the long-term home; for v1 the env var is the
// toggle. When a proper settings package exists, the caller should check it
// first and only call IsGroundingEnabled as a fallback.
func IsGroundingEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_GROUNDING_ENABLED")))
	return v == "true" || v == "1" || v == "yes"
}

// RecallInput carries the parameters for a single pre-strategy recall pass.
type RecallInput struct {
	// UserInput is the raw user text for this turn. Used as the recall query.
	UserInput string
	// SessionID is the current Chat session identifier.
	SessionID string
	// UserID is the user namespace root. Empty falls back to "default".
	UserID string
	// TurnID is the message ID of the user turn. Optional; stored in the
	// consultation log for traceability.
	TurnID string
}

// Recall performs the pre-strategy memory lookup. It:
//
//  1. Returns an empty result (Enabled=false) if the grounding gate is off.
//  2. Returns an empty result (Enabled=true, TimedOut=true) if the Vanta call
//     exceeds RecallTimeout.
//  3. Returns all hits sorted descending by Similarity, with Surfaced populated
//     to the top-K above SimilarityThreshold.
//
// Errors from the underlying Vanta call are logged but never returned — the
// recall step is best-effort.
func (r *Recaller) Recall(ctx context.Context, in RecallInput) GroundingResult {
	if !IsGroundingEnabled() {
		return GroundingResult{Enabled: false}
	}
	if r.svc == nil {
		slog.Debug("grounding: recall skipped — memory service not configured")
		return GroundingResult{Enabled: true, SessionID: in.SessionID, UserID: in.UserID, RecalledAt: time.Now()}
	}

	// Build query excerpt for logging.
	excerpt := in.UserInput
	if len(excerpt) > 200 {
		excerpt = excerpt[:200]
	}

	// Run with a bounded timeout so recall can never block dispatch.
	tctx, cancel := context.WithTimeout(ctx, RecallTimeout)
	defer cancel()

	ns := GroundingNamespace(in.UserID)
	opts := memory.RecallOpts{
		Namespaces: []string{ns},
		Ranking:    "relevance",
		Query:      in.UserInput,
		Limit:      r.limit,
	}

	start := time.Now()
	mems, err := r.svc.Recall(tctx, opts)
	elapsed := time.Since(start)

	base := GroundingResult{
		Enabled:      true,
		SessionID:    in.SessionID,
		UserID:       in.UserID,
		QueryExcerpt: excerpt,
		RecalledAt:   time.Now(),
	}

	if err != nil {
		if tctx.Err() != nil {
			slog.Warn("grounding: recall timed out", "elapsed_ms", elapsed.Milliseconds())
			base.TimedOut = true
		} else {
			slog.Warn("grounding: recall error (non-fatal)", "err", err, "elapsed_ms", elapsed.Milliseconds())
		}
		return base
	}

	slog.Debug("grounding: recall complete", "hits", len(mems), "elapsed_ms", elapsed.Milliseconds())

	// Convert to typed MemoryHit slice, sort descending by Similarity.
	hits := make([]MemoryHit, 0, len(mems))
	for _, m := range mems {
		hits = append(hits, MemoryHit{
			MemoryKey:  m.MemoryKey,
			Namespace:  m.Namespace,
			Summary:    m.Summary,
			Body:       m.Body,
			Similarity: m.Confidence, // Conduit stores similarity as confidence on recall
			Origin:     m.Origin,
			Tags:       m.Tags,
			RevisionID: m.RevisionID,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		return hits[i].Similarity > hits[j].Similarity
	})

	// Build surfaced subset: above threshold, capped at MaxSurfacedMemories,
	// and within the MaxSurfaceTokens budget.
	surfaced := make([]MemoryHit, 0, MaxSurfacedMemories)
	usedTokens := 0
	for _, h := range hits {
		if h.Similarity < SimilarityThreshold {
			continue
		}
		if len(surfaced) >= MaxSurfacedMemories {
			break
		}
		// Token budget check: estimate tokens for this entry's summary.
		entryTokens := len(h.Summary)/4 + 5 // 5 tokens for bullet + separator overhead
		if usedTokens+entryTokens > MaxSurfaceTokens {
			break
		}
		surfaced = append(surfaced, h)
		usedTokens += entryTokens
	}

	base.Hits = hits
	base.Surfaced = surfaced
	return base
}

// SystemPromptBlock renders the "## Relevant memories" block to inject into
// the LLM system prompt for a turn. Returns empty string when there are no
// surfaced memories (the caller should not add a blank section).
func SystemPromptBlock(result GroundingResult) string {
	if !result.Enabled || len(result.Surfaced) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Relevant memories\n")
	for _, m := range result.Surfaced {
		sb.WriteString("- ")
		sb.WriteString(m.Summary)
		sb.WriteString("\n")
	}
	return sb.String()
}

// LogConsultations writes one grounding_consultations row per hit to the logger.
// consumed=true for hits in result.Surfaced, false for the rest. Returns the row
// IDs of surfaced rows (for outcome write-back). Errors are logged but never
// propagated — logging must not gate dispatch.
func LogConsultations(logger ConsultationLogger, result GroundingResult, turnID string) []int64 {
	if logger == nil || !result.Enabled || len(result.Hits) == 0 {
		return nil
	}

	// Build a lookup for surfaced keys.
	surfacedKeys := make(map[string]struct{}, len(result.Surfaced))
	for _, s := range result.Surfaced {
		surfacedKeys[s.MemoryKey] = struct{}{}
	}

	var surfacedIDs []int64
	for _, h := range result.Hits {
		_, consumed := surfacedKeys[h.MemoryKey]
		id, err := logger.LogGroundingConsultation(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, ConsultationEntry{
			SessionID:  result.SessionID,
			TurnID:     turnID,
			MemoryKey:  h.MemoryKey,
			Namespace:  h.Namespace,
			Summary:    h.Summary,
			Similarity: h.Similarity,
			Consumed:   consumed,
		})
		if err != nil {
			slog.Warn("grounding: log consultation error (non-fatal)", "err", err, "memory_key", h.MemoryKey)
			continue
		}
		if consumed {
			surfacedIDs = append(surfacedIDs, id)
		}
	}
	return surfacedIDs
}
