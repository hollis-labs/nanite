// TASKS/reflex-taxonomy/04-halt-turn-synchronicity.md — proves both halves
// of "halt must actually halt": (1) no LLM provider call happens for the
// turn that fires a halt_session reflex, and (2) the session row is
// stamped halted_at/halted_reason by that SAME turn, not just discovered
// later by frontend_readiness.go on a subsequent request.
//
// reminder_engine_integration_test.go documents this package's usual
// tradeoff for generateResponse coverage: it's a large function that
// needs a live LLM provider round-trip, so most tests here exercise the
// narrower helper generateResponse delegates to instead of the whole
// function. That tradeoff doesn't fit this task: the fix under test
// lives in generateResponse's OWN control flow (the reflex call site
// aborting before the LLM call), not in a helper it delegates to, so a
// test of a hand-reimplemented version of that control flow would not
// actually prove the fix. This test invokes generateResponse itself, up
// to and past the abort point, wiring every dependency it touches before
// that point to something real (a real *store.Store with real
// migrations, the real reflexes.Engine/Executor/HaltHook pattern
// container.go wires) or to a minimal fake for interfaces this test has
// no other reason to exercise (SessionService, AgentService,
// ToolService) — mirroring the wiring style chat_path_grants_e2e_test.go
// and chat_reflex_dispatch_integration_test.go already use in this
// package. The llmcontracts.Provider fake's call count is the
// load-bearing assertion for half (1).
package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// haltTestSessions is a minimal SessionService fake. generateResponse only
// calls Get before this task's abort point; every other method panics if
// ever reached so a future change that starts depending on one fails this
// test loudly instead of silently returning a zero value.
type haltTestSessions struct{ st *store.Store }

func (f *haltTestSessions) Create(context.Context, CreateSessionOpts) (*store.Session, error) {
	panic("haltTestSessions: Create not implemented")
}
func (f *haltTestSessions) Get(_ context.Context, id string) (*store.Session, error) {
	return f.st.GetSession(context.Background(), id)
}
func (f *haltTestSessions) List(context.Context, bool) ([]store.Session, error) {
	panic("haltTestSessions: List not implemented")
}
func (f *haltTestSessions) Update(context.Context, *store.Session) error {
	panic("haltTestSessions: Update not implemented")
}
func (f *haltTestSessions) UpdateMetadata(context.Context, string, string) error {
	panic("haltTestSessions: UpdateMetadata not implemented")
}
func (f *haltTestSessions) Archive(context.Context, string) error {
	panic("haltTestSessions: Archive not implemented")
}
func (f *haltTestSessions) Fork(context.Context, string, ForkOpts) (*store.Session, error) {
	panic("haltTestSessions: Fork not implemented")
}
func (f *haltTestSessions) ListMessages(context.Context, string, int) ([]store.Message, error) {
	panic("haltTestSessions: ListMessages not implemented")
}
func (f *haltTestSessions) Search(context.Context, string, SearchOpts) ([]store.SearchResult, error) {
	panic("haltTestSessions: Search not implemented")
}
func (f *haltTestSessions) ListMessagesPage(context.Context, string, int, int) (*store.MessagePage, error) {
	panic("haltTestSessions: ListMessagesPage not implemented")
}
func (f *haltTestSessions) ListMessagesAround(context.Context, string, string, int, int) (*store.MessagePage, error) {
	panic("haltTestSessions: ListMessagesAround not implemented")
}
func (f *haltTestSessions) DetectInterruptedTurn(context.Context, *store.Session, bool) map[string]any {
	panic("haltTestSessions: DetectInterruptedTurn not implemented")
}
func (*haltTestSessions) GetMessage(context.Context, string) (*store.Message, error) {
	return nil, nil
}

func (*haltTestSessions) CreateMessage(context.Context, *store.Message) error {
	return nil
}

func (*haltTestSessions) EnvelopeLookup(context.Context, []store.Message) map[string]*store.EnvelopeInstance {
	return nil
}

func (f *haltTestSessions) ListPendingEnvelopes(context.Context, string) ([]chat.Envelope, error) {
	panic("haltTestSessions: ListPendingEnvelopes not implemented")
}

// haltTestAgents is a minimal AgentService fake. generateResponse only
// calls ResolveForSession before this task's abort point.
type haltTestAgents struct{ agent *store.AgentProfile }

