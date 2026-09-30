package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// sessionStartCounter counts EmitSessionStart calls; every other event is
// the fakeEventEmitter no-op.
type sessionStartCounter struct {
	fakeEventEmitter
	starts int
}

func (c *sessionStartCounter) EmitSessionStart(context.Context, string, string, string, string) {
	c.starts++
}

func newSessionCreateTestService(t *testing.T) (SessionService, *sessionStartCounter, func(string) (string, error)) {
	t.Helper()
	st := newConfigTestStore(t)
	events := &sessionStartCounter{}
	svc := NewSessionService(SessionServiceDeps{
		Sessions:    st,
		Writer:      st,
		Agents:      st,
		AgentReader: st,
		Settings:    st,
		Events:      events,
		Runtime:     st,
		EventLog:    st,
		Envelopes:   st,
	})
	runtimeOf := func(id string) (string, error) {
		return st.GetSessionSubagentRuntime(context.Background(), id)
	}
	return svc, events, runtimeOf
}

func TestSessionCreateSetsRuntimeMetadataAndPrimaryWithoutEvents(t *testing.T) {
	ctx := context.Background()
	svc, events, runtimeOf := newSessionCreateTestService(t)

	sess, err := svc.Create(ctx, CreateSessionOpts{
		Provider:        "anthropic",
		Model:           "m",
		AgentID:         "agent-x",
		Metadata:        `{"harness_profile":"lean"}`,
		SubagentRuntime: "cli",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Metadata != `{"harness_profile":"lean"}` {
		t.Fatalf("metadata = %q, want the harness selection", got.Metadata)
	}
	rt, err := runtimeOf(sess.ID)
	if err != nil || rt != "cli" {
		t.Fatalf("subagent runtime = %q, %v; want cli", rt, err)
	}

	st := svc.(*sessionServiceImpl).agentReader
	primary, err := st.GetSessionPrimaryAgent(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSessionPrimaryAgent: %v", err)
	}
	if primary.AgentID != "agent-x" {
		t.Fatalf("primary agent = %q, want agent-x", primary.AgentID)
	}

	// The HTTP handler emits activity session-created itself; Create must
	// not add a session-start on top of it.
	if events.starts != 0 {
		t.Fatalf("Create emitted %d session-start events, want 0", events.starts)
	}
}

func TestSessionCreateReturnsRuntimeErrorUnwrapped(t *testing.T) {
	svc, _, _ := newSessionCreateTestService(t)

	_, err := svc.Create(context.Background(), CreateSessionOpts{SubagentRuntime: "bogus"})
	if err == nil {
		t.Fatal("Create with an invalid runtime succeeded")
	}
	// The handler writes err.Error() as the 500 body, so the store's own
	// message must arrive without a service prefix.
	if !strings.HasPrefix(err.Error(), "set session subagent runtime:") {
		t.Fatalf("error = %q, want the store's message unwrapped", err)
	}
}

// resolutionCounter wraps the store's settings and agent reads so a test can
// assert Create never consulted them.
type resolutionCounter struct {
	SettingsStore
	AgentReader
	settingsReads int
	slugReads     int
}

func (c *resolutionCounter) GetUserSettings(ctx context.Context) (*store.UserSettings, error) {
	c.settingsReads++
	return c.SettingsStore.GetUserSettings(ctx)
}

func (c *resolutionCounter) GetAgentBySlug(ctx context.Context, slug string) (*store.AgentProfile, error) {
	c.slugReads++
	return c.AgentReader.GetAgentBySlug(ctx, slug)
}

func TestSessionCreateSkipAgentBindingResolvesAndBindsNothing(t *testing.T) {
	ctx := context.Background()
	st := newConfigTestStore(t)
	counter := &resolutionCounter{SettingsStore: st, AgentReader: st}
	svc := NewSessionService(SessionServiceDeps{
		Sessions:    st,
		Writer:      st,
		Agents:      st,
		AgentReader: counter,
		Settings:    counter,
		Runtime:     st,
	})

	// With no AgentID, Create would normally walk settings -> "default" slug.
	sess, err := svc.Create(ctx, CreateSessionOpts{
		SubagentRuntime:  "api",
		SkipAgentBinding: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if counter.settingsReads != 0 || counter.slugReads != 0 {
		t.Fatalf("Create resolved an agent (settings reads %d, slug reads %d); want none", counter.settingsReads, counter.slugReads)
	}
	// With an AgentID, Create would normally bind it; the flag ignores it.
	withID, err := svc.Create(ctx, CreateSessionOpts{AgentID: "agent-x", SkipAgentBinding: true})
	if err != nil {
		t.Fatalf("Create with AgentID: %v", err)
	}
	for _, id := range []string{sess.ID, withID.ID} {
		bound, err := st.ListSessionAgents(ctx, id)
		if err != nil {
			t.Fatalf("ListSessionAgents: %v", err)
		}
		if len(bound) != 0 {
			t.Fatalf("session %s agents = %v, want none", id, bound)
		}
	}
	// The runtime write still happens.
	if rt, err := st.GetSessionSubagentRuntime(ctx, sess.ID); err != nil || rt != "api" {
		t.Fatalf("subagent runtime = %q, %v; want api", rt, err)
	}
}
