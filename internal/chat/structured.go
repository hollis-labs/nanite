package chat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// StructuredMessage is the universal wrapper for all assistant messages.
type StructuredMessage struct {
	Version   int           `json:"v"`
	Text      string        `json:"text"`
	Tier      string        `json:"tier"`
	Hash      string        `json:"hash,omitempty"`
	Envelopes []EnvelopeRef `json:"envelopes,omitempty"`
	ToolCalls []ToolCallRef `json:"tool_calls,omitempty"`
	Flags     MessageFlags  `json:"flags"`
}

// MessageFlags contains boolean flags for message state.
type MessageFlags struct {
	Truncated   bool `json:"truncated,omitempty"`
	HasError    bool `json:"has_error,omitempty"`
	Provisional bool `json:"provisional,omitempty"`
}

// ToolCallRef records a tool call made during the response.
//
// ErrorReason carries the verbatim tool error string (or block/deny/cancel
// message) when Status is one of "error", "denied", "blocked", or "cancelled".
// It is the same string as the matching tool_result block's Content. This is
// what the failure-footer enrichment (CW-20260501-0013) inlines so the model
// sees the actual reason next to the failed tool name in the harness footer,
// raising the salience of "memory service not configured" / "query is
// required" / etc. and reducing the H2 surface that produced the c121
// "I don't have access to a memory recall tool" hallucination.
type ToolCallRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	HasEnvelope bool   `json:"has_envelope,omitempty"`
	ErrorReason string `json:"error_reason,omitempty"`
}

// EnvelopeRef is a typed reference to an envelope embedded in the message.
//
// CW-20260429-0019: EnvelopeRef must round-trip the routing-relevant fields
// (RenderTarget, Target, Mode, RenderTargetBlocked, plus the addressable
// {ID, Title, Subtitle} surface) so that on page reload the FE can route a
// persisted card to the correct drawer/panel. Prior to this change the
// projection in chat_generate.go threw away every routing hint; the persisted
// `envelopes` field was effectively `[]{type, data}` and cards always
// rendered inline regardless of the per-type schema's default_render_target.
//
// JSON keys mirror chat.Envelope so the on-wire shape is consistent across
// the parsed-envelope path and the persisted reference path.
type EnvelopeRef struct {
	Type                string          `json:"type"`
	Data                json.RawMessage `json:"data"`
	ID                  string          `json:"id,omitempty"`
	Title               string          `json:"title,omitempty"`
	Subtitle            string          `json:"subtitle,omitempty"`
	Target              string          `json:"target,omitempty"`
	Mode                string          `json:"mode,omitempty"`
	RenderTarget        string          `json:"render_target,omitempty"`
	RenderTargetBlocked string          `json:"render_target_blocked,omitempty"`
}

// WrapResponse creates a StructuredMessage wrapping the assistant's response text.
func WrapResponse(text, tier string, toolCalls []ToolCallRef, envelopes []EnvelopeRef, truncated, hasError bool) StructuredMessage {
	msg := StructuredMessage{
		Version:   1,
		Text:      text,
		Tier:      tier,
		Envelopes: envelopes,
		ToolCalls: toolCalls,
		Flags: MessageFlags{
			Truncated: truncated,
			HasError:  hasError,
		},
	}
	// Compute hash of final text.
	if text != "" {
		msg.Hash = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	}
	return msg
}

// MarshalContent returns the JSON string for saving to Message.Content.
func (sm StructuredMessage) MarshalContent() string {
	data, err := json.Marshal(sm)
	if err != nil {
		return sm.Text // fallback to raw text
	}
	return string(data)
}

// replayContent unwraps a persisted store.Message.Content value into the
// text that should actually be replayed into a *later* turn's LLM-facing
// conversation history (internal/chat/context_client.go's
// AssembleSlotSources message-history build — the only reader of prior
// turns' persisted content).
//
// Assistant turns are stored as StructuredMessage JSON
// (chat.WrapResponse -> MarshalContent), and StructuredMessage.Envelopes
// carries the FULL, untruncated Card/envelope data payload for that turn
// (internal/chat/structured.go's EnvelopeRef.Data). That field has zero
// readers anywhere else in the codebase — page-reload rendering reads the
// separate store.Message.Envelope column (internal/api/envelopes.go), not
// this one. Replaying Envelopes verbatim as literal history text on every
// subsequent turn is exactly the "context-replay gap" described in
// docs/engineering/architecture/08-cards.md and 06-session-lifecycle-and-
// recovery.md: a card shown once would otherwise cost full-payload context
// on every later turn of a long session, defeating the "harness injects
// rich Card data, agent context stays light" design.
//
// Only StructuredMessage.Text is used for replay; Envelopes (and
// ToolCalls) are deliberately discarded here. This does not affect the
// *current* turn's tool-result content the agent sees (already excluded
// upstream) or the assistant's own live response construction — WrapResponse
// still writes the full Envelopes slice into Message.Content for storage;
// only what gets READ back for history replay changes.
//
// Content that isn't StructuredMessage-shaped (plain user text, legacy
// pre-structured rows, envelope_response rows formatted via
// FormatEnvelopeResponseContent, which are prefixed "[envelope:...]" rather
// than "{") is returned unchanged — same fallback shape as
// internal/recovery/pack.MessagePlainText, which unwraps the same JSON
// envelope for a different purpose (Recovery Pack replay text).
func replayContent(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return content
	}
	var sm StructuredMessage
	if err := json.Unmarshal([]byte(trimmed), &sm); err != nil || sm.Version == 0 {
		return content
	}
	return sm.Text
}

// ValidateStructured checks a StructuredMessage for consistency and returns warnings.
func ValidateStructured(sm StructuredMessage) []string {
	var warnings []string
	if sm.Version != 1 {
		warnings = append(warnings, fmt.Sprintf("unknown version: %d", sm.Version))
	}
	if sm.Tier != "default" && sm.Tier != "tool" {
		warnings = append(warnings, fmt.Sprintf("unknown tier: %s", sm.Tier))
	}
	if sm.Hash != "" && sm.Text != "" {
		expected := fmt.Sprintf("%x", sha256.Sum256([]byte(sm.Text)))
		if sm.Hash != expected {
			warnings = append(warnings, "hash mismatch — content may be corrupted")
		}
	}
	return warnings
}

// LogStructuredWarnings validates a StructuredMessage and logs any warnings.
func LogStructuredWarnings(sm StructuredMessage) {
	if warnings := ValidateStructured(sm); len(warnings) > 0 {
		for _, w := range warnings {
			slog.Warn("chat: structured message warning", "warning", w)
		}
	}
}
