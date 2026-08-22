package service

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// CreateSessionOpts holds the parameters for creating a new session.
type CreateSessionOpts struct {
	ProjectID string
	Model     string
	Provider  string
	AgentID   string // optional; falls back to settings default, then the real "default" agent row
}

// ForkOpts holds the parameters for forking a session.
type ForkOpts struct {
	IncludeMessages bool
	Provider        string
	Model           string
}

// SearchOpts holds optional filters for message search.
type SearchOpts struct {
	ProjectID string
	Limit     int
}

// SessionService encapsulates session lifecycle operations.
// It consolidates logic currently spread across API handlers and Engine.
type SessionService interface {
	Create(ctx context.Context, opts CreateSessionOpts) (*store.Session, error)
	Get(ctx context.Context, id string) (*store.Session, error)
	List(ctx context.Context, includeArchived bool) ([]store.Session, error)
	Update(ctx context.Context, sess *store.Session) error
	Archive(ctx context.Context, id string) error
	Fork(ctx context.Context, sourceID string, opts ForkOpts) (*store.Session, error)
	ListMessages(ctx context.Context, sessionID string, limit int) ([]store.Message, error)
	Search(ctx context.Context, query string, opts SearchOpts) ([]store.SearchResult, error)
}

// sessionServiceImpl is the concrete implementation of SessionService.
type sessionServiceImpl struct {
	sessions SessionReader
	writer   SessionWriter
	agents   AgentWriter // for EnsureSessionAgent on create
	// agentReader resolves the real "default" agent row's ID on create when
	// neither an explicit AgentID nor a user-settings default is available.
	// TASKS/adhoc/01-eliminate-file-based-agent-runtime.md: added to stop
	// this call site from falling back to the literal placeholder string
	// "file-default" (session_agents.agent_id has no FK, so a bad value
	// here was never caught at write time).
	agentReader AgentReader
	settings    SettingsStore
	events      EventEmitter // may be nil

	// onArchive is a best-effort hook fired after the writer.ArchiveSession
	// succeeds. Phase 4c.8 (CW-20260508-0002): the chat service uses this
	// to Stop + drop any long-lived agent runtime session bound to the
	// archived chat session. nil-safe.
	onArchive func(ctx context.Context, sessionID string)
}

// SetArchiveHook installs (or clears) the onArchive callback. The container
// uses this post-chatSvc construction since sessionService is built first.
// Idempotent across calls.
func (s *sessionServiceImpl) SetArchiveHook(hook func(ctx context.Context, sessionID string)) {
	s.onArchive = hook
}

// SessionServiceDeps groups the dependencies for constructing a SessionService.
type SessionServiceDeps struct {
	Sessions SessionReader
	Writer   SessionWriter
	Agents   AgentWriter
	// AgentReader backs Create's "default" agent fallback resolution — see
	// sessionServiceImpl.agentReader's doc comment.
	AgentReader AgentReader
	Settings    SettingsStore
	Events      EventEmitter // optional
}

// NewSessionService creates a new SessionService.
func NewSessionService(deps SessionServiceDeps) SessionService {
	return &sessionServiceImpl{
		sessions:    deps.Sessions,
		writer:      deps.Writer,
		agents:      deps.Agents,
		agentReader: deps.AgentReader,
		settings:    deps.Settings,
		events:      deps.Events,
	}
}

func (s *sessionServiceImpl) Create(ctx context.Context, opts CreateSessionOpts) (*store.Session, error) {
	sess := &store.Session{
		ProjectID: opts.ProjectID,
		Model:     opts.Model,
		Provider:  opts.Provider,
	}
	if err := s.writer.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Resolve agent: explicit param → user settings default → real
	// "default" agent row. TASKS/adhoc/01-eliminate-file-based-agent-
	// runtime.md: this used to fall back to the literal placeholder string
	// "file-default", written straight into session_agents.agent_id (no FK
	// on that column, so a bad value was never caught at write time).
	agentID := opts.AgentID
	if agentID == "" {
		if settings, err := s.settings.GetUserSettings(ctx); err == nil && settings.DefaultAgent != "" {
			agentID = settings.DefaultAgent
		}
	}
	if agentID == "" && s.agentReader != nil {
		if defaultAgent, err := s.agentReader.GetAgentBySlug(ctx, "default"); err == nil && defaultAgent != nil {
			agentID = defaultAgent.ID
		}
	}

	// Assign the resolved agent as primary (best-effort — matches the
	// pre-existing contract of this write). Skip it entirely in the true
	// edge case where even the "default" agent row can't be resolved (no
	// such row exists at all) rather than write an empty/placeholder
	// agent_id — the session itself is already created and stays usable
	// without a primary-agent binding.
	if agentID != "" {
		_ = s.agents.EnsureSessionAgent(ctx, sess.ID, agentID, "default", true)
	}

	// Emit session start event.
	if s.events != nil {
		s.events.EmitSessionStart(ctx, sess.ID, agentID, opts.Model, "default")
	}

	return sess, nil
}

func (s *sessionServiceImpl) Get(_ context.Context, id string) (*store.Session, error) {
	sess, err := s.sessions.GetSession(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", id, err)
	}
	return sess, nil
}

func (s *sessionServiceImpl) List(_ context.Context, includeArchived bool) ([]store.Session, error) {
	return s.sessions.ListSessions(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, includeArchived)
}

func (s *sessionServiceImpl) Update(_ context.Context, sess *store.Session) error {
	return s.writer.UpdateSession(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sess)
}

func (s *sessionServiceImpl) Archive(ctx context.Context, id string) error {
	if err := s.writer.ArchiveSession(ctx, id); err != nil {
		return fmt.Errorf("archive session %s: %w", id, err)
	}

	if s.onArchive != nil {
		s.onArchive(ctx, id)
	}

	if s.events != nil {
		s.events.EmitSessionEnd(ctx, id)
	}

	return nil
}

func (s *sessionServiceImpl) Fork(_ context.Context, sourceID string, opts ForkOpts) (*store.Session, error) {
	overrides := &store.Session{
		Provider: opts.Provider,
		Model:    opts.Model,
	}
	return s.writer.ForkSession(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sourceID, overrides, opts.IncludeMessages)
}

func (s *sessionServiceImpl) ListMessages(_ context.Context, sessionID string, limit int) ([]store.Message, error) {
	return s.sessions.ListMessages(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID, limit)
}

func (s *sessionServiceImpl) Search(_ context.Context, query string, opts SearchOpts) ([]store.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	return s.sessions.SearchMessages(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, query, opts.ProjectID, limit)
}
