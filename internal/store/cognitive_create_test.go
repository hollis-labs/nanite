package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestCognitiveCreateRollsBackBindingFailure(t *testing.T) {
	s := newTestStore(t)
	profile := &AgentProfile{Name: "Native", Slug: "native"}
	if err := s.CreateAgent(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	// Fail after the view INSERT, at the primary-binding write.
	if _, err := s.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_cognitive_binding BEFORE INSERT ON session_agents BEGIN SELECT RAISE(ABORT, 'binding refused'); END`); err != nil {
		t.Fatal(err)
	}
	view := &Session{ID: "rollback-view", Provider: "anthropic"}
	if err := s.CreateCognitiveSession(t.Context(), view, profile.ID); err == nil {
		t.Fatal("binding failure was ignored")
	}
	if _, err := s.GetSession(t.Context(), view.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("view leaked after rollback: %v", err)
	}
}

func TestCognitiveCreateCommitsNativePolicyAndBinding(t *testing.T) {
	s := newTestStore(t)
	profile := &AgentProfile{Name: "Native", Slug: "native"}
	if err := s.CreateAgent(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	view := &Session{Provider: "anthropic", Metadata: `{"label":"example"}`}
	if err := s.CreateCognitiveSession(t.Context(), view, profile.ID); err != nil {
		t.Fatal(err)
	}
	var runtime, bound string
	if err := s.DB.QueryRowContext(t.Context(), `SELECT subagent_runtime FROM sessions WHERE id=?`, view.ID).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(t.Context(), `SELECT agent_id FROM session_agents WHERE session_id=? AND is_primary=1`, view.ID).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if runtime != "api" || bound != profile.ID {
		t.Fatalf("incomplete create: runtime=%q binding=%q", runtime, bound)
	}
}
