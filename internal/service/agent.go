package service

import (
	"context"
	"fmt"
	"log"

	"github.com/hollis-labs/conduit/internal/store"
)

// AgentService encapsulates agent profile CRUD and the session-agent
// resolution fallback chain that was previously inlined in generateResponse.
type AgentService interface {
	Get(ctx context.Context, id string) (*store.AgentProfile, error)
	GetBySlug(ctx context.Context, slug string) (*store.AgentProfile, error)
	List(ctx context.Context) ([]store.AgentProfile, error)
	Create(ctx context.Context, agent *store.AgentProfile) error
	Update(ctx context.Context, agent *store.AgentProfile) error
	Delete(ctx context.Context, id string) error
	ResolveForSession(ctx context.Context, sessionID string) (agent *store.AgentProfile, mode *store.AgentMode, err error)
	ListModes(ctx context.Context, agentID string) ([]store.AgentMode, error)
}

// defaultFallbackAgent is the agent ID used when no session-agent binding
// or user-settings default exists. Matches the existing hardcoded "mentat-001".
const defaultFallbackAgent = "mentat-001"

// defaultFallbackSlug is the slug-based fallback when neither the session
// agent nor the default agent ID can be found. Matches existing behavior.
const defaultFallbackSlug = "mentat"

// agentServiceImpl implements AgentService backed by the store sub-interfaces.
type agentServiceImpl struct {
	agents   AgentReader
	writers  AgentWriter
	settings SettingsStore
	events   EventEmitter
}

// AgentServiceConfig holds dependencies for constructing an AgentService.
type AgentServiceConfig struct {
	Agents   AgentReader
	Writers  AgentWriter
	Settings SettingsStore
	Events   EventEmitter
}

// NewAgentService creates an AgentService from its required dependencies.
func NewAgentService(cfg AgentServiceConfig) AgentService {
	return &agentServiceImpl{
		agents:   cfg.Agents,
		writers:  cfg.Writers,
		settings: cfg.Settings,
		events:   cfg.Events,
	}
}

func (s *agentServiceImpl) Get(_ context.Context, id string) (*store.AgentProfile, error) {
	return s.agents.GetAgent(id)
}

func (s *agentServiceImpl) GetBySlug(_ context.Context, slug string) (*store.AgentProfile, error) {
	return s.agents.GetAgentBySlug(slug)
}

func (s *agentServiceImpl) List(_ context.Context) ([]store.AgentProfile, error) {
	return s.agents.ListAgents()
}

func (s *agentServiceImpl) Create(_ context.Context, agent *store.AgentProfile) error {
	return s.writers.CreateAgent(agent)
}

func (s *agentServiceImpl) Update(_ context.Context, agent *store.AgentProfile) error {
	return s.writers.UpdateAgent(agent)
}

func (s *agentServiceImpl) Delete(_ context.Context, id string) error {
	return s.writers.DeleteAgent(id)
}

func (s *agentServiceImpl) ListModes(_ context.Context, agentID string) ([]store.AgentMode, error) {
	return s.agents.ListAgentModes(agentID)
}

// ResolveForSession implements the agent resolution fallback chain previously
// inlined in engine.go generateResponse (lines 446-495):
//
//  1. Look up the session's primary agent binding (session_agents table).
//  2. If no binding exists, check user_settings.default_agent.
//  3. If still empty, fall back to the hardcoded default ("mentat-001").
//  4. Auto-assign the resolved agent to the session.
//  5. Load the agent profile by ID, falling back to slug lookup.
//  6. Reject disabled agents.
//  7. Load the agent mode (falling back to empty mode on miss).
func (s *agentServiceImpl) ResolveForSession(ctx context.Context, sessionID string) (*store.AgentProfile, *store.AgentMode, error) {
	agentID, modeName, autoAssigned := s.resolveBinding(sessionID)

	// Load the agent profile. Fall back to slug lookup for compat.
	agent, err := s.agents.GetAgent(agentID)
	if err != nil {
		agent, err = s.agents.GetAgentBySlug(defaultFallbackSlug)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve agent for session %s: %w", sessionID, err)
		}
	}

	if agent.Status == "disabled" {
		return nil, nil, fmt.Errorf("agent %q is disabled", agent.Name)
	}

	// Auto-assign to session if we had to fall back.
	if autoAssigned {
		if err := s.writers.EnsureSessionAgent(sessionID, agent.ID, modeName, true); err != nil {
			log.Printf("agent-service: failed to auto-assign agent %s to session %s: %v", agent.ID, sessionID, err)
		}
		if s.events != nil {
			s.events.EmitAgentAssigned(ctx, sessionID, agent.ID, modeName)
		}
	}

	// Load mode — fall back to empty mode on miss.
	mode, err := s.agents.GetAgentMode(agent.ID, modeName)
	if err != nil {
		log.Printf("agent-service: could not load mode %s/%s: %v (using base prompt)", agent.ID, modeName, err)
		mode = &store.AgentMode{}
	}

	return agent, mode, nil
}

// resolveBinding determines the agent ID and mode for a session.
// Returns the resolved agentID, modeName, and whether auto-assignment is needed.
func (s *agentServiceImpl) resolveBinding(sessionID string) (agentID, modeName string, autoAssigned bool) {
	sa, err := s.agents.GetSessionPrimaryAgent(sessionID)
	if err == nil {
		return sa.AgentID, sa.Mode, false
	}

	// No binding — check user settings for a configured default.
	if s.settings != nil {
		if us, err := s.settings.GetUserSettings(); err == nil && us.DefaultAgent != "" {
			log.Printf("agent-service: no primary agent for session %s, using settings default %s", sessionID, us.DefaultAgent)
			return us.DefaultAgent, "default", true
		}
	}

	// Ultimate fallback.
	log.Printf("agent-service: no primary agent for session %s, falling back to %s", sessionID, defaultFallbackAgent)
	return defaultFallbackAgent, "default", true
}
