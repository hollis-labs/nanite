package service

// Tests for CW-20260419-0019: persistPartialAssistant helper that saves a
// partial assistant message row on early-return error paths in generateResponse.

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// capturingStore is a minimal Store stub that records the last CreateMessage call.
type capturingStore struct {
	minimalStore
	lastMsg *store.Message
	callCount int
}

func (c *capturingStore) CreateMessage(msg *store.Message) error {
	c.lastMsg = msg
	c.callCount++
	return nil
}

// TestPersistPartialAssistant_StoresRow verifies that persistPartialAssistant
// calls CreateMessage exactly once with the expected content and metadata.
func TestPersistPartialAssistant_StoresRow(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-1", "msg-1", "agent-1", "partial content from stream")

	if cs.callCount != 1 {
		t.Fatalf("expected CreateMessage called once, got %d", cs.callCount)
	}
	msg := cs.lastMsg
	if msg == nil {
		t.Fatal("lastMsg is nil")
	}
	if msg.SessionID != "sess-1" {
		t.Errorf("session_id: got %q, want %q", msg.SessionID, "sess-1")
	}
	if msg.ID != "msg-1" {
		t.Errorf("id: got %q, want %q", msg.ID, "msg-1")
	}
	if msg.AgentID != "agent-1" {
		t.Errorf("agent_id: got %q, want %q", msg.AgentID, "agent-1")
	}
	if msg.Role != "assistant" {
		t.Errorf("role: got %q, want %q", msg.Role, "assistant")
	}
	// Metadata must carry had_error=true so the FE rehydration path picks it up.
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Metadata), &meta); err != nil {
		t.Fatalf("metadata is not valid JSON: %q — %v", msg.Metadata, err)
	}
	if v, ok := meta["had_error"]; !ok || v != true {
		t.Errorf("expected metadata.had_error=true, got %v (full metadata: %q)", v, msg.Metadata)
	}
	// Content must be a valid structured message JSON (not empty).
	if msg.Content == "" {
		t.Error("content must not be empty")
	}
	var structured map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Content), &structured); err != nil {
		t.Errorf("content is not valid JSON: %q", msg.Content)
	}
}

// TestPersistPartialAssistant_EmptyContentPlaceholder verifies that when no
// content was streamed, the stored row uses the "[generation interrupted]"
// placeholder rather than an empty string — so the FE always has something to
// display on refresh.
func TestPersistPartialAssistant_EmptyContentPlaceholder(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-2", "msg-2", "agent-2", "")

	if cs.callCount != 1 {
		t.Fatalf("expected CreateMessage called once, got %d", cs.callCount)
	}
	// Content should contain the placeholder text in the structured message.
	if cs.lastMsg == nil || cs.lastMsg.Content == "" {
		t.Fatal("expected non-empty content for placeholder row")
	}
	var structured map[string]interface{}
	if err := json.Unmarshal([]byte(cs.lastMsg.Content), &structured); err != nil {
		t.Fatalf("content is not valid JSON: %q", cs.lastMsg.Content)
	}
	text, _ := structured["text"].(string)
	if text != "[generation interrupted]" {
		t.Errorf("expected placeholder text %q, got %q", "[generation interrupted]", text)
	}
}

// TestPersistPartialAssistant_RealStore exercises the full path against a real
// SQLite store to assert the row is actually readable after the call.  This is
// the integration-level check that covers all early-return sites that flow
// through the helper (they all call the same helper, so one integration test
// suffices for the persistence guarantee).
func TestPersistPartialAssistant_RealStore(t *testing.T) {
	s, err := store.New(t.TempDir() + "/persist_partial.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Create a session so the FK constraint in CreateMessage succeeds.
	sess := &store.Session{ID: "test-session", Status: "active"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	svc := &chatServiceImpl{store: s}
	const msgID = "test-msg-partial"
	svc.persistPartialAssistant("test-session", msgID, "test-agent", "hello from error path")

	// Verify row exists and has had_error metadata.
	msg, err := s.GetMessage(msgID)
	if err != nil {
		t.Fatalf("GetMessage: %v — row was not persisted", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("role: got %q, want %q", msg.Role, "assistant")
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Metadata), &meta); err != nil {
		t.Fatalf("metadata is not valid JSON: %q — %v", msg.Metadata, err)
	}
	if v, ok := meta["had_error"]; !ok || v != true {
		t.Errorf("expected metadata.had_error=true; got %v (metadata: %q)", v, msg.Metadata)
	}
}
