package service

import (
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

func drainWarning(t *testing.T, ch chan chat.StreamEvent) (count int, last chat.StreamEvent) {
	t.Helper()
	for {
		select {
		case ev := <-ch:
			count++
			last = ev
		default:
			return
		}
	}
}

func TestMaybeEmitEmbeddingWarning_EmitsOncePerSession(t *testing.T) {
	s := &chatServiceImpl{
		embeddingStatus:   EmbeddingStatusDisabled,
		embeddingProvider: "",
	}
	ch := make(chan chat.StreamEvent, 4)

	s.maybeEmitEmbeddingWarning("session-1", ch)
	s.maybeEmitEmbeddingWarning("session-1", ch)

	count, last := drainWarning(t, ch)
	if count != 1 {
		t.Fatalf("expected 1 warning emit, got %d", count)
	}
	if last.Type != "tool_warning" {
		t.Errorf("unexpected type: %q", last.Type)
	}
	var payload chat.ToolWarningPayload
	if err := json.Unmarshal([]byte(last.Data), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.ToolName != "memory" {
		t.Errorf("tool_name: got %q, want memory", payload.ToolName)
	}
	if payload.Level != "warning" {
		t.Errorf("level: got %q, want warning", payload.Level)
	}
}

func TestMaybeEmitEmbeddingWarning_SkipsWhenActive(t *testing.T) {
	s := &chatServiceImpl{embeddingStatus: EmbeddingStatusActive}
	ch := make(chan chat.StreamEvent, 2)
	s.maybeEmitEmbeddingWarning("session-x", ch)
	count, _ := drainWarning(t, ch)
	if count != 0 {
		t.Errorf("expected no emit when active, got %d", count)
	}
}

func TestMaybeEmitEmbeddingWarning_DistinctSessions(t *testing.T) {
	s := &chatServiceImpl{embeddingStatus: EmbeddingStatusUnreachable, embeddingProvider: "ollama"}
	ch := make(chan chat.StreamEvent, 4)

	s.maybeEmitEmbeddingWarning("a", ch)
	s.maybeEmitEmbeddingWarning("b", ch)

	count, _ := drainWarning(t, ch)
	if count != 2 {
		t.Errorf("expected one emit per distinct session, got %d", count)
	}
}
