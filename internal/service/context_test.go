package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestContextService_PruneAfterTurn(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "prune-sess"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// PruneAfterTurn on an empty session should be a no-op.
	if err := svc.PruneAfterTurn(context.Background(), "prune-sess"); err != nil {
		t.Fatalf("PruneAfterTurn: %v", err)
	}
}

func TestContextService_AssembleSlots(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: "slot-sess-1", Title: "test"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateMessage(context.Background(), &store.Message{
		ID:        "msg-slot-1",
		SessionID: "slot-sess-1",
		Role:      "user",
		Content:   "hello slots",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	agent := &store.AgentProfile{
		ID:           "agent-slot",
		Name:         "SlotAgent",
		Slug:         "slot",
		SystemPrompt: "You are a slot test agent.",
		Status:       "active",
	}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Blocks) == 0 {
		t.Error("expected at least one slot block")
	}
	if result.SystemPrompt == "" {
		t.Error("expected non-empty system prompt")
	}
	if len(result.Messages) == 0 {
		t.Error("expected at least one message")
	}
	if result.Window == nil {
		t.Error("expected non-nil window")
	}

	// Verify system slot is in blocks.
	foundSystem := false
	for _, b := range result.Blocks {
		if b.SlotName == "system" {
			foundSystem = true
		}
	}
	if !foundSystem {
		t.Error("expected system slot in blocks")
	}
}

// Verify the interface is satisfied at compile time.
var _ ContextService = (*contextServiceImpl)(nil)
