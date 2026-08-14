package service

import (
	"context"
	"fmt"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/nanite/internal/store"
)

// WorkflowContextAssembler resolves a (sessionID, agentID) pair into the
// same system prompt + message history a Chat/GUI/CLI turn receives.
// A narrow interface (not the full ContextService) so ExecuteLLMStep
// depends on only what it needs, and tests can supply a fake instead of
// standing up a real store + session/agent services.
type WorkflowContextAssembler interface {
	AssembleContext(ctx context.Context, sessionID, agentID string) (systemPrompt string, messages []llmtypes.ChatMessage, err error)
}

// contextServiceWorkflowAssembler is the production WorkflowContextAssembler.
// It resolves the store rows a workflow step only carries IDs for (session,
// agent, mode, workspace — mirroring the resolution generateResponse
// performs via SessionService.Get / AgentService.Get / store.GetWorkspace,
// plus a read-only mode lookup — see resolveMode) and delegates the actual
// assembly to the existing ContextService.AssembleContext. Memory recall is
// not a separate step here — contextbroker.MemorySource is one of the
// ContextBroker's default sources, so it rides inside AssembleContext's
// enrichment automatically.
type contextServiceWorkflowAssembler struct {
	sessions SessionService
	agents   AgentService
	store    *store.Store
	context  ContextService
}

// NewWorkflowContextAssembler constructs the production WorkflowContextAssembler.
func NewWorkflowContextAssembler(sessions SessionService, agents AgentService, st *store.Store, ctxSvc ContextService) WorkflowContextAssembler {
	return &contextServiceWorkflowAssembler{sessions: sessions, agents: agents, store: st, context: ctxSvc}
}

func (a *contextServiceWorkflowAssembler) AssembleContext(ctx context.Context, sessionID, agentID string) (string, []llmtypes.ChatMessage, error) {
	if a.sessions == nil || a.agents == nil || a.store == nil || a.context == nil {
		return "", nil, fmt.Errorf("workflow: context assembler is not fully configured")
	}
	if sessionID == "" || agentID == "" {
		return "", nil, fmt.Errorf("workflow: context assembly requires both session_id and agent_id")
	}

	session, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return "", nil, fmt.Errorf("workflow: context assembly: load session %q: %w", sessionID, err)
	}
	agent, err := a.agents.Get(ctx, agentID)
	if err != nil {
		return "", nil, fmt.Errorf("workflow: context assembly: load agent %q: %w", agentID, err)
	}

	var workspace *store.Workspace
	if session.WorkspaceID != "" {
		workspace, _ = a.store.GetWorkspace(session.WorkspaceID)
	}

	mode := a.resolveMode(ctx, sessionID, agentID)

	return a.context.AssembleContext(ctx, session, agent, mode, workspace)
}

// resolveMode looks up the session's bound mode (session_agents.mode) via
// the read-only store.GetSessionPrimaryAgent — deliberately not
// AgentService.ResolveForSession, which auto-assigns a default agent to an
// unbound session as a side effect; a workflow step's context-assembly
// lookup must not silently rebind a chat session's primary agent.
//
// The resolved mode is only applied when the session's primary-agent
// binding's AgentID matches this call's agentID — a workflow step can name
// an agent identity that differs from the session's chat binding, and
// there's no principled way to know which of that other agent's modes
// should apply, so the safe default is no mode (matching pre-opt-in
// behavior) rather than guessing.
//
// Any lookup failure (no binding row, mode slug not found on the agent)
// degrades to nil — mode-addendum content is enrichment, not identity; the
// turn still proceeds on the agent's base prompt.
func (a *contextServiceWorkflowAssembler) resolveMode(ctx context.Context, sessionID, agentID string) *store.AgentMode {
	sa, err := a.store.GetSessionPrimaryAgent(sessionID)
	if err != nil || sa == nil || sa.AgentID != agentID || sa.Mode == "" {
		return nil
	}
	modes, err := a.agents.ListModes(ctx, agentID)
	if err != nil {
		return nil
	}
	for i := range modes {
		if modes[i].Slug == sa.Mode {
			return &modes[i]
		}
	}
	return nil
}
