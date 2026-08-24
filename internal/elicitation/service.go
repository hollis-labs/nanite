package elicitation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// Emitter is the narrow surface Service uses to push an elicitation-prompt
// envelope to the chat frontend. *messaging.Service satisfies this
// structurally; tests inject a stub.
type Emitter interface {
	// EmitElicitationPrompt delivers the elicitation-prompt envelope to the
	// given session/agent so the UI can render the input widget. It returns
	// the created envelope instance ID that the response route uses.
	EmitElicitationPrompt(ctx context.Context, input EmitInput) (envelopeInstanceID string, err error)
}

// EmitInput is what Service passes to Emitter.EmitElicitationPrompt.
type EmitInput struct {
	SessionID string
	AgentID   string
	// Data is the JSON-serializable EnvelopeData payload.
	Data EnvelopeData
}

// Service manages in-flight elicitation requests. Safe for concurrent use.
//
// Lifecycle of a request:
//  1. caller → Service.Elicit() — creates a pending Request, emits the
//     elicitation-prompt envelope, starts the timeout goroutine.
//  2. user acts (or timeout fires) → Service.Respond() — resolves the pending
//     request and closes the result channel.
//  3. Service.Elicit() returns the Response to the caller.
//
// The live path is server-side: Nanite-owned tools call Service.Elicit when
// they need mid-call user input. Origin is recorded for observability but does
// not change behavior.
type Service struct {
	emitter        Emitter
	timeoutSeconds int

	mu      sync.Mutex
	pending map[string]*Request // id → request
}

// New constructs a Service.
//
// timeoutSeconds ≤ 0 picks up NANITE_ELICITATION_TIMEOUT_SEC from the
// environment, falling back to DefaultTimeoutSeconds (5 min).
func New(emitter Emitter, timeoutSeconds int) *Service {
	if timeoutSeconds <= 0 {
		if raw := os.Getenv("NANITE_ELICITATION_TIMEOUT_SEC"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				timeoutSeconds = n
			}
		}
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = DefaultTimeoutSeconds
	}
	return &Service{
		emitter:        emitter,
		timeoutSeconds: timeoutSeconds,
		pending:        make(map[string]*Request),
	}
}

// Elicit issues an elicitation request:
//  1. Creates and registers a pending Request.
//  2. Emits the elicitation-prompt envelope to the frontend.
//  3. Blocks until the user responds or the timeout fires.
//  4. Returns the Response (action + optional content/reason).
//
// ctx cancellation (e.g. parent request canceled) causes Elicit to return
// immediately with action=cancel + reason="context_canceled".
func (s *Service) Elicit(ctx context.Context, req ElicitInput) (Response, error) {
	timeoutDur := time.Duration(s.timeoutSeconds) * time.Second

	id := newID()
	schemaType := SchemaTypeString
	if req.Schema != nil && req.Schema.Type != "" {
		schemaType = req.Schema.Type
	}

	pending := &Request{
		ID:         id,
		Message:    req.Message,
		Schema:     req.Schema,
		ToolCallID: req.ToolCallID,
		SessionID:  req.SessionID,
		AgentID:    req.AgentID,
		Origin:     req.Origin,
		CreatedAt:  time.Now(),
		result:     make(chan Response, 1),
	}

	s.mu.Lock()
	s.pending[id] = pending
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	// Emit envelope so the UI renders the input widget.
	data := EnvelopeData{
		ElicitationID: id,
		Message:       req.Message,
		SchemaType:    string(schemaType),
		ToolCallID:    req.ToolCallID,
		Origin:        req.Origin,
		TimeoutAt:     time.Now().Add(timeoutDur),
	}
	if req.Schema != nil {
		data.SchemaTitle = req.Schema.Title
		data.SchemaDescription = req.Schema.Description
	}
	if _, err := s.emitter.EmitElicitationPrompt(ctx, EmitInput{
		SessionID: req.SessionID,
		AgentID:   req.AgentID,
		Data:      data,
	}); err != nil {
		return Response{Action: ActionCancel, Reason: "emit_failed"}, errElicitEmit(err)
	}

	// Wait for response, timeout, or context cancellation.
	select {
	case resp := <-pending.result:
		return resp, nil
	case <-time.After(timeoutDur):
		return Response{Action: ActionCancel, Reason: "timeout"}, nil
	case <-ctx.Done():
		return Response{Action: ActionCancel, Reason: "context_canceled"}, ctx.Err()
	}
}

// Respond resolves a pending elicitation by ID. Called when the frontend
// submits a response via the envelope response pipeline.
//
// Returns an error if no pending request with the given ID exists (already
// resolved, timed out, or unknown).
func (s *Service) Respond(id string, resp Response) error {
	s.mu.Lock()
	req, ok := s.pending[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("elicitation: no pending request with id %q", id)
	}
	// Non-blocking send: if the result channel is already closed (race with
	// timeout), this is a no-op. The channel is buffered (cap=1) so we never
	// block.
	select {
	case req.result <- resp:
	default:
		// Already resolved (timeout or concurrent respond); silently drop.
	}
	return nil
}

// RespondFromEnvelopeData extracts an elicitation response from a ResponseV1
// data map (as submitted by the UI) and routes it to Respond.
//
// Expected data keys:
//   - "elicitation_id" — the pending request ID
//   - "action"         — "accept" | "decline" | "cancel"
//   - "content"        — optional string content (for string schemas)
func (s *Service) RespondFromEnvelopeData(data map[string]any) error {
	id, _ := data["elicitation_id"].(string)
	if id == "" {
		return fmt.Errorf("elicitation: missing elicitation_id in response data")
	}
	actionStr, _ := data["action"].(string)
	content, _ := data["content"].(string)

	var action Action
	switch Action(actionStr) {
	case ActionAccept, ActionDecline, ActionCancel:
		action = Action(actionStr)
	default:
		action = ActionCancel
	}

	return s.Respond(id, Response{
		Action:  action,
		Content: content,
	})
}

// ValidateResponse checks that a user response matches the expected schema.
// For boolean schemas, content must be empty (accept/decline carries the signal).
// For string schemas, content is required when action=accept.
// Returns nil on success, a descriptive error otherwise.
func ValidateResponse(schema *RequestedSchema, resp Response) error {
	if resp.Action != ActionAccept {
		// decline and cancel don't need content validation.
		return nil
	}
	if schema == nil {
		return nil // no schema constraint
	}
	switch schema.Type {
	case SchemaTypeBoolean:
		// Boolean: accept means "yes"; content should not be set.
		// We're lenient — ignore content if present.
		return nil
	case SchemaTypeString:
		// String accept requires non-empty content.
		if resp.Content == "" {
			return fmt.Errorf("elicitation: string schema response requires non-empty content when action=accept")
		}
	}
	return nil
}

// MarshalEnvelopeData marshals EnvelopeData to a map[string]any suitable
// for use as envelope Data. Used when building the elicitation-prompt envelope.
func MarshalEnvelopeData(d EnvelopeData) (map[string]any, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ElicitInput is the caller-supplied input to Elicit.
type ElicitInput struct {
	// Message is the question shown to the user.
	Message string
	// Schema constrains the response. Nil means string with no constraints.
	Schema *RequestedSchema
	// ToolCallID is the originating tool-call ID for correlation.
	ToolCallID string
	// SessionID and AgentID identify where the envelope should be delivered.
	SessionID string
	AgentID   string
	// Origin is a source label recorded in the emitted envelope. Current
	// production callers set "server".
	Origin string
}
