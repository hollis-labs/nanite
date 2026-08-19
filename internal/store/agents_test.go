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
	if a.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
}

// TestCreateAgent_ParentDispatchAllowlistDefault — CW-20260512-0107
// (SP-20260512-0008 W2A). Creating an agent without specifying
// parent_dispatch_allowlist must default to "[]" so the migration 059
// column-default invariant holds in code.
func TestCreateAgent_ParentDispatchAllowlistDefault(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{Name: "Plain", Slug: "plain-bot", SystemPrompt: "x"}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.ParentDispatchAllowlist != "[]" {
		t.Errorf("ParentDispatchAllowlist = %q, want \"[]\" after default CreateAgent", a.ParentDispatchAllowlist)
	}

	got, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.ParentDispatchAllowlist != "[]" {
		t.Errorf("round-tripped ParentDispatchAllowlist = %q, want \"[]\"", got.ParentDispatchAllowlist)
	}
}

// TestCreateAgent_ParentDispatchAllowlistRoundTrip — non-default value
// must round-trip through INSERT + scan unchanged.
func TestCreateAgent_ParentDispatchAllowlistRoundTrip(t *testing.T) {
	s := newTestStore(t)
	a := &AgentProfile{
		Name:                    "Trusted",
		Slug:                    "trusted-bot",
		SystemPrompt:            "x",
		ParentDispatchAllowlist: `["researcher","planner","worker"]`,
	}
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	got, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.ParentDispatchAllowlist != `["researcher","planner","worker"]` {
		t.Errorf("ParentDispatchAllowlist round-trip = %q, want canonical 3-role list", got.ParentDispatchAllowlist)
	}
}

// TestUpdateAgent_ParentDispatchAllowlistPersists — UPDATE must persist
// the new column.
func TestUpdateAgent_ParentDispatchAllowlistPersists(t *testing.T) {
	s := newTestStore(t)
	a := makeTestAgent(t, s, "update-target")
	a.ParentDispatchAllowlist = `["worker"]`
	if err := s.UpdateAgent(a); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	got, err := s.GetAgent(a.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.ParentDispatchAllowlist != `["worker"]` {
		t.Errorf("ParentDispatchAllowlist after UPDATE = %q, want [\"worker\"]", got.ParentDispatchAllowlist)
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
	// CW-20260512-0111: migration 060 seeds 4 internal profile rows
	// (default, worker, planner, hint-selector). Capture the baseline
	// after migrations, then assert the two newly-created rows on top.
	baseline, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents baseline: %v", err)
	}
	baselineCount := len(baseline)

	makeTestAgent(t, s, "agent-a")
	makeTestAgent(t, s, "agent-b")

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if got, want := len(agents), baselineCount+2; got != want {
		t.Fatalf("expected %d agents (baseline %d + 2 test-created), got %d", want, baselineCount, got)
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

// TestDeleteAgent_NoPragmaToggle verifies DeleteAgent removes the profile and
// its junction-table references atomically. Regression guard for the audit
// finding where PRAGMA foreign_keys=OFF was applied pool-wide around the
// operation. Asserts:
//   - agent_profiles row is gone
//   - agent_skills, agent_prompt_templates rows are gone
//   - messages rows remain with agent_id NULLed (user data preserved)
//   - FK enforcement is still ON after the operation (run an FK-violating
//     INSERT and expect it to fail).
func TestDeleteAgent_NoPragmaToggle(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws1")
	agent := makeTestAgent(t, s, "del-agent")

	// skill + prompt-template assignments
	sk := &Skill{Name: "S", Slug: "s-del", Description: "d", Category: "t", ToolBindings: `[]`}
	if err := s.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if err := s.AssignSkillToAgent(agent.ID, sk.ID, ""); err != nil {
		t.Fatalf("AssignSkillToAgent: %v", err)
	}
	pt := &PromptTemplate{Name: "T", Slug: "t-del", Scope: "system", Template: "hi", Priority: 1}
	if err := s.CreatePromptTemplate(pt); err != nil {
		t.Fatalf("CreatePromptTemplate: %v", err)
	}
	if err := s.AssignPromptTemplateToAgent(agent.ID, pt.ID); err != nil {
		t.Fatalf("AssignPromptTemplateToAgent: %v", err)
	}

	// session + message referencing the agent
	sess := &Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.EnsureSessionAgent(sess.ID, agent.ID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}
	msg := &Message{SessionID: sess.ID, AgentID: agent.ID, Role: "assistant", Content: "hi"}
	if err := s.CreateMessage(msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if err := s.DeleteAgent("del-agent"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	// agent_profiles row gone
	var n int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM agent_profiles WHERE id = ?", agent.ID).Scan(&n); err != nil {
		t.Fatalf("count agent_profiles: %v", err)
	}
	if n != 0 {
		t.Errorf("expected agent_profiles row to be gone, got %d", n)
	}

	// junctions cleared
	for _, table := range []string{"agent_skills", "agent_prompt_templates", "session_agents"} {
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE agent_id = ?", agent.ID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("expected %s rows cleared, got %d", table, n)
		}
	}

	// message preserved, agent_id nulled
	var agentIDVal *string
	if err := s.DB.QueryRow("SELECT agent_id FROM messages WHERE id = ?", msg.ID).Scan(&agentIDVal); err != nil {
		t.Fatalf("select message after delete: %v", err)
	}
	if agentIDVal != nil {
		t.Errorf("expected message.agent_id to be NULL, got %q", *agentIDVal)
	}

	// FK enforcement still ON — insert with bogus session_id should fail.
	_, err := s.DB.Exec(
		`INSERT INTO session_agents (session_id, agent_id, mode, joined_at, is_primary)
		 VALUES ('no-such-session', 'no-such-agent', 'default', '2026-01-01', 0)`,
	)
	if err == nil {
		t.Error("expected FK failure after DeleteAgent; FK enforcement appears disabled")
	}
}

func TestCreateAgent_RejectsUserSlug(t *testing.T) {
	s := newTestStore(t)
	profile := &AgentProfile{
		Slug: "user",
		Name: "sneaky",
	}
	if err := s.CreateAgent(profile); err == nil {
		t.Fatal("expected error for slug=user, got nil")
	}
}

func TestCreateAgent_RejectsUserID(t *testing.T) {
	s := newTestStore(t)
	profile := &AgentProfile{
		ID:   "user",
		Slug: "not-user",
		Name: "also sneaky",
	}
	if err := s.CreateAgent(profile); err == nil {
		t.Fatal("expected error for id=user, got nil")
	}
}
