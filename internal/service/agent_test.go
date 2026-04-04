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
	modes       map[string][]store.AgentMode   // keyed by agentID
}

func newStubReader() *stubAgentReader {
	return &stubAgentReader{
		agents:      make(map[string]*store.AgentProfile),
		slugIndex:   make(map[string]*store.AgentProfile),
		sessionBind: make(map[string]*store.SessionAgent),
		modes:       make(map[string][]store.AgentMode),
	}
}

func (s *stubAgentReader) addAgent(a *store.AgentProfile) {
	s.agents[a.ID] = a
	if a.Slug != "" {
		s.slugIndex[a.Slug] = a
	}
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

func (s *stubAgentReader) GetAgentMode(agentID, slug string) (*store.AgentMode, error) {
	for _, m := range s.modes[agentID] {
		if m.Slug == slug {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("mode not found")
}

func (s *stubAgentReader) ListAgentModes(agentID string) ([]store.AgentMode, error) {
	return s.modes[agentID], nil
}

func (s *stubAgentReader) GetSessionPrimaryAgent(sessionID string) (*store.SessionAgent, error) {
	if sa, ok := s.sessionBind[sessionID]; ok {
		return sa, nil
	}
	return nil, fmt.Errorf("no primary agent")
}

func (s *stubAgentReader) ListSessionAgents(string) ([]store.SessionAgent, error)     { return nil, nil }
func (s *stubAgentReader) ListAgentSkills(string) ([]store.Skill, error)              { return nil, nil }
func (s *stubAgentReader) ListAgentProjects(string) ([]store.Project, error)           { return nil, nil }
func (s *stubAgentReader) ListProjectAgents(string) ([]store.AgentProfile, error)     { return nil, nil }
func (s *stubAgentReader) GetAgentAssignedModes(string) ([]store.Mode, error)         { return nil, nil }

type stubAgentWriter struct {
	created []store.AgentProfile
	deleted []string
	ensured []string // sessionIDs that got EnsureSessionAgent
}

func (s *stubAgentWriter) CreateAgent(a *store.AgentProfile) error {
	s.created = append(s.created, *a)
	return nil
}
func (s *stubAgentWriter) UpdateAgent(*store.AgentProfile) error                    { return nil }
func (s *stubAgentWriter) DeleteAgent(slug string) error                            { s.deleted = append(s.deleted, slug); return nil }
func (s *stubAgentWriter) UpsertAgentBySlug(*store.AgentProfile) error              { return nil }
func (s *stubAgentWriter) CreateAgentMode(*store.AgentMode) error                   { return nil }
func (s *stubAgentWriter) EnsureSessionAgent(sid, _, _ string, _ bool) error        { s.ensured = append(s.ensured, sid); return nil }
func (s *stubAgentWriter) SetSessionAgentMode(string, string, string) error         { return nil }
func (s *stubAgentWriter) DeleteSessionAgent(string, string) error                  { return nil }
func (s *stubAgentWriter) AssignSkillToAgent(string, string, string) error          { return nil }
func (s *stubAgentWriter) RemoveSkillFromAgent(string, string) error                { return nil }
func (s *stubAgentWriter) AddAgentProject(string, string) error                     { return nil }
func (s *stubAgentWriter) RemoveAgentProject(string, string) error                  { return nil }
func (s *stubAgentWriter) AssignModeToAgent(string, string) error                   { return nil }
func (s *stubAgentWriter) UnassignModeFromAgent(string, string) error               { return nil }

type stubSettings struct {
	defaultAgent string
}

func (s *stubSettings) GetUserSettings() (*store.UserSettings, error) {
	return &store.UserSettings{DefaultAgent: s.defaultAgent}, nil
}
func (s *stubSettings) UpdateUserSettings(*store.UserSettings) error                    { return nil }
func (s *stubSettings) GetPluginSettings(string) (*store.PluginSettings, error)         { return nil, nil }
func (s *stubSettings) UpsertPluginSettings(string, map[string]any) error               { return nil }
func (s *stubSettings) UpsertPluginSchema(string, []store.ConfigField) error            { return nil }
func (s *stubSettings) ListPluginSettings() ([]*store.PluginSettings, error)            { return nil, nil }
func (s *stubSettings) UpdatePluginIcon(string, string) error                           { return nil }
func (s *stubSettings) GetPluginSettingValue(string, string) (string, error)            { return "", nil }

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

func TestAgentService_ResolveForSession_BoundAgent(t *testing.T) {
	reader := newStubReader()
	agent := &store.AgentProfile{ID: "agent-1", Name: "Bound", Slug: "bound", Status: "active"}
	reader.addAgent(agent)
	reader.sessionBind["sess-1"] = &store.SessionAgent{
		SessionID: "sess-1", AgentID: "agent-1", Mode: "code", IsPrimary: true,
	}
	reader.modes["agent-1"] = []store.AgentMode{
		{ID: "m1", AgentID: "agent-1", Slug: "code", Name: "Code Mode"},
	}

	writer := &stubAgentWriter{}
	svc := NewAgentService(AgentServiceConfig{
		Agents:  reader,
		Writers: writer,
	})

	got, mode, err := svc.ResolveForSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if got.ID != "agent-1" {
		t.Errorf("agent ID = %q, want %q", got.ID, "agent-1")
	}
	if mode.Slug != "code" {
		t.Errorf("mode slug = %q, want %q", mode.Slug, "code")
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

	got, _, err := svc.ResolveForSession(context.Background(), "unbound-sess")
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

func TestAgentService_ResolveForSession_HardcodedFallback(t *testing.T) {
	reader := newStubReader()
	// Add the mentat fallback agent by slug (the slug-based fallback path).
	fallback := &store.AgentProfile{ID: "mentat-001", Name: "Mentat", Slug: "mentat", Status: "active"}
	reader.addAgent(fallback)

	writer := &stubAgentWriter{}
	svc := NewAgentService(AgentServiceConfig{
		Agents:   reader,
		Writers:  writer,
		Settings: &stubSettings{defaultAgent: ""},
	})

	got, _, err := svc.ResolveForSession(context.Background(), "orphan-sess")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if got.ID != "mentat-001" {
		t.Errorf("agent ID = %q, want %q", got.ID, "mentat-001")
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

	_, _, err := svc.ResolveForSession(context.Background(), "sess-d")
	if err == nil {
		t.Fatal("expected error for disabled agent, got nil")
	}
}

func TestAgentService_ResolveForSession_ModeFallback(t *testing.T) {
	reader := newStubReader()
	agent := &store.AgentProfile{ID: "a1", Name: "NoMode", Slug: "nomode", Status: "active"}
	reader.addAgent(agent)
	reader.sessionBind["sess-m"] = &store.SessionAgent{
		SessionID: "sess-m", AgentID: "a1", Mode: "nonexistent", IsPrimary: true,
	}
	// No modes registered — should fall back to empty AgentMode.

	svc := NewAgentService(AgentServiceConfig{
		Agents:  reader,
		Writers: &stubAgentWriter{},
	})

	_, mode, err := svc.ResolveForSession(context.Background(), "sess-m")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if mode.Slug != "" {
		t.Errorf("expected empty fallback mode, got slug %q", mode.Slug)
	}
}
