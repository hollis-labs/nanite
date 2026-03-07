package store

import "testing"

func makeTestAgent(t *testing.T, s *Store, slug string) *AgentProfile {
	t.Helper()
	a := &AgentProfile{
		Name:         "Test Agent " + slug,
		Slug:         slug,
		SystemPrompt: "You are a test agent.",
	}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return a
}

func TestCreateAgent(t *testing.T) {
	s := newTestStore(t)

	a := &AgentProfile{
		Name:         "Bot",
		Slug:         "bot",
		SystemPrompt: "You are a bot.",
	}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if a.ID == "" {
		t.Error("expected ID to be generated")
	}
	if a.Modes != "[]" {
		t.Errorf("expected default modes '[]', got %q", a.Modes)
	}
	if a.DefaultMode != "default" {
		t.Errorf("expected default_mode 'default', got %q", a.DefaultMode)
	}
	if a.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

func TestGetAgent(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "get-agent")

	got, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, a.ID)
	}
	if got.Slug != "get-agent" {
		t.Errorf("Slug mismatch: got %q, want %q", got.Slug, "get-agent")
	}
	if got.SystemPrompt != "You are a test agent." {
		t.Errorf("SystemPrompt mismatch: got %q", got.SystemPrompt)
	}
}

func TestGetAgentBySlug(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "slug-test")

	got, err := s.GetAgentBySlug("slug-test")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, a.ID)
	}
}

func TestListAgents(t *testing.T) {
	s := newTestStore(t)
	makeTestAgent(t, s, "agent-a")
	makeTestAgent(t, s, "agent-b")

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

func TestCreateAgentMode(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "mode-agent")

	m := &AgentMode{
		AgentID:        a.ID,
		Slug:           "coder",
		Name:           "Coder",
		PromptAddendum: "You are in coder mode.",
	}
	if err := s.CreateAgentMode(m); err != nil {
		t.Fatalf("CreateAgentMode: %v", err)
	}
	if m.ID == "" {
		t.Error("expected mode ID to be generated")
	}
}

func TestListAgentModes(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "modes-agent")

	for _, slug := range []string{"alpha", "beta"} {
		m := &AgentMode{AgentID: a.ID, Slug: slug, Name: slug, PromptAddendum: "test"}
		if err := s.CreateAgentMode(m); err != nil {
			t.Fatalf("CreateAgentMode %s: %v", slug, err)
		}
	}

	modes, err := s.ListAgentModes(a.ID)
	if err != nil {
		t.Fatalf("ListAgentModes: %v", err)
	}
	if len(modes) != 2 {
		t.Fatalf("expected 2 modes, got %d", len(modes))
	}
}

func TestEnsureSessionAgent(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")
	a := makeTestAgent(t, s, "ensure-agent")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// First call: insert.
	if err := s.EnsureSessionAgent(sess.ID, a.ID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent (insert): %v", err)
	}

	// Second call: upsert (should not error).
	if err := s.EnsureSessionAgent(sess.ID, a.ID, "coder", true); err != nil {
		t.Fatalf("EnsureSessionAgent (upsert): %v", err)
	}

	// Verify mode was updated.
	sa, err := s.GetSessionPrimaryAgent(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionPrimaryAgent: %v", err)
	}
	if sa.Mode != "coder" {
		t.Errorf("expected mode 'coder', got %q", sa.Mode)
	}
}

func TestGetSessionPrimaryAgent(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")
	a := makeTestAgent(t, s, "primary-agent")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.EnsureSessionAgent(sess.ID, a.ID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	sa, err := s.GetSessionPrimaryAgent(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionPrimaryAgent: %v", err)
	}
	if sa.AgentID != a.ID {
		t.Errorf("AgentID mismatch: got %q, want %q", sa.AgentID, a.ID)
	}
	if !sa.IsPrimary {
		t.Error("expected IsPrimary to be true")
	}
}

func TestListSessionAgents(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")
	a1 := makeTestAgent(t, s, "list-sa-1")
	a2 := makeTestAgent(t, s, "list-sa-2")

	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.EnsureSessionAgent(sess.ID, a1.ID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent 1: %v", err)
	}
	if err := s.EnsureSessionAgent(sess.ID, a2.ID, "default", false); err != nil {
		t.Fatalf("EnsureSessionAgent 2: %v", err)
	}

	agents, err := s.ListSessionAgents(sess.ID)
	if err != nil {
		t.Fatalf("ListSessionAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 session agents, got %d", len(agents))
	}
}
