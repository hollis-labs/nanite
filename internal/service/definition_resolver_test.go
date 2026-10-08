package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func TestFileDefinitionResolverPinsSourceAndRejectsCollisions(t *testing.T) {
	dir := t.TempDir()
	source := strings.ReplaceAll(string(embeddedChatDefinition), "def:nanite-default", "def:file-test")
	path := filepath.Join(dir, "test.md")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := agentdef.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := agentdef.Digest(d)
	pin := DefinitionRef{d.DefinitionID, d.Revision, digest}
	resolver, err := NewFileDefinitionResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := resolver.Resolve(t.Context(), pin)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := MapChatDefinition(verified)
	if err != nil || !strings.Contains(cfg.Instructions, "Respect") && !strings.Contains(cfg.Instructions, "respect host") {
		t.Fatal(cfg, err)
	}
	if DefinitionRefFromMesh(pin.MeshRef()) != pin {
		t.Fatal("pin spelling mapping lost fields")
	}
	if err := os.WriteFile(path, []byte(source+"\nChanged instructions.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(t.Context(), pin); !errors.Is(err, ErrDefinitionDigestMismatch) {
		t.Fatal("changed source satisfied old pin", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "collision.md"), embeddedChatDefinition, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileDefinitionResolver(dir); err == nil {
		t.Fatal("embedded revision overwritten")
	}
}
func TestChatDefinitionUnsupportedSemantics(t *testing.T) {
	base, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*agentdef.Definition){
		func(d *agentdef.Definition) { d.Behavior.Hooks = []string{"before-turn"} },
		func(d *agentdef.Definition) { d.Requirements.Tools = []string{"write-anything"} },
		func(d *agentdef.Definition) { d.Continuity.Mode = agentdef.Durable },
		func(d *agentdef.Definition) { d.HarnessProfile.Permissions.Profile = "yolo" },
		func(d *agentdef.Definition) {
			d.Extensions = map[string]agentdef.Extension{"com.hollislabs.nanite/native-policy": {Version: "1", Area: "harness_profile", Mandatory: true, Data: map[string]any{"permission_mode": "yolo"}}}
		},
	}
	for _, mutate := range mutations {
		d := *base.Definition
		mutate(&d)
		if _, mapErr := MapChatDefinition(VerifiedDefinition{base.Ref, &d}); !errors.Is(mapErr, ErrUnsupportedDefinition) {
			t.Fatal("unsupported semantics applied", mapErr)
		}
	}
	d := *base.Definition
	d.HarnessProfile.Permissions.Profile = "read-only"
	d.Extensions = map[string]agentdef.Extension{"example.org/optional": {Version: "1", Area: "behavior", Data: map[string]any{"model": "unauthorized", "grant": "write"}}}
	cfg, err := MapChatDefinition(VerifiedDefinition{base.Ref, &d})
	if err != nil || cfg.Model != (ModelSelection{}) || cfg.PermissionProfile != "read-only" {
		t.Fatal(cfg, err)
	}
}
func TestDefinedViewPinAndConfigStaySeparateFromMetadata(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	base, _ := EmbeddedDefinition()
	calls := 0
	v := &CognitiveViews{Store: f.st, Resolver: &FileDefinitionResolver{}, Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		calls++
		return ModelSelection{"characterization", "characterization-model"}, nil
	})}
	wrong := base.Ref
	wrong.SemanticDigest = "sha256:" + strings.Repeat("a", 64)
	if _, err := v.Create(t.Context(), CreateDefinedView{DefinitionRef: wrong}); !errors.Is(err, ErrDefinitionDigestMismatch) || calls != 0 {
		t.Fatal("model authorized before verified pin", calls, err)
	}
	view, err := v.Create(t.Context(), CreateDefinedView{DefinitionRef: base.Ref, Metadata: `{"definition_ref":"forged","permission_profile":"yolo"}`})
	if err != nil {
		t.Fatal(err)
	}
	ref, cfg, err := v.Get(t.Context(), view.ID)
	if err != nil || ref != base.Ref || cfg.PermissionProfile != "default" {
		t.Fatal(ref, cfg, err)
	}
	record, err := f.st.GetCognitiveView(t.Context(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	var persisted ChatDefinitionConfig
	if err = json.Unmarshal([]byte(record.ChatConfigJSON), &persisted); err != nil {
		t.Fatal(err)
	}
	messages, err := f.st.ListMessages(t.Context(), view.ID, 100)
	if err != nil || len(messages) != 0 {
		t.Fatal(messages, err)
	}
	// A persisted snapshot survives event-log expiry and process replacement.
	turn := "persisted-turn"
	user := &store.Message{ID: "persisted-user", SessionID: view.ID, Content: "question"}
	initial, _ := json.Marshal(CognitiveTurnSnapshot{SessionViewID: view.ID, TurnID: turn, RunID: turn, OutputMessageID: turn, State: "submitted"})
	if err = f.st.CreateCognitiveTurn(t.Context(), user, turn, string(initial)); err != nil {
		t.Fatal(err)
	}
	restored, err := NewCognitiveTurns(f.st).Get(view.ID, turn)
	if err != nil || restored.State != "failed" || restored.Message.Error.Code != "process_lost" {
		t.Fatal(restored, err)
	}
	again, err := NewCognitiveTurns(f.st).Get(view.ID, turn)
	if err != nil || again.Revision != restored.Revision {
		t.Fatal("restart failure did not persist", again, err)
	}
}

func TestNativeDefinitionPermissionPostureNarrowsHostYolo(t *testing.T) {
	tool := llmtypes.ToolUseBlock{ID: "call-write", Name: "write", Input: map[string]any{"value": "x"}}
	f := newHandleMessageFixture(t, []characterizationProviderStep{{events: toolTurnEvents(tool)}, {events: doneEvents("blocked")}})
	f.tools.definitions = []llmtypes.ToolDefinition{{Name: tool.Name, Description: "fixture write"}}
	f.svc.tools = &writePermissionFixture{ToolService: f.tools}
	f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeYolo, nil)
	f.svc.cognitiveApprovals = NewCognitiveApprovals(f.svc.permissions)
	base, _ := EmbeddedDefinition()
	d := *base.Definition
	d.HarnessProfile.Permissions.Profile = "read-only"
	digest, err := agentdef.Digest(&d)
	if err != nil {
		t.Fatal(err)
	}
	base.Ref.SemanticDigest = digest
	v := &CognitiveViews{Store: f.st, Resolver: definitionResolverFunc(func(context.Context, DefinitionRef) (VerifiedDefinition, error) {
		return VerifiedDefinition{base.Ref, &d}, nil
	}), Models: ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		return ModelSelection{"characterization", "characterization-model"}, nil
	})}
	view, err := v.Create(t.Context(), CreateDefinedView{DefinitionRef: base.Ref, Metadata: `{"permission_mode":"yolo","provider":"pty-claude","grant":"write"}`})
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.svc.SubmitCognitiveTurn(t.Context(), view.ID, "write it")
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := f.svc.streams.GetStream(id)
	events := drainStream(stream)
	if calls := f.tools.calls(); len(calls) != 0 {
		t.Fatal("definition/metadata widened host policy", calls)
	}
	result := findEvent(events, "tool_result")
	if result == nil || !result.IsError || !strings.Contains(result.Summary, "PERMISSION DENIED") {
		t.Fatal(eventTypes(events))
	}
	// No global mutation: the same host engine still grants the operator mode.
	if decision := f.svc.permissions.Check(t.Context(), view.ID, tool.Name, tool.Input, permissionlib.ToolMeta{}); decision.Decision != permissionlib.DecisionAllow {
		t.Fatal(decision)
	}
}

type definitionResolverFunc func(context.Context, DefinitionRef) (VerifiedDefinition, error)

func (f definitionResolverFunc) Resolve(ctx context.Context, p DefinitionRef) (VerifiedDefinition, error) {
	return f(ctx, p)
}

type writePermissionFixture struct{ ToolService }

func (f *writePermissionFixture) GetToolMeta(context.Context, string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{IsReadOnly: false, IsDestructive: true}, true
}
