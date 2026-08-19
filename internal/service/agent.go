package service

import (
	"context"
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
	// ResolveForSession implements the agent resolution fallback chain
	// (session binding -> user-settings default -> hardcoded fallback).
	// Phase 0 item 21 ("Cut Modes, in full") removed this method's second
	// (*store.AgentMode) return value — Legacy Agent Mode is gone.
	ResolveForSession(ctx context.Context, sessionID string) (agent *store.AgentProfile, err error)
	// ResolveForSessionReadOnly resolves the same effective agent as
	// ResolveForSession (session binding -> user-settings default ->
	// hardcoded fallback) but never mutates session-agent-binding state:
	// unlike ResolveForSession, it does not call EnsureSessionAgent or
	// emit AgentAssigned when the session has no existing binding. Use
	// this for read-only policy lookups (e.g. resolveMessageWakePolicy)
	// that should not have the side effect of auto-binding a session to
	// an agent merely because something checked its effective policy.
	ResolveForSessionReadOnly(ctx context.Context, sessionID string) (agent *store.AgentProfile, err error)
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
	// Legacy "file-<slug>" identity: resolve through the in-memory def so the
	// deterministic runtime identity keeps working.
	if agent.IsFileBasedID(id) {
		slug := agent.SlugFromFileID(id)
		if d := s.findDefBySlug(slug); d != nil {
			return s.resolveFileProfile(d), nil
		}
	}
	// Stamped managed agents carry a real UUID == their DB row PK; an
	// in-memory def (if loaded) wins so GUI/CLI edits reloaded into the
	// registry are visible without a restart, otherwise fall through to DB.
	if d := s.findDefByID(id); d != nil {
		return s.resolveFileProfile(d), nil
	}
	return s.agents.GetAgent(id)
}

