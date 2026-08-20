package selftools

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/messaging"
)

// newTestMessaging creates a real messaging.Service backed by the test store.
func newTestMessaging(t *testing.T) *messaging.Service {
	t.Helper()
	s := newTestStore(t)
	sqlStore := messaging.NewSQLiteStore(s.DB)
	svc := messaging.NewService(sqlStore, s.DB, nil, s)
	return svc
}

// stubElicitationService is a local copy of internal/mcp/elicitation_test.go's
// test double, needed here because this file (originally
// internal/mcp/elicitation_pilot_test.go) moved alongside SelfToolsTransport
// (TASKS/harness-reactive-self-tools/01) — its subject, message_send's
// directive-elicitation gate, is self-tool behavior, but its stub type is
// unexported in another package's _test.go file and so cannot be imported
// across the package boundary this move introduces.
type stubElicitationService struct {
	elicitFn func(ctx context.Context, req elicitation.ElicitInput) (elicitation.Response, error)
}

func (s *stubElicitationService) Elicit(ctx context.Context, req elicitation.ElicitInput) (elicitation.Response, error) {
	if s.elicitFn != nil {
		return s.elicitFn(ctx, req)
	}
	return elicitation.Response{Action: elicitation.ActionAccept}, nil
}

func (s *stubElicitationService) Respond(id string, resp elicitation.Response) error {
	return nil
}

func (s *stubElicitationService) RespondFromEnvelopeData(data map[string]any) error {
	return nil
}

// TestPilotTool_DirectiveRequiresElicitation verifies the end-to-end pilot:
// message_send with kind=directive calls elicitation before sending.
//
// Acceptance criteria from CW-20260420-0018 D5: wire ONE existing write-tool
// to use elicitation. This test confirms the full path: CallTool → elicitation
// gate → messaging send.
func TestPilotTool_DirectiveRequiresElicitation(t *testing.T) {
	st := newSelfTools(t)
	st.Messaging = newTestMessaging(t)

	// Wire an elicitation service that auto-accepts and records the call.
	accepted := false
	st.Elicitation = &stubElicitationService{
		elicitFn: func(_ context.Context, req elicitation.ElicitInput) (elicitation.Response, error) {
			accepted = true
			if req.Origin != "server" {
				t.Errorf("origin: got %q, want %q", req.Origin, "server")
			}
			return elicitation.Response{Action: elicitation.ActionAccept}, nil
		},
	}

	result, err := st.CallTool(context.Background(), "message_send", map[string]any{
		"from_session_id": "sess-pilot",
		"from_agent_id":   "user",
		"to_session_id":   "sess-target",
		"to_agent_id":     "user",
		"kind":            "notification",
		"type":            "directive",
		"body":            "execute plan alpha",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	// Elicitation must have been called.
	if !accepted {
		t.Error("elicitation was not called for directive kind")
	}

	// Result text should confirm the message was sent.
	if len(result.Content) == 0 || result.Content[0].Text == "" {
		t.Error("expected non-empty result text after successful send")
	}
}

// TestPilotTool_DirectiveDeclined verifies that declining elicitation returns
// a non-error result without actually sending the message.
func TestPilotTool_DirectiveDeclined(t *testing.T) {
	st := newSelfTools(t)
	st.Messaging = newTestMessaging(t)

	st.Elicitation = &stubElicitationService{
		elicitFn: func(_ context.Context, _ elicitation.ElicitInput) (elicitation.Response, error) {
			return elicitation.Response{Action: elicitation.ActionDecline}, nil
		},
	}

	result, err := st.CallTool(context.Background(), "message_send", map[string]any{
		"from_session_id": "sess-pilot",
		"from_agent_id":   "user",
		"to_session_id":   "sess-target",
		"to_agent_id":     "user",
		"kind":            "notification",
		"type":            "directive",
		"body":            "execute plan alpha",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error (unexpected): %v", result.Content)
	}

	// The result should indicate the send was aborted.
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty result")
	}
	text := result.Content[0].Text
	if text == "" {
		t.Error("expected non-empty result text after decline")
	}
	// Should mention aborted / declined, not "sent".
	if len(text) >= 4 && text[:4] == "sent" {
		t.Errorf("result should not start with 'sent' after decline, got: %s", text)
	}
}

// TestPilotTool_NonDirectiveSkipsElicitation verifies that regular message kinds
// do NOT trigger elicitation.
func TestPilotTool_NonDirectiveSkipsElicitation(t *testing.T) {
	st := newSelfTools(t)
	st.Messaging = newTestMessaging(t)

	elicitCalled := false
	st.Elicitation = &stubElicitationService{
		elicitFn: func(_ context.Context, _ elicitation.ElicitInput) (elicitation.Response, error) {
			elicitCalled = true
			return elicitation.Response{Action: elicitation.ActionAccept}, nil
		},
	}

	result, err := st.CallTool(context.Background(), "message_send", map[string]any{
		"from_session_id": "sess-pilot",
		"from_agent_id":   "user",
		"to_session_id":   "sess-target",
		"to_agent_id":     "user",
		"kind":            "notification",
		"type":            "status_update",
		"body":            "heads up",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}

	if elicitCalled {
		t.Error("elicitation was incorrectly called for non-directive kind")
	}
}
