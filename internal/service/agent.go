package service

import (
	"context"
	"fmt"
	"log/slog"

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

// defaultFallbackSlug is the agent slug used when no session-agent binding
// or user-settings default resolves to a valid agent — the ultimate
// fallback, resolved via GetBySlug so it stays correct regardless of
// whichever real agent_profiles.id the "default" agent happens to have.
//
// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md removed the old
// defaultFallbackAgent ID-shaped constant ("file-default", a synthetic
// runtime alias no real agent_profiles row ever carried as its actual
// primary key) — every fallback in this file now resolves by slug only.
const defaultFallbackSlug = "default"

// agentServiceImpl implements AgentService backed entirely by the
// agent_profiles DB. TASKS/adhoc/01-eliminate-file-based-agent-runtime.md
// removed the in-memory file-definition registry this type used to consult
// before ever touching the DB (fileDefs/findDefByID/findDefBySlug/
// resolveFileProfile, and the "file-<slug>" synthetic-ID resolution branch
// in Get) — every agent, including the 9 internal builtin profiles, is a
// real agent_profiles row with a real ID by the time this service is
// constructed (AutoIngestAgents runs before NewAgentService in
// container.go), so there is no more parallel runtime identity to resolve
// through.
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
	return s.agents.GetAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
}

func (s *agentServiceImpl) GetBySlug(_ context.Context, slug string) (*store.AgentProfile, error) {
	return s.agents.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, slug)
}

func (s *agentServiceImpl) List(_ context.Context) ([]store.AgentProfile, error) {
	return s.agents.ListAgents(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
}

func (s *agentServiceImpl) Create(_ context.Context, agent *store.AgentProfile) error {
	return s.writers.CreateAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, agent)
}

func (s *agentServiceImpl) Update(_ context.Context, agent *store.AgentProfile) error {
	return s.writers.UpdateAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, agent)
}

func (s *agentServiceImpl) Delete(_ context.Context, id string) error {
	return s.writers.DeleteAgent(context.

		// ResolveForSession implements the agent resolution fallback chain previously
		// inlined in engine.go generateResponse (lines 446-495):
		//
		//  1. Look up the session's primary agent binding (session_agents table).
		//  2. If no binding exists, check user_settings.default_agent.
		//  3. If still empty, fall back to the hardcoded default slug ("default").
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
		TODO(), id)
}

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

	// Load the agent profile by ID. resolveBinding's own ultimate fallback
	// returns a bare slug ("default"), not a real ID, so that lookup misses
	// here by design and falls through to the GetBySlug fallback below —
	// same two-hop shape a stale pre-reconciliation session_agents/
	// user_settings value degrades to as well (TASKS/adhoc/01's migration
	// 123 reconciles those to real IDs going forward, but this fallback
	// stays as a defensive net regardless).
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
		if err := s.writers.EnsureSessionAgent(ctx, sessionID, resolved.ID, modeName, true); err != nil {
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
	sa, err := s.agents.GetSessionPrimaryAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID)
	if err == nil {
		return sa.AgentID, sa.Mode, false
	}

	// No binding — check user settings for a configured default.
	if s.settings != nil {
		if us, err := s.settings.GetUserSettings(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */); err == nil && us.DefaultAgent != "" {
			slog.Info("agent-service: no primary agent, using settings default", "session_id", sessionID, "agent", us.DefaultAgent)
			return us.DefaultAgent, "default", true
		}
	}

	// Ultimate fallback.
	slog.Info("agent-service: no primary agent, falling back", "session_id", sessionID, "agent", defaultFallbackSlug)
	return defaultFallbackSlug, "default", true
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
	role, err := s.agents.GetRole(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, profile.RoleID)
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
