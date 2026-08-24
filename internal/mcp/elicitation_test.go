package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/elicitation"
)

// stubElicitationService implements ElicitationService for tests.
type stubElicitationService struct {
	respondFn func(id string, resp elicitation.Response) error
	elicitFn  func(ctx context.Context, req elicitation.ElicitInput) (elicitation.Response, error)
}

func (s *stubElicitationService) Elicit(ctx context.Context, req elicitation.ElicitInput) (elicitation.Response, error) {
	if s.elicitFn != nil {
		return s.elicitFn(ctx, req)
	}
	return elicitation.Response{Action: elicitation.ActionAccept}, nil
}

func (s *stubElicitationService) Respond(id string, resp elicitation.Response) error {
	if s.respondFn != nil {
		return s.respondFn(id, resp)
	}
	return nil
}

func (s *stubElicitationService) RespondFromEnvelopeData(data map[string]any) error {
	id, _ := data["elicitation_id"].(string)
	action, _ := data["action"].(string)
	content, _ := data["content"].(string)
	var act elicitation.Action
	switch elicitation.Action(action) {
	case elicitation.ActionAccept, elicitation.ActionDecline, elicitation.ActionCancel:
		act = elicitation.Action(action)
	default:
		act = elicitation.ActionCancel
	}
	return s.Respond(id, elicitation.Response{Action: act, Content: content})
}

// --- Server-side handler unit test ---

// TestElicitUserInput_ServerSide_Accept verifies the server-side path: a tool
// calls ElicitUserInput, the service returns accept, the tool receives it.
func TestElicitUserInput_ServerSide_Accept(t *testing.T) {
	svc := &stubElicitationService{
		elicitFn: func(_ context.Context, req elicitation.ElicitInput) (elicitation.Response, error) {
			if req.Origin != "server" {
				t.Errorf("origin: got %q, want %q", req.Origin, "server")
			}
			if req.Message != "Proceed with bulk delete?" {
				t.Errorf("message: got %q", req.Message)
			}
			return elicitation.Response{Action: elicitation.ActionAccept}, nil
		},
	}

	params := ElicitationCreateParams{
		Message: "Proceed with bulk delete?",
		RequestedSchema: &ElicitationRequestedSchema{
			Type: "boolean",
		},
	}
	resp, err := ElicitUserInput(context.Background(), svc, "sess-1", "agent-1", "tc-001", params)
	if err != nil {
		t.Fatalf("ElicitUserInput error: %v", err)
	}
	if resp.Action != "accept" {
		t.Errorf("action: got %q, want %q", resp.Action, "accept")
	}
}

// TestElicitUserInput_ServerSide_Decline verifies that decline is propagated
// correctly so the tool can abort its operation.
func TestElicitUserInput_ServerSide_Decline(t *testing.T) {
	svc := &stubElicitationService{
		elicitFn: func(_ context.Context, _ elicitation.ElicitInput) (elicitation.Response, error) {
			return elicitation.Response{Action: elicitation.ActionDecline}, nil
		},
	}

	resp, err := ElicitUserInput(context.Background(), svc, "sess-1", "agent-1", "tc-002",
		ElicitationCreateParams{Message: "Are you sure?"})
	if err != nil {
		t.Fatalf("ElicitUserInput error: %v", err)
	}
	if resp.Action != "decline" {
		t.Errorf("action: got %q, want %q", resp.Action, "decline")
	}
}

// TestElicitUserInput_NilService verifies that a nil service auto-accepts (non-blocking).
func TestElicitUserInput_NilService(t *testing.T) {
	resp, err := ElicitUserInput(context.Background(), nil, "sess-1", "agent-1", "tc-003",
		ElicitationCreateParams{Message: "Confirm?"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Action != "accept" {
		t.Errorf("nil service should auto-accept, got %q", resp.Action)
	}
}

// --- Timeout test ---

// TestElicitUserInput_Timeout verifies that a timeout returns action=cancel.
// We exercise the timeout via context cancellation (same code path) in a
// bounded way so the test doesn't take 5 minutes.
func TestElicitUserInput_Timeout(t *testing.T) {
	// Use a context that cancels after a short delay to simulate timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	svc := &stubElicitationService{
		elicitFn: func(ctx context.Context, _ elicitation.ElicitInput) (elicitation.Response, error) {
			// Block until ctx is canceled (simulating no user response).
			<-ctx.Done()
			return elicitation.Response{Action: elicitation.ActionCancel, Reason: "timeout"}, ctx.Err()
		},
	}

	resp, err := ElicitUserInput(ctx, svc, "sess-3", "agent-1", "tc-timeout",
		ElicitationCreateParams{Message: "Will you respond?"})
	if !errors.Is(err, context.DeadlineExceeded) {
		// context_canceled or deadline — both are acceptable "no response" paths.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("unexpected error type: %v", err)
		}
	}
	if resp.Action != "cancel" {
		t.Errorf("timeout: action: got %q, want %q", resp.Action, "cancel")
	}
}

// --- Schema validation test ---

// TestElicitSchemaValidation_StringRequiresContent mirrors the service-level
// validation at the MCP layer: a string-schema accept with no content is
// a rejection path.
func TestElicitSchemaValidation_StringRequiresContent(t *testing.T) {
	// The MCP elicitation layer delegates schema validation to
	// elicitation.ValidateResponse. We test the plumbing here.
	stringSchema := &elicitation.RequestedSchema{Type: elicitation.SchemaTypeString}

	// Accept with empty content — should fail.
	err := elicitation.ValidateResponse(stringSchema, elicitation.Response{
		Action:  elicitation.ActionAccept,
		Content: "",
	})
	if err == nil {
		t.Error("expected validation error for string accept with empty content")
	}

	// Accept with content — should succeed.
	err = elicitation.ValidateResponse(stringSchema, elicitation.Response{
		Action:  elicitation.ActionAccept,
		Content: "some text",
	})
	if err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}
