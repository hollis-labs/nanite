package service

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentMembershipSetSessionAgentKeepsOnePrimary(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	svc := NewAgentMembershipService(st, st)

	sess := &store.Session{Title: "membership"}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// First primary: nothing to demote.
	prev, err := svc.SetSessionAgent(ctx, sess.ID, "agent-a", "plan", true)
	if err != nil {
		t.Fatalf("SetSessionAgent(a): %v", err)
	}
	if prev != "" {
		t.Fatalf("previous primary for a fresh session = %q, want empty", prev)
	}

	// A participant does not touch the primary and reports none.
	prev, err = svc.SetSessionAgent(ctx, sess.ID, "agent-c", "default", false)
	if err != nil {
		t.Fatalf("SetSessionAgent(c): %v", err)
	}
	if prev != "" {
		t.Fatalf("previous primary for a participant = %q, want empty", prev)
	}

	// A new primary demotes the old one and reports it.
	prev, err = svc.SetSessionAgent(ctx, sess.ID, "agent-b", "default", true)
	if err != nil {
		t.Fatalf("SetSessionAgent(b): %v", err)
	}
	if prev != "agent-a" {
		t.Fatalf("previous primary = %q, want agent-a", prev)
	}

	agents, err := svc.ListSessionAgents(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListSessionAgents: %v", err)
	}
	byID := map[string]store.SessionAgent{}
	primaries := 0
	for _, sa := range agents {
		byID[sa.AgentID] = sa
		if sa.IsPrimary {
			primaries++
		}
	}
	if len(byID) != 3 {
		t.Fatalf("session agents = %v, want agent-a, agent-b and agent-c", agents)
	}
	if primaries != 1 || !byID["agent-b"].IsPrimary {
		t.Fatalf("want agent-b as the only primary, got %v", agents)
	}
	// Demotion keeps the demoted agent's mode.
	if got := byID["agent-a"].Mode; got != "plan" {
		t.Fatalf("demoted agent-a mode = %q, want plan", got)
	}

	// Re-asserting the current primary reports itself as previous; the
	// handler compares the two before emitting agent.switched.
	prev, err = svc.SetSessionAgent(ctx, sess.ID, "agent-b", "default", true)
	if err != nil {
		t.Fatalf("SetSessionAgent(b again): %v", err)
	}
	if prev != "agent-b" {
		t.Fatalf("previous primary = %q, want agent-b", prev)
	}
}
