package store

import (
	"context"
	"errors"
	"testing"
)

func TestAgentKnownTool_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "known-tool-rt")

	row := AgentKnownTool{
		AgentID:    agent.ID,
		ToolName:   "shell.exec",
		Pinned:     true,
		TTLSeconds: 3600,
		Reason:     "core working set",
	}
	if err := s.InsertAgentKnownTool(ctx, row); err != nil {
		t.Fatalf("InsertAgentKnownTool: %v", err)
	}

	list, err := s.ListAgentKnownTools(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentKnownTools: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListAgentKnownTools: got %d, want 1", len(list))
	}

	got, err := s.GetAgentKnownTool(ctx, agent.ID, "shell.exec")
	if err != nil {
		t.Fatalf("GetAgentKnownTool: %v", err)
	}
	if got.ToolName != "shell.exec" {
		t.Errorf("ToolName: got %q, want %q", got.ToolName, "shell.exec")
	}
	if !got.Pinned {
		t.Error("Pinned: got false, want true")
	}
	if got.TTLSeconds != 3600 {
		t.Errorf("TTLSeconds: got %d, want 3600", got.TTLSeconds)
	}
	if got.Reason != "core working set" {
		t.Errorf("Reason: got %q, want %q", got.Reason, "core working set")
	}
	if got.AddedAt == "" {
		t.Error("AddedAt: expected default-populated timestamp, got empty")
	}

	if err := s.DeleteAgentKnownTool(ctx, agent.ID, "shell.exec"); err != nil {
		t.Fatalf("DeleteAgentKnownTool: %v", err)
	}
	if _, err := s.GetAgentKnownTool(ctx, agent.ID, "shell.exec"); !errors.Is(err, ErrAgentKnownToolNotFound) {
		t.Fatalf("GetAgentKnownTool after delete: got %v, want ErrAgentKnownToolNotFound", err)
	}
}

func TestAgentKnownTool_BumpActivation(t *testing.T) {
	// FU-14 (2026-05-20): BumpActivation is now a no-op so that
	// activation_count + last_used_at no longer churn between turns and
	// invalidate Anthropic's prompt cache. This test validates the new
	// behavior: BumpActivation returns nil (preserves the call-site API)
	// but performs no DB write. Tool-call telemetry lives in event_log
	// instead.
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "bump-activation")

	// BumpActivation on an unknown tool should NOT create a row anymore.
	if err := s.BumpActivation(ctx, agent.ID, "grep"); err != nil {
		t.Fatalf("BumpActivation #1: %v", err)
	}
	_, err := s.GetAgentKnownTool(ctx, agent.ID, "grep")
	if err == nil {
		t.Fatal("expected ErrAgentKnownToolNotFound after BumpActivation no-op")
	}

	// BumpActivation on an existing row should NOT mutate it.
	if err := s.InsertAgentKnownTool(ctx, AgentKnownTool{
		AgentID:         agent.ID,
		ToolName:        "preseeded",
		Pinned:          true,
		ActivationCount: 42,
		Reason:          "role_seed",
	}); err != nil {
		t.Fatalf("InsertAgentKnownTool: %v", err)
	}
	if err := s.BumpActivation(ctx, agent.ID, "preseeded"); err != nil {
		t.Fatalf("BumpActivation #2: %v", err)
	}
	got, err := s.GetAgentKnownTool(ctx, agent.ID, "preseeded")
	if err != nil {
		t.Fatalf("GetAgentKnownTool: %v", err)
	}
	if got.ActivationCount != 42 {
		t.Errorf("ActivationCount mutated by no-op BumpActivation: got %d, want 42",
			got.ActivationCount)
	}
	if got.LastUsedAt != "" {
		t.Errorf("LastUsedAt mutated by no-op BumpActivation: got %q, want empty",
			got.LastUsedAt)
	}
}
