package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// AgentMembershipService owns an agent's memberships: which agents take part
// in a session (session_agents) and which projects an agent is scoped to
// (agent_projects). It is separate from AgentService so ChatService, which
// depends on AgentService for resolution, does not also carry membership
// CRUD it never uses.
type AgentMembershipService struct {
	agents  AgentReader
	writers AgentWriter
}

func NewAgentMembershipService(agents AgentReader, writers AgentWriter) *AgentMembershipService {
	return &AgentMembershipService{agents: agents, writers: writers}
}

// ListSessionAgents returns every agent bound to a session, oldest first.
func (s *AgentMembershipService) ListSessionAgents(ctx context.Context, sessionID string) ([]store.SessionAgent, error) {
	return s.agents.ListSessionAgents(ctx, sessionID)
}

// GetSessionPrimaryAgent returns the session's primary agent binding. It
// returns an error when the session has none.
func (s *AgentMembershipService) GetSessionPrimaryAgent(ctx context.Context, sessionID string) (*store.SessionAgent, error) {
	return s.agents.GetSessionPrimaryAgent(ctx, sessionID)
}

// SetSessionAgent binds an agent to a session in the given mode. A session
// has at most one primary agent: setting a new primary first demotes the
// current one (keeping its mode), then upserts the new binding. The two
// writes are separate statements, not one transaction.
//
// previousPrimaryID is the agent that was primary before the call, or ""
// when primary is false or the session had none. Callers compare it with
// agentID to decide whether the primary actually changed.
func (s *AgentMembershipService) SetSessionAgent(ctx context.Context, sessionID, agentID, mode string, primary bool) (previousPrimaryID string, err error) {
	if primary {
		if cur, err := s.agents.GetSessionPrimaryAgent(ctx, sessionID); err == nil {
			previousPrimaryID = cur.AgentID
			_ = s.writers.EnsureSessionAgent(ctx, sessionID, cur.AgentID, cur.Mode, false)
		}
	}
	if err := s.writers.EnsureSessionAgent(ctx, sessionID, agentID, mode, primary); err != nil {
		return previousPrimaryID, err
	}
	return previousPrimaryID, nil
}

// RemoveSessionAgent unbinds an agent from a session. It returns an error
// when no such binding exists.
func (s *AgentMembershipService) RemoveSessionAgent(ctx context.Context, sessionID, agentID string) error {
	return s.writers.DeleteSessionAgent(ctx, sessionID, agentID)
}

// ListAgentProjects returns the projects an agent is scoped to.
func (s *AgentMembershipService) ListAgentProjects(ctx context.Context, agentID string) ([]store.Project, error) {
	return s.agents.ListAgentProjects(ctx, agentID)
}

// AddAgentProject scopes an agent to a project.
func (s *AgentMembershipService) AddAgentProject(ctx context.Context, agentID, projectID string) error {
	return s.writers.AddAgentProject(ctx, agentID, projectID)
}

// RemoveAgentProject removes an agent's scope to a project.
func (s *AgentMembershipService) RemoveAgentProject(ctx context.Context, agentID, projectID string) error {
	return s.writers.RemoveAgentProject(ctx, agentID, projectID)
}

// ListProjectAgents returns the agents scoped to a project.
func (s *AgentMembershipService) ListProjectAgents(ctx context.Context, projectID string) ([]store.AgentProfile, error) {
	return s.agents.ListProjectAgents(ctx, projectID)
}
