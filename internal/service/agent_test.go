package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- test doubles ---

type stubAgentReader struct {
	agents      map[string]*store.AgentProfile // keyed by ID
	slugIndex   map[string]*store.AgentProfile // keyed by slug
	sessionBind map[string]*store.SessionAgent // keyed by sessionID
	roles       map[string]*store.Role         // keyed by ID
}

func newStubReader() *stubAgentReader {
	return &stubAgentReader{
		agents:      make(map[string]*store.AgentProfile),
		slugIndex:   make(map[string]*store.AgentProfile),
		sessionBind: make(map[string]*store.SessionAgent),
		roles:       make(map[string]*store.Role),
	}
}

func (s *stubAgentReader) addAgent(a *store.AgentProfile) {
	s.agents[a.ID] = a
	if a.Slug != "" {
		s.slugIndex[a.Slug] = a
	}
}

func (s *stubAgentReader) addRole(r *store.Role) {
	s.roles[r.ID] = r
}

// GetRole mirrors store.Store.GetRole's contract: (nil, nil) on a miss,
// never an error for "not found".
func (s *stubAgentReader) GetRole(id string) (*store.Role, error) {
	if r, ok := s.roles[id]; ok {
		return r, nil
	}
	return nil, nil
}

func (s *stubAgentReader) GetAgent(id string) (*store.AgentProfile, error) {
	if a, ok := s.agents[id]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("not found")
}

func (s *stubAgentReader) GetAgentBySlug(slug string) (*store.AgentProfile, error) {
	if a, ok := s.slugIndex[slug]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("not found")
}

func (s *stubAgentReader) ListAgents() ([]store.AgentProfile, error) {
	out := make([]store.AgentProfile, 0, len(s.agents))
	for _, a := range s.agents {
		out = append(out, *a)
	}
	return out, nil
}

func (s *stubAgentReader) ListAgentsBySource(string) ([]store.AgentProfile, error) {
	return nil, nil
}

func (s *stubAgentReader) GetSessionPrimaryAgent(sessionID string) (*store.SessionAgent, error) {
	if sa, ok := s.sessionBind[sessionID]; ok {
		return sa, nil
	}
	return nil, fmt.Errorf("no primary agent")
}

func (s *stubAgentReader) ListSessionAgents(string) ([]store.SessionAgent, error) { return nil, nil }
func (s *stubAgentReader) ListAgentSkills(string) ([]store.Skill, error)          { return nil, nil }
func (s *stubAgentReader) ListAgentProjects(string) ([]store.Project, error)      { return nil, nil }
func (s *stubAgentReader) ListProjectAgents(string) ([]store.AgentProfile, error) { return nil, nil }

type stubAgentWriter struct {
	created []store.AgentProfile
	deleted []string
	ensured []string // sessionIDs that got EnsureSessionAgent
}

func (s *stubAgentWriter) CreateAgent(a *store.AgentProfile) error {
	s.created = append(s.created, *a)
	return nil
}
func (s *stubAgentWriter) UpdateAgent(*store.AgentProfile) error { return nil }
func (s *stubAgentWriter) DeleteAgent(slug string) error {
	s.deleted = append(s.deleted, slug)
	return nil
}
func (s *stubAgentWriter) UpsertAgentBySlug(*store.AgentProfile) error { return nil }
func (s *stubAgentWriter) EnsureSessionAgent(sid, _, _ string, _ bool) error {
	s.ensured = append(s.ensured, sid)
	return nil
}
func (s *stubAgentWriter) SetSessionAgentMode(string, string, string) error { return nil }
func (s *stubAgentWriter) DeleteSessionAgent(string, string) error          { return nil }
func (s *stubAgentWriter) AssignSkillToAgent(string, string, string) error  { return nil }
func (s *stubAgentWriter) RemoveSkillFromAgent(string, string) error        { return nil }
func (s *stubAgentWriter) AddAgentProject(string, string) error             { return nil }
func (s *stubAgentWriter) RemoveAgentProject(string, string) error          { return nil }

type stubSettings struct {
	defaultAgent string
}

