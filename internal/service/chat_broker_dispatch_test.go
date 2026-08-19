package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentbroker "github.com/hollis-labs/agentkit/broker"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// fakeAgentBroker is a controllable broker.Broker used by the
// CW-20260509-0046 call-site tests. It records the input it received
// and returns the canned decision/error.
type fakeAgentBroker struct {
	gotInput  agentbroker.Input
	decision  agentbroker.Decision
	returnErr error
	called    int
}

func (f *fakeAgentBroker) Decide(_ context.Context, in agentbroker.Input) (agentbroker.Decision, error) {
	f.called++
	f.gotInput = in
	return f.decision, f.returnErr
}

// recordingBrokerStore is a minimal Store impl that records
// InsertAgentBrokerDecision calls plus the LogEvent invocations the
// persist helper produces. Implements only the surface
// chat_broker_dispatch.go consumes (the upstream call site does not
// touch the rest of the Store interface). chatServiceImpl.store is
// typed as the full Store, so this stub embeds the same minimalStore
// scaffold the rest of the chat tests use.
type recordingBrokerStore struct {
	minimalStore

	insertedRows []*store.AgentBrokerDecision
	insertErr    error
	loggedEvents []recordedEvent
	nextRowID    int64
}

type recordedEvent struct {
	sessionID string
	eventType string
	category  string
	detail    string
	metadata  string
}

func (r *recordingBrokerStore) InsertAgentBrokerDecision(row *store.AgentBrokerDecision) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.nextRowID++
	row.ID = r.nextRowID
	row.CreatedAt = "2026-05-10T00:00:00Z"
	r.insertedRows = append(r.insertedRows, row)
	return nil
}

func (r *recordingBrokerStore) LogEvent(sessionID, eventType, category, detail, metadata string) {
	r.loggedEvents = append(r.loggedEvents, recordedEvent{
		sessionID: sessionID,
		eventType: eventType,
		category:  category,
		detail:    detail,
		metadata:  metadata,
	})
}

// fakeToolService is a controllable ToolService used to capture the
// synthesized task_execute call when the broker returns a dispatch
// decision. Only Execute is exercised here; the rest of the surface is
// not consulted on the upstream path.
type fakeToolService struct {
	gotAgentID  string
	gotTool     string
	gotInput    map[string]any
	returnRes   *ToolResult
	returnErr   error
	called      int
}

func (f *fakeToolService) Execute(_ context.Context, agentID, toolName string, input map[string]any) (*ToolResult, error) {
	f.called++
	f.gotAgentID = agentID
	f.gotTool = toolName
	f.gotInput = input
	return f.returnRes, f.returnErr
}

// The remaining ToolService methods are unused by attemptBrokerDispatch
// but satisfy the interface so we can install the fake on chatServiceImpl.
func (f *fakeToolService) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return nil, nil
}
func (f *fakeToolService) HandleRequestTools(context.Context, map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	return nil, "", nil
}
func (f *fakeToolService) ListSummaries() []toolclient.ToolSummary  { return nil }
func (f *fakeToolService) GetToolMeta(string) (ToolMetaInfo, bool)  { return ToolMetaInfo{}, false }
func (f *fakeToolService) GetToolSchema(string) map[string]any      { return nil }