func (f *haltTestAgents) Get(context.Context, string) (*store.AgentProfile, error) {
	panic("haltTestAgents: Get not implemented")
}
func (f *haltTestAgents) GetBySlug(context.Context, string) (*store.AgentProfile, error) {
	panic("haltTestAgents: GetBySlug not implemented")
}
func (f *haltTestAgents) List(context.Context) ([]store.AgentProfile, error) {
	panic("haltTestAgents: List not implemented")
}
func (f *haltTestAgents) Create(context.Context, *store.AgentProfile) error {
	panic("haltTestAgents: Create not implemented")
}
func (f *haltTestAgents) Update(context.Context, *store.AgentProfile) error {
	panic("haltTestAgents: Update not implemented")
}
func (f *haltTestAgents) Delete(context.Context, string) error {
	panic("haltTestAgents: Delete not implemented")
}
func (f *haltTestAgents) ResolveForSession(context.Context, string) (*store.AgentProfile, error) {
	return f.agent, nil
}
func (f *haltTestAgents) ResolveForSessionReadOnly(context.Context, string) (*store.AgentProfile, error) {
	return f.agent, nil
}

// haltTestTools is a minimal ToolService fake. generateResponse only calls
// SelectForAgent before this task's abort point; an empty selection keeps
// the pre-abort "no tools" warning path harmless for this test.
type haltTestTools struct{}

func (haltTestTools) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return &ToolSelection{}, nil
}
func (haltTestTools) Execute(context.Context, string, string, map[string]any) (*ToolResult, error) {
	panic("haltTestTools: Execute not implemented")
}
func (haltTestTools) HandleRequestTools(context.Context, string, map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	panic("haltTestTools: HandleRequestTools not implemented")
}
func (haltTestTools) ListSummaries() []toolclient.ToolSummary { return nil }
func (haltTestTools) GetToolMeta(context.Context, string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, false
}
func (haltTestTools) GetToolSchema(string) map[string]any { return nil }

// The synchronous halt and non-halt control are exercised from verified,
// digest-pinned content. No retained profile or mutable class rule is consulted.
func TestGenerateResponse_PinnedReflexHaltAndReminder(t *testing.T) {
	for _, halt := range []bool{true, false} {
		t.Run(map[bool]string{true: "halt", false: "reminder"}[halt], func(t *testing.T) {
			ctx := context.WithValue(t.Context(), cognitiveTurnContextKey{}, true)
			st := newTestStore(t)
			action := `{"kind":"inject_reminder","body":"Pinned reminder"}`
			if halt {
				action = `{"kind":"halt_session","reason":"Pinned synchronous halt"}`
			}
			body := []byte(`{"version":"1","rules":[{"id":"owned-rule","name":"Owned rule","priority":1,"trigger":{"kind":"predicate","spec":{"kind":"mail_unread_count","op":">=","value":0}},"action":` + action + `}]}`)
			source := strings.Replace(string(embeddedChatDefinition), "def:nanite-default", "def:pinned-synchronous", 1)
			extension := "extensions:\n  " + agentpolicy.ReflexNamespace + ":\n    version: \"1\"\n    area: behavior\n    mandatory: true\n    data:\n      bundle:\n        uri: resource:owned\n        digest: " + agentdef.ArtifactDigest(body) + "\n"
			source = strings.Replace(source, "continuity:\n", extension+"continuity:\n", 1)
			pin, err := st.InstallAgentDefinition(ctx, []byte(source), []store.DefinitionResource{{URI: "resource:owned", Content: body}})
			if err != nil {
				t.Fatal(err)
			}
			views := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
				return ModelSelection{"mock-provider", "mock-model"}, nil
			})}
			view, err := views.Create(ctx, CreateDefinedView{DefinitionRef: DefinitionRefFromMesh(pin)})
			if err != nil {
				t.Fatal(err)
			}
			prov := &mockStreamProvider{events: doneEvents("Pinned answer")}
			registry := provider.NewRegistry()
			registry.Register("mock-provider", prov)
			svc := &chatServiceImpl{sessions: &haltTestSessions{st: st}, store: st, providers: registry, streams: NewStreamManager(), context: NewContextService(ContextServiceConfig{Client: chat.NewContextClient(st)})}
			out := make(chan chat.StreamEvent, 128)
			svc.generateResponse(ctx, view.ID, "pinned-synchronous-output", "private input", out)
			events := drainStream(out)
			state, err := st.GetSessionHalt(ctx, view.ID)
			if err != nil {
				t.Fatal(err)
			}
			if halt {
				if prov.callCount != 0 || !state.IsHalted() || state.HaltedReason == nil || *state.HaltedReason != "Pinned synchronous halt" || findEvent(events, "error") == nil {
					t.Fatalf("halt did not stop synchronously: calls=%d state=%+v events=%v", prov.callCount, state, eventTypes(events))
				}
			} else {
				if prov.callCount != 1 || state.IsHalted() || findEvent(events, "delta") == nil {
					t.Fatalf("reminder aborted turn: calls=%d state=%+v events=%v", prov.callCount, state, eventTypes(events))
				}
			}
		})
	}
}
