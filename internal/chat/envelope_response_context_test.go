package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestAssembleContext_EnvelopeResponseRoleMappedToUser verifies that messages
// stored with role=envelope_response are surfaced to the provider as user
// messages (Anthropic accepts only user/assistant), and that the marker-prefixed
// content is preserved so the LLM can distinguish them from ordinary user text.
func TestAssembleContext_EnvelopeResponseRoleMappedToUser(t *testing.T) {
	cb, s := newTestBroker(t)

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{Name: "T", Slug: "t", SystemPrompt: "x"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	content := FormatEnvelopeResponseContent("ticket-form", StatusSubmitted, `{"id":"t1"}`)
	msg := &store.Message{SessionID: sess.ID, Role: RoleEnvelopeResponse, Content: content}
	if err := s.CreateMessage(msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	_, messages, err := cb.AssembleContext(context.Background(), sess, agent, &store.AgentMode{}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Role != "user" {
		t.Fatalf("expected role 'user' (remapped), got %q", messages[0].Role)
	}
	if !strings.HasPrefix(messages[0].Content, "[envelope:ticket-form status:submitted]") {
		t.Fatalf("expected envelope prefix, got %q", messages[0].Content)
	}
}

func TestFormatEnvelopeResponseContent(t *testing.T) {
	got := FormatEnvelopeResponseContent("q", StatusCancelled, "")
	want := "[envelope:q status:cancelled] {}"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
