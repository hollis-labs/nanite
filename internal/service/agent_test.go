package service

import (
	"context"
	"fmt"
	"testing"

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

// TestAgentService_ResolveForSession_HardcodedFallback is the DB-only
// replacement for the pre-TASKS/adhoc/01-eliminate-file-based-agent-
// runtime.md version of this test (which supplied the built-in default
// agent via the now-removed FileAgents in-memory registry and asserted the
// synthetic "file-default" ID). The ultimate fallback is now a bare slug
// ("default", defaultFallbackSlug) resolved purely through the DB reader:
// resolveBinding returns that slug as the "agentID," s.Get(ctx, "default")
// misses (no row's real ID is literally "default"), and resolveForSession's
// own error-catching net falls through to GetBySlug(ctx, "default") —
// which finds the real row.
func TestAgentService_ResolveForSession_HardcodedFallback(t *testing.T) {
	reader := newStubReader()
	writer := &stubAgentWriter{}

	// Real DB row with a real, non-slug-shaped ID -- mirrors any of the 9
	// internal builtin profiles' actual agent_profiles.id post-ingest.
	defaultAgent := &store.AgentProfile{ID: "agt-default-real-id", Name: "Default", Slug: "default", Status: "active"}
	reader.addAgent(defaultAgent)

	svc := NewAgentService(AgentServiceConfig{
		Agents:   reader,
		Writers:  writer,
		Settings: &stubSettings{defaultAgent: ""},
	})

	got, err := svc.ResolveForSession(context.Background(), "orphan-sess")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if got.ID != "agt-default-real-id" {
		t.Errorf("agent ID = %q, want %q", got.ID, "agt-default-real-id")
	}
	if got.Slug != "default" {
		t.Errorf("agent slug = %q, want %q", got.Slug, "default")
	}
	if len(writer.ensured) != 1 || writer.ensured[0] != "orphan-sess" {
		t.Errorf("auto-assign expected for orphan-sess, got %v", writer.ensured)
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

// TestAgentService_Get_FileBackedAgentOverlaysDBOnlyFields and
// TestAgentService_Get_FileBackedAgentNoDBRowYet (TASKS/phase-1/12-fix-
// agent-service-get-drops-new-db-only-columns.md's regression pins) were
// removed by TASKS/adhoc/01-eliminate-file-based-agent-runtime.md: the
// in-memory file-definition registry (FileAgents/fileDefs/resolveFileProfile/
// agent.OverlayDBFields) they exercised no longer exists. Get/GetBySlug/List
// are now plain passthroughs to the DB reader (see TestAgentService_CRUD
// above) -- role_id/model_id/runtime_kind/consumer_id/activation_mode/
// class/default_state are just ordinary columns on that same row now, with
// no separate file-derived view to overlay them onto or diverge from.
