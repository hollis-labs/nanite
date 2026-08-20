package reactions

import "encoding/json"

// Outcome is the per-reaction result Fire recorded for one
// selftool_reactions row — designed so TASKS/harness-reactive-self-tools/
// 05-selftool-reaction-telemetry.md's EmitReactionTrace can fold it
// directly into event_log.metadata's "outcome" field (see that task's own
// Context: "success"/"error"/"skipped_not_implemented") without Fire's
// signature changing.
type Outcome string

const (
	// OutcomeSuccess: the reaction kind executed (internal_api_call — a
	// non-2xx response counts as OutcomeError, not this) or resolved
	// (render_card) without error.
	OutcomeSuccess Outcome = "success"
	// OutcomeError: the reaction kind is implemented and Fire attempted
	// it, but execution/resolution failed (a non-2xx internal_api_call
	// response, a config parse failure, a reaction_kind lookup failure,
	// etc). Recorded per-reaction; does not abort Fire's loop over
	// sibling reactions.
	OutcomeError Outcome = "error"
	// OutcomeSkippedNotImplemented: the row's reaction_kind is not
	// implemented=true (external_api_call/callback today, or any future
	// kind added to selftool_reaction_kinds before this engine grows real
	// execution code for it) — Fire records this rather than silently
	// pretending the reaction fired.
	OutcomeSkippedNotImplemented Outcome = "skipped_not_implemented"
)

// FiredReaction is Fire's per-row result — one per selftool_reactions row
// it processed for the tool call.
type FiredReaction struct {
	// ReactionID is the selftool_reactions.id row this result came from.
	ReactionID string `json:"reaction_id"`
	// Kind is reaction_kind_id (a selftool_reaction_kinds.slug value —
	// render_card/internal_api_call/external_api_call/callback).
	Kind string `json:"kind"`
	// Category is the reaction kind's selftool_reaction_kinds.category
	// ("render" or "execute") — carried through for 05's telemetry
	// metadata without a second lookup. Empty when the kind lookup itself
	// failed (Outcome == OutcomeError in that case too).
	Category string `json:"category,omitempty"`
	// Config is the raw JSON config used (selftool_reactions.config, as
	// stored) — carried verbatim so 05's telemetry can fold "the config
	// used" into its trace record without a second store read.
	Config string `json:"config"`
	// Outcome is this reaction's result — see the Outcome type.
	Outcome Outcome `json:"outcome"`
	// Error is populated when Outcome == OutcomeError — a short,
	// human-readable failure reason (config parse failure, non-2xx HTTP
	// response, reaction_kind lookup failure). Empty otherwise.
	Error string `json:"error,omitempty"`
	// Payload is the resolved JSON output this reaction kind produced,
	// when applicable. Only render_card populates this today — the
	// resolved envelope payload (render_card.go's ResolveRenderCard);
	// TASKS/harness-reactive-self-tools/04-render-card-construction.md's
	// marker-embedding helper reads this field (via RenderCardPayload,
	// below) directly. internal_api_call performs a live side effect and
	// has no payload to surface, so this stays nil for that kind.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Result is Fire's return value for one Fire call (one harness-reactive
// self-tool invocation) — every enabled selftool_reactions row for
// ToolName, each independently resolved/executed/skipped, in Reactions.
// Designed so 05-selftool-reaction-telemetry.md's EmitReactionTrace can
// build one event_log row per entry in Reactions without Fire's own
// signature changing (tool name, reaction kind, config used, and
// success/error are all already present per-entry above).
type Result struct {
	// ToolName is the self-tool name Fire was called for (matches
	// selftool_reactions.tool_name).
	ToolName string `json:"tool_name"`
	// Reactions is one entry per enabled selftool_reactions row found for
	// ToolName, in the order ListEnabledSelftoolReactions returned them
	// (created_at ascending). Empty (not nil) when the tool has no
	// enabled reactions configured — the normal case for the ~70 existing
	// self-tools that aren't harness-reactive at all.
	Reactions []FiredReaction `json:"reactions"`
}

// RenderCardPayload returns the resolved JSON payload from the first
// successfully-resolved render_card reaction in Result, if any — the
// convenience accessor 04-render-card-construction.md's marker-embedding
// helper is expected to call rather than hand-scanning Reactions. Returns
// (nil, false) when no render_card reaction fired successfully (none
// configured, or the one configured failed to resolve).
func (r Result) RenderCardPayload() (json.RawMessage, bool) {
	for _, fr := range r.Reactions {
		if fr.Kind == KindRenderCard && fr.Outcome == OutcomeSuccess && len(fr.Payload) > 0 {
			return fr.Payload, true
		}
	}
	return nil, false
}