// TestAttemptBrokerDispatch_NilBroker_NoOp covers the production
// nil-safe contract: when ChatServiceConfig.AgentBroker is unwired,
// the call site is a pure pass-through. No row written, no LogEvent,
// no envelope emitted, the Consulted bit stays false.
func TestAttemptBrokerDispatch_NilBroker_NoOp(t *testing.T) {
	rs := &recordingBrokerStore{}
	s := &chatServiceImpl{
		store:       rs,
		agentBroker: nil,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(context.Background(), "session-1", "turn-1", "hello", "agent-1", ls, ch)
	close(ch)

	if out.Consulted {
		t.Errorf("Consulted = true, want false when broker is nil")
	}
	if out.Dispatched {
		t.Errorf("Dispatched = true, want false when broker is nil")
	}
	if len(rs.insertedRows) != 0 {
		t.Errorf("inserted %d rows, want 0", len(rs.insertedRows))
	}
	if len(rs.loggedEvents) != 0 {
		t.Errorf("logged %d events, want 0", len(rs.loggedEvents))
	}
	if got := drain(ch); got != 0 {
		t.Errorf("emitted %d events, want 0", got)
	}
}

// TestAttemptBrokerDispatch_ChatDecision_WritesRow covers the
// chat-direct path: broker returns AgentProfile == "" → no dispatch,
// no plugin_envelope, but agent_broker_decisions row IS written so v2
// telemetry analysis can count chat-direct decisions vs dispatches.
//
// CW-20260509-0048: an `agent_broker_decision` SSE event IS emitted
// even on the chat-direct path so FE inspectors / audit panels see
// every consultation on the wire (the wire signal mirrors the
// agent_broker_decisions row + event_log entry, both of which fire
// in the chat-direct branch too).
func TestAttemptBrokerDispatch_ChatDecision_WritesRow(t *testing.T) {
	fb := &fakeAgentBroker{
		decision: agentbroker.Decision{
			AgentProfile: agentbroker.ProfileChat,
			Reason:       "default-chat-handle",
			Confidence:   0,
		},
	}
	rs := &recordingBrokerStore{}
	tools := &fakeToolService{}
	s := &chatServiceImpl{
		store:       rs,
		agentBroker: fb,
		tools:       tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(context.Background(), "session-1", "turn-1", "what's 2+2", "agent-1", ls, ch)
	close(ch)

	if !out.Consulted {
		t.Errorf("Consulted = false, want true")
	}
	if out.Dispatched {
		t.Errorf("Dispatched = true, want false for chat-direct")
	}
	if out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = true, want false for chat-direct")
	}
	if !out.EmittedSSEDecision {
		t.Errorf("EmittedSSEDecision = false, want true for chat-direct (CW-20260509-0048)")
	}
	if tools.called != 0 {
		t.Errorf("tool service called %d times, want 0 for chat-direct", tools.called)
	}

	// SSE event assertions — exactly one agent_broker_decision event,
	// no plugin_envelope.
	events := collect(ch)
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1 (agent_broker_decision only)", len(events))
	}
	if events[0].Type != agentBrokerDecisionEventType {
		t.Errorf("event Type = %q, want %q", events[0].Type, agentBrokerDecisionEventType)
	}
	var payload agentBrokerDecisionPayload
	if err := json.Unmarshal([]byte(events[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal SSE payload failed: %v", err)
	}
	if payload.Decision != "" {
		t.Errorf("payload.Decision = %q, want empty for chat-direct (ProfileChat)", payload.Decision)
	}
	if payload.Reason != "default-chat-handle" {
		t.Errorf("payload.Reason = %q, want default-chat-handle", payload.Reason)
	}
	if payload.SessionID != "session-1" {
		t.Errorf("payload.SessionID = %q, want session-1", payload.SessionID)
	}
	if payload.TurnID != "turn-1" {
		t.Errorf("payload.TurnID = %q, want turn-1", payload.TurnID)
	}
	if payload.AgentBrokerDecisionID == 0 {
		t.Errorf("payload.AgentBrokerDecisionID = 0, want non-zero (row was inserted)")
	}
	if payload.CreatedAt == "" {
		t.Errorf("payload.CreatedAt is empty, want server-side timestamp")
	}

	// The agent_broker_decisions row MUST be written for chat-direct
	// decisions too — telemetry tracks both branches.
	if len(rs.insertedRows) != 1 {
		t.Fatalf("inserted %d rows, want 1", len(rs.insertedRows))
	}
	row := rs.insertedRows[0]
	if row.SessionID != "session-1" {
		t.Errorf("row.SessionID = %q, want session-1", row.SessionID)
	}
	if row.TurnID != "turn-1" {
		t.Errorf("row.TurnID = %q, want turn-1", row.TurnID)
	}
	if row.Decision != agentbroker.ProfileChat {
		t.Errorf("row.Decision = %q, want %q", row.Decision, agentbroker.ProfileChat)
	}
	if row.Reason != "default-chat-handle" {
		t.Errorf("row.Reason = %q, want default-chat-handle", row.Reason)
	}
	if row.UserInputHash == "" {
		t.Errorf("row.UserInputHash is empty; SHA-256 expected")
	}

	// LogEvent companion entry — log-tailers observe broker activity
	// without joining tables.
	if len(rs.loggedEvents) != 1 {
		t.Fatalf("logged %d events, want 1", len(rs.loggedEvents))
	}
	if rs.loggedEvents[0].eventType != "agent_broker_decision" {
		t.Errorf("logged eventType = %q, want agent_broker_decision", rs.loggedEvents[0].eventType)
	}
	if out.AgentBrokerDecisionID != row.ID {
		t.Errorf("outcome ID = %d, want %d", out.AgentBrokerDecisionID, row.ID)
	}

	// Wire-event AgentBrokerDecisionID must match the persisted row id —
	// consumers correlate the SSE event with the durable row + log entry.
	if payload.AgentBrokerDecisionID != row.ID {
		t.Errorf("payload.AgentBrokerDecisionID = %d, want %d (row.ID)", payload.AgentBrokerDecisionID, row.ID)
	}
}

// TestAttemptBrokerDispatch_DispatchDecision_SynthesizesTaskExecute
// covers the load-bearing dispatch path: broker returns a non-empty
// AgentProfile, the call site MUST synthesize a task_execute call
// (routing through s.tools.Execute so the existing reflex/grounding
// scaffold in callExecuteTask fires) and emit the resulting envelope
// onto the SSE stream as a plugin_envelope event.
func TestAttemptBrokerDispatch_DispatchDecision_SynthesizesTaskExecute(t *testing.T) {
	fb := &fakeAgentBroker{
		decision: agentbroker.Decision{
			AgentProfile: agentbroker.ProfileWorker,
			Reason:       "mode=work",
			Confidence:   1.0,
		},
	}
	rs := &recordingBrokerStore{}
	tools := &fakeToolService{
		returnRes: &ToolResult{
			// Worker dispatch returns the envelope JSON as the tool
			// result Output — same shape callExecuteTask produces.
			Output: `{"kind":"envelope","version":1,"type":"report-card","data":{"title":"Done"}}`,
		},
	}
	s := &chatServiceImpl{
		store:       rs,
		agentBroker: fb,
		tools:       tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	// Pre-populate classification so the broker.Input projection
	// includes scope_tier + execution_pattern (the real
	// generateResponse path runs classifyAndAttach upstream of the
	// broker call).
	ls.SetClassification(classify.TierMedium, classify.PatternSubagent)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(context.Background(), "session-1", "turn-1", "/work refactor the foo", "agent-1", ls, ch)
	close(ch)

	if !out.Consulted {
		t.Errorf("Consulted = false, want true")
	}
	if !out.Dispatched {
		t.Errorf("Dispatched = false, want true for worker decision")
	}
	if !out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = false, want true on dispatch with envelope output")
	}

	// task_execute call shape — the load-bearing assertion. The
	// synthesized call routes through s.tools.Execute → SelfTools
	// Transport → callExecuteTask, where the reflex/grounding/inner-
	// broker scaffold runs (boundary preserved per
	// decisions.nanite.architecture.agent_broker_v1).
	if tools.called != 1 {
		t.Fatalf("tool service called %d times, want 1", tools.called)
	}
	if tools.gotTool != "task_execute" {
		t.Errorf("tool name = %q, want task_execute", tools.gotTool)
	}
	if tools.gotAgentID != "agent-1" {
		t.Errorf("agent_id = %q, want agent-1", tools.gotAgentID)
	}
	if got, want := tools.gotInput["session_id"], "session-1"; got != want {
		t.Errorf("input.session_id = %v, want %q", got, want)
	}
	if got, want := tools.gotInput["parent_agent_id"], "agent-1"; got != want {
		t.Errorf("input.parent_agent_id = %v, want %q", got, want)
	}
	if got, want := tools.gotInput["message"], "/work refactor the foo"; got != want {
		t.Errorf("input.message = %v, want %q", got, want)
	}
	if got, want := tools.gotInput["turn_id"], "turn-1"; got != want {
		t.Errorf("input.turn_id = %v, want %q", got, want)
	}

	// Two events on a successful dispatch:
	//   1. agent_broker_decision (CW-20260509-0048 wire signal — every
	//      consultation gets one).
	//   2. plugin_envelope (existing CW-20260509-0046 dispatch path —
	//      synthesized task_execute output routed to FE).
	// Order is documented (broker-decision before envelope) so consumers
	// can match the wire event to the dispatch envelope by session_id +
	// turn_id when both arrive.
	events := collect(ch)
	if len(events) != 2 {
		t.Fatalf("emitted %d events, want 2 (agent_broker_decision + plugin_envelope)", len(events))
	}
	if events[0].Type != agentBrokerDecisionEventType {
		t.Errorf("event[0] Type = %q, want %q", events[0].Type, agentBrokerDecisionEventType)
	}
	if events[1].Type != "plugin_envelope" {
		t.Errorf("event[1] Type = %q, want plugin_envelope", events[1].Type)
	}
	if events[1].Envelope == "" {
		t.Errorf("event[1] Envelope is empty")
	}

	// agent_broker_decision payload mirrors the dispatch decision.
	var payload agentBrokerDecisionPayload
	if err := json.Unmarshal([]byte(events[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal SSE payload failed: %v", err)
	}
	if payload.Decision != agentbroker.ProfileWorker {
		t.Errorf("payload.Decision = %q, want %q", payload.Decision, agentbroker.ProfileWorker)
	}
	if payload.Reason != "mode=work" {
		t.Errorf("payload.Reason = %q, want mode=work", payload.Reason)
	}
	if payload.Confidence != 1.0 {
		t.Errorf("payload.Confidence = %g, want 1.0", payload.Confidence)
	}
	if payload.ScopeTier != classify.TierMedium.String() {
		t.Errorf("payload.ScopeTier = %q, want %q", payload.ScopeTier, classify.TierMedium.String())
	}

	if !out.EmittedSSEDecision {
		t.Errorf("EmittedSSEDecision = false, want true on dispatch")
	}

	// Telemetry row mirrors the dispatch decision.
	if len(rs.insertedRows) != 1 {
		t.Fatalf("inserted %d rows, want 1", len(rs.insertedRows))
	}
	if rs.insertedRows[0].Decision != agentbroker.ProfileWorker {
		t.Errorf("row.Decision = %q, want %q", rs.insertedRows[0].Decision, agentbroker.ProfileWorker)
	}
}

// TestAttemptBrokerDispatch_BrokerError_FallsThrough covers the
// fault-tolerance contract: when broker.Decide returns an error, the
// call site logs and falls through to chat-direct (no row written, no
// dispatch). A broker failure MUST NOT kill the turn.
func TestAttemptBrokerDispatch_BrokerError_FallsThrough(t *testing.T) {
	fb := &fakeAgentBroker{
		returnErr: errors.New("broker exploded"),
	}
	rs := &recordingBrokerStore{}
	tools := &fakeToolService{}
	s := &chatServiceImpl{
		store:       rs,
		agentBroker: fb,
		tools:       tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(context.Background(), "session-1", "turn-1", "hi", "agent-1", ls, ch)
	close(ch)

	if out.Consulted {
		t.Errorf("Consulted = true, want false on broker error (should be treated as no-decision)")
	}
	if out.Dispatched {
		t.Errorf("Dispatched = true, want false on broker error")
	}
	if len(rs.insertedRows) != 0 {
		t.Errorf("inserted %d rows on broker error, want 0", len(rs.insertedRows))
	}
	if tools.called != 0 {
		t.Errorf("tool service called %d times on broker error, want 0", tools.called)
	}
}

// TestAttemptBrokerDispatch_ToolExecuteError_FallsThrough covers the
// dispatch-side fault-tolerance: when task_execute itself errors, the
// telemetry row is still written (broker decision was made) but no
// envelope is emitted. The chat-direct LLM loop runs as the natural
// fallback.
func TestAttemptBrokerDispatch_ToolExecuteError_FallsThrough(t *testing.T) {
	fb := &fakeAgentBroker{
		decision: agentbroker.Decision{
			AgentProfile: agentbroker.ProfileWorker,
			Reason:       "mode=work",
			Confidence:   1.0,
		},
	}
	rs := &recordingBrokerStore{}
	tools := &fakeToolService{
		returnErr: errors.New("dispatch unavailable"),
	}
	s := &chatServiceImpl{
		store:       rs,
		agentBroker: fb,
		tools:       tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(context.Background(), "session-1", "turn-1", "fix the bug", "agent-1", ls, ch)
	close(ch)

	if !out.Consulted {
		t.Errorf("Consulted = false, want true (broker did decide)")
	}
	if !out.Dispatched {
		t.Errorf("Dispatched = false, want true (decision was to dispatch even though execute failed)")
	}
	if out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = true, want false on tool execute error")
	}
	if !out.EmittedSSEDecision {
		t.Errorf("EmittedSSEDecision = false, want true (SSE wire event fires even when downstream execute fails)")
	}
	if len(rs.insertedRows) != 1 {
		t.Errorf("inserted %d rows, want 1 (telemetry row still written)", len(rs.insertedRows))
	}
	// Exactly one event — the agent_broker_decision SSE — fires before
	// the synthesized task_execute call. The plugin_envelope side-
	// channel is suppressed because the downstream execute errored.
	events := collect(ch)
	if len(events) != 1 {
		t.Fatalf("emitted %d events on tool execute error, want 1 (agent_broker_decision only)", len(events))
	}
	if events[0].Type != agentBrokerDecisionEventType {
		t.Errorf("event Type = %q, want %q", events[0].Type, agentBrokerDecisionEventType)
	}
}

// TestAttemptBrokerDispatch_ToolExecuteMalformedJSON_NoEmit covers the
// envelope-shape guard. When task_execute returns a non-JSON string
// (shouldn't happen in production, but the call site must defend
// against it so we never push malformed payloads onto the stream),
// the row is written, Dispatched=true, but EmittedEnvelope=false.
func TestAttemptBrokerDispatch_ToolExecuteMalformedJSON_NoEmit(t *testing.T) {
	fb := &fakeAgentBroker{
		decision: agentbroker.Decision{
			AgentProfile: agentbroker.ProfileWorker,
			Reason:       "mode=work",
			Confidence:   1.0,
		},
	}
	rs := &recordingBrokerStore{}
	tools := &fakeToolService{
		returnRes: &ToolResult{Output: "not valid json {"},
	}
	s := &chatServiceImpl{
		store:       rs,
		agentBroker: fb,
		tools:       tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(context.Background(), "session-1", "turn-1", "fix it", "agent-1", ls, ch)
	close(ch)

	if !out.Dispatched {
		t.Errorf("Dispatched = false, want true")
	}
	if out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = true on malformed JSON, want false")
	}
	if !out.EmittedSSEDecision {
		t.Errorf("EmittedSSEDecision = false, want true (SSE wire event fires even when envelope shape guard rejects the dispatch output)")
	}
	// One event — the agent_broker_decision SSE. The plugin_envelope
	// is correctly suppressed by the JSON-shape guard so the FE never
	// receives a malformed payload.
	events := collect(ch)
	if len(events) != 1 {
		t.Fatalf("emitted %d events on malformed JSON, want 1 (agent_broker_decision only)", len(events))
	}
	if events[0].Type != agentBrokerDecisionEventType {
		t.Errorf("event Type = %q, want %q", events[0].Type, agentBrokerDecisionEventType)
	}
}

// TestAttemptBrokerDispatch_BuildsInputFromClassifications validates
// the primitive-projection seam. The broker MUST receive the loopState's
// scope/pattern (from classifyAndAttach upstream) and any reflex match —
// projected to the broker.Input primitive shape.
//
// Phase 0 item 21 ("Cut Modes, in full") deleted classify.ClassifyMode,
// the per-turn mode classifier that used to populate broker.Input.Mode /
// ModeConfidence here (see TASKS/phase-0/21-cut-modes.md). This test
// used to assert a "/work" prefix projected ModeWork at confidence 1.0;
// it now asserts the opposite — in.Mode/in.ModeConfidence stay at their
// zero values regardless of input text, which is exactly what makes
// agentkit's DeterministicBroker rules 2-4 (mode=work / mode=plan)
// permanently unreachable without touching the external agentkit module.
func TestAttemptBrokerDispatch_BuildsInputFromClassifications(t *testing.T) {
	fb := &fakeAgentBroker{
		decision: agentbroker.Decision{
			AgentProfile: agentbroker.ProfileChat,
			Reason:       "default-chat-handle",
		},
	}
	rs := &recordingBrokerStore{}
	tools := &fakeToolService{}
	s := &chatServiceImpl{
		store:       rs,
		agentBroker: fb,
		tools:       tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetClassification(classify.TierMedium, classify.PatternSubagent)

	ch := make(chan chat.StreamEvent, 4)
	// "/work" prefix used to trigger SignalSlashWork in the now-deleted
	// classify.ClassifyMode at confidence 1.0. It's kept in the fixture
	// text to prove the mode signal genuinely never fires anymore, not
	// just that it was never exercised.
	_ = s.attemptBrokerDispatch(context.Background(), "session-1", "turn-1", "/work refactor the foo", "agent-1", ls, ch)
	close(ch)

	if fb.called != 1 {
		t.Fatalf("broker called %d times, want 1", fb.called)
	}
	got := fb.gotInput
	if got.UserText != "/work refactor the foo" {
		t.Errorf("input.UserText = %q, want \"/work refactor the foo\"", got.UserText)
	}
	if got.Mode != "" {
		t.Errorf("input.Mode = %q, want \"\" (classify.ClassifyMode was cut — Mode must never be populated)", got.Mode)
	}
	if got.ModeConfidence != 0 {
		t.Errorf("input.ModeConfidence = %g, want 0 (classify.ClassifyMode was cut — ModeConfidence must never be populated)", got.ModeConfidence)
	}
	if got.ScopeTier != classify.TierMedium.String() {
		t.Errorf("input.ScopeTier = %q, want %q", got.ScopeTier, classify.TierMedium.String())
	}
	if got.ExecutionPattern != classify.PatternSubagent.String() {
		t.Errorf("input.ExecutionPattern = %q, want %q", got.ExecutionPattern, classify.PatternSubagent.String())
	}
}

// TestEmitAgentBrokerDecisionSSE_FullPayload exercises the
// CW-20260509-0048 SSE emitter directly. Validates the wire payload
// shape with all fields populated (row write succeeded, dispatch
// decision, all input projections set) so a future FE inspector
// consumer has a documented decode contract.
func TestEmitAgentBrokerDecisionSSE_FullPayload(t *testing.T) {
	s := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 1)

	input := agentbroker.Input{
		UserText:        "/work refactor the foo",
		Mode:            agentbroker.ModeWork,
		ModeConfidence:  1.0,
		ScopeTier:       classify.TierMedium.String(),
		ReflexMatchID:   "refactor-handler",
		ReflexAgentSlug: agentbroker.ProfileWorker,
	}
	decision := agentbroker.Decision{
		AgentProfile: agentbroker.ProfileWorker,
		Reason:       "mode=work",
		Confidence:   1.0,
	}
	row := &store.AgentBrokerDecision{
		ID:        42,
		CreatedAt: "2026-05-10T01:23:45Z",
	}

	emitted := s.emitAgentBrokerDecisionSSE(ch, "session-1", "turn-7", input, decision, row)
	close(ch)

	if !emitted {
		t.Fatalf("emitAgentBrokerDecisionSSE returned false, want true")
	}
	events := collect(ch)
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(events))
	}
	if events[0].Type != agentBrokerDecisionEventType {
		t.Errorf("event Type = %q, want %q", events[0].Type, agentBrokerDecisionEventType)
	}

	var got agentBrokerDecisionPayload
	if err := json.Unmarshal([]byte(events[0].Data), &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	want := agentBrokerDecisionPayload{
		AgentBrokerDecisionID: 42,
		SessionID:             "session-1",
		TurnID:                "turn-7",
		Decision:              agentbroker.ProfileWorker,
		Reason:                "mode=work",
		Confidence:            1.0,
		ModeSignal:            agentbroker.ModeWork,
		ScopeTier:             classify.TierMedium.String(),
		ReflexID:              "refactor-handler",
		CreatedAt:             "2026-05-10T01:23:45Z",
	}
	if got != want {
		t.Errorf("payload mismatch:\n got:  %+v\n want: %+v", got, want)
	}
}

// TestEmitAgentBrokerDecisionSSE_NilChannelNoOp covers the wire-up
// guard: when the SSE channel is nil (test scaffolds, degenerate
// generateResponse paths), the emitter is a no-op. No panic, no
// emit, returns false so callers can skip the EmittedSSEDecision
// flag flip.
func TestEmitAgentBrokerDecisionSSE_NilChannelNoOp(t *testing.T) {
	s := &chatServiceImpl{}
	emitted := s.emitAgentBrokerDecisionSSE(nil, "session-1", "turn-1",
		agentbroker.Input{UserText: "hi"},
		agentbroker.Decision{AgentProfile: agentbroker.ProfileChat, Reason: "default-chat-handle"},
		nil)
	if emitted {
		t.Errorf("emitAgentBrokerDecisionSSE(nil ch) = true, want false")
	}
}

// TestEmitAgentBrokerDecisionSSE_NilRow covers the store-failure
// branch: the broker decided, but the agent_broker_decisions insert
// failed and persistAgentBrokerDecision returned nil. The wire event
// MUST still fire (so consumers see the decision even when the
// telemetry table is unavailable) but with AgentBrokerDecisionID=0
// and no CreatedAt.
func TestEmitAgentBrokerDecisionSSE_NilRow(t *testing.T) {
	s := &chatServiceImpl{}
	ch := make(chan chat.StreamEvent, 1)

	emitted := s.emitAgentBrokerDecisionSSE(ch, "session-1", "turn-1",
		agentbroker.Input{UserText: "hi", Mode: agentbroker.ModeWork},
		agentbroker.Decision{AgentProfile: agentbroker.ProfileChat, Reason: "default-chat-handle"},
		nil)
	close(ch)

	if !emitted {
		t.Fatalf("emitAgentBrokerDecisionSSE returned false, want true")
	}
	events := collect(ch)
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(events))
	}
	var payload agentBrokerDecisionPayload
	if err := json.Unmarshal([]byte(events[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.AgentBrokerDecisionID != 0 {
		t.Errorf("payload.AgentBrokerDecisionID = %d, want 0 (row was nil)", payload.AgentBrokerDecisionID)
	}
	if payload.CreatedAt != "" {
		t.Errorf("payload.CreatedAt = %q, want empty (row was nil)", payload.CreatedAt)
	}
	if payload.Reason != "default-chat-handle" {
		t.Errorf("payload.Reason = %q, want default-chat-handle (decision still fires on wire)", payload.Reason)
	}
	if payload.ModeSignal != agentbroker.ModeWork {
		t.Errorf("payload.ModeSignal = %q, want %q", payload.ModeSignal, agentbroker.ModeWork)
	}
}

// TestHashUserInput covers the SHA-256 projection used for
// user_input_hash. Empty input maps to the empty string (not the
// digest of empty); non-empty input produces a stable 64-char hex
// digest.
func TestHashUserInput(t *testing.T) {
	if got := hashUserInput(""); got != "" {
		t.Errorf("hashUserInput(\"\") = %q, want \"\" (empty maps to empty, not sha256(\"\"))", got)
	}
	got := hashUserInput("hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("hashUserInput(\"hello\") = %q, want %q", got, want)
	}
	// Stability across calls — same input hashes the same.
	if got2 := hashUserInput("hello"); got2 != got {
		t.Errorf("hashUserInput is not stable: %q != %q", got2, got)
	}
}
