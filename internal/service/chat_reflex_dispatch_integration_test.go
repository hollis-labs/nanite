// Classification remains available; historical dispatch rules remain inert.
package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/toolclient"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// recordingReflexDispatchToolService is a minimal ToolService fake that
// records the args of every Execute call. Only Execute is exercised by
// attemptReflexDispatch; the rest of the interface is unused stubs.
type recordingReflexDispatchToolService struct {
	calls []recordedToolExecuteCall
}

type recordedToolExecuteCall struct {
	agentID  string
	toolName string
	input    map[string]any
}

func (f *recordingReflexDispatchToolService) Execute(_ context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	f.calls = append(f.calls, recordedToolExecuteCall{agentID: agentID, toolName: toolName, input: input})
	return &ToolResult{
		Output: `{"kind":"envelope","version":1,"type":"report-card","data":{"title":"planner dispatched"}}`,
	}, nil
}
func (f *recordingReflexDispatchToolService) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return nil, nil
}
func (f *recordingReflexDispatchToolService) HandleRequestTools(context.Context, string, map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	return nil, "", nil
}
func (f *recordingReflexDispatchToolService) ListSummaries() []toolclient.ToolSummary { return nil }
func (f *recordingReflexDispatchToolService) GetToolMeta(context.Context, string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, false
}
func (f *recordingReflexDispatchToolService) GetToolSchema(string) map[string]any { return nil }

// Retained routing rules do not gain authority from a matching classification.
func TestAttemptReflexDispatch_RetainedOpenSubagentRuleCannotRouteToPlanner(t *testing.T) {
	st, actor, session := newRetiredDispatchFixture(t, "open-subagent-rule")
	insertRetainedDispatchRule(t, st, store.AgentReflex{
		Name: "dispatch_to_agent_open_subagent", ClassTag: "advisor",
		TriggerKind: store.ReflexTriggerPredicate,
		TriggerSpec: `{"kind":"scope_tier","tier":"open"}`,
		ActionKind:  store.ReflexActionDispatchToAgent,
		ActionSpec:  `{"agent_slug":"planner","confidence":0.75}`, Priority: 10,
	})
	message := "scaffold the new reporting module from a blank slate"
	ls := classifiedRetiredDispatchTurn(t, session.ID, message)
	tier, pattern := ls.Classification()
	if tier != classify.TierOpen || pattern != classify.PatternSubagent {
		t.Fatalf("classification=%s/%s", tier, pattern)
	}
	assertRetainedDispatchInert(t, st, actor.ID, session.ID, message, ls)
}

// recordingReflexPluginHooks is a minimal reflexes.PluginHooks fake used
// to prove attemptReflexDispatch/matchDispatchToAgentReflex now emit the
// same EmitReflexFired/EmitReflexActionStaged hooks Engine.EvaluateState
// always did — TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md's gap
// 3. ApplyFilter is a pass-through no-op; neither dispatch_to_agent call
// site runs FilterReflexAction (that filter is Engine.EvaluateState's own
// mechanism, not part of this task's telemetry scope).
type recordingReflexPluginHooks struct {
	fired  int
	staged int
}

func (h *recordingReflexPluginHooks) ApplyFilter(_ string, data interface{}, _ pluginpkg.FilterContext) (interface{}, error) {
	return data, nil
}
func (h *recordingReflexPluginHooks) EmitReflexFired(string, map[string]any)        { h.fired++ }
func (h *recordingReflexPluginHooks) EmitReflexActionStaged(string, map[string]any) { h.staged++ }

// TestAttemptReflexDispatch_RealSeededReflex_TierSmall_NoDispatch confirms
// the former Rule 6 ("default, no dispatch") absence-of-match behavior:
// a turn that does NOT classify to (open, subagent) produces no match, no
// event_log row, and no task_execute call — the chat-direct loop is
// untouched, same as when the retired broker's rules all missed.
func TestAttemptReflexDispatch_RealSeededReflex_TierSmall_NoDispatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-miss.db")
	st, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	if _, err := reflexes.SeedBaseReflexes(context.Background(), st, nil); err != nil {
		t.Fatalf("SeedBaseReflexes: %v", err)
	}

	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}

	// Deliberately NOT "what does this function do" (the message this
	// test used before TASKS/phase-4/03-migrate-promptrouter-to-
	// reflexes.md landed): "what does" is one of researcher-mention's
	// migrated phrase-match triggers (dispatch_to_agent_researcher_
	// mention, seeds.go), which now correctly fires on that phrase
	// regardless of tier — the new intended behavior, not a regression,
	// but it means that message no longer exercises the true "nothing
	// fires" case this test is for. Swapped to a message that contains
	// none of the 6 migrated phrase catalogs and stays well under the
	// classifier's trivial-tier token floor.
	userMessage := "please say hello to the team"
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, "sess-rule6-1", userMessage, nil)
	gotTier, gotPattern := ls.Classification()
	if gotTier == classify.TierOpen && gotPattern == classify.PatternSubagent {
		t.Fatalf("test fixture message classified as (open, subagent), want anything else — fixture needs adjusting")
	}

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptReflexDispatch(
		context.Background(),
		"sess-rule6-1", "turn-rule6-1", userMessage,
		"agent-rule6-1", "advisor",
		ls, ch,
	)
	close(ch)

	if out.Matched {
		t.Errorf("Matched = true, want false (no dispatch_to_agent reflex should fire for a trivial/small-tier turn)")
	}
	if len(tools.calls) != 0 {
		t.Errorf("ToolService.Execute called %d times, want 0", len(tools.calls))
	}
	events, err := st.ListEvents(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, e := range events {
		if e.EventType == "dispatch_to_agent" {
			t.Errorf("unexpected dispatch_to_agent event_log row on a non-matching turn: %+v", e)
		}
	}
}