func (s *stubSettings) GetUserSettings() (*store.UserSettings, error) {
	return &store.UserSettings{DefaultAgent: s.defaultAgent}, nil
}
func (s *stubSettings) UpdateUserSettings(*store.UserSettings) error            { return nil }
func (s *stubSettings) GetPluginSettings(string) (*store.PluginSettings, error) { return nil, nil }
func (s *stubSettings) UpsertPluginSettings(string, map[string]any) error       { return nil }
func (s *stubSettings) UpsertPluginSchema(string, []store.ConfigField) error    { return nil }
func (s *stubSettings) ListPluginSettings() ([]*store.PluginSettings, error)    { return nil, nil }
func (s *stubSettings) UpdatePluginIcon(string, string) error                   { return nil }
func (s *stubSettings) GetPluginSettingValue(string, string) (string, error)    { return "", nil }

// --- tests ---

func TestAgentService_CRUD(t *testing.T) {
	reader := newStubReader()
	writer := &stubAgentWriter{}
	svc := NewAgentService(AgentServiceConfig{
		Agents:  reader,
		Writers: writer,
	})
	ctx := context.Background()

	agent := &store.AgentProfile{ID: "a1", Name: "Test", Slug: "test", Status: "active"}
	reader.addAgent(agent)

	// Get by ID
	got, err := svc.Get(ctx, "a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Test" {
		t.Errorf("Get name = %q, want %q", got.Name, "Test")
	}

	// Get by slug
	got, err = svc.GetBySlug(ctx, "test")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.ID != "a1" {
		t.Errorf("GetBySlug ID = %q, want %q", got.ID, "a1")
	}

	// List
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List len = %d, want 1", len(all))
	}

	// Delete
	if err := svc.Delete(ctx, "test"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != "test" {
		t.Errorf("Delete called with %v, want [test]", writer.deleted)
	}
}

// TestAgentService_ResolveForSession_BoundAgent used to also assert the
// resolved Legacy AgentMode (mode.Slug == "code") — Phase 0 item 21 ("Cut
// Modes, in full") deleted store.AgentMode and ResolveForSession's second
// return value entirely, so this test now only covers agent resolution.
func TestAgentService_ResolveForSession_BoundAgent(t *testing.T) {
	reader := newStubReader()
	agent := &store.AgentProfile{ID: "agent-1", Name: "Bound", Slug: "bound", Status: "active"}
	reader.addAgent(agent)
	reader.sessionBind["sess-1"] = &store.SessionAgent{
		SessionID: "sess-1", AgentID: "agent-1", Mode: "code", IsPrimary: true,
	}

	writer := &stubAgentWriter{}
	svc := NewAgentService(AgentServiceConfig{
		Agents:  reader,
		Writers: writer,
	})

	got, err := svc.ResolveForSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if got.ID != "agent-1" {
		t.Errorf("agent ID = %q, want %q", got.ID, "agent-1")
	}
	// Should NOT auto-assign since a binding already existed.
	if len(writer.ensured) != 0 {
		t.Errorf("expected no auto-assign, got %v", writer.ensured)
	}
}

func TestAgentService_ResolveForSession_SettingsDefault(t *testing.T) {
	reader := newStubReader()
	agent := &store.AgentProfile{ID: "settings-agent", Name: "Settings", Slug: "settings", Status: "active"}
	reader.addAgent(agent)

	writer := &stubAgentWriter{}
	svc := NewAgentService(AgentServiceConfig{
		Agents:   reader,
		Writers:  writer,
		Settings: &stubSettings{defaultAgent: "settings-agent"},
	})

	got, err := svc.ResolveForSession(context.Background(), "unbound-sess")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if got.ID != "settings-agent" {
		t.Errorf("agent ID = %q, want %q", got.ID, "settings-agent")
	}
	if len(writer.ensured) != 1 || writer.ensured[0] != "unbound-sess" {
		t.Errorf("auto-assign expected for unbound-sess, got %v", writer.ensured)
	}
}

// TestAgentService_ResolveForSessionReadOnly_NoAutoAssign is the
// regression test for the code-review finding on
// internal/service/messaging_reactor.go's resolveMessageWakePolicy:
// resolving the effective agent for an unbound session must not create a
// session_agents row (EnsureSessionAgent) or emit AgentAssigned, unlike
// ResolveForSession's deliberate auto-assign-on-first-touch behavior.
func TestAgentService_ResolveForSessionReadOnly_NoAutoAssign(t *testing.T) {
	reader := newStubReader()
	agent := &store.AgentProfile{ID: "settings-agent", Name: "Settings", Slug: "settings", Status: "active"}
	reader.addAgent(agent)

	writer := &stubAgentWriter{}
	events := &fakeEventEmitter{}
	svc := NewAgentService(AgentServiceConfig{
		Agents:   reader,
		Writers:  writer,
		Settings: &stubSettings{defaultAgent: "settings-agent"},
		Events:   events,
	})

	got, err := svc.ResolveForSessionReadOnly(context.Background(), "unbound-sess")
	if err != nil {
		t.Fatalf("ResolveForSessionReadOnly: %v", err)
	}
	if got.ID != "settings-agent" {
		t.Errorf("agent ID = %q, want %q (resolution chain must still run identically to ResolveForSession)", got.ID, "settings-agent")
	}
	if len(writer.ensured) != 0 {
		t.Errorf("expected no EnsureSessionAgent call from the read-only path, got %v", writer.ensured)
	}
	if len(events.agentAssigned) != 0 {
		t.Errorf("expected no AgentAssigned event from the read-only path, got %v", events.agentAssigned)
	}
}

func TestAgentService_ResolveForSession_HardcodedFallback(t *testing.T) {
	reader := newStubReader()
	writer := &stubAgentWriter{}

	// Provide the built-in default agent via FileAgents (the new fallback path).
	defaultDef := &agent.Definition{
		Name:         "Default",
		Slug:         "default",
		SystemPrompt: "You are a helpful assistant.",
		Source:       "builtin",
	}

	svc := NewAgentService(AgentServiceConfig{
		Agents:     reader,
		Writers:    writer,
		Settings:   &stubSettings{defaultAgent: ""},
		FileAgents: []*agent.Definition{defaultDef},
	})

	got, err := svc.ResolveForSession(context.Background(), "orphan-sess")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if got.ID != "file-default" {
		t.Errorf("agent ID = %q, want %q", got.ID, "file-default")
	}
	if got.Slug != "default" {
		t.Errorf("agent slug = %q, want %q", got.Slug, "default")
	}
}

func TestAgentService_ResolveForSession_DisabledAgent(t *testing.T) {
	reader := newStubReader()
	agent := &store.AgentProfile{ID: "disabled-1", Name: "Disabled", Slug: "off", Status: "disabled"}
	reader.addAgent(agent)
	reader.sessionBind["sess-d"] = &store.SessionAgent{
		SessionID: "sess-d", AgentID: "disabled-1", Mode: "default", IsPrimary: true,
	}

	svc := NewAgentService(AgentServiceConfig{
		Agents:  reader,
		Writers: &stubAgentWriter{},
	})

	_, err := svc.ResolveForSession(context.Background(), "sess-d")
	if err == nil {
		t.Fatal("expected error for disabled agent, got nil")
	}
}

// Phase 0 item 21 ("Cut Modes, in full") deleted
// TestAgentService_ResolveForSession_ModeFallback — it asserted
// ResolveForSession fell back to an empty *store.AgentMode when the
// session's bound mode slug didn't resolve. There is no more Legacy
// AgentMode resolution step to fall back from.

// TestAgentService_Get_FileBackedAgentOverlaysDBOnlyFields is the
// regression test for TASKS/phase-1/12-fix-agent-service-get-drops-new-db-
// only-columns.md. Before this fix, Get/GetBySlug/List returned
// Definition.ToProfile() as-is for a file-backed agent, silently dropping
// role_id/model_id/runtime_kind/consumer_id — pure DB-only columns with no
// frontmatter representation at all (Phase 1 items 02/03) — even when a
// real agent_profiles DB row for the same slug carried real, non-default
// values for all four. It also dropped a DB-side edit to
// activation_mode/class/default_state whenever it diverged from what the
// file's own frontmatter declared, since those three DO have frontmatter
// fields and ToProfile()'s pre-existing "file wins when non-empty"
// handling took priority over the DB row unconditionally — this test's
// fixture deliberately gives the file its own (would-be-wrong) opinion on
// all three to prove the DB row wins even then, not just when the file is
// silent.
func TestAgentService_Get_FileBackedAgentOverlaysDBOnlyFields(t *testing.T) {
	reader := newStubReader()
	dbRow := &store.AgentProfile{
		ID:             "widget-uuid-1",
		Name:           "Widget (DB)",
		Slug:           "widget-agent",
		Status:         "active",
		RoleID:         "role-99",
		ModelID:        "claude-widget-model",
		RuntimeKind:    "api",
		ConsumerID:     "loom",
		ActivationMode: "concurrent",
		Class:          "harness",
		DefaultState:   "active",
	}
	reader.addAgent(dbRow)

	fileDef := &agent.Definition{
		Name:           "Widget (file)",
		Slug:           "widget-agent",
		SystemPrompt:   "You build widgets.",
		Source:         "project",
		SourceRef:      "/fake/widget-agent.md",
		ActivationMode: "singleton",
		Class:          "advisor",
		DefaultState:   "sleeping",
	}

	svc := NewAgentService(AgentServiceConfig{
		Agents:     reader,
		Writers:    &stubAgentWriter{},
		FileAgents: []*agent.Definition{fileDef},
	})
	ctx := context.Background()

	assertOverlaid := func(t *testing.T, label string, got *store.AgentProfile, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if got.SystemPrompt != fileDef.SystemPrompt {
			t.Errorf("%s: SystemPrompt = %q, want file-derived %q", label, got.SystemPrompt, fileDef.SystemPrompt)
		}
		if got.RoleID != dbRow.RoleID {
			t.Errorf("%s: RoleID = %q, want DB value %q", label, got.RoleID, dbRow.RoleID)
		}
		if got.ModelID != dbRow.ModelID {
			t.Errorf("%s: ModelID = %q, want DB value %q", label, got.ModelID, dbRow.ModelID)
		}
		if got.RuntimeKind != dbRow.RuntimeKind {
			t.Errorf("%s: RuntimeKind = %q, want DB value %q", label, got.RuntimeKind, dbRow.RuntimeKind)
		}
		if got.ConsumerID != dbRow.ConsumerID {
			t.Errorf("%s: ConsumerID = %q, want DB value %q", label, got.ConsumerID, dbRow.ConsumerID)
		}
		if got.ActivationMode != dbRow.ActivationMode {
			t.Errorf("%s: ActivationMode = %q, want DB value %q (not file's %q)", label, got.ActivationMode, dbRow.ActivationMode, fileDef.ActivationMode)
		}
		if got.Class != dbRow.Class {
			t.Errorf("%s: Class = %q, want DB value %q (not file's %q)", label, got.Class, dbRow.Class, fileDef.Class)
		}
		if got.DefaultState != dbRow.DefaultState {
			t.Errorf("%s: DefaultState = %q, want DB value %q (not file's %q)", label, got.DefaultState, dbRow.DefaultState, fileDef.DefaultState)
		}
	}

	// Get, by the legacy file-based ID ("file-<slug>" — fileDef carries no
	// `id:` frontmatter, so its CanonicalID() is deterministic).
	got, err := svc.Get(ctx, fileDef.CanonicalID())
	assertOverlaid(t, "Get(file-based ID)", got, err)

	// GetBySlug.
	got, err = svc.GetBySlug(ctx, "widget-agent")
	assertOverlaid(t, "GetBySlug", got, err)

	// List.
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found *store.AgentProfile
	for i := range all {
		if all[i].Slug == "widget-agent" {
			found = &all[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("List: widget-agent not found among %d results", len(all))
	}
	assertOverlaid(t, "List", found, nil)
}

// TestAgentService_Get_FileBackedAgentNoDBRowYet confirms the "file-backed
// but no DB row yet" case (a genuinely new file not yet ingested) degrades
// to the file-only view rather than crashing or fabricating data — Done
// means bullet 2 of TASKS/phase-1/12-fix-agent-service-get-drops-new-db-
// only-columns.md.
func TestAgentService_Get_FileBackedAgentNoDBRowYet(t *testing.T) {
	reader := newStubReader() // no DB row anywhere for "brand-new"
	fileDef := &agent.Definition{
		Name:         "Brand New",
		Slug:         "brand-new",
		SystemPrompt: "Fresh off disk.",
		Source:       "project",
	}
	svc := NewAgentService(AgentServiceConfig{
		Agents:     reader,
		Writers:    &stubAgentWriter{},
		FileAgents: []*agent.Definition{fileDef},
	})

	got, err := svc.GetBySlug(context.Background(), "brand-new")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.SystemPrompt != fileDef.SystemPrompt {
		t.Errorf("SystemPrompt = %q, want %q", got.SystemPrompt, fileDef.SystemPrompt)
	}
	dbOnlyFields := map[string]string{
		"RoleID":      got.RoleID,
		"ModelID":     got.ModelID,
		"RuntimeKind": got.RuntimeKind,
		"ConsumerID":  got.ConsumerID,
	}
	for name, val := range dbOnlyFields {
		if val != "" {
			t.Errorf("%s = %q, want empty default (no DB row exists yet)", name, val)
		}
	}
}
