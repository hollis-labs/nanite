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
// agent, workspace — mirroring the resolution generateResponse performs via
// SessionService.Get / AgentService.Get / store.GetWorkspace) and delegates
// the actual assembly to ContextService.AssembleSlots — the same slot-based
// method Chat/GUI/CLI turns call (chat_generate.go's assembleTurnContext) —
// collapsing its SlotAssemblyResult down to the flat systemPrompt + messages
// shape this interface returns. Memory recall is not a separate step here —
// contextbroker.MemorySource is one of the ContextBroker's default sources,
// so it rides inside AssembleSlots' enrichment automatically.
//
// Phase 0 item 21 ("Cut Modes, in full") removed this file's resolveMode
// (agent-mode lookup via session_agents.mode) and resolveSessionMode
// (session.CurrentModeID -> store.GetMode) helpers along with the
// AssembleSlots arguments they fed — both Legacy Agent Mode and Session Mode
// are gone.
//
// tools, extraSystemPrefix, and toolsLazyHint are turn-specific concerns
// AssembleSlots also accepts (selected tool defs for the Tools slot, a
// dynamic per-turn prefix, and the lazy-tool-partition pointer) that have no
// analogue at this call's (sessionID, agentID) granularity — a workflow
// step's tool surface is already capability-restricted and passed straight
// to the provider by workflow_step_executor.go, not selected here. They are
// passed as zero values; every other slot (Universal, Rules, Permissions,
// Workspace, UserContext) is sourced independently of those three and so is
// unaffected.
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

	// providerWindowSize=0 falls back to ctxpkg.DefaultContextWindowSize
	// (ctxpkg.NewContextWindow) — this call site has no provider/model to
	// look up a real budget from, matching chat_generate.go's own
	// contextWindowSize "returns 0 on miss" fallback behavior.
	result, err := a.context.AssembleSlots(ctx, session, agent, workspace, nil, "", 0, "")
	if err != nil {
		return "", nil, fmt.Errorf("workflow: context assembly: assemble slots: %w", err)
	}
	return result.SystemPrompt, result.Messages, nil
}
