// Package mcp provides G4 elicitation/create support (CW-20260420-0018).
//
// MCP spec 2025-06-18 defines elicitation/create as a method that allows a
// tool, mid-call, to ask the user a question and await their response before
// continuing. Nanite's own tools call ElicitUserInput when they need mid-call
// user confirmation; the helper acquires user input by issuing an elicitation
// request through the Service and blocking until the user or timeout responds.
//
// Design contract (from ticket):
//   - Elicitation is additive; strategy-loop "when to ask" logic is untouched.
//   - Timeout defaults to 5 min, configurable via NANITE_ELICITATION_TIMEOUT_SEC.
//   - UI: text input for string schemas, accept/decline buttons for boolean.
//   - Envelope type: "elicitation-prompt" (registered as a core type in the
//     external github.com/hollis-labs/go-envelopes module's manifest/envelopes.yaml,
//     loaded into the in-process registry via chat.EnvelopeRegistry()/
//     envelopes.LoadCore — config/envelopes.yaml does not exist in this repo).
package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/elicitation"
)

// ElicitationService is the narrow surface the MCP layer uses for elicitation.
// *elicitation.Service satisfies this structurally; tests inject a stub.
type ElicitationService interface {
	Elicit(ctx context.Context, req elicitation.ElicitInput) (elicitation.Response, error)
	Respond(id string, resp elicitation.Response) error
	RespondFromEnvelopeData(data map[string]any) error
}

// ElicitationCreateParams mirrors the MCP 2025-06-18 spec for elicitation/create.
// Nanite tools pass this shape to ElicitUserInput when they need mid-call input.
type ElicitationCreateParams struct {
	// Message is the question shown to the user.
	Message string `json:"message"`
	// RequestedSchema constrains the allowed response. The type field drives
	// the UI widget (boolean → accept/decline, string → text input).
	RequestedSchema *ElicitationRequestedSchema `json:"requestedSchema,omitempty"`
}

type ElicitationRequestedSchema struct {
	Type        string `json:"type"` // "boolean" | "string"
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// elicitationResponse is what the MCP server returns to the in-flight tool
// call after the user responds (or the timeout fires). Matches the MCP spec
// 2025-06-18 elicitation response shape.
type elicitationResponse struct {
	// Action is accept | decline | cancel.
	Action string `json:"action"`
	// Content carries the user's response text when action=accept and the
	// schema type is "string". Omitted for boolean schemas.
	Content string `json:"content,omitempty"`
}

// ElicitUserInput is the server-side elicitation helper. Nanite's own write
// tools call this when they need mid-call user confirmation before proceeding.
// Exported (CW self-tools move, TASKS/harness-reactive-self-tools/01) because
// its primary caller (message_send kind=directive) now lives in
// internal/selftools, a separate package from this file.
//
// sessionID / agentID identify where to deliver the elicitation-prompt envelope.
// toolCallID is echoed back for correlation (MCP tool_use_id).
//
// Returns the user's response. If the user declines, returns ActionDecline so
// the tool knows to abort. If the request times out, returns ActionCancel +
// reason=timeout.
func ElicitUserInput(
	ctx context.Context,
	svc ElicitationService,
	sessionID, agentID, toolCallID string,
	params ElicitationCreateParams,
) (elicitationResponse, error) {
	if svc == nil {
		// Elicitation not wired — fall back to auto-accept so the tool doesn't
		// hang. Log-worthy but not an error from the tool caller's perspective.
		return elicitationResponse{Action: "accept"}, nil
	}

	var schema *elicitation.RequestedSchema
	if params.RequestedSchema != nil {
		schemaType := elicitation.SchemaType(params.RequestedSchema.Type)
		if schemaType != elicitation.SchemaTypeBoolean && schemaType != elicitation.SchemaTypeString {
			schemaType = elicitation.SchemaTypeString // default
		}
		schema = &elicitation.RequestedSchema{
			Type:        schemaType,
			Title:       params.RequestedSchema.Title,
			Description: params.RequestedSchema.Description,
		}
	}

	resp, err := svc.Elicit(ctx, elicitation.ElicitInput{
		Message:    params.Message,
		Schema:     schema,
		ToolCallID: toolCallID,
		SessionID:  sessionID,
		AgentID:    agentID,
		Origin:     "server",
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return elicitationResponse{Action: "cancel"}, fmt.Errorf("mcp: elicitation: %w", err)
	}
	return elicitationResponse{
		Action:  string(resp.Action),
		Content: resp.Content,
	}, nil
}

// elicitationTimeoutSeconds returns the configured timeout, falling back to
// the package default. Exported for tests that need to verify the default.
func elicitationTimeoutSeconds() int {
	return elicitation.DefaultTimeoutSeconds
}

// ElicitationEnvelopeType is the envelope type name for the elicitation-prompt.
const ElicitationEnvelopeType = "elicitation-prompt"

// FormatTimeoutAt formats a timeout duration as an ISO 8601 timestamp string
// suitable for the elicitation-prompt envelope's timeout_at field.
func FormatTimeoutAt(d time.Duration) string {
	return time.Now().Add(d).UTC().Format(time.RFC3339)
}
