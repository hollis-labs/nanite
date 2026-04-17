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

	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-1": {ID: "ag-1", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store: &recordingSessionStore{
			parents: map[string]*store.Session{
				"sess-parent": {ID: "sess-parent", WorkspaceID: "ws-1"},
			},
		},
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
}
