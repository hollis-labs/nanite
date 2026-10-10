package service

import (
	"context"
	"errors"
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

func TestSessionCreateActorClaimsRefuseBeforeEffects(t *testing.T) {
	ctx := t.Context()
	svc, events, _ := newSessionCreateTestService(t)
	for _, id := range []string{"agent-x", "host-settings-uuid", "msg://agent/claimed"} {
		created, err := svc.Create(ctx, CreateSessionOpts{AgentID: id, Provider: "fixture", Model: "model", SkipAgentBinding: true})
		if created != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("claim admitted: %+v %v", created, err)
		}
	}
	rows, err := svc.List(ctx, true)
	if err != nil || len(rows) != 0 || events.starts != 0 {
		t.Fatalf("refused actor creation effects: %+v %v starts=%d", rows, err, events.starts)
	}
}

func TestSessionCreateDefaultUsesActualPinnedDefinitionWithoutEnrollment(t *testing.T) {
	ctx := t.Context()
	st := newConfigTestStore(t)
	if err := installEmbeddedChatDefinition(ctx, st); err != nil {
		t.Fatal(err)
	}
	verified, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	native := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}, DefaultDefinitionRef: verified.Ref, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		return ModelSelection{Provider: "private-model-authorizer", Model: "private-model"}, nil
	})}
	svc := NewSessionService(SessionServiceDeps{Native: native, Sessions: st, Writer: st, Agents: st, AgentReader: st})
	created, err := svc.Create(ctx, CreateSessionOpts{Metadata: `{"caller_presentation":"test"}`})
	if err != nil {
		t.Fatal(err)
	}
	view, err := st.GetCognitiveView(ctx, created.ID)
	if err != nil || view.DefinitionRefJSON == "" {
		t.Fatalf("pin not persisted: %+v %v", view, err)
	}
	bindings, err := st.ListSessionAgents(ctx, created.ID)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("native view minted actor: %+v %v", bindings, err)
	}
	if created.Provider != "private-model-authorizer" || created.Model != "private-model" || created.Metadata != `{"caller_presentation":"test"}` {
		t.Fatalf("host/presentation mapping: %+v", created)
	}
}

func TestSessionCreateReturnsRuntimeErrorUnwrapped(t *testing.T) {
	svc, _, _ := newSessionCreateTestService(t)

	_, err := svc.Create(context.Background(), CreateSessionOpts{SubagentRuntime: "bogus", SkipAgentBinding: true})
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
	if withID != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatal("skip flag admitted actor claim", withID, err)
	}
	for _, id := range []string{sess.ID} {
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
