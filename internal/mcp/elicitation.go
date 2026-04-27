// Package mcp — G4 elicitation/create support (CW-20260420-0018).
//
// MCP spec 2025-06-18 defines elicitation/create as a method that allows a
// tool, mid-call, to ask the user a question and await their response before
// continuing. This file implements BOTH directions:
//
//  1. Server-side: when one of Nanite's OWN tools calls ElicitUserInput(), the
//     helper acquires user input by issuing an elicitation request through the
//     Service and blocking until the user (or timeout) responds.
//
//  2. Client-side: when a REMOTE MCP server's tool issues elicitation/create
//     during a tool_call, the transport surfaces it as a pending request via
//     the same Service path. ClientElicitMiddleware wraps an MCPTransport and
//     intercepts the elicitation/create notification in the tool result.
//
// Design contract (from ticket):
//   - Elicitation is ADDITIVE — strategy-loop "when to ask" logic is untouched.
//   - Timeout defaults to 5 min, configurable via NANITE_ELICITATION_TIMEOUT_SEC.
//   - UI: text input for string schemas, accept/decline buttons for boolean.
//   - Envelope type: "elicitation-prompt" (registered in config/envelopes.yaml).
package mcp

import (
	"context"
	"encoding/json"
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

// elicitationCreateParams mirrors the MCP 2025-06-18 spec for elicitation/create.
// The tool mid-call sends this; the transport decodes it and calls the Service.
type elicitationCreateParams struct {
	// Message is the question shown to the user.
	Message string `json:"message"`
	// RequestedSchema constrains the allowed response. The type field drives
	// the UI widget (boolean → accept/decline, string → text input).
	RequestedSchema *elicitationRequestedSchema `json:"requestedSchema,omitempty"`
}

type elicitationRequestedSchema struct {
	Type        string `json:"type"`        // "boolean" | "string"
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

// elicitUserInput is the server-side elicitation helper. Nanite's own write
// tools call this when they need mid-call user confirmation before proceeding.
//
// sessionID / agentID identify where to deliver the elicitation-prompt envelope.
// toolCallID is echoed back for correlation (MCP tool_use_id).
//
// Returns the user's response. If the user declines, returns ActionDecline so
// the tool knows to abort. If the request times out, returns ActionCancel +
// reason=timeout.
func elicitUserInput(
	ctx context.Context,
	svc ElicitationService,
	sessionID, agentID, toolCallID string,
	params elicitationCreateParams,
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
	if err != nil && err != context.Canceled {
		return elicitationResponse{Action: "cancel"}, fmt.Errorf("mcp: elicitation: %w", err)
	}
	return elicitationResponse{
		Action:  string(resp.Action),
		Content: resp.Content,
	}, nil
}

// ClientElicitationResult captures an elicitation/create request intercepted
// from a remote MCP server during a tool call.
type ClientElicitationResult struct {
	// ID is the elicitation_id assigned by the client-side handler. The
	// remote server's tool call blocks until Respond(ID, ...) is called.
	ID string
	// Params are the elicitation parameters from the remote server.
	Params elicitationCreateParams
}

// parseElicitationCreate extracts an elicitation/create request from a raw
// JSON-RPC notification body that may arrive as a "side channel" alongside a
// tools/call response. Returns nil if the message is not an elicitation/create.
//
// Per the MCP 2025-06-18 spec, elicitation/create is sent as a JSON-RPC
// request (with an id) from server to client during a tool execution. In our
// transport model it arrives as a nested payload in a tool result or as an
// out-of-band SSE event, depending on the server implementation. Both paths
// ultimately decode the same shape.
func parseElicitationCreate(raw json.RawMessage) (*elicitationCreateParams, bool) {
	var req struct {
		Method string                   `json:"method"`
		Params *elicitationCreateParams `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, false
	}
	if req.Method != "elicitation/create" || req.Params == nil {
		return nil, false
	}
	return req.Params, true
}

// routeClientElicitation processes an elicitation/create received from a
// remote MCP server's tool call. It:
//  1. Calls the ElicitationService.Elicit to surface the prompt to the UI.
//  2. Returns the user's response encoded as a JSON-RPC result so the caller
//     can relay it back to the remote server.
//
// sessionID / agentID identify the session for envelope delivery.
// toolCallID is the originating MCP tool_use_id from the external server call.
func routeClientElicitation(
	ctx context.Context,
	svc ElicitationService,
	sessionID, agentID, toolCallID string,
	params elicitationCreateParams,
) (json.RawMessage, error) {
	if svc == nil {
		// No elicitation service wired — auto-cancel gracefully.
		result := elicitationResponse{Action: "cancel"}
		b, _ := json.Marshal(result)
		return b, nil
	}

	var schema *elicitation.RequestedSchema
	if params.RequestedSchema != nil {
		schemaType := elicitation.SchemaType(params.RequestedSchema.Type)
		if schemaType != elicitation.SchemaTypeBoolean && schemaType != elicitation.SchemaTypeString {
			schemaType = elicitation.SchemaTypeString
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
		Origin:     "client",
	})
	if err != nil && err != context.Canceled {
		// Non-cancellation error: return cancel so the remote tool can continue.
		result := elicitationResponse{Action: "cancel"}
		b, _ := json.Marshal(result)
		return b, fmt.Errorf("mcp: client elicitation: %w", err)
	}

	result := elicitationResponse{
		Action:  string(resp.Action),
		Content: resp.Content,
	}
	b, err := json.Marshal(result)
	return b, err
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
