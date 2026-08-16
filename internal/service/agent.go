package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/override"
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
	// ResolveForSessionReadOnly resolves the same effective agent+mode as
	// ResolveForSession (session binding -> user-settings default ->
	// hardcoded fallback) but never mutates session-agent-binding state:
	// unlike ResolveForSession, it does not call EnsureSessionAgent or
	// emit AgentAssigned when the session has no existing binding. Use
	// this for read-only policy lookups (e.g. resolveMessageWakePolicy)
	// that should not have the side effect of auto-binding a session to
	// an agent merely because something checked its effective policy.
	ResolveForSessionReadOnly(ctx context.Context, sessionID string) (agent *store.AgentProfile, mode *store.AgentMode, err error)
	ListModes(ctx context.Context, agentID string) ([]store.AgentMode, error)
}

// defaultFallbackAgent is the agent ID used when no session-agent binding
// or user-settings default exists. Points to the file-based default agent.
const defaultFallbackAgent = "file-default"

// defaultFallbackSlug is the slug-based fallback when neither the session
// agent nor the default agent ID can be found.
const defaultFallbackSlug = "default"

// OverrideReader loads session-level config overrides.
type OverrideReader interface {
	GetSessionOverrides(sessionID string) (string, error)
}

// agentServiceImpl implements AgentService backed by file-based definitions
// (primary) with DB fallback for user-created agents.
type agentServiceImpl struct {
	agents    AgentReader
	writers   AgentWriter
	settings  SettingsStore
	events    EventEmitter
	overrides OverrideReader

	// mu guards fileDefs. The slice was historically immutable after
	// construction, but the managed-agent write contract reloads a single
	// def in place after a GUI/API/CLI edit so changes are visible without a
	// restart (AgentConfigService → ReloadFileAgent / RemoveFileAgent).
	mu       sync.RWMutex
	fileDefs []*agent.Definition // file-based agent definitions, priority-ordered
}

// AgentRegistryReloader lets the managed-agent write path refresh the live
// in-memory registry after a file/DB write so reads reflect the change
// without a restart. Implemented by agentServiceImpl.
type AgentRegistryReloader interface {
	// ReloadFileAgent replaces (or appends) the in-memory definition for
	// def.Slug with def. def is expected to already carry its resolved
	// Source/SourceRef/ID.
	ReloadFileAgent(def *agent.Definition)
	// RemoveFileAgent drops the in-memory definition for slug, if present.
	RemoveFileAgent(slug string)
}

// AgentServiceConfig holds dependencies for constructing an AgentService.
type AgentServiceConfig struct {
	Agents     AgentReader
	Writers    AgentWriter
	Settings   SettingsStore
	Events     EventEmitter
	FileAgents []*agent.Definition // from agent.Discover() + builtin
	Overrides  OverrideReader
}

// NewAgentService creates an AgentService from its required dependencies.
func NewAgentService(cfg AgentServiceConfig) AgentService {
	return &agentServiceImpl{
		agents:    cfg.Agents,
		writers:   cfg.Writers,
		settings:  cfg.Settings,
		events:    cfg.Events,
		fileDefs:  cfg.FileAgents,
		overrides: cfg.Overrides,
	}
}

func (s *agentServiceImpl) Get(_ context.Context, id string) (*store.AgentProfile, error) {
	// Legacy "file-<slug>" identity: resolve through the in-memory def so the
	// deterministic runtime identity keeps working.
	if agent.IsFileBasedID(id) {
		slug := agent.SlugFromFileID(id)
		if d := s.findDefBySlug(slug); d != nil {
			return d.ToProfile(), nil
		}
	}
	// Stamped managed agents carry a real UUID == their DB row PK; an
	// in-memory def (if loaded) wins so GUI/CLI edits reloaded into the
	// registry are visible without a restart, otherwise fall through to DB.
	if d := s.findDefByID(id); d != nil {
		return d.ToProfile(), nil
	}
	return s.agents.GetAgent(id)
}

func (s *agentServiceImpl) GetBySlug(_ context.Context, slug string) (*store.AgentProfile, error) {
	// File-based agents take priority.
	if d := s.findDefBySlug(slug); d != nil {
		return d.ToProfile(), nil
	}
	return s.agents.GetAgentBySlug(slug)
}

