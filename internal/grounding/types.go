// Package grounding implements memory-as-grounding (CW-20260419-0028, Phase 5 / E2).
//
// Pre-strategy-loop memory recall: before dispatch classification runs, the
// grounding layer queries Vanta for memories relevant to the current user
// input. Results are typed structs (not free text) so the E3 strategy loop
// can consume them directly.
//
// The two key outputs are:
//   - GroundingResult   — typed recall results for the current turn.
//   - Outcome           — heuristic-derived signal from the user's follow-up.
//
// The consultation log and outcome log are written to SQLite via the
// ConsultationLogger interface (implemented by *store.Store in
// internal/store/grounding_log.go).
//
// Privacy: the recall step is gated by NANITE_GROUNDING_ENABLED=true (or the
// memory.grounding.enabled settings flag). When the gate is off the step is a
// no-op and no rows are written.
package grounding

import "time"

// DefaultRecallLimit is the number of memories fetched per turn when not
// overridden. E3 / settings may lower this; never exceed 10 in v1.
const DefaultRecallLimit = 5

// SimilarityThreshold is the minimum similarity score for a memory to be
// included in the system-prompt block surfaced to the LLM. Memories below
// this threshold are still logged (consumed=false) but not forwarded to the
// model.
const SimilarityThreshold = 0.55

// MaxSurfacedMemories caps the LLM-facing "## Relevant memories" block to
// this many items, regardless of how many hits exceeded the threshold.
const MaxSurfacedMemories = 3

// MaxSurfaceTokens is the per-turn token budget for the surfaced memories
// block. We estimate 4 bytes / token.
const MaxSurfaceTokens = 200

// MemoryHit is one recalled memory, typed for consumption by the strategy /
// dispatch layers. It does not carry the raw Conduit Revision — only the
// fields grounding consumers need.
type MemoryHit struct {
	// MemoryKey is the Conduit memory key (unique within its namespace).
	MemoryKey string `json:"memory_key"`
	// Namespace is the Conduit namespace the hit came from.
	Namespace string `json:"namespace"`
	// Summary is the short summary stored with the memory.
	Summary string `json:"summary"`
	// Body is the full body text (may be empty for compact memories).
	Body string `json:"body"`
	// Similarity is the recall score [0, 1]. May be 0 when the recall mode
	// does not produce scores (activation / chronological). Relevance mode
	// produces scores; that is what grounding uses.
	Similarity float64 `json:"similarity"`
	// Origin is the memory origin tag (user, feedback, project, etc.).
	Origin string `json:"origin"`
	// Tags are the memory's tags.
	Tags []string `json:"tags"`
	// RevisionID is the Conduit revision identifier, used for write-back
	// of outcome signals.
	RevisionID string `json:"revision_id,omitempty"`
}

// GroundingResult is the output of a single pre-strategy memory recall step.
// It carries all hits (including below-threshold ones) and a pre-filtered
// slice ready for LLM injection.
type GroundingResult struct {
	// Hits contains all memories returned by Vanta, sorted descending by
	// Similarity. Hits below SimilarityThreshold are included here so the
	// consultation logger can mark them consumed=false.
	Hits []MemoryHit `json:"hits"`

	// Surfaced contains the subset of Hits that exceeded SimilarityThreshold
	// and fit within MaxSurfacedMemories. These are the memories that will be
	// injected into the LLM's system prompt block.
	Surfaced []MemoryHit `json:"surfaced"`

	// SessionID of the turn that produced this result.
	SessionID string `json:"session_id"`
	// UserID used as the recall namespace root. Empty means "default".
	UserID string `json:"user_id,omitempty"`
	// QueryExcerpt is the first 200 chars of the user input used as the
	// recall query. Stored in the consultation log.
	QueryExcerpt string `json:"query_excerpt,omitempty"`
	// RecalledAt is the wall-clock time the recall completed.
	RecalledAt time.Time `json:"recalled_at"`
	// TimedOut is true when the recall call was abandoned after the
	// configurable timeout. The result is still usable but Hits may be
	// empty.
	TimedOut bool `json:"timed_out,omitempty"`
	// Enabled reports whether the grounding gate was on. When false, all
	// other fields are zero-value and no log rows are written.
	Enabled bool `json:"enabled"`
}

// OutcomeKind classifies the user's follow-up signal relative to the
// grounding result.
type OutcomeKind string

const (
	// OutcomeAccepted means the user's next turn appeared to accept the
	// result (no pruning words detected; not a quick narrow refinement).
	OutcomeAccepted OutcomeKind = "accepted"
	// OutcomeRefined means the user appeared to narrow or re-scope the
	// request, suggesting the grounded response was too broad.
	OutcomeRefined OutcomeKind = "refined"
	// OutcomeUnknown is used when insufficient signal is available
	// (no follow-up turn observed within the observation window).
	OutcomeUnknown OutcomeKind = "unknown"
)

// Outcome is the write-back signal derived from the user's follow-up turn.
// One Outcome row is written per grounding consultation once a follow-up
// is observed (or the window expires).
type Outcome struct {
	// ConsultationID references the grounding_consultations row this
	// outcome belongs to. Set by the store after insert.
	ConsultationID int64 `json:"consultation_id"`
	// Kind is the heuristic classification.
	Kind OutcomeKind `json:"kind"`
	// FollowUpExcerpt is the first 200 chars of the follow-up user turn.
	FollowUpExcerpt string `json:"follow_up_excerpt,omitempty"`
	// SecondsSinceAck is the wall-clock gap between the assistant ack and
	// the follow-up user turn. Used by the refinement heuristic.
	SecondsSinceAck float64 `json:"seconds_since_ack,omitempty"`
	// PruningWordsFound lists the pruning words detected in the follow-up
	// (e.g. ["just", "only"]). Empty when Kind != OutcomeRefined.
	PruningWordsFound []string `json:"pruning_words_found,omitempty"`
}

// ConsultationEntry is one row in grounding_consultations. One row per
// memory hit per turn (not one row per turn).
type ConsultationEntry struct {
	// SessionID of the turn.
	SessionID string
	// TurnID is the message ID of the user turn (may be empty when not tracked).
	TurnID string
	// MemoryKey of the recalled hit.
	MemoryKey string
	// Namespace of the recalled hit.
	Namespace string
	// Summary of the recalled hit.
	Summary string
	// Similarity score of the recalled hit.
	Similarity float64
	// Consumed is true when the hit exceeded the similarity threshold and
	// was included in the LLM-facing system-prompt block.
	Consumed bool
}

// ConsultationLogger is the narrow store surface the grounding layer uses
// to persist consultation rows. *store.Store satisfies this interface.
type ConsultationLogger interface {
	// LogGroundingConsultation persists one consultation entry.
	// Returns the auto-assigned row ID so outcome write-back can reference it.
	// Errors are returned; callers should log and continue — never gate dispatch.
	LogGroundingConsultation(entry ConsultationEntry) (int64, error)

	// LogGroundingOutcome persists one outcome entry referencing a consultation row.
	LogGroundingOutcome(outcome Outcome) error
}
