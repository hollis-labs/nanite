package service

import (
	"context"
	"fmt"
	"log"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
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
// or user-settings default exists. Points to the file-based default agent.
const defaultFallbackAgent = "file-default"

// defaultFallbackSlug is the slug-based fallback when neither the session
// agent nor the default agent ID can be found.
const defaultFallbackSlug = "default"

// agentServiceImpl implements AgentService backed by file-based definitions
// (primary) with DB fallback for user-created agents.
type agentServiceImpl struct {
	agents   AgentReader
	writers  AgentWriter
	settings SettingsStore
	events   EventEmitter
	fileDefs []*agent.Definition // file-based agent definitions, priority-ordered
}

// AgentServiceConfig holds dependencies for constructing an AgentService.
type AgentServiceConfig struct {
	Agents     AgentReader
	Writers    AgentWriter
	Settings   SettingsStore
	Events     EventEmitter
	FileAgents []*agent.Definition // from agent.Discover() + builtin
}

// NewAgentService creates an AgentService from its required dependencies.
func NewAgentService(cfg AgentServiceConfig) AgentService {
	return &agentServiceImpl{
		agents:   cfg.Agents,
		writers:  cfg.Writers,
		settings: cfg.Settings,
		events:   cfg.Events,
		fileDefs: cfg.FileAgents,
	}
}

func (s *agentServiceImpl) Get(_ context.Context, id string) (*store.AgentProfile, error) {
	// Check file-based agents first.
	if agent.IsFileBasedID(id) {
		slug := agent.SlugFromFileID(id)
		for _, d := range s.fileDefs {
			if d.Slug == slug {
				p := d.ToProfile()
				return p, nil
			}
		}
	}
	return s.agents.GetAgent(id)
}

func (s *agentServiceImpl) GetBySlug(_ context.Context, slug string) (*store.AgentProfile, error) {
	// File-based agents take priority.
	for _, d := range s.fileDefs {
		if d.Slug == slug {
			return d.ToProfile(), nil
		}
	}
	return s.agents.GetAgentBySlug(slug)
}

func (s *agentServiceImpl) List(_ context.Context) ([]store.AgentProfile, error) {
	// Start with file-based agents.
	seen := make(map[string]bool, len(s.fileDefs))
	var result []store.AgentProfile
	for _, d := range s.fileDefs {
		result = append(result, *d.ToProfile())
		seen[d.Slug] = true
	}

	// Append DB agents whose slug is not already present.
	dbAgents, err := s.agents.ListAgents()
	if err != nil {
		return result, err // return file-based agents even if DB fails
	}
	for _, a := range dbAgents {
		if !seen[a.Slug] {
			result = append(result, a)
		}
	}
	return result, nil
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
	if agent.IsFileBasedID(agentID) {
		slug := agent.SlugFromFileID(agentID)
		for _, d := range s.fileDefs {
			if d.Slug == slug {
				return d.ToModes(), nil
			}
		}
	}
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

	// Load the agent profile. Check file-based agents first, then DB.
	resolved, err := s.Get(ctx, agentID)
	if err != nil {
		resolved, err = s.GetBySlug(ctx, defaultFallbackSlug)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve agent for session %s: %w", sessionID, err)
		}
	}

	if resolved.Status == "disabled" {
		return nil, nil, fmt.Errorf("agent %q is disabled", resolved.Name)
	}

	// Auto-assign to session if we had to fall back.
	if autoAssigned {
		if err := s.writers.EnsureSessionAgent(sessionID, resolved.ID, modeName, true); err != nil {
			log.Printf("agent-service: failed to auto-assign agent %s to session %s: %v", resolved.ID, sessionID, err)
		}
		if s.events != nil {
			s.events.EmitAgentAssigned(ctx, sessionID, resolved.ID, modeName)
		}
	}

	// Load mode — check file-based modes first, then DB.
	mode, err := s.resolveMode(ctx, resolved.ID, modeName)
	if err != nil {
		log.Printf("agent-service: could not load mode %s/%s: %v (using base prompt)", resolved.ID, modeName, err)
		mode = &store.AgentMode{}
	}

	return resolved, mode, nil
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

// resolveMode loads a mode for an agent, checking file-based definitions first.
func (s *agentServiceImpl) resolveMode(_ context.Context, agentID, modeName string) (*store.AgentMode, error) {
	if agent.IsFileBasedID(agentID) {
		slug := agent.SlugFromFileID(agentID)
		for _, d := range s.fileDefs {
			if d.Slug == slug {
				for _, m := range d.ToModes() {
					if m.Slug == modeName {
						return &m, nil
					}
				}
				return nil, fmt.Errorf("mode %q not found for file agent %q", modeName, slug)
			}
		}
	}
	return s.agents.GetAgentMode(agentID, modeName)
}
