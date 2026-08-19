package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
)

// recoveryInfoCardCancelToken is the wire-level wrap key the FE reads to
// surface a [Cancel retry] button on a recovery info-card. The token
// round-trips back to POST /api/sessions/{id}/recovery/cancel which
// resolves the broker and calls Broker.Cancel(sessionID, token).
//
// The key lives at envelope-wrap level (sibling of id/type/data) — the
// info-card schema (manifest/schemas/info-card.schema.json in
// go-envelopes) sets additionalProperties:false, so wire metadata that
// is not card-shaped MUST live outside `data`. The same convention is
// used by EnvelopeRouting (target / render_target / mode).
const recoveryInfoCardCancelToken = "cancel_token"

// recoveryEnvelopeKindMissing is the only envelope.Kind the projection
// cannot route. Treated as a programming error in the broker; the sink
// degrades to a generic error-report so the user is never left with a
// silent failure.
const recoveryEnvelopeKindMissing = ""

// recoveryGiphyQueries is the per-error Giphy decoration pool. Mirrors
// the convention in internal/chat/errors.go (errorGiphyQueries) so
// recovery error-reports look at home next to chat-loop error-reports.
var recoveryGiphyQueries = []string{
	"agent unavailable",
	"trying again later",
	"this is fine fire",
	"computer error funny",
	"oops mistake",
}

// recoveryEnvelopeSink satisfies broker.EnvelopeSink by projecting
// the broker's typed Envelope onto the existing plugin_envelope wire
// format. Each envelope.Kind maps onto a registered envelope schema:
//
//	info-card             -> manifest/schemas/info-card.schema.json
//	error-report          -> manifest/schemas/error-report.schema.json
//	chat-loop-terminated  -> manifest/schemas/chat-loop-terminated.schema.json
//
// The sink does not emit new envelope schemas — wire compatibility is
// the contract with the FE EnvelopeRenderer (no FE schema landing
// required for the projection to render).
//
// info-card emissions also carry a wrap-level `cancel_token` field so
// the FE can surface a [Cancel retry] affordance. The token round-
// trips through POST /api/sessions/{id}/recovery/cancel, which calls
// Broker.Cancel for session-scoped validation.
type recoveryEnvelopeSink struct {
	streams *StreamManager
}

// Emit projects env onto the wire and broadcasts it on the chat
// session's plugin_envelope SSE channel. Returns nil even on a missing
// StreamManager (degraded-mode boot, tests) — the broker treats sink
// errors as advisory and continues the recovery flow.
func (e *recoveryEnvelopeSink) Emit(sessionID string, env broker.Envelope) error {
	if e.streams == nil {
		return nil
	}
	envelopeType, payload, err := projectRecoveryEnvelope(env)
	if err != nil {
		slog.Warn("recovery_envelope_sink: project failed",
			"session_id", sessionID,
			"kind", env.Kind,
			"err", err,
		)
		return err
	}
	wrap, err := buildRecoveryEnvelopeWrap(envelopeType, payload, env.CancelToken)
	if err != nil {
		slog.Warn("recovery_envelope_sink: marshal wrap failed",
			"session_id", sessionID,
			"kind", env.Kind,
			"err", err,
		)
		return err
	}
	e.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
		Type:     "plugin_envelope",
		Envelope: string(wrap),
	})
	return nil
}

