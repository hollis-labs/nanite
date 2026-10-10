package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func TestStoredDefinitionCatalogResolvesPristineArtifacts(t *testing.T) {
	st, err := storetest.New(t, t.Context(), filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err = installEmbeddedChatDefinition(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	catalog, err := st.ListAgentDefinitionArtifacts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r := &StoredDefinitionResolver{Store: st}
	for _, artifact := range catalog {
		resolved, resolveErr := r.Resolve(t.Context(), DefinitionRefFromMesh(artifact.Ref))
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		cfg, mappingErr := mapChatDefinition(t.Context(), resolved)
		if mappingErr != nil {
			t.Fatalf("%s: %v", artifact.Ref.ID, mappingErr)
		}
		if strings.TrimSpace(cfg.Instructions) == "" {
			t.Fatal("empty applied instructions")
		}
	}
	base, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Resolve(t.Context(), base.Ref); err != nil {
		t.Fatalf("archived exact general-chat pin: %v", err)
	}
	rows, err := st.ListAgents(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("artifact installation created host profiles: %+v %v", rows, err)
	}
}

func TestPinnedDefinitionReflexStopsProviderWithoutBorrowedProfile(t *testing.T) {
	ctx := context.WithValue(t.Context(), cognitiveTurnContextKey{}, true)
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "pinned-halt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	body := []byte(`{"version":"1","rules":[{"id":"stop","name":"Stop requested","priority":1,"trigger":{"kind":"predicate","spec":{"kind":"mail_unread_count","op":">=","value":0}},"action":{"kind":"halt_session","reason":"Pinned stop policy"}}]}`)
	source := strings.Replace(string(embeddedChatDefinition), "def:nanite-default", "def:pinned-halt", 1)
	extension := "extensions:\n  " + agentpolicy.ReflexNamespace + ":\n    version: \"1\"\n    area: behavior\n    mandatory: true\n    data:\n      bundle:\n        uri: resource:stop\n        digest: " + agentdef.ArtifactDigest(body) + "\n"
	source = strings.Replace(source, "continuity:\n", extension+"continuity:\n", 1)
	pin, err := st.InstallAgentDefinition(ctx, []byte(source), []store.DefinitionResource{{URI: "resource:stop", Content: body}})
	if err != nil {
		t.Fatal(err)
	}
	views := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		return ModelSelection{"mock-provider", "mock-model"}, nil
	})}
	view, err := views.Create(ctx, CreateDefinedView{DefinitionRef: DefinitionRefFromMesh(pin), Metadata: `{"agent_id":"old-profile","grant":"all"}`})
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockStreamProvider{events: []llmtypes.StreamEvent{{Type: "done"}}}
	registry := provider.NewRegistry()
	registry.Register("mock-provider", mock)
	svc := &chatServiceImpl{sessions: &haltTestSessions{st: st}, store: st, providers: registry, streams: NewStreamManager(), context: NewContextService(ContextServiceConfig{Client: chat.NewContextClient(st)})}
	// No AgentService or ToolService is supplied: borrowing the old default or
	// selecting its grants would panic before the meaningful halt assertions.
	ch := make(chan chat.StreamEvent, 64)
	svc.generateResponse(ctx, view.ID, "pinned-output", "Stop here", ch)
	var failure bool
	for e := range ch {
		if e.Type == "error" {
			failure = true
		}
	}
	halt, err := st.GetSessionHalt(ctx, view.ID)
	if err != nil || !halt.IsHalted() {
		t.Fatalf("pinned handler did not halt: %+v %v", halt, err)
	}
	if mock.callCount != 0 || !failure {
		t.Fatalf("provider dispatched after pinned halt: calls=%d failure=%v", mock.callCount, failure)
	}
	if _, err = st.GetSessionPrimaryAgent(ctx, view.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("definition minted/bound actor: %v", err)
	}
	record, err := st.GetCognitiveView(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cfg ChatDefinitionConfig
	if json.Unmarshal([]byte(record.ChatConfigJSON), &cfg) != nil || cfg.ReflexBundle == nil {
		t.Fatal("bundle not applied from verified pin")
	}
}

func TestPinnedDefinitionReflexCollectionFailureAndCancellationRefuse(t *testing.T) {
	st, err := storetest.New(t, t.Context(), filepath.Join(t.TempDir(), "reflex-error.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	session := &store.Session{ID: "uncreated-private-view"}
	bundle := &agentpolicy.ReflexBundle{Rules: []agentpolicy.ReflexRule{}}
	agent := &store.AgentProfile{Class: "advisor", DefinitionReflex: bundle}
	svc := &chatServiceImpl{store: st}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	actions, err := svc.evaluateDefinitionReflexes(ctx, session, agent, nil)
	if !errors.Is(err, context.Canceled) || len(actions) != 0 {
		t.Fatalf("canceled policy evaluation: %+v %v", actions, err)
	}
	if err = st.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	actions, err = svc.evaluateDefinitionReflexes(t.Context(), session, agent, nil)
	if err == nil || len(actions) != 0 {
		t.Fatalf("failed collector silently admitted behavior: %+v %v", actions, err)
	}
}
