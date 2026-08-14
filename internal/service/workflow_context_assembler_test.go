package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// newWorkflowContextAssemblerTestDeps stands up a real in-memory store plus
// SessionService/AgentService/ContextService backed by it — mirroring how
// container.go wires the same trio in production — so these tests exercise
// the actual resolution path (including the mode lookup) rather than a
// mocked stand-in.
func newWorkflowContextAssemblerTestDeps(t *testing.T) (*store.Store, SessionService, AgentService, ContextService) {
	t.Helper()
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sessions := NewSessionService(SessionServiceDeps{
		Sessions: s,
		Writer:   s,
		Agents:   s,
		Settings: s,
	})
	agents := NewAgentService(AgentServiceConfig{
		Agents:   s,
		Writers:  s,
		Settings: s,
	})
	ctxSvc := NewContextService(ContextServiceConfig{Client: chat.NewContextClient(s)})
	return s, sessions, agents, ctxSvc
}

func TestWorkflowContextAssembler_AssembleContext_IncludesModeAddendumWhenAgentMatchesSessionBinding(t *testing.T) {
	s, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)

	agentProfile := &store.AgentProfile{
		ID:           "agent-mode-1",
		Name:         "ModeAgent",
		Slug:         "mode-agent",
		SystemPrompt: "You are a test agent.",
		Status:       "active",
	}
	if err := s.CreateAgent(agentProfile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	agentMode := &store.AgentMode{
		ID:             "mode-row-1",
		AgentID:        agentProfile.ID,
		Slug:           "focused",
		Name:           "Focused",
		PromptAddendum: "SENTINEL_MODE_ADDENDUM",
	}
	if err := s.CreateAgentMode(agentMode); err != nil {
		t.Fatalf("CreateAgentMode: %v", err)
	}

	sess := &store.Session{ID: "sess-mode-1", Title: "test"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Bind the session's primary agent to agentProfile with the "focused"
	// mode — the same session_agents row ResolveForSession would read.
	if err := s.EnsureSessionAgent(sess.ID, agentProfile.ID, agentMode.Slug, true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	asm := NewWorkflowContextAssembler(sessions, agents, s, ctxSvc)
	systemPrompt, _, err := asm.AssembleContext(context.Background(), sess.ID, agentProfile.ID)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if !strings.Contains(systemPrompt, "SENTINEL_MODE_ADDENDUM") {
		t.Fatalf("expected assembled system prompt to include the session-bound mode's PromptAddendum, got: %q", systemPrompt)
	}
}

func TestWorkflowContextAssembler_AssembleContext_SkipsModeWhenBoundToADifferentAgent(t *testing.T) {
	s, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)

	boundAgent := &store.AgentProfile{ID: "agent-bound", Name: "Bound", Slug: "bound", SystemPrompt: "bound agent", Status: "active"}
	if err := s.CreateAgent(boundAgent); err != nil {
		t.Fatalf("CreateAgent(bound): %v", err)
	}
	boundMode := &store.AgentMode{ID: "mode-bound", AgentID: boundAgent.ID, Slug: "focused", Name: "Focused", PromptAddendum: "SENTINEL_SHOULD_NOT_APPEAR"}
	if err := s.CreateAgentMode(boundMode); err != nil {
		t.Fatalf("CreateAgentMode(bound): %v", err)
	}

	otherAgent := &store.AgentProfile{ID: "agent-other", Name: "Other", Slug: "other", SystemPrompt: "other agent", Status: "active"}
	if err := s.CreateAgent(otherAgent); err != nil {
		t.Fatalf("CreateAgent(other): %v", err)
	}

	sess := &store.Session{ID: "sess-mode-2", Title: "test"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// The session's primary agent is boundAgent, not otherAgent.
	if err := s.EnsureSessionAgent(sess.ID, boundAgent.ID, boundMode.Slug, true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	asm := NewWorkflowContextAssembler(sessions, agents, s, ctxSvc)
	// The workflow step asks for otherAgent's identity — a different agent
	// than the session's chat binding.
	systemPrompt, _, err := asm.AssembleContext(context.Background(), sess.ID, otherAgent.ID)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if strings.Contains(systemPrompt, "SENTINEL_SHOULD_NOT_APPEAR") {
		t.Fatalf("must not apply a different agent's session-bound mode addendum, got: %q", systemPrompt)
	}
}

func TestWorkflowContextAssembler_AssembleContext_NoSessionBinding_NoModeAddendum(t *testing.T) {
	s, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)

	agentProfile := &store.AgentProfile{ID: "agent-unbound", Name: "Unbound", Slug: "unbound", SystemPrompt: "unbound agent", Status: "active"}
	if err := s.CreateAgent(agentProfile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sess := &store.Session{ID: "sess-mode-3", Title: "test"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// No EnsureSessionAgent call — the session has no primary-agent binding.

	asm := NewWorkflowContextAssembler(sessions, agents, s, ctxSvc)
	if _, _, err := asm.AssembleContext(context.Background(), sess.ID, agentProfile.ID); err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
}
