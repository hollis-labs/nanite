package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- test doubles ---

type stubAgentReader struct {
	verifiedBindings map[string]bool                // Explicit private host-port decisions, never inferred from a profile.
	agents           map[string]*store.AgentProfile // keyed by ID
	slugIndex        map[string]*store.AgentProfile // keyed by slug
	sessionBind      map[string]*store.SessionAgent // keyed by sessionID
	roles            map[string]*store.Role         // keyed by ID
}

func newStubReader() *stubAgentReader {
	return &stubAgentReader{
		verifiedBindings: make(map[string]bool),
		agents:           make(map[string]*store.AgentProfile),
		slugIndex:        make(map[string]*store.AgentProfile),
		sessionBind:      make(map[string]*store.SessionAgent),
		roles:            make(map[string]*store.Role),
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
func (s *stubAgentReader) GetRole(ctx context.Context, id string) (*store.Role, error) {
	if r, ok := s.roles[id]; ok {
		return r, nil
	}
	return nil, nil
}

func (s *stubAgentReader) GetAgent(ctx context.Context, id string) (*store.AgentProfile, error) {
	if a, ok := s.agents[id]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("not found")
}

func (s *stubAgentReader) GetAgentForActor(_ context.Context, actor string) (*store.AgentProfile, error) {
	if !s.verifiedBindings[actor] {
		return nil, store.ErrVerifiedActorRequired
	}
	p := s.agents[actor]
	if p == nil || p.Status == "disabled" {
		return nil, store.ErrVerifiedActorRequired
	}
	return p, nil
}

func (s *stubAgentReader) GetAgentBySlug(ctx context.Context, slug string) (*store.AgentProfile, error) {
	if a, ok := s.slugIndex[slug]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("not found")
}

func (s *stubAgentReader) ListAgents(ctx context.Context) ([]store.AgentProfile, error) {
	out := make([]store.AgentProfile, 0, len(s.agents))
	for _, a := range s.agents {
		out = append(out, *a)
	}
	return out, nil
}

func (s *stubAgentReader) ListAgentsBySource(context.Context, string) ([]store.AgentProfile, error) {
	return nil, nil
}

func (s *stubAgentReader) GetSessionPrimaryAgent(ctx context.Context, sessionID string) (*store.SessionAgent, error) {
	if sa, ok := s.sessionBind[sessionID]; ok {
		return sa, nil
	}
	return nil, sql.ErrNoRows
}

func (s *stubAgentReader) ListSessionAgents(context.Context, string) ([]store.SessionAgent, error) {
	return nil, nil
}
func (s *stubAgentReader) ListAgentSkills(context.Context, string) ([]store.Skill, error) {
	return nil, nil
}
func (s *stubAgentReader) ListAgentProjects(context.Context, string) ([]store.Project, error) {
	return nil, nil
}
func (s *stubAgentReader) ListProjectAgents(context.Context, string) ([]store.AgentProfile, error) {
	return nil, nil
}

type stubAgentWriter struct {
	created []store.AgentProfile
	deleted []string
	ensured []string // sessionIDs that got EnsureSessionAgent
}

func (s *stubAgentWriter) CreateAgent(ctx context.Context, a *store.AgentProfile) error {
	s.created = append(s.created, *a)
	return nil
}
func (s *stubAgentWriter) UpdateAgent(context.Context, *store.AgentProfile) error { return nil }
func (s *stubAgentWriter) DeleteAgent(ctx context.Context, slug string) error {
	s.deleted = append(s.deleted, slug)
	return nil
}
func (s *stubAgentWriter) UpsertAgentBySlug(context.Context, *store.AgentProfile) error { return nil }
func (s *stubAgentWriter) EnsureSessionAgent(ctx context.Context, sid, _, _ string, _ bool) error {
	s.ensured = append(s.ensured, sid)
	return nil
}
func (s *stubAgentWriter) SetSessionAgentMode(context.Context, string, string, string) error {
	return nil
}
func (s *stubAgentWriter) DeleteSessionAgent(context.Context, string, string) error { return nil }
func (s *stubAgentWriter) AssignSkillToAgent(context.Context, string, string, string) error {
	return nil
}
func (s *stubAgentWriter) RemoveSkillFromAgent(context.Context, string, string) error { return nil }
func (s *stubAgentWriter) AddAgentProject(context.Context, string, string) error      { return nil }
func (s *stubAgentWriter) RemoveAgentProject(context.Context, string, string) error   { return nil }

type stubSettings struct {
	defaultAgent string
}

func (s *stubSettings) GetUserSettings(ctx context.Context) (*store.UserSettings, error) {
	return &store.UserSettings{DefaultAgent: s.defaultAgent}, nil
}
func (s *stubSettings) UpdateUserSettings(context.Context, *store.UserSettings) error { return nil }
func (s *stubSettings) GetPluginSettings(context.Context, string) (*store.PluginSettings, error) {
	return nil, nil
}
func (s *stubSettings) UpsertPluginSettings(context.Context, string, map[string]any) error {
	return nil
}
func (s *stubSettings) UpsertPluginSchema(context.Context, string, []store.ConfigField) error {
	return nil
}
func (s *stubSettings) ListPluginSettings(ctx context.Context) ([]*store.PluginSettings, error) {
	return nil, nil
}
func (s *stubSettings) UpdatePluginIcon(context.Context, string, string) error { return nil }
func (s *stubSettings) GetPluginSettingValue(context.Context, string, string) (string, error) {
	return "", nil
}

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
	agent := &store.AgentProfile{ID: "msg://agent/private-bound", Name: "Bound", Slug: "bound", Status: "active"}
	reader.addAgent(agent)
	reader.verifiedBindings[agent.ID] = true
	reader.sessionBind["sess-1"] = &store.SessionAgent{
		SessionID: "sess-1", AgentID: "msg://agent/private-bound", Mode: "code", IsPrimary: true,
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
	if got.ID != "msg://agent/private-bound" {
		t.Errorf("agent ID = %q, want %q", got.ID, "msg://agent/private-bound")
	}
	// Should NOT auto-assign since a binding already existed.
	if len(writer.ensured) != 0 {
		t.Errorf("expected no auto-assign, got %v", writer.ensured)
	}
}

func TestAgentService_ResolveForSession_SettingsDefault(t *testing.T) {
	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{ID: "historical-default", Name: "Historical", Slug: "default", Status: "active"})
	writer := &stubAgentWriter{}
	events := &fakeEventEmitter{}
	svc := NewAgentService(AgentServiceConfig{Agents: reader, Writers: writer, Settings: &stubSettings{defaultAgent: "historical-default"}, Events: events})
	for _, resolve := range []func(context.Context, string) (*store.AgentProfile, error){svc.ResolveForSession, svc.ResolveForSessionReadOnly} {
		got, err := resolve(t.Context(), "unbound-session")
		if got != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatal("implicit enrollment", got, err)
		}
	}
	if len(writer.ensured) != 0 || len(events.agentAssigned) != 0 {
		t.Fatal("missing binding wrote authority", writer.ensured, events.agentAssigned)
	}
}

