package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentMembershipSetSessionAgentKeepsOnePrimary(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	svc := NewAgentMembershipService(st, st)
	identities := map[string]string{}
	for _, slug := range []string{"agent-a", "agent-b", "agent-c"} {
		p := &store.AgentProfile{Name: slug, Slug: slug, SystemPrompt: "Private prior host binding."}
		if err := persistTestActor(ctx, st, p); err != nil {
			t.Fatal(err)
		}
		identities[slug] = p.ID
	}

	sess := &store.Session{Title: "membership"}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// First primary: nothing to demote.
	prev, err := svc.SetSessionAgent(ctx, sess.ID, identities["agent-a"], "plan", true)
	if err != nil {
		t.Fatalf("SetSessionAgent(a): %v", err)
	}
	if prev != "" {
		t.Fatalf("previous primary for a fresh session = %q, want empty", prev)
	}

	// A participant does not touch the primary and reports none.
	prev, err = svc.SetSessionAgent(ctx, sess.ID, identities["agent-c"], "default", false)
	if err != nil {
		t.Fatalf("SetSessionAgent(c): %v", err)
	}
	if prev != "" {
		t.Fatalf("previous primary for a participant = %q, want empty", prev)
	}

	// A new primary demotes the old one and reports it.
	prev, err = svc.SetSessionAgent(ctx, sess.ID, identities["agent-b"], "default", true)
	if err != nil {
		t.Fatalf("SetSessionAgent(b): %v", err)
	}
	if prev != identities["agent-a"] {
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
	if primaries != 1 || !byID[identities["agent-b"]].IsPrimary {
		t.Fatalf("want agent-b as the only primary, got %v", agents)
	}
	// Demotion keeps the demoted agent's mode.
	if got := byID[identities["agent-a"]].Mode; got != "plan" {
		t.Fatalf("demoted agent-a mode = %q, want plan", got)
	}

	// Re-asserting the current primary reports itself as previous; the
	// handler compares the two before emitting agent.switched.
	prev, err = svc.SetSessionAgent(ctx, sess.ID, identities["agent-b"], "default", true)
	if err != nil {
		t.Fatalf("SetSessionAgent(b again): %v", err)
	}
	if prev != identities["agent-b"] {
		t.Fatalf("previous primary = %q, want agent-b", prev)
	}
}

func TestSessionPrimaryRefusalPreservesCurrentBindingAndModes(t *testing.T) {
	st := newConfigTestStore(t)
	svc := NewAgentMembershipService(st, st)
	p := &store.AgentProfile{Name: "Prior primary", Slug: "primary-fenced"}
	if err := persistTestActor(t.Context(), st, p); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{Title: "Fenced switch"}
	if err := st.CreateSession(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSessionAgent(t.Context(), sess.ID, p.ID, "plan", true); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM session_actor_bindings ORDER BY session_id,agent_id`
	before := immutableConfigSnapshot(t, st, query)
	for _, target := range []string{"unbound", "msg://agent/unverified"} {
		if _, err := svc.SetSessionAgent(t.Context(), sess.ID, target, "default", true); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("switch %s: %v", target, err)
		}
		immutableConfigUnchanged(t, st, query, before)
	}
	// Replacing a revoked primary narrows its old binding without giving it
	// renewed runtime admission; the new target is independently prior-bound.
	next := &store.AgentProfile{Name: "Next prior primary", Slug: "next-primary-fenced"}
	if err := persistTestActor(t.Context(), st, next); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSessionAgent(t.Context(), sess.ID, next.ID, "default", true); err != nil {
		t.Fatal(err)
	}
	primary, err := st.GetSessionPrimaryAgent(t.Context(), sess.ID)
	if err != nil || primary.AgentID != next.ID {
		t.Fatalf("replacement: %+v %v", primary, err)
	}
	if _, err := st.GetAgentForActor(t.Context(), p.ID); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatal(err)
	}
}
