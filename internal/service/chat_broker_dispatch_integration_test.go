// CW-20260509-0048: integration coverage for the agent-broker SSE +
// event_log emission path. Pairs with the unit tests in
// chat_broker_dispatch_test.go (which use a stub store) by running the
// real *store.Store with migrations applied — so the agent_broker_decisions
// row, the event_log entry, AND the agent_broker_decision SSE event
// are all asserted end-to-end against the production storage layer.
//
// Acceptance criterion (per exec prompt §"Acceptance"):
//
//	"broker fires → both broker_decisions row AND event_log row exist;
//	 SSE channel emits broker_decision event with the documented payload."
//
// This test gates the row, the log entry, and the wire payload in one
// pass.

package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	agentbroker "github.com/hollis-labs/go-agent-broker/broker"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestAttemptBrokerDispatch_Integration_PersistsRowEventLogAndSSE wires
// chatServiceImpl against a real *store.Store (migrations applied) and
// asserts the three CW-20260509-0048 outputs in one shot:
//
//  1. agent_broker_decisions row exists with the expected columns.
//  2. event_log row of type "agent_broker_decision" exists with metadata
//     pointing at the row id (the durable companion entry).
//  3. SSE channel received the agent_broker_decision wire event with a
//     payload matching the documented schema (decision, reason,
//     confidence, mode_signal, scope_tier, reflex_id, plus row id +
//     created_at + session/turn ids).
func TestAttemptBrokerDispatch_Integration_PersistsRowEventLogAndSSE(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "broker-sse.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fb := &fakeAgentBroker{
		decision: agentbroker.Decision{
			AgentProfile: agentbroker.ProfileWorker,
			Reason:       "mode=work",
			Confidence:   1.0,
		},
	}
	tools := &fakeToolService{
		returnRes: &ToolResult{
			Output: `{"kind":"envelope","version":1,"type":"report-card","data":{"title":"Done"}}`,
		},
	}
	s := &chatServiceImpl{
		store:       st,
		agentBroker: fb,
		tools:       tools,
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.SetClassification(classify.TierMedium, classify.PatternSubagent)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(
		context.Background(),
		"sess-int-1", "turn-int-1",
		"/work refactor the authn module",
		"agent-int-1",
		ls, ch,
	)
	close(ch)

	// (1) Outcome surface.
	if !out.Consulted {
		t.Errorf("Consulted = false, want true")
	}
	if !out.Dispatched {
		t.Errorf("Dispatched = false, want true")
	}
	if !out.EmittedSSEDecision {
		t.Errorf("EmittedSSEDecision = false, want true")
	}
	if out.AgentBrokerDecisionID == 0 {
		t.Fatalf("AgentBrokerDecisionID = 0, want non-zero (row was persisted)")
	}

	// (2) agent_broker_decisions row — the durable telemetry record.
	rows, err := st.ListRecentAgentBrokerDecisions(10)
	if err != nil {
		t.Fatalf("ListRecentAgentBrokerDecisions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListRecent returned %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != out.AgentBrokerDecisionID {
		t.Errorf("row.ID = %d, want %d (matches outcome.AgentBrokerDecisionID)", row.ID, out.AgentBrokerDecisionID)
	}
	if row.SessionID != "sess-int-1" {
		t.Errorf("row.SessionID = %q, want sess-int-1", row.SessionID)
	}
	if row.TurnID != "turn-int-1" {
		t.Errorf("row.TurnID = %q, want turn-int-1", row.TurnID)
	}
	if row.Decision != agentbroker.ProfileWorker {
		t.Errorf("row.Decision = %q, want %q", row.Decision, agentbroker.ProfileWorker)
	}
	if row.Reason != "mode=work" {
		t.Errorf("row.Reason = %q, want mode=work", row.Reason)
	}
	if row.Confidence != 1.0 {
		t.Errorf("row.Confidence = %g, want 1.0", row.Confidence)
	}
	if row.ModeSignal != agentbroker.ModeWork {
		t.Errorf("row.ModeSignal = %q, want %q", row.ModeSignal, agentbroker.ModeWork)
	}
	if row.ScopeTier != classify.TierMedium.String() {
		t.Errorf("row.ScopeTier = %q, want %q", row.ScopeTier, classify.TierMedium.String())
	}
	if row.UserInputHash == "" {
		t.Errorf("row.UserInputHash is empty; SHA-256 expected")
	}
	if row.CreatedAt == "" {
		t.Errorf("row.CreatedAt is empty; SQLite default expected")
	}

	// (3) event_log entry — the operational/audit companion row.
	events, err := st.ListEvents("", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var brokerEvent *store.EventLog
	for i := range events {
		if events[i].EventType == "agent_broker_decision" {
			brokerEvent = &events[i]
			break
		}
	}
	if brokerEvent == nil {
		t.Fatalf("event_log has no agent_broker_decision row; got %d events of other types", len(events))
	}
	if brokerEvent.SessionID != "sess-int-1" {
		t.Errorf("event_log.SessionID = %q, want sess-int-1", brokerEvent.SessionID)
	}
	if brokerEvent.Category != "info" {
		t.Errorf("event_log.Category = %q, want info", brokerEvent.Category)
	}
	if brokerEvent.Detail != "mode=work" {
		t.Errorf("event_log.Detail = %q, want mode=work (decision reason)", brokerEvent.Detail)
	}
	// metadata is a JSON blob with the row id + decision fields. Verify
	// the parser-friendly shape (a sloppy substring check is fine here —
	// the unit tests already cover the exact format).
	var meta map[string]any
	if err := json.Unmarshal([]byte(brokerEvent.Metadata), &meta); err != nil {
		t.Fatalf("event_log metadata not JSON: %v\nblob: %s", err, brokerEvent.Metadata)
	}
	if got, want := meta["agent_broker_decision_id"], float64(row.ID); got != want {
		t.Errorf("event_log metadata.agent_broker_decision_id = %v, want %v", got, want)
	}
	if got, want := meta["agent_profile"], agentbroker.ProfileWorker; got != want {
		t.Errorf("event_log metadata.agent_profile = %v, want %q", got, want)
	}

	// (4) SSE wire event — the live signal for FE inspectors / audit.
	sseEvents := collect(ch)
	if len(sseEvents) != 2 {
		t.Fatalf("collected %d SSE events, want 2 (agent_broker_decision + plugin_envelope)", len(sseEvents))
	}
	wireEvent := sseEvents[0]
	if wireEvent.Type != agentBrokerDecisionEventType {
		t.Fatalf("SSE event[0] Type = %q, want %q", wireEvent.Type, agentBrokerDecisionEventType)
	}
	var payload agentBrokerDecisionPayload
	if err := json.Unmarshal([]byte(wireEvent.Data), &payload); err != nil {
		t.Fatalf("unmarshal SSE payload: %v\ndata: %s", err, wireEvent.Data)
	}
	if payload.AgentBrokerDecisionID != row.ID {
		t.Errorf("SSE payload.AgentBrokerDecisionID = %d, want %d", payload.AgentBrokerDecisionID, row.ID)
	}
	if payload.SessionID != "sess-int-1" {
		t.Errorf("SSE payload.SessionID = %q, want sess-int-1", payload.SessionID)
	}
	if payload.TurnID != "turn-int-1" {
		t.Errorf("SSE payload.TurnID = %q, want turn-int-1", payload.TurnID)
	}
	if payload.Decision != agentbroker.ProfileWorker {
		t.Errorf("SSE payload.Decision = %q, want %q", payload.Decision, agentbroker.ProfileWorker)
	}
	if payload.Reason != "mode=work" {
		t.Errorf("SSE payload.Reason = %q, want mode=work", payload.Reason)
	}
	if payload.Confidence != 1.0 {
		t.Errorf("SSE payload.Confidence = %g, want 1.0", payload.Confidence)
	}
	if payload.ModeSignal != agentbroker.ModeWork {
		t.Errorf("SSE payload.ModeSignal = %q, want %q", payload.ModeSignal, agentbroker.ModeWork)
	}
	if payload.ScopeTier != classify.TierMedium.String() {
		t.Errorf("SSE payload.ScopeTier = %q, want %q", payload.ScopeTier, classify.TierMedium.String())
	}
	if payload.CreatedAt != row.CreatedAt {
		t.Errorf("SSE payload.CreatedAt = %q, want %q (matches row.CreatedAt)", payload.CreatedAt, row.CreatedAt)
	}

	// Order assertion: the wire event lands BEFORE the plugin_envelope so
	// consumers that correlate by (session_id, turn_id) can attach the
	// envelope to the same broker decision.
	if sseEvents[1].Type != "plugin_envelope" {
		t.Errorf("SSE event[1] Type = %q, want plugin_envelope", sseEvents[1].Type)
	}
}

// TestAttemptBrokerDispatch_Integration_ChatDirectFiresWireEvent covers
// the chat-direct branch end-to-end with a real store: the row, the
// event_log entry, and the SSE event all fire. No plugin_envelope —
// no dispatch happened.
func TestAttemptBrokerDispatch_Integration_ChatDirectFiresWireEvent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "broker-sse.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fb := &fakeAgentBroker{
		decision: agentbroker.Decision{
			AgentProfile: agentbroker.ProfileChat,
			Reason:       "default-chat-handle",
			Confidence:   0,
		},
	}
	s := &chatServiceImpl{
		store:       st,
		agentBroker: fb,
		tools:       &fakeToolService{},
	}
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	ch := make(chan chat.StreamEvent, 4)
	out := s.attemptBrokerDispatch(
		context.Background(),
		"sess-chat-1", "turn-chat-1",
		"hi there",
		"agent-chat-1",
		ls, ch,
	)
	close(ch)

	if out.Dispatched {
		t.Errorf("Dispatched = true, want false on chat-direct")
	}
	if out.EmittedEnvelope {
		t.Errorf("EmittedEnvelope = true, want false on chat-direct")
	}
	if !out.EmittedSSEDecision {
		t.Errorf("EmittedSSEDecision = false, want true (chat-direct still fires the wire event)")
	}

	// Row + event_log + wire event all present.
	rows, err := st.ListRecentAgentBrokerDecisions(10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListRecent: %v, len=%d", err, len(rows))
	}

	events, err := st.ListEvents("", 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	hasBrokerLog := false
	for _, e := range events {
		if e.EventType == "agent_broker_decision" {
			hasBrokerLog = true
			break
		}
	}
	if !hasBrokerLog {
		t.Errorf("event_log missing agent_broker_decision row for chat-direct")
	}

	sseEvents := collect(ch)
	if len(sseEvents) != 1 {
		t.Fatalf("collected %d SSE events on chat-direct, want 1", len(sseEvents))
	}
	if sseEvents[0].Type != agentBrokerDecisionEventType {
		t.Errorf("SSE event Type = %q, want %q", sseEvents[0].Type, agentBrokerDecisionEventType)
	}
	var payload agentBrokerDecisionPayload
	if err := json.Unmarshal([]byte(sseEvents[0].Data), &payload); err != nil {
		t.Fatalf("unmarshal SSE payload: %v", err)
	}
	if payload.Decision != "" {
		t.Errorf("SSE payload.Decision = %q, want empty (chat-direct is ProfileChat)", payload.Decision)
	}
	if payload.Reason != "default-chat-handle" {
		t.Errorf("SSE payload.Reason = %q, want default-chat-handle", payload.Reason)
	}
}
