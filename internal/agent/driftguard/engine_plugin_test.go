package driftguard

import (
	"context"
	"testing"

	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestEnginePluginHooksAndActionFilter(t *testing.T) {
	st := newReflexTestStore(t)
	if err := st.CreateAgent(&store.AgentProfile{
		ID:           "agent-a",
		Name:         "Agent A",
		Slug:         "agent-a",
		Class:        "advisor",
		SystemPrompt: "test",
		Source:       "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	id, err := st.InsertAgentReflex(context.Background(), store.AgentReflex{
		AgentID:     "agent-a",
		Name:        "test-reflex",
		TriggerKind: store.ReflexTriggerEvent,
		TriggerSpec: `{"name":"mail_received"}`,
		ActionKind:  store.ReflexActionInjectReminder,
		ActionSpec:  `{"body":"original"}`,
		Status:      store.ReflexStatusActive,
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex: %v", err)
	}

	hooks := &fakeReflexPluginHooks{}
	engine := NewEngine(st, nil)
	engine.SetPluginHooks(hooks)

	out, err := engine.EvaluateState(context.Background(), "agent-a", "advisor", State{
		SessionID:       "sess-1",
		AgentID:         "agent-a",
		AgentClass:      "advisor",
		MailUnreadCount: 1,
		TickN:           1,
	})
	if err != nil {
		t.Fatalf("EvaluateState: %v", err)
	}
	if len(out.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(out.Actions))
	}
	if out.Actions[0].Spec["body"] != "filtered" {
		t.Fatalf("body = %v, want filtered", out.Actions[0].Spec["body"])
	}
	if out.Actions[0].ReflexID != id {
		t.Fatalf("ReflexID = %q, want %q", out.Actions[0].ReflexID, id)
	}
	if hooks.stateFilters != 1 || hooks.actionFilters != 1 || hooks.fired != 1 || hooks.staged != 1 {
		t.Fatalf("hooks state=%d action=%d fired=%d staged=%d, want 1 each",
			hooks.stateFilters, hooks.actionFilters, hooks.fired, hooks.staged)
	}
}

type fakeReflexPluginHooks struct {
	stateFilters  int
	actionFilters int
	fired         int
	staged        int
}

func (f *fakeReflexPluginHooks) ApplyFilter(name string, data interface{}, ctx pluginpkg.FilterContext) (interface{}, error) {
	switch name {
	case pluginpkg.FilterReflexState:
		f.stateFilters++
	case pluginpkg.FilterReflexAction:
		f.actionFilters++
		action := data.(AppliedAction)
		action.Spec["body"] = "filtered"
		return action, nil
	}
	return data, nil
}

func (f *fakeReflexPluginHooks) EmitReflexFired(sessionID string, data map[string]any) {
	f.fired++
}

func (f *fakeReflexPluginHooks) EmitReflexActionStaged(sessionID string, data map[string]any) {
	f.staged++
}