// projectRecoveryEnvelope maps a broker.Envelope onto the (envelope-
// type, json-payload) pair the FE EnvelopeRenderer consumes.
//
// Wire shape per Kind:
//
//   - info-card           -> {title, body, variant}                 (variant from Severity)
//   - error-report        -> {code, message, details, giphy_query, timestamp}
//   - chat-loop-terminated -> {reason, code, iteration, consecutive_failures, last_error?, last_tool?, timestamp}
//
// The chat-loop-terminated.code is fixed to "retry_budget_exhausted"
// (one of the schema's enum values) — the broker only reaches the
// chat-loop-terminated branch on CauseRestartExhausted, which matches
// the schema's "retry budget exhausted" semantics.
func projectRecoveryEnvelope(env broker.Envelope) (string, []byte, error) {
	switch env.Kind {
	case "info-card":
		payload, err := json.Marshal(infoCardPayload{
			Title:   env.Title,
			Body:    env.Content,
			Variant: variantFromSeverity(env.Severity),
		})
		if err != nil {
			return "", nil, fmt.Errorf("marshal info-card payload: %w", err)
		}
		return "info-card", payload, nil

	case "error-report":
		payload, err := json.Marshal(errorReportPayload{
			Code:       "agent_recovery_failed",
			Message:    formatErrorReportMessage(env.Title, env.Content),
			GiphyQuery: pickRecoveryGiphyQuery(),
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			return "", nil, fmt.Errorf("marshal error-report payload: %w", err)
		}
		return "error-report", payload, nil

	case "chat-loop-terminated":
		payload, err := json.Marshal(chatLoopTerminatedRecoveryPayload{
			Reason:              formatErrorReportMessage(env.Title, env.Content),
			Code:                "retry_budget_exhausted",
			Iteration:           0,
			ConsecutiveFailures: 0,
			Timestamp:           time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			return "", nil, fmt.Errorf("marshal chat-loop-terminated payload: %w", err)
		}
		return "chat-loop-terminated", payload, nil

	case recoveryEnvelopeKindMissing:
		// Defensive: broker should never emit an envelope without a
		// kind. Surface as a generic error-report so the user always
		// sees something rather than a silent broker drop.
		payload, err := json.Marshal(errorReportPayload{
			Code:       "agent_recovery_internal_error",
			Message:    "Recovery broker emitted an envelope without a kind.",
			GiphyQuery: pickRecoveryGiphyQuery(),
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			return "", nil, fmt.Errorf("marshal fallback error-report: %w", err)
		}
		return "error-report", payload, nil

	default:
		return "", nil, fmt.Errorf("unknown recovery envelope kind: %q", env.Kind)
	}
}

// buildRecoveryEnvelopeWrap produces the {id, type, data, display_class,
// cancel_token?} wire shape consumed by useChat's plugin_envelope handler.
// id is "" — recovery envelopes are stream-only signals (no DB row, no
// respond endpoint). cancel_token is omitted when empty so the FE can rely on
// presence to drive [Cancel retry] visibility.
func buildRecoveryEnvelopeWrap(envelopeType string, payload []byte, cancelToken string) ([]byte, error) {
	wrap := map[string]any{
		"id":            "", // stream-only; no DB row
		"type":          envelopeType,
		"data":          json.RawMessage(payload),
		"display_class": string(EnvelopeDisplayClassAlert),
	}
	if cancelToken != "" {
		wrap[recoveryInfoCardCancelToken] = cancelToken
	}
	return json.Marshal(wrap)
}

// variantFromSeverity maps the broker's Severity ("info"/"warning"/
// "error") onto the info-card schema's variant enum (info/success/
// warning/danger). The recovery broker never emits "success" — that
// would imply a positive outcome card, which is out of scope for v1.
func variantFromSeverity(severity string) string {
	switch severity {
	case "info":
		return "info"
	case "warning":
		return "warning"
	case "error":
		return "danger"
	default:
		return "info"
	}
}

// formatErrorReportMessage joins the broker's Title + Content into a
// single human-readable sentence for the error-report.message field.
// Empty inputs degrade gracefully; the schema requires non-empty
// strings.
func formatErrorReportMessage(title, content string) string {
	switch {
	case title != "" && content != "":
		return title + ": " + content
	case title != "":
		return title
	case content != "":
		return content
	default:
		return "Agent recovery failed."
	}
}

// pickRecoveryGiphyQuery returns one of the recovery-themed Giphy
// queries. math/rand is appropriate — the query is cosmetic UI garnish
// embedded in the envelope; no security decision depends on it.
//
//nolint:gosec // G404: cosmetic Giphy-query selection, non-security.
func pickRecoveryGiphyQuery() string {
	return recoveryGiphyQueries[rand.Intn(len(recoveryGiphyQueries))]
}

// infoCardPayload mirrors manifest/schemas/info-card.schema.json.
type infoCardPayload struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Variant string `json:"variant,omitempty"`
}

// errorReportPayload mirrors manifest/schemas/error-report.schema.json.
type errorReportPayload struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	GiphyQuery string         `json:"giphy_query"`
	Timestamp  string         `json:"timestamp"`
}

// chatLoopTerminatedRecoveryPayload mirrors
// manifest/schemas/chat-loop-terminated.schema.json. Distinct from
// service.chatLoopTerminatedPayload (which lives next to the chat-loop
// emitter and is shaped for the loopState path) — this projection
// originates from the recovery broker and supplies different field
// values for the same schema. Keeping a sibling type here avoids a
// reverse import edge from chat_loop_terminated.go into the recovery
// sink.
type chatLoopTerminatedRecoveryPayload struct {
	Reason              string `json:"reason"`
	Code                string `json:"code"`
	Iteration           int    `json:"iteration"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastError           string `json:"last_error,omitempty"`
	LastTool            string `json:"last_tool,omitempty"`
	Timestamp           string `json:"timestamp"`
}
