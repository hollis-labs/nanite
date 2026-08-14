package service

import (
	"context"
	"fmt"
	"log/slog"

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
// agent, mode, workspace, session-level mode — mirroring the resolution
// generateResponse performs via SessionService.Get / AgentService.Get /
// store.GetWorkspace / store.GetMode, plus a read-only agent-mode lookup —
// see resolveMode) and delegates the actual assembly to
// ContextService.AssembleSlots — the same slot-based method Chat/GUI/CLI
// turns call (chat_generate.go's assembleTurnContext) — collapsing its
// SlotAssemblyResult down to the flat systemPrompt + messages shape this
// interface returns. Memory recall is not a separate step here —
// contextbroker.MemorySource is one of the ContextBroker's default sources,
// so it rides inside AssembleSlots' enrichment automatically.
//
// tools, extraSystemPrefix, and toolsLazyHint are turn-specific concerns
// AssembleSlots also accepts (selected tool defs for the Tools slot, a
// dynamic per-turn prefix, and the lazy-tool-partition pointer) that have no
// analogue at this call's (sessionID, agentID) granularity — a workflow
// step's tool surface is already capability-restricted and passed straight
// to the provider by workflow_step_executor.go, not selected here. They are
// passed as zero values; every other slot (Universal, Rules, Permissions,
// Workspace, session-level Mode, UserContext) is sourced independently of
// those three and so is unaffected.
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
	sessionMode := a.resolveSessionMode(session)

	// providerWindowSize=0 falls back to ctxpkg.DefaultContextWindowSize
	// (ctxpkg.NewContextWindow) — this call site has no provider/model to
	// look up a real budget from, matching chat_generate.go's own
	// contextWindowSize "returns 0 on miss" fallback behavior.
	result, err := a.context.AssembleSlots(ctx, session, agent, mode, workspace, nil, "", 0, sessionMode, "")
	if err != nil {
		return "", nil, fmt.Errorf("workflow: context assembly: assemble slots: %w", err)
	}
	return result.SystemPrompt, result.Messages, nil
}

// resolveSessionMode looks up the session-level mode pointer
// (session.CurrentModeID via the read-only store.GetMode), mirroring the B1
// resolution chat_generate.go performs before calling AssembleSlots. This is
// a session-scoped mode-switch pointer distinct from resolveMode's agent-mode
// lookup below — AssembleSlots takes both independently (sessionMode feeds
// the Mode slot; the agent mode returned by resolveMode still feeds the
// Agent slot).
//
// Best-effort: a missing/dangling FK or store error degrades to nil rather
// than failing the turn — mode-addendum content is enrichment, not identity.
func (a *contextServiceWorkflowAssembler) resolveSessionMode(session *store.Session) *store.Mode {
	if session.CurrentModeID == nil || *session.CurrentModeID == "" {
		return nil
	}
	m, err := a.store.GetMode(*session.CurrentModeID)
	if err != nil {
		slog.Warn("workflow: GetMode failed for session-mode pointer",
			"session_id", session.ID, "mode_id", *session.CurrentModeID, "err", err)
		return nil
	}
	return m
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
