package reactions

// TASKS/harness-reactive-self-tools/05-selftool-reaction-telemetry.md —
// telemetry for fired (and defensively-skipped) self-tool reactions. A
// new, parallel, thin wrapper mirroring internal/agent/reflexes/
// telemetry.go's EmitFirings/TraceStore split in *shape* only —
// deliberately not sharing code with it, per docs/engineering/
// architecture/11-harness-reactive-self-tools.md's "Telemetry" section
// and TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md
// decision 7: reflexes' own mechanisms stay untouched by this batch.
//
// Sink: event_log (internal/store/events.go), reused as-is — no schema
// change. Field mapping (matching the design doc's own list verbatim):
//
//   - event_type = the fired reaction's kind slug (render_card,
//     internal_api_call, external_api_call, callback)
//   - category   = CategorySelftoolReaction ("selftool_reaction") — a new
//     value, distinct from reflexes' own "reflex" (internal/agent/
//     reflexes/telemetry.go's EmitFirings), so the two telemetry streams
//     stay independently queryable via the existing
//     (*store.Store).ListEvents(category, limit) path with no
//     cross-contamination.
//   - detail     = the tool name (result.ToolName)
//   - metadata   = a JSON traceRecord: tool name, reaction id/kind/
//     category, the config used, the triggering tool_call_id (caller-
//     local context threaded in the same way reflexes' own
//     FiringContext threads audit context Resolve() itself never sees —
//     see traceRecord's own doc comment), and success/error via the
//     outcome field. Outcome is reactions.Outcome's own value ("success",
//     "error", or "skipped_not_implemented"), copied verbatim from
//     FiredReaction.Outcome — result.go's own doc comment explains Result
//     was designed specifically so this fold-in needs nothing beyond
//     Result plus a caller-supplied tool_call_id, without Fire's own
//     signature changing.
//
// Full coverage: EmitReactionTrace writes one event_log row per entry in
// result.Reactions, unconditionally — a defensively-skipped
// implemented=false reaction gets a row exactly like an executed one
// does (distinguished only by its outcome field), so nothing about what
// reactions.Fire found configured for a tool silently vanishes from the
// trace.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// CategorySelftoolReaction is the event_log.category value every
// EmitReactionTrace row uses — distinct from reflexes' own "reflex"
// category (internal/agent/reflexes/telemetry.go's EmitFirings), so the
// two telemetry streams stay independently queryable via the existing
// ListEvents(category, limit) path with no cross-contamination.
const CategorySelftoolReaction = "selftool_reaction"

// TraceStore is the narrow persistence surface EmitReactionTrace needs:
// the event_log write. *store.Store (internal/store/events.go) already
// satisfies this via its LogEvent method — kept as a narrow, one-method
// interface, not the full *store.Store, mirroring internal/agent/
// reflexes/telemetry.go's TraceStore narrowing (internal/agent/reflexes/
// telemetry.go:56-60). Unlike that TraceStore, this one carries no
// fired_count/last_fired_at bump method: that state is reflex-specific
// (agent_reflexes rows) — self-tool reactions have no equivalent counter
// to bump, so adding an unused method here would only widen this
// package's dependency surface for nothing.
type TraceStore interface {
	LogEvent(sessionID, eventType, category, detail, metadata string)
}

// traceRecord is the one consistent shape every fired (or
// defensively-skipped) self-tool reaction emits to the unified sink —
// built entirely from data reactions.Fire already computed (one
// FiredReaction) plus the one piece of caller-local context Fire itself
// never sees: the triggering tool_call_id. Mirrors internal/agent/
// reflexes/telemetry.go's FiringContext split (caller-local audit
// context that isn't part of the engine's own decision logic, kept out
// of Fire's own signature) without introducing a same-named type here —
// a single string parameter is the whole of this package's caller-local
// context need, so a dedicated struct would be ceremony without benefit.
type traceRecord struct {
	ToolName   string          `json:"tool_name"`
	ReactionID string          `json:"reaction_id"`
	Kind       string          `json:"kind"`
	Category   string          `json:"category,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Outcome    string          `json:"outcome"`
	Error      string          `json:"error,omitempty"`
}

// EmitReactionTrace is the sink every harness-reactive self-tool handler
// is expected to call immediately after reactions.Fire returns (the
// worked example, TASKS/harness-reactive-self-tools/
// 07-worked-example-task-update-report.md, wires this in as the concrete
// proof) — one event_log row per entry in result.Reactions, regardless
// of outcome (see this file's package doc comment for the full field
// mapping). A no-op (nil error, nothing written) when result.Reactions
// is empty, the normal case for the overwhelming majority of self-tools
// that have no configured reactions at all.
//
// Returns a non-nil error only when ts is nil — a caller-programming
// error, not a per-reaction failure. An individual reaction's own
// outcome (success/error/skipped_not_implemented) is always recorded in
// its own row instead of aborting the loop over its siblings; a
// per-reaction JSON-marshal failure (defensive only — traceRecord's own
// fields are all plain strings plus a raw-JSON passthrough of
// FiredReaction.Config, which reactions.Fire only ever populates from a
// selftool_reactions.config column) falls back to an empty "{}" metadata
// blob for that one row, logs a warning, and continues rather than
// dropping the row or aborting its siblings.
func EmitReactionTrace(ctx context.Context, ts TraceStore, toolCallID string, result Result) error {
	if ts == nil {
		return fmt.Errorf("reactions.EmitReactionTrace(%q): TraceStore is nil", result.ToolName)
	}
	if len(result.Reactions) == 0 {
		return nil
	}

	logger := slog.Default()

	for _, fr := range result.Reactions {
		rec := traceRecord{
			ToolName:   result.ToolName,
			ReactionID: fr.ReactionID,
			Kind:       fr.Kind,
			Category:   fr.Category,
			ToolCallID: toolCallID,
			Outcome:    string(fr.Outcome),
			Error:      fr.Error,
		}
		if fr.Config != "" {
			// Embedded verbatim as a nested JSON value (not a
			// double-escaped string) — fr.Config is already raw JSON
			// text, as stored in selftool_reactions.config (result.go's
			// own doc comment on FiredReaction.Config).
			rec.Config = json.RawMessage(fr.Config)
		}

		metaJSON, err := json.Marshal(rec)
		if err != nil {
			logger.Warn("reactions.EmitReactionTrace: marshal trace record failed",
				"tool", result.ToolName, "reaction_id", fr.ReactionID, "kind", fr.Kind, "err", err)
			metaJSON = []byte("{}")
		}

		ts.LogEvent("", fr.Kind, CategorySelftoolReaction, result.ToolName, string(metaJSON))
	}

	return nil
}
