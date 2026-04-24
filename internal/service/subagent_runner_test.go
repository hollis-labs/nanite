package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

func TestDrainCapture_DeltasConcatenateIntoSummary(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "Hello "}
	ch <- chat.StreamEvent{Type: "delta", Content: "world"}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "Hello world" {
		t.Errorf("summary = %q, want %q", summary, "Hello world")
	}
	if envelope != "{}" {
		t.Errorf("envelope = %q, want \"{}\" (no envelope events)", envelope)
	}
}

func TestDrainCapture_CapturesEnvelopeAsResultJSON(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "summary text"}
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"finding":"x"}`}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "summary text" {
		t.Errorf("summary = %q", summary)
	}
	if envelope != `{"finding":"x"}` {
		t.Errorf("envelope = %q", envelope)
	}
}

func TestDrainCapture_LastEnvelopeWins(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"first":1}`}
	ch <- chat.StreamEvent{Type: "delta", Content: "between"}
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"second":2}`}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	_, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if envelope != `{"second":2}` {
		t.Errorf("envelope = %q, want last-wins", envelope)
	}
}

// TestDrainCapture_CapturesStreamEndEnvelope verifies that the
// aggregated envelope JSON that production generateResponse attaches
// to stream_end.Envelope (chat_generate.go:837) is captured as
// Result.ResultJSON. Without this, ResultJSON stays "{}" when
// plugin_envelope routing doesn't fire mid-stream.
func TestDrainCapture_CapturesStreamEndEnvelope(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "delta", Content: "final answer"}
	ch <- chat.StreamEvent{Type: "stream_end", Envelope: `[{"type":"x"}]`}
	close(ch)

	summary, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "final answer" {
		t.Errorf("summary = %q", summary)
	}
	if envelope != `[{"type":"x"}]` {
		t.Errorf("envelope = %q, want stream_end.Envelope content", envelope)
	}
}

// TestDrainCapture_StreamEndEnvelopeWinsOverMidStream verifies that
// when both a mid-stream plugin_envelope and a terminal stream_end
// carry an Envelope, stream_end wins — it's the aggregated truth that
// production generateResponse emits last (chat_generate.go:837).
func TestDrainCapture_StreamEndEnvelopeWinsOverMidStream(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"mid":1}`}
	ch <- chat.StreamEvent{Type: "stream_end", Envelope: `{"final":2}`}
	close(ch)

	_, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if envelope != `{"final":2}` {
		t.Errorf("envelope = %q, want stream_end.Envelope to win", envelope)
	}
}

func TestDrainCapture_EmptySummaryFallback(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, _, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "" {
		t.Errorf("summary = %q, want empty (fallback applied by caller, not drainCapture)", summary)
	}
}

func TestDrainCapture_ErrorEventTerminatesDrain(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "partial"}
	ch <- chat.StreamEvent{Type: "error", Error: "provider exploded"}
	ch <- chat.StreamEvent{Type: "delta", Content: "should not appear"}
	close(ch)

	_, _, err := drainCapture(ch)
	if err == nil {
		t.Fatal("expected error from drainCapture, got nil")
	}
	if !errors.Is(err, errStreamFailure) && err.Error() == "" {
		t.Errorf("error = %v", err)
	}
}

func TestDrainCapture_IgnoresOtherEventTypes(t *testing.T) {
	ch := make(chan chat.StreamEvent, 16)
	ch <- chat.StreamEvent{Type: "stream_start"}
	ch <- chat.StreamEvent{Type: "tool_call", Tool: "x"}
	ch <- chat.StreamEvent{Type: "tool_result", Tool: "x", Summary: "ok"}
	ch <- chat.StreamEvent{Type: "status", Content: "thinking"}
	ch <- chat.StreamEvent{Type: "delta", Content: "real text"}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "real text" {
		t.Errorf("summary = %q, want only delta content", summary)
	}
	if envelope != "{}" {
		t.Errorf("envelope = %q", envelope)
	}
}

// stubAgentReaderForRunner satisfies agentSlugResolver for ChatRunner tests.
type stubAgentReaderForRunner struct {
	agents map[string]*store.AgentProfile
}

func (s *stubAgentReaderForRunner) GetAgentBySlug(slug string) (*store.AgentProfile, error) {
	a, ok := s.agents[slug]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", slug)
	}
	return a, nil
}

func TestChatRunner_ResolveRoleFails(t *testing.T) {
	r := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{}},
	}
	_, err := r.Run(context.Background(), &subagent.Run{
		ID:              "run-1",
		Role:            "nonexistent",
		ParentSessionID: "sess-1",
		Prompt:          "p",
	})
	if err == nil {
		t.Fatal("expected error for unknown role, got nil")
	}
	if !errors.Is(err, errRoleResolveFailed) && !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error = %v, want wrapped role-not-found", err)
	}
}

// recordingSessionStore implements sessionStoreForRunner in memory.
type recordingSessionStore struct {
	created  []*store.Session
	parents  map[string]*store.Session
	bindings []sessionAgentBinding
	messages []*store.Message
}

type sessionAgentBinding struct {
	sessionID, agentID, mode string
	isPrimary                bool
}

func (r *recordingSessionStore) CreateSession(s *store.Session) error {
	r.created = append(r.created, s)
	return nil
}

