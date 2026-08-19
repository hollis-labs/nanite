// Integration coverage for TASKS/phase-4/02
// (dispatch-to-agent-reflex-action-kind-and-broker-migration.md)'s Done
// means: "The former Rule 5 behavior (open-tier subagent-pattern turns
// route to planner) is reproduced via a reflex, verified in a real
// session."
//
// This exercises the REAL pipeline end to end against a REAL *store.Store
// (migrations applied, including 119_agent_reflex_dispatch_to_agent.sql):
// real classify.Classify (via classifyAndAttach — the same helper
// generateResponse calls), the REAL seeded agent_reflexes row (via
// reflexes.SeedBaseReflexes — not a hand-authored fixture, so this proves
// the actual seed data in seeds.go reproduces Rule 5, not just that the
// evaluator/executor machinery works in the abstract), the real
// reflexes.Engine, and the real attemptReflexDispatch call site. Only the
// outermost boundary — ToolService.Execute, i.e. what happens once
// task_execute is invoked — is faked, mirroring the same boundary the
// now-deleted chat_broker_dispatch_integration_test.go used to verify the
// retired broker call site (a real subagent spawn needs a live LLM
// provider, which this worker environment has no credentials for; see the
// task file's Work Log for the full accounting of what was and wasn't
// exercised).
package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
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
func (f *recordingReflexDispatchToolService) HandleRequestTools(context.Context, map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	return nil, "", nil
}
func (f *recordingReflexDispatchToolService) ListSummaries() []toolclient.ToolSummary { return nil }
func (f *recordingReflexDispatchToolService) GetToolMeta(string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, false
}
func (f *recordingReflexDispatchToolService) GetToolSchema(string) map[string]any { return nil }

