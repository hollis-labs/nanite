// Package elicitation implements Nanite's server-side MCP elicitation/create
// support (spec 2025-06-18).
//
// Elicitation lets a Nanite-owned tool, mid-call, ask the user a question and
// await their response before continuing. This package manages requests from
// Nanite's own tools: it creates a pending request, emits an
// elicitation-prompt envelope to the chat surface, and routes the envelope
// response back to the waiting caller.
//
// Design pins (CW-20260420-0018, G4):
//   - D1: Elicitation is ADDITIVE — it does not replace the strategy-loop
//     "when to ask" logic. Both can coexist.
//   - D2: Timeout defaults to 5 minutes, configurable via NANITE_ELICITATION_TIMEOUT_SEC.
//     Auto-declines with action=cancel + reason=timeout on expiry.
//   - D3: UI surface supports text input for string-type schemas and
//     accept/decline buttons for boolean schemas only (v1). No rich JSON-Schema
//     form widgets.
//   - D4: The elicitation-prompt envelope carries the request; the response
//     routes back via the envelope response pipeline (ResponseV1).
//   - D5: One existing write tool (message_send kind=directive) is wired
//     to issue elicitation for confirmation before sending.
package elicitation

import (
	"time"
)

// Action is the three-way outcome the user can return.
type Action string

const (
	// ActionAccept means the user submitted a response (for boolean: yes;
	// for string: the text they entered).
	ActionAccept Action = "accept"
	// ActionDecline means the user explicitly declined (for boolean: no).
	ActionDecline Action = "decline"
	// ActionCancel means the elicitation was abandoned — either the user
	// dismissed without answering or the timeout expired.
	ActionCancel Action = "cancel"
)

// SchemaType describes what kind of response the elicitation expects.
// Only "boolean" and "string" are supported in v1 per the D3 decision.
type SchemaType string

const (
	SchemaTypeBoolean SchemaType = "boolean"
	SchemaTypeString  SchemaType = "string"
)

// RequestedSchema constrains the user's response. The elicitation UI
// renders a boolean schema as accept/decline buttons and a string schema
// as a text input field.
type RequestedSchema struct {
	// Type is the expected response type: "boolean" or "string".
	// Unknown types fall back to string rendering.
	Type SchemaType `json:"type"`
	// Title is an optional short label shown above the input widget.
	Title string `json:"title,omitempty"`
	// Description is an optional helper text shown below the input.
	Description string `json:"description,omitempty"`
}

// Request is the in-flight elicitation created by a tool mid-call.
// Stored in Service's pending map while awaiting user response.
type Request struct {
	// ID uniquely identifies this elicitation request. Used as the
	// envelope instance ID so the response routes back correctly.
	ID string

	// Message is the question text shown to the user.
	Message string

	// Schema constrains the response. Defaults to string when nil.
	Schema *RequestedSchema

	// ToolCallID is the originating tool-call ID (MCP tool_use_id).
	// Stored so the caller can correlate the response back to its
	// in-flight tool call.
	ToolCallID string

	// SessionID and AgentID identify where the response should be
	// delivered. Used by the envelope pipeline.
	SessionID string
	AgentID   string

	// Origin identifies where this elicitation came from. Current production
	// callers set "server" for Nanite-owned tools.
	Origin string

	// CreatedAt is when the request was created; used for timeout math.
	CreatedAt time.Time

	// result is closed by Respond or by the timeout goroutine.
	// Exactly one Response is sent; callers block on Await().
	result chan Response
}

// Response is what the user (or timeout) returns.
type Response struct {
	// Action is accept | decline | cancel.
	Action Action `json:"action"`
	// Content carries the user's response text when Action=accept
	// and the schema type is "string". Empty for boolean schemas.
	Content string `json:"content,omitempty"`
	// Reason carries additional context when Action=cancel, e.g. "timeout".
	Reason string `json:"reason,omitempty"`
}

// EnvelopeData is the data payload emitted as the elicitation-prompt envelope.
// The UI reads this to render the correct input widget.
type EnvelopeData struct {
	// ElicitationID echoes the Request.ID for correlation.
	ElicitationID string `json:"elicitation_id"`
	// Message is the question text.
	Message string `json:"message"`
	// SchemaType is "boolean" or "string" (defaults to "string").
	SchemaType string `json:"schema_type"`
	// SchemaTitle and SchemaDescription are optional widget labels.
	SchemaTitle       string `json:"schema_title,omitempty"`
	SchemaDescription string `json:"schema_description,omitempty"`
	// ToolCallID is carried through for originating tool-call correlation.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Origin identifies the source label; current production callers set
	// "server".
	Origin string `json:"origin"`
	// TimeoutAt is when the server will auto-cancel if no response arrives.
	TimeoutAt time.Time `json:"timeout_at"`
}

// DefaultTimeoutSeconds is the fallback when NANITE_ELICITATION_TIMEOUT_SEC
// is not set. Five minutes gives the user ample time without leaving a
// blocked tool call open indefinitely.
const DefaultTimeoutSeconds = 5 * 60
