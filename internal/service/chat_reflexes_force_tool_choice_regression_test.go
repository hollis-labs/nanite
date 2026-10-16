package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// Neither priority nor definition metadata can issue a tool capability. An
// unrepresentable tool-choice bundle refuses before view/transcript creation.
func TestPinnedCompetingToolChoicesCannotClaimAuthority(t *testing.T) {
	for _, priority := range []string{"10", "90"} {
		t.Run(priority, func(t *testing.T) {
			st := newTestStore(t)
			ctx := t.Context()
			body := []byte(`{"version":"1","rules":[{"id":"one","name":"Choice one","priority":10,"trigger":{"kind":"event","spec":{"name":"probe"}},"action":{"kind":"force_tool_choice","tool_name":"tool_one"}},{"id":"two","name":"Choice two","priority":` + priority + `,"trigger":{"kind":"event","spec":{"name":"probe"}},"action":{"kind":"force_tool_choice","tool_name":"tool_two"}}]}`)
			source := strings.Replace(string(embeddedChatDefinition), "def:nanite-default", "def:unsupported-choice", 1)
			ext := "extensions:\n  " + agentpolicy.ReflexNamespace + ":\n    version: \"1\"\n    area: behavior\n    mandatory: true\n    data:\n      bundle:\n        uri: resource:choice\n        digest: " + agentdef.ArtifactDigest(body) + "\n"
			source = strings.Replace(source, "continuity:\n", ext+"continuity:\n", 1)
			pin, err := st.InstallAgentDefinition(ctx, []byte(source), []store.DefinitionResource{{URI: "resource:choice", Content: body}})
			if err != nil {
				t.Fatal(err)
			}
			modelCalls := 0
			views := &CognitiveViews{Store: st, Resolver: &StoredDefinitionResolver{Store: st}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
				modelCalls++
				return ModelSelection{"private-provider", "private-model"}, nil
			})}
			if view, operationErr := views.Create(ctx, CreateDefinedView{DefinitionRef: DefinitionRefFromMesh(pin), Metadata: `{"grant":"all","tool_name":"tool_one"}`}); view != nil || !errors.Is(operationErr, store.ErrVerifiedActorRequired) {
				t.Fatal(view, operationErr)
			}
			sessions, err := st.ListSessions(ctx, false)
			if err != nil || len(sessions) != 0 || modelCalls != 0 {
				t.Fatal("unsupported semantics reached admission", sessions, modelCalls, err)
			}
		})
	}
}