// TestAttemptReflexDispatch_RealSeededReflex_ScopeTierOpenSubagent_RoutesToPlanner
// is the Rule 5 parity check: a real store with the real seeded
// dispatch_to_agent_open_subagent reflex (advisor class), a real
// classify.Classify run (via classifyAndAttach) over a message that
// classifies to (TierOpen, PatternSubagent) — matching what the retired
// broker's Rule 5 keyed on — fires the reflex and synthesizes a
// task_execute call, exactly reproducing the former agent-broker
// behavior for that tier/pattern combination.
func TestAttemptReflexDispatch_RealSeededReflex_ScopeTierOpenSubagent_RoutesToPlanner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch.db")
	st, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Real seed data — the same call container.go makes at boot. Proves
	// seeds.go's dispatch_to_agent_open_subagent entry (not a
	// hand-authored test fixture) actually reproduces Rule 5.
	if _, err := reflexes.SeedBaseReflexes(context.Background(), st, nil); err != nil {
		t.Fatalf("SeedBaseReflexes: %v", err)
	}

	tools := &recordingReflexDispatchToolService{}
	s := &chatServiceImpl{
		store:        st,
		reflexEngine: reflexes.NewEngine(st, nil),
		tools:        tools,
	}

	// Real pre-loop classification (classifyAndAttach — the same helper
	// generateResponse calls right before the retired broker call site
	// used to run, and where attemptReflexDispatch runs now). This
	// message hits ScopeTierOpenKeywords ("scaffold"); classifyPattern
	// maps any TierOpen message to PatternSubagent
	// (internal/classify/classify.go), matching what the retired
	// broker's Rule 5 required.
	//
	// Deliberately NOT "build a full implementation... end-to-end" (the
	// message this test used before TASKS/phase-4/03-migrate-
	// promptrouter-to-reflexes.md landed): that message contains "build",
	// one of worker-execute's migrated phrase-match triggers
	// (dispatch_to_agent_worker_execute, seeds.go), which now fires
	// FIRST (priority 20, above dispatch_to_agent_open_subagent's 10)
	// and correctly wins per task 02's own design note 2 ("a specific
	// phrase match wins over the general tier/pattern rule") — that's
	// the new INTENDED behavior, not a regression, but it means this
	// fixture message no longer isolates Rule 5 in particular. Swapped
	// to a message that reaches TierOpen via a different keyword
	// ("scaffold") while containing none of the 6 migrated phrase
	// catalogs, so this test still isolates the general open+subagent
	// fallback reflex specifically.
	userMessage := "scaffold the new reporting module from a blank slate"
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	classifyAndAttach(ls, "sess-rule5-1", userMessage, nil)
	gotTier, gotPattern := ls.Classification()
	if gotTier != classify.TierOpen || gotPattern != classify.PatternSubagent {
		t.Fatalf("test fixture message classified as (%s, %s), want (open, subagent) — fixture message needs adjusting, not the production code",
			gotTier, gotPattern)
	}

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptReflexDispatch(
		context.Background(),
		"sess-rule5-1", "turn-rule5-1", userMessage,
		"agent-rule5-1", "advisor",
		ls, ch,
	)
	close(ch)

	if !out.Matched {
		t.Fatalf("attemptReflexDispatch: Matched = false, want true (dispatch_to_agent_open_subagent should have fired)")
	}
	if out.ReflexName != "dispatch_to_agent_open_subagent" {
		t.Errorf("ReflexName = %q, want dispatch_to_agent_open_subagent", out.ReflexName)
	}
	if out.AgentSlug != "planner" {
		t.Errorf("AgentSlug = %q, want planner (Rule 5 parity)", out.AgentSlug)
	}
	if out.Confidence != 0.75 {
		t.Errorf("Confidence = %g, want 0.75 (Rule 5's fixed confidence)", out.Confidence)
	}
	if !out.Invoked {
		t.Errorf("Invoked = false, want true (task_execute should have been called)")
	}
	if !out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = false, want true")
	}

	// The synthesized task_execute call itself — same shape the retired
	// broker produced (design note 4 in chat_reflex_dispatch.go: no
	// agent_slug override threaded through; AssignRole resolves the real
	// target downstream).
	if len(tools.calls) != 1 {
		t.Fatalf("ToolService.Execute called %d times, want 1", len(tools.calls))
	}
	call := tools.calls[0]
	if call.toolName != "task_execute" {
		t.Errorf("tool name = %q, want task_execute", call.toolName)
	}
	if call.input["session_id"] != "sess-rule5-1" {
		t.Errorf("task_execute session_id = %v, want sess-rule5-1", call.input["session_id"])
	}
	if call.input["message"] != userMessage {
		t.Errorf("task_execute message = %v, want %q", call.input["message"], userMessage)
	}

	// event_log — decision log §14 write-site-discipline: real structured
	// reasoning, not a bare event name.
	events, err := st.ListEvents("", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var dispatchEvent *store.EventLog
	for i := range events {
		if events[i].EventType == "dispatch_to_agent" {
			dispatchEvent = &events[i]
			break
		}
	}
	if dispatchEvent == nil {
		t.Fatalf("event_log has no dispatch_to_agent row; got %d events of other types", len(events))
	}
	if dispatchEvent.SessionID != "sess-rule5-1" {
		t.Errorf("event_log.SessionID = %q, want sess-rule5-1", dispatchEvent.SessionID)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(dispatchEvent.Metadata), &meta); err != nil {
		t.Fatalf("event_log metadata not JSON: %v\nblob: %s", err, dispatchEvent.Metadata)
	}
	if got, want := meta["agent_slug"], "planner"; got != want {
		t.Errorf("event_log metadata.agent_slug = %v, want %q", got, want)
	}
	if got, want := meta["scope_tier"], "open"; got != want {
		t.Errorf("event_log metadata.scope_tier = %v, want %q", got, want)
	}
	if got, want := meta["execution_pattern"], "subagent"; got != want {
		t.Errorf("event_log metadata.execution_pattern = %v, want %q", got, want)
	}
	if _, ok := meta["alternatives_considered"]; !ok {
		t.Errorf("event_log metadata missing alternatives_considered")
	}
}

// TestAttemptReflexDispatch_RealSeededReflex_TierSmall_NoDispatch confirms
// the former Rule 6 ("default, no dispatch") absence-of-match behavior:
// a turn that does NOT classify to (open, subagent) produces no match, no
// event_log row, and no task_execute call — the chat-direct loop is
// untouched, same as when the retired broker's rules all missed.
func TestAttemptReflexDispatch_RealSeededReflex_TierSmall_NoDispatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reflex-dispatch-miss.db")
	st, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

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
	events, err := st.ListEvents("", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, e := range events {
		if e.EventType == "dispatch_to_agent" {
			t.Errorf("unexpected dispatch_to_agent event_log row on a non-matching turn: %+v", e)
		}
	}
}
