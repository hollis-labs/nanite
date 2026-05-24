package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentKnowledgeSeed_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "knowledge-seed-rt")

	row := AgentKnowledgeSeed{
		AgentID:   agent.ID,
		SeedKey:   "boot-conventions",
		Namespace: "user/chrispian/knowledge",
		Body:      "Always prefer explicit error wrapping.",
		TagsJSON:  `["convention","boot"]`,
	}
	if err := s.InsertAgentKnowledgeSeed(ctx, row); err != nil {
		t.Fatalf("InsertAgentKnowledgeSeed: %v", err)
	}

	list, err := s.ListAgentKnowledgeSeeds(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentKnowledgeSeeds: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListAgentKnowledgeSeeds: got %d, want 1", len(list))
	}

	got, err := s.GetAgentKnowledgeSeed(ctx, agent.ID, "boot-conventions")
	if err != nil {
		t.Fatalf("GetAgentKnowledgeSeed: %v", err)
	}
	if got.Namespace != "user/chrispian/knowledge" {
		t.Errorf("Namespace: got %q", got.Namespace)
	}
	if got.TagsJSON != `["convention","boot"]` {
		t.Errorf("TagsJSON: got %q", got.TagsJSON)
	}
	if got.AppliedAt != "" {
		t.Errorf("AppliedAt: expected empty for unapplied seed, got %q", got.AppliedAt)
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt: expected default-populated timestamp, got empty")
	}

	if err := s.DeleteAgentKnowledgeSeed(ctx, agent.ID, "boot-conventions"); err != nil {
		t.Fatalf("DeleteAgentKnowledgeSeed: %v", err)
	}
	if _, err := s.GetAgentKnowledgeSeed(ctx, agent.ID, "boot-conventions"); !errors.Is(err, ErrAgentKnowledgeSeedNotFound) {
		t.Fatalf("GetAgentKnowledgeSeed after delete: got %v, want ErrAgentKnowledgeSeedNotFound", err)
	}
}

// TestAgentKnowledgeSeed_MarkApplied — FU-7f. After
// MarkAgentKnowledgeSeedApplied the applied_at column must be populated.
// Calling on a missing row must return ErrAgentKnowledgeSeedNotFound.
func TestAgentKnowledgeSeed_MarkApplied(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "knowledge-seed-mark-applied")

	row := AgentKnowledgeSeed{
		AgentID:   agent.ID,
		SeedKey:   "boot-conventions",
		Namespace: "user/chrispian/knowledge",
		Body:      "x",
	}
	if err := s.InsertAgentKnowledgeSeed(ctx, row); err != nil {
		t.Fatalf("InsertAgentKnowledgeSeed: %v", err)
	}
	if err := s.MarkAgentKnowledgeSeedApplied(ctx, agent.ID, "boot-conventions"); err != nil {
		t.Fatalf("MarkAgentKnowledgeSeedApplied: %v", err)
	}
	got, err := s.GetAgentKnowledgeSeed(ctx, agent.ID, "boot-conventions")
	if err != nil {
		t.Fatalf("GetAgentKnowledgeSeed: %v", err)
	}
	if got.AppliedAt == "" {
		t.Error("AppliedAt: expected non-empty timestamp after MarkApplied, got empty")
	}

	// Missing row must surface as ErrAgentKnowledgeSeedNotFound.
	err = s.MarkAgentKnowledgeSeedApplied(ctx, agent.ID, "no-such-key")
	if !errors.Is(err, ErrAgentKnowledgeSeedNotFound) {
		t.Fatalf("MarkAgentKnowledgeSeedApplied on missing row: got %v, want ErrAgentKnowledgeSeedNotFound", err)
	}
}

func TestAgentKnowledgeSeed_DefaultsTagsJSON(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "knowledge-seed-tags")

	row := AgentKnowledgeSeed{
		AgentID:   agent.ID,
		SeedKey:   "no-tags",
		Namespace: "user/chrispian/knowledge",
		Body:      "x",
		// TagsJSON intentionally omitted.
	}
	if err := s.InsertAgentKnowledgeSeed(ctx, row); err != nil {
		t.Fatalf("InsertAgentKnowledgeSeed: %v", err)
	}
	got, err := s.GetAgentKnowledgeSeed(ctx, agent.ID, "no-tags")
	if err != nil {
		t.Fatalf("GetAgentKnowledgeSeed: %v", err)
	}
	if got.TagsJSON != "[]" {
		t.Errorf("TagsJSON default: got %q, want \"[]\"", got.TagsJSON)
	}
}