func (s *agentServiceImpl) GetBySlug(_ context.Context, slug string) (*store.AgentProfile, error) {
	// File-based agents take priority.
	if d := s.findDefBySlug(slug); d != nil {
		return s.resolveFileProfile(d), nil
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
		result = append(result, *s.resolveFileProfile(d))
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

// resolveFileProfile returns d's file-derived profile (Definition.ToProfile())
// overlaid with the real agent_profiles DB row's authoritative values, when
// one exists for d.Slug — TASKS/phase-1/12-fix-agent-service-get-drops-new-db-
// only-columns.md. Before this fix, Get/GetBySlug/List returned
// d.ToProfile() as-is for every file-backed def, silently dropping
// role_id/model_id/runtime_kind/consumer_id (and, on file/DB divergence,
// activation_mode/class/default_state) for every one of the ~33
// file-discovered agents in this project once a DB row existed for them —
// see agent.OverlayDBFields's doc comment for the full precedence rationale.
// A missing/errored DB lookup (the "file-backed but no DB row yet" case, a
// genuinely new file not yet ingested) degrades to the unmodified file-only
// view rather than erroring — those DB-only fields simply stay at
// ToProfile()'s zero-value default.
func (s *agentServiceImpl) resolveFileProfile(d *agent.Definition) *store.AgentProfile {
	p := d.ToProfile()
	row, err := s.agents.GetAgentBySlug(d.Slug)
	if err != nil || row == nil {
		return p
	}
	return agent.OverlayDBFields(p, row)
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

// ResolveForSession implements the agent resolution fallback chain previously
// inlined in engine.go generateResponse (lines 446-495):
//
//  1. Look up the session's primary agent binding (session_agents table).
//  2. If no binding exists, check user_settings.default_agent.
//  3. If still empty, fall back to the hardcoded default ("file-default").
//  4. Auto-assign the resolved agent to the session.
//  5. Load the agent profile by ID, falling back to slug lookup.
//  6. Reject disabled agents.
//  7. Apply the role -> agent -> task cascade (Phase 1 item 01) to
//     system_prompt/class/model/provider.
//
// Phase 0 item 21 ("Cut Modes, in full") removed the step that used to be
// numbered 7 here ("load the agent mode") — Legacy Agent Mode is gone, so
// ResolveForSession no longer returns a *store.AgentMode second value. The
// cascade step above reuses that freed slot.
func (s *agentServiceImpl) ResolveForSession(ctx context.Context, sessionID string) (*store.AgentProfile, error) {
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
func (s *agentServiceImpl) ResolveForSessionReadOnly(ctx context.Context, sessionID string) (*store.AgentProfile, error) {
	return s.resolveForSession(ctx, sessionID, false)
}

// resolveForSession is the shared implementation behind ResolveForSession
// and ResolveForSessionReadOnly. allowAutoAssign gates step 4
// (EnsureSessionAgent + EmitAgentAssigned) only — every other step in the
// resolution chain runs identically regardless of its value.
func (s *agentServiceImpl) resolveForSession(ctx context.Context, sessionID string, allowAutoAssign bool) (*store.AgentProfile, error) {
	agentID, modeName, autoAssigned := s.resolveBinding(sessionID)

	// Load the agent profile. Check file-based agents first, then DB.
	resolved, err := s.Get(ctx, agentID)
	if err != nil {
		resolved, err = s.GetBySlug(ctx, defaultFallbackSlug)
		if err != nil {
			return nil, fmt.Errorf("resolve agent for session %s: %w", sessionID, err)
		}
	}

	if resolved.Status == "disabled" {
		return nil, fmt.Errorf("agent %q is disabled", resolved.Name)
	}

	// Phase 1 item 01 (TASKS/phase-1/01-add-roles-table-and-cascade-
	// resolution.md): role -> agent -> task closest-wins cascade for
	// system_prompt/class/model selection, per architecture/
	// 01-agent-construction.md. s.roleForProfile is nil today for every
	// row (agent_profiles.role_id doesn't exist until
	// 02-add-agents-composition-columns.md adds and backfills it), and no
	// caller threads a task/invocation-level override into
	// ResolveForSession yet, so this call is a proven no-op passthrough
	// right now (see role_cascade_test.go) -- it establishes the
	// resolution seam at this insertion point rather than a second one,
	// ready for 02 to make the role layer real without this function's
	// shape changing again.
	resolved = applyScalarCascade(resolved, s.roleForProfile(ctx, resolved), nil)

	// Auto-assign to session if we had to fall back (only for the
	// mutating variant — see resolveForSession's doc comment). modeName
	// here is the session_agents.mode binding string — a separate table/
	// column from the cut Legacy AgentMode / Session Mode systems (see
	// GLOSSARY.md's naming-collision guidance) and out of this task's
	// scope; resolveBinding's own fallback ("default") threads through
	// unchanged.
	if autoAssigned && allowAutoAssign {
		if err := s.writers.EnsureSessionAgent(sessionID, resolved.ID, modeName, true); err != nil {
			slog.Warn("agent-service: failed to auto-assign agent", "agent", resolved.ID, "session_id", sessionID, "err", err)
		}
		if s.events != nil {
			s.events.EmitAgentAssigned(ctx, sessionID, resolved.ID, modeName)
		}
	}

	return resolved, nil
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

// roleForProfile resolves the store.Role a profile's role binding points
// at, or nil when the profile has no role_id set (still true for every
// pre-existing row until 10-data-migrate-nanite-agents-md.md backfills one
// -- out of Phase 1 scope) or the lookup fails for any reason (unknown ID,
// store error). Made real by 02-add-agents-composition-columns.md, which
// added agent_profiles.role_id; before that column existed this always
// returned nil, making resolveForSession's cascade call a proven no-op
// (see role_cascade_test.go's TestAgentService_ResolveForSession_
// CascadeIsNoOpToday, which still passes unchanged: its fixture profile
// leaves RoleID unset, so this function still returns nil for it).
func (s *agentServiceImpl) roleForProfile(_ context.Context, profile *store.AgentProfile) *store.Role {
	if profile == nil || profile.RoleID == "" {
		return nil
	}
	role, err := s.agents.GetRole(profile.RoleID)
	if err != nil || role == nil {
		return nil
	}
	return role
}

// applyScalarCascade resolves the role -> agent -> task cascade
// (ResolveAgentCascade) for an already-loaded profile and writes the
// resolved system_prompt/class/model/provider/model_id back onto a copy of
// it. Only these five scalars are written back into the per-turn
// resolution path — per architecture/01-agent-construction.md, tools/
// skills/permissions "bind at the composition (agents) level, not fixed by
// role... the actual grant is adjustable per composition/scope," so a
// role's tool/skill/permission defaults are a composition-creation-time
// seed hint (see internal/store/roles.go), not something re-merged into
// every turn's live tool/skill grant. ResolveAgentCascade still computes
// the full merge (including tools/skills/permissions) so that part of the
// cascade logic is exercised and tested (role_cascade_test.go) even
// though it isn't applied here. runtime_kind is deliberately excluded --
// see AgentOverrideConfig's doc comment.
func applyScalarCascade(profile *store.AgentProfile, role *store.Role, taskOverride *override.OverrideConfig) *store.AgentProfile {
	if profile == nil {
		return profile
	}
	cascade := ResolveAgentCascade(role, profile, taskOverride)
	out := *profile
	if cascade.SystemPrompt != "" {
		out.SystemPrompt = cascade.SystemPrompt
	}
	if cascade.Class != "" {
		out.Class = cascade.Class
	}
	if cascade.Model != "" {
		out.DefaultModel = cascade.Model
	}
	if cascade.Provider != "" {
		out.DefaultProvider = cascade.Provider
	}
	if cascade.ModelID != "" {
		out.ModelID = cascade.ModelID
	}
	return &out
}
