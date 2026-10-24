package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// An API session is held to the host revision it was admitted under, as a CLI
// session is: after the host moves, the next turn fails with a stated cause and
// the provider is not called (CW-20261024-0010).
func TestAPITurnFailsAfterTheHostRevisionChanges(t *testing.T) {
	ctx := context.WithValue(t.Context(), cognitiveTurnContextKey{}, true)
	st := newTestStore(t)
	pin, err := st.InstallAgentDefinition(ctx, []byte(strings.Replace(string(embeddedChatDefinition), "def:nanite-default", "def:api-pin", 1)), nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: "api-pin", Title: "api-pin", Source: "test", DefinitionRef: pin, Enabled: true,
		Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "mock-provider", Model: "mock-model", Directories: []string{canonicalTempDir(t)}}})
	if err != nil {
		t.Fatal(err)
	}
	views := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		return ModelSelection{"mock-provider", "mock-model"}, nil
	})}
	view, err := views.Create(ctx, CreateDefinedView{DefinitionRef: DefinitionRefFromMesh(pin), HostSettings: &HostSettingsRef{ID: host.ID, Revision: host.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	prov := &mockStreamProvider{events: doneEvents("Answer")}
	registry := provider.NewRegistry()
	registry.Register("mock-provider", prov)
	svc := &chatServiceImpl{sessions: &haltTestSessions{st: st}, store: st, providers: registry, streams: NewStreamManager(), context: NewContextService(ContextServiceConfig{Client: chat.NewContextClient(st)})}
	turn := func(id string) []chat.StreamEvent {
		out := make(chan chat.StreamEvent, 128)
		svc.generateResponse(ctx, view.ID, id, "hello", out)
		return drainStream(out)
	}
	if events := turn("first"); prov.callCount != 1 || findEvent(events, "delta") == nil {
		t.Fatalf("turn under the admitted revision: calls=%d events=%v", prov.callCount, eventTypes(events))
	}
	changed := host
	changed.Settings.Model = "other-model"
	if _, err = st.UpdateAgentHostSettings(ctx, changed, host.Revision); err != nil {
		t.Fatal(err)
	}
	events := turn("second")
	errEvent := findEvent(events, "error")
	if prov.callCount != 1 || errEvent == nil || errEvent.StructuredError == nil || errEvent.StructuredError.Details["cause"] != CauseHostRevisionChanged {
		t.Fatalf("turn after the host moved: calls=%d events=%v error=%+v", prov.callCount, eventTypes(events), errEvent)
	}
}
