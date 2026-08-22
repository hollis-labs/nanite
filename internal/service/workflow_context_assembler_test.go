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
// the actual resolution path rather than a mocked stand-in.
func newWorkflowContextAssemblerTestDeps(t *testing.T) (*store.Store, SessionService, AgentService, ContextService) {
	t.Helper()
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sessions := NewSessionService(SessionServiceDeps{
		Sessions:    s,
		Writer:      s,
		Agents:      s,
		AgentReader: s,
		Settings:    s,
	})
	agents := NewAgentService(AgentServiceConfig{
		Agents:   s,
		Writers:  s,
		Settings: s,
	})
	ctxSvc := NewContextService(ContextServiceConfig{Client: chat.NewContextClient(s)})
	return s, sessions, agents, ctxSvc
}

// Phase 0 item 21 ("Cut Modes, in full") retired this file's four original
// mode-addendum tests —
// TestWorkflowContextAssembler_AssembleContext_IncludesModeAddendumWhenAgentMatchesSessionBinding,
// TestWorkflowContextAssembler_AssembleContext_SkipsModeWhenBoundToADifferentAgent,
// TestWorkflowContextAssembler_AssembleContext_NoSessionBinding_NoModeAddendum, and
// TestWorkflowContextAssembler_AssembleContext_IncludesSessionLevelModeAddendum —
// along with resolveMode / resolveSessionMode in workflow_context_assembler.go
// (both deleted; see that file's updated doc comment). All four asserted
// something about a mode's PromptAddendum reaching (or being correctly
// excluded from) the assembled system prompt; Legacy Agent Mode and Session
// Mode are both gone, so there is no more mode addendum to include or
// exclude. TestWorkflowContextAssembler_AssembleContext_ResolvesAgentAndSession
// below replaces them with coverage of what AssembleContext still does:
// resolve the named session + agent and return a non-error system prompt
// carrying the agent's own SystemPrompt.

func TestWorkflowContextAssembler_AssembleContext_ResolvesAgentAndSession(t *testing.T) {
	s, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)

	agentProfile := &store.AgentProfile{
		ID:           "agent-workflow-1",
		Name:         "WorkflowAgent",
		Slug:         "workflow-agent",
		SystemPrompt: "SENTINEL_AGENT_SYSTEM_PROMPT",
		Status:       "active",
	}
	if err := s.CreateAgent(context.Background(), agentProfile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sess := &store.Session{ID: "sess-workflow-1", Title: "test"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.EnsureSessionAgent(context.Background(), sess.ID, agentProfile.ID, "default", true); err != nil {
		t.Fatalf("EnsureSessionAgent: %v", err)
	}

	asm := NewWorkflowContextAssembler(sessions, agents, s, ctxSvc)
	systemPrompt, _, err := asm.AssembleContext(context.Background(), sess.ID, agentProfile.ID)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	if !strings.Contains(systemPrompt, "SENTINEL_AGENT_SYSTEM_PROMPT") {
		t.Fatalf("expected assembled system prompt to include the agent's SystemPrompt, got: %q", systemPrompt)
	}
}

// TestWorkflowContextAssembler_AssembleContext_NoSessionBinding confirms
// AssembleContext still succeeds when the session has no primary-agent
// binding (session_agents row) — unrelated to mode, still real coverage.
func TestWorkflowContextAssembler_AssembleContext_NoSessionBinding(t *testing.T) {
	s, sessions, agents, ctxSvc := newWorkflowContextAssemblerTestDeps(t)

	agentProfile := &store.AgentProfile{ID: "agent-unbound", Name: "Unbound", Slug: "unbound", SystemPrompt: "unbound agent", Status: "active"}
	if err := s.CreateAgent(context.Background(), agentProfile); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	sess := &store.Session{ID: "sess-mode-3", Title: "test"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// No EnsureSessionAgent call — the session has no primary-agent binding.

	asm := NewWorkflowContextAssembler(sessions, agents, s, ctxSvc)
	if _, _, err := asm.AssembleContext(context.Background(), sess.ID, agentProfile.ID); err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
}