func TestAgentService_ResolveForSessionReadOnly_NoAutoAssign(t *testing.T) {
	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{ID: "historical-default", Name: "Historical", Slug: "default", Status: "active"})
	writer := &stubAgentWriter{}
	events := &fakeEventEmitter{}
	svc := NewAgentService(AgentServiceConfig{Agents: reader, Writers: writer, Settings: &stubSettings{defaultAgent: "historical-default"}, Events: events})
	for _, resolve := range []func(context.Context, string) (*store.AgentProfile, error){svc.ResolveForSession, svc.ResolveForSessionReadOnly} {
		got, err := resolve(t.Context(), "unbound-session")
		if got != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatal("implicit enrollment", got, err)
		}
	}
	if len(writer.ensured) != 0 || len(events.agentAssigned) != 0 {
		t.Fatal("missing binding wrote authority", writer.ensured, events.agentAssigned)
	}
}

func TestAgentService_ResolveForSession_HardcodedFallback(t *testing.T) {
	reader := newStubReader()
	reader.addAgent(&store.AgentProfile{ID: "historical-default", Name: "Historical", Slug: "default", Status: "active"})
	writer := &stubAgentWriter{}
	events := &fakeEventEmitter{}
	svc := NewAgentService(AgentServiceConfig{Agents: reader, Writers: writer, Settings: &stubSettings{defaultAgent: "historical-default"}, Events: events})
	for _, resolve := range []func(context.Context, string) (*store.AgentProfile, error){svc.ResolveForSession, svc.ResolveForSessionReadOnly} {
		got, err := resolve(t.Context(), "unbound-session")
		if got != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatal("implicit enrollment", got, err)
		}
	}
	if len(writer.ensured) != 0 || len(events.agentAssigned) != 0 {
		t.Fatal("missing binding wrote authority", writer.ensured, events.agentAssigned)
	}
}

func TestAgentService_ResolveForSession_DisabledAgent(t *testing.T) {
	reader := newStubReader()
	agent := &store.AgentProfile{ID: "msg://agent/private-disabled", Name: "Disabled", Slug: "off", Status: "disabled"}
	reader.addAgent(agent)
	reader.verifiedBindings[agent.ID] = true
	reader.sessionBind["sess-d"] = &store.SessionAgent{
		SessionID: "sess-d", AgentID: "msg://agent/private-disabled", Mode: "default", IsPrimary: true,
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
