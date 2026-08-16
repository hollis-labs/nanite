package driftguard

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestStateCollector_CollectsUserMessagesAndStructuredRefs(t *testing.T) {
	st := newReflexTestStore(t)
	if err := st.Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := st.CreateSession(&store.Session{ID: "sess-reflex", WorkspaceID: "default", Title: "reflex"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.CreateMessage(&store.Message{
		ID:        "u1",
		SessionID: "sess-reflex",
		Role:      "user",
		Content:   "Let's document that and create ticket NAN-128.",
	}); err != nil {
		t.Fatalf("CreateMessage user: %v", err)
	}
	if err := st.CreateMessage(&store.Message{
		ID:        "a1",
		SessionID: "sess-reflex",
		Role:      "assistant",
		Content:   `{"v":1,"text":"Done.","tool_calls":[{"name":"torque_task_create"}],"envelopes":[{"type":"options"}]}`,
	}); err != nil {
		t.Fatalf("CreateMessage assistant: %v", err)
	}
	if err := st.RecordUsage("sess-reflex", "a1", "test-model", 11, 7, 0, 0, 0); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	got, err := (&StateCollector{Store: st, Window: 2}).Collect(context.Background(), "sess-reflex", "agent-1", "process")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got.UserMessages) != 1 || got.UserMessages[0].Content == "" {
		t.Fatalf("expected one user message, got %#v", got.UserMessages)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("expected one assistant message, got %#v", got.Messages)
	}
	if len(got.Messages[0].ToolNames) != 1 || got.Messages[0].ToolNames[0] != "torque_task_create" {
		t.Fatalf("expected structured tool refs, got %#v", got.Messages[0].ToolNames)
	}
	if len(got.Messages[0].EnvelopeTypes) != 1 || got.Messages[0].EnvelopeTypes[0] != "options" {
		t.Fatalf("expected structured envelope refs, got %#v", got.Messages[0].EnvelopeTypes)
	}
}

func newReflexTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