func (r *recordingSessionStore) GetSession(id string) (*store.Session, error) {
	if s, ok := r.parents[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("session not found: %s", id)
}

func (r *recordingSessionStore) EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error {
	r.bindings = append(r.bindings, sessionAgentBinding{sessionID, agentID, mode, isPrimary})
	return nil
}

func (r *recordingSessionStore) CreateMessage(m *store.Message) error {
	r.messages = append(r.messages, m)
	return nil
}

// fakeChatService is a chatInvoker that emits canned events.
type fakeChatService struct {
	events []chat.StreamEvent
	called int
}

func (f *fakeChatService) generateResponse(_ context.Context, _, _, _ string, ch chan chat.StreamEvent) {
	defer close(ch)
	f.called++
	for _, e := range f.events {
		ch <- e
	}
}

func TestChatRunner_DrainsSummaryAndEnvelope(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "Project X has "},
		{Type: "delta", Content: "3 open tasks."},
		{Type: "plugin_envelope", Envelope: `{"open_tasks":3}`},
		{Type: "stream_end"},
	}}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-1": {ID: "ag-1", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-1", Role: "role-1", ParentSessionID: "sess-parent", Prompt: "summarize",
	}
	result, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Summary != "Project X has 3 open tasks." {
		t.Errorf("Summary = %q", result.Summary)
	}
	if result.ResultJSON != `{"open_tasks":3}` {
		t.Errorf("ResultJSON = %q", result.ResultJSON)
	}
	if fake.called != 1 {
		t.Errorf("generateResponse called %d times, want 1", fake.called)
	}
	if run.ChildSessionID == "" {
		t.Error("ChildSessionID not set on run")
	}

	// User message must be persisted before invokeChat so the provider
	// context assembly (ListMessages) sees the prompt. Without this the
	// LLM receives an empty conversation and ignores run.Prompt.
	if len(st.messages) != 1 {
		t.Fatalf("messages created = %d, want 1", len(st.messages))
	}
	userMsg := st.messages[0]
	if userMsg.SessionID != run.ChildSessionID {
		t.Errorf("user message SessionID = %q, want child %q", userMsg.SessionID, run.ChildSessionID)
	}
	if userMsg.Role != "user" {
		t.Errorf("user message Role = %q, want \"user\"", userMsg.Role)
	}
	if userMsg.Content != "summarize" {
		t.Errorf("user message Content = %q, want %q", userMsg.Content, "summarize")
	}
}

// TestChatRunner_UserMessageCreationError verifies that a failure to
// persist the user message aborts Run before invoking the chat loop —
// otherwise the provider would see an empty conversation and silently
// produce wrong output.
func TestChatRunner_UserMessageCreationError(t *testing.T) {
	fake := &fakeChatService{}
	st := &messageFailingStore{
		recordingSessionStore: recordingSessionStore{
			parents: map[string]*store.Session{
				"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
			},
		},
		createMessageErr: errors.New("db write failed"),
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-1": {ID: "ag-1"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}
	_, err := runner.Run(context.Background(), &subagent.Run{
		ID: "run-1", Role: "role-1", ParentSessionID: "sess-parent", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected error from CreateMessage failure")
	}
	if !strings.Contains(err.Error(), "create user message") {
		t.Errorf("error = %v, want create-user-message wrap", err)
	}
	if fake.called != 0 {
		t.Errorf("generateResponse called %d times, want 0 (should abort before chat invocation)", fake.called)
	}
}

// messageFailingStore extends recordingSessionStore with an injected
// CreateMessage error.
type messageFailingStore struct {
	recordingSessionStore
	createMessageErr error
}

func (m *messageFailingStore) CreateMessage(msg *store.Message) error {
	if m.createMessageErr != nil {
		return m.createMessageErr
	}
	return m.recordingSessionStore.CreateMessage(msg)
}

// TestChatRunner_ProviderOverride_UsesFallbackWhenEmpty verifies that
// when run.Provider is empty, createChildSession uses agent.DefaultProvider.
func TestChatRunner_ProviderOverride_UsesFallbackWhenEmpty(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "done"},
		{Type: "stream_end"},
	}}
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-a": {ID: "ag-a", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-empty-prov", Role: "role-a", ParentSessionID: "sess-p", Prompt: "go",
		// Provider intentionally empty — should fall back to agent default
	}
	_, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected 1 child session created, got %d", len(st.created))
	}
	if st.created[0].Provider != "anthropic" {
		t.Errorf("child session Provider = %q, want %q (agent default)", st.created[0].Provider, "anthropic")
	}
}

// TestChatRunner_ProviderOverride_UsesOverrideWhenSet verifies that a
// non-empty run.Provider is used in preference to agent.DefaultProvider.
func TestChatRunner_ProviderOverride_UsesOverrideWhenSet(t *testing.T) {
	fake := &fakeChatService{events: []chat.StreamEvent{
		{Type: "delta", Content: "done"},
		{Type: "stream_end"},
	}}
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p", WorkspaceID: "ws-1"},
		},
	}
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-a": {ID: "ag-a", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   fake,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-override", Role: "role-a", ParentSessionID: "sess-p", Prompt: "go",
		Provider: "pty-claude", // override: heavy-execution worker
	}
	_, err := runner.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected 1 child session created, got %d", len(st.created))
	}
	if st.created[0].Provider != "pty-claude" {
		t.Errorf("child session Provider = %q, want %q (override)", st.created[0].Provider, "pty-claude")
	}
}
