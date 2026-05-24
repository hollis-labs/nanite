package store

import (
	"context"
	"testing"
)

// TestReapExpiredAgentKnownTools_DeletesOnlyExpiredNonPinned asserts the
// three-row scenario from the FU-7d brief: pinned (immortal), fresh (within
// TTL), and expired (last_used_at + ttl_seconds < now). Only the expired row
// should be deleted.
func TestReapExpiredAgentKnownTools_DeletesOnlyExpiredNonPinned(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "known-tools-reaper")

	// Use raw SQL to set last_used_at deterministically so we don't rely on
	// sleeping the test. InsertAgentKnownTool's normal path goes through
	// datetime('now') for last_used_at, which is fine for the fresh row but
	// not for the expired one.
	cases := []struct {
		toolName   string
		pinned     int
		lastUsed   string // SQLite datetime literal, or NULL semantics via sentinel
		ttlSeconds any    // int64 or nil
		shouldKeep bool
	}{
		// Pinned + recently used + short TTL. Pinned guard alone is
		// enough; even with an expired TTL this row must survive.
		{
			toolName:   "pinned-tool",
			pinned:     1,
			lastUsed:   "datetime('now', '-2 hours')",
			ttlSeconds: int64(3600),
			shouldKeep: true,
		},
		// Fresh non-pinned row — last_used_at is very recent so TTL has
		// not elapsed.
		{
			toolName:   "fresh-tool",
			pinned:     0,
			lastUsed:   "datetime('now')",
			ttlSeconds: int64(3600),
			shouldKeep: true,
		},
		// Expired non-pinned row — last_used_at + ttl_seconds is in the
		// past. This is the only row the sweep should delete.
		{
			toolName:   "expired-tool",
			pinned:     0,
			lastUsed:   "datetime('now', '-2 hours')",
			ttlSeconds: int64(3600),
			shouldKeep: false,
		},
	}

	for _, c := range cases {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO agent_known_tools
			    (agent_id, tool_name, pinned, activation_count,
			     last_used_at, added_at, ttl_seconds, reason)
			 VALUES (?, ?, ?, 1,
			         `+c.lastUsed+`,
			         datetime('now'),
			         ?, '')`,
			agent.ID, c.toolName, c.pinned, c.ttlSeconds,
		)
		if err != nil {
			t.Fatalf("seed %s: %v", c.toolName, err)
		}
	}

	deleted, err := s.ReapExpiredAgentKnownTools(ctx)
	if err != nil {
		t.Fatalf("ReapExpiredAgentKnownTools: %v", err)
	}
	if deleted != 1 {
		t.Errorf("ReapExpiredAgentKnownTools: deleted = %d, want 1", deleted)
	}

	for _, c := range cases {
		_, err := s.GetAgentKnownTool(ctx, agent.ID, c.toolName)
		if c.shouldKeep && err != nil {
			t.Errorf("%s should have been kept, got err: %v", c.toolName, err)
		}
		if !c.shouldKeep && err == nil {
			t.Errorf("%s should have been deleted, but still exists", c.toolName)
		}
	}
}

// TestReapExpiredAgentKnownTools_NullTTLIsImmortal asserts that rows whose
// ttl_seconds is SQL NULL (the role-seed default — no TTL configured) are
// never deleted by the reaper, even when last_used_at is ancient and the
// row is not pinned.
func TestReapExpiredAgentKnownTools_NullTTLIsImmortal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "known-tools-null-ttl")

	// Seed a non-pinned row with NULL ttl_seconds and an ancient
	// last_used_at. If the reaper SQL is wrong this is the row it would
	// erroneously delete.
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_known_tools
		    (agent_id, tool_name, pinned, activation_count,
		     last_used_at, added_at, ttl_seconds, reason)
		 VALUES (?, ?, 0, 1,
		         datetime('now', '-30 days'),
		         datetime('now'),
		         NULL, 'role seed default')`,
		agent.ID, "no-ttl-tool",
	)
	if err != nil {
		t.Fatalf("seed no-ttl-tool: %v", err)
	}

	// Also seed a row with last_used_at NULL (never activated). Reaper
	// should leave that alone too — there's no clock to age against.
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO agent_known_tools
		    (agent_id, tool_name, pinned, activation_count,
		     last_used_at, added_at, ttl_seconds, reason)
		 VALUES (?, ?, 0, 0,
		         NULL,
		         datetime('now', '-30 days'),
		         3600, 'never used')`,
		agent.ID, "never-used-tool",
	)
	if err != nil {
		t.Fatalf("seed never-used-tool: %v", err)
	}

	deleted, err := s.ReapExpiredAgentKnownTools(ctx)
	if err != nil {
		t.Fatalf("ReapExpiredAgentKnownTools: %v", err)
	}
	if deleted != 0 {
		t.Errorf("ReapExpiredAgentKnownTools: deleted = %d, want 0", deleted)
	}

	if _, err := s.GetAgentKnownTool(ctx, agent.ID, "no-ttl-tool"); err != nil {
		t.Errorf("no-ttl-tool should still exist: %v", err)
	}
	if _, err := s.GetAgentKnownTool(ctx, agent.ID, "never-used-tool"); err != nil {
		t.Errorf("never-used-tool should still exist: %v", err)
	}
}

// TestReapExpiredAgentKnownSkills mirrors the tools test for the parallel
// agent_known_skills table — same shape, separate sweep statement.
func TestReapExpiredAgentKnownSkills_DeletesOnlyExpiredNonPinned(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "known-skills-reaper")

	cases := []struct {
		skillName  string
		pinned     int
		lastUsed   string
		ttlSeconds any
		shouldKeep bool
	}{
		{"pinned-skill", 1, "datetime('now', '-2 hours')", int64(3600), true},
		{"fresh-skill", 0, "datetime('now')", int64(3600), true},
		{"expired-skill", 0, "datetime('now', '-2 hours')", int64(3600), false},
	}

	for _, c := range cases {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO agent_known_skills
			    (agent_id, skill_name, pinned, activation_count,
			     last_used_at, added_at, ttl_seconds, reason)
			 VALUES (?, ?, ?, 1,
			         `+c.lastUsed+`,
			         datetime('now'),
			         ?, '')`,
			agent.ID, c.skillName, c.pinned, c.ttlSeconds,
		)
		if err != nil {
			t.Fatalf("seed %s: %v", c.skillName, err)
		}
	}

	deleted, err := s.ReapExpiredAgentKnownSkills(ctx)
	if err != nil {
		t.Fatalf("ReapExpiredAgentKnownSkills: %v", err)
	}
	if deleted != 1 {
		t.Errorf("ReapExpiredAgentKnownSkills: deleted = %d, want 1", deleted)
	}

	for _, c := range cases {
		_, err := s.GetAgentKnownSkill(ctx, agent.ID, c.skillName)
		if c.shouldKeep && err != nil {
			t.Errorf("%s should have been kept, got err: %v", c.skillName, err)
		}
		if !c.shouldKeep && err == nil {
			t.Errorf("%s should have been deleted, but still exists", c.skillName)
		}
	}
}