func (s *agentServiceImpl) List(_ context.Context) ([]store.AgentProfile, error) {
	// Start with file-based agents (snapshot under lock).
	s.mu.RLock()
	defs := make([]*agent.Definition, len(s.fileDefs))
	copy(defs, s.fileDefs)
	s.mu.RUnlock()

	seen := make(map[string]bool, len(defs))
	var result []store.AgentProfile
	for _, d := range defs {
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

// findDefBySlug returns the in-memory definition for slug, or nil.
func (s *agentServiceImpl) findDefBySlug(slug string) *agent.Definition {
	if slug == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.fileDefs {
		if d.Slug == slug {
			return d
		}
	}
	return nil
}

// findDefByID returns the in-memory definition whose canonical identity
// matches id (the stamped UUID for managed agents), or nil.
func (s *agentServiceImpl) findDefByID(id string) *agent.Definition {
	if id == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.fileDefs {
		if d.CanonicalID() == id {
			return d
		}
	}
	return nil
}

// ReloadFileAgent implements AgentRegistryReloader.
func (s *agentServiceImpl) ReloadFileAgent(def *agent.Definition) {
	if def == nil || def.Slug == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.fileDefs {
		if d.Slug == def.Slug {
			s.fileDefs[i] = def
			return
		}
	}
	s.fileDefs = append(s.fileDefs, def)
}

// RemoveFileAgent implements AgentRegistryReloader.
func (s *agentServiceImpl) RemoveFileAgent(slug string) {
	if slug == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.fileDefs[:0]
	for _, d := range s.fileDefs {
		if d.Slug != slug {
			out = append(out, d)
		}
	}
	s.fileDefs = out
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
		if d := s.findDefBySlug(agent.SlugFromFileID(agentID)); d != nil {
			return d.ToModes(), nil
		}
	}
	// Stamped managed agents carry a UUID id; modes live in the file def, not
	// the DB. Prefer the def's inline modes, fall back to DB-stored modes.
	if d := s.findDefByID(agentID); d != nil {
		return d.ToModes(), nil
	}
	return s.agents.ListAgentModes(agentID)
}

// ResolveForSession implements the agent resolution fallback chain previously
// inlined in engine.go generateResponse (lines 446-495):
//
//  1. Look up the session's primary agent binding (session_agents table).
//  2. If no binding exists, check user_settings.default_agent.
//  3. If still empty, fall back to the hardcoded default ("file-default").
//  4. Auto-assign the resolved agent to the session.
//  5. Load the agent profile by ID, falling back to slug lookup.
//  6. Reject disabled agents.
//  7. Load the agent mode (falling back to empty mode on miss).
func (s *agentServiceImpl) ResolveForSession(ctx context.Context, sessionID string) (*store.AgentProfile, *store.AgentMode, error) {
	return s.resolveForSession(ctx, sessionID, true)
}

// ResolveForSessionReadOnly implements AgentService's non-mutating sibling
// to ResolveForSession — see that method's interface doc comment. Added in
// response to a code-review finding: messaging_reactor.go's
// resolveMessageWakePolicy was calling the mutating ResolveForSession from
// a fire-and-forget goroutine on every eligible A2A SendMessage, so a
// read-only "what policy applies here" check was silently auto-binding
// unbound sessions to an agent (EnsureSessionAgent) and emitting
// AgentAssigned as an unintended side effect. This variant runs the exact
// same resolution chain but skips step 4 below entirely.
func (s *agentServiceImpl) ResolveForSessionReadOnly(ctx context.Context, sessionID string) (*store.AgentProfile, *store.AgentMode, error) {
	return s.resolveForSession(ctx, sessionID, false)
}

// resolveForSession is the shared implementation behind ResolveForSession
// and ResolveForSessionReadOnly. allowAutoAssign gates step 4
// (EnsureSessionAgent + EmitAgentAssigned) only — every other step in the
// resolution chain runs identically regardless of its value.
func (s *agentServiceImpl) resolveForSession(ctx context.Context, sessionID string, allowAutoAssign bool) (*store.AgentProfile, *store.AgentMode, error) {
	agentID, modeName, autoAssigned := s.resolveBinding(sessionID)

	// Load the agent profile. Check file-based agents first, then DB.
	resolved, err := s.Get(ctx, agentID)
	if err != nil {
		resolved, err = s.GetBySlug(ctx, defaultFallbackSlug)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve agent for session %s: %w", sessionID, err)
		}
	}

	// Apply session-level overrides (model, provider) from the override cascade.
	if s.overrides != nil {
		overridesJSON, err := s.overrides.GetSessionOverrides(sessionID)
		if err == nil && overridesJSON != "{}" {
			var sessionOvr override.OverrideConfig
			if json.Unmarshal([]byte(overridesJSON), &sessionOvr) == nil {
				base := override.OverrideConfig{
					Model:    resolved.DefaultModel,
					Provider: resolved.DefaultProvider,
				}
				effective := override.Resolve(base, nil, &sessionOvr)
				if effective.Model != "" {
					resolved.DefaultModel = effective.Model
				}
				if effective.Provider != "" {
					resolved.DefaultProvider = effective.Provider
				}
			}
		}
	}

	if resolved.Status == "disabled" {
		return nil, nil, fmt.Errorf("agent %q is disabled", resolved.Name)
	}

	// Auto-assign to session if we had to fall back (only for the
	// mutating variant — see resolveForSession's doc comment).
	if autoAssigned && allowAutoAssign {
		if err := s.writers.EnsureSessionAgent(sessionID, resolved.ID, modeName, true); err != nil {
			slog.Warn("agent-service: failed to auto-assign agent", "agent", resolved.ID, "session_id", sessionID, "err", err)
		}
		if s.events != nil {
			s.events.EmitAgentAssigned(ctx, sessionID, resolved.ID, modeName)
		}
	}

	// Load mode — check file-based modes first, then DB.
	mode, err := s.resolveMode(ctx, resolved.ID, modeName)
	if err != nil {
		slog.Warn("agent-service: could not load mode (using base prompt)", "agent", resolved.ID, "mode", modeName, "err", err)
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
			slog.Info("agent-service: no primary agent, using settings default", "session_id", sessionID, "agent", us.DefaultAgent)
			return us.DefaultAgent, "default", true
		}
	}

	// Ultimate fallback.
	slog.Info("agent-service: no primary agent, falling back", "session_id", sessionID, "agent", defaultFallbackAgent)
	return defaultFallbackAgent, "default", true
}

// resolveMode loads a mode for an agent, checking file-based definitions first.
func (s *agentServiceImpl) resolveMode(_ context.Context, agentID, modeName string) (*store.AgentMode, error) {
	var def *agent.Definition
	if agent.IsFileBasedID(agentID) {
		def = s.findDefBySlug(agent.SlugFromFileID(agentID))
	} else {
		def = s.findDefByID(agentID)
	}
	if def != nil {
		for _, m := range def.ToModes() {
			if m.Slug == modeName {
				return &m, nil
			}
		}
		return nil, fmt.Errorf("mode %q not found for file agent %q", modeName, def.Slug)
	}
	return s.agents.GetAgentMode(agentID, modeName)
}
