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
// agent, workspace — the same resolution generateResponse performs via
// SessionService.Get / AgentService.Get / store.GetWorkspace) and delegates
// the actual assembly to the existing ContextService.AssembleContext. Memory
// recall is not a separate step here — contextbroker.MemorySource is one of
// the ContextBroker's default sources, so it rides inside AssembleContext's
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

	return a.context.AssembleContext(ctx, session, agent, nil, workspace)
}
