package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/store"
)

// CreateSessionOpts holds the parameters for creating a new session.
type CreateSessionOpts struct {
	ProjectID string
	Model     string
	Provider  string
	AgentID   string // optional; falls back to settings default, then the real "default" agent row
	// Metadata is the session's initial metadata JSON (the validated harness
	// selection). Empty leaves the column at its default.
	Metadata string
	// SubagentRuntime is "api" or "cli"; empty leaves the column unset.
	// Callers validate it first (store.ValidSubagentRuntime).
	SubagentRuntime string
	// SkipAgentBinding makes Create stop after the insert and runtime write:
	// no agent resolution, no primary binding, and AgentID is ignored. The
	// caller owns binding when it uses this option. Cognitive API creation
	// uses its own atomic transaction rather than this best-effort path.
	SkipAgentBinding bool
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
	// UpdateMetadata replaces the session's metadata JSON.
	UpdateMetadata(ctx context.Context, id, metadataJSON string) error
	Archive(ctx context.Context, id string) error
	Fork(ctx context.Context, sourceID string, opts ForkOpts) (*store.Session, error)
	ListMessages(ctx context.Context, sessionID string, limit int) ([]store.Message, error)
	// GetMessage returns one message.
	GetMessage(ctx context.Context, id string) (*store.Message, error)
	// CreateMessage appends a message to a session.
	CreateMessage(ctx context.Context, msg *store.Message) error
	Search(ctx context.Context, query string, opts SearchOpts) ([]store.SearchResult, error)
	// ListMessagesPage returns an offset-paginated page of a session's
	// messages in chronological order.
	ListMessagesPage(ctx context.Context, sessionID string, limit, offset int) (*store.MessagePage, error)
	// ListMessagesAround returns a window of messages centered on messageID.
	ListMessagesAround(ctx context.Context, sessionID, messageID string, before, after int) (*store.MessagePage, error)
	// DetectInterruptedTurn reports whether sess has a turn whose agent is
	// gone, and records the detection in the event log. nil means not
	// interrupted.
	DetectInterruptedTurn(ctx context.Context, sess *store.Session, hasLiveStream bool) map[string]any
	// ListPendingEnvelopes returns the session's unanswered action-required
	// plugin envelopes, for the GUI to rehydrate after a reload.
	ListPendingEnvelopes(ctx context.Context, sessionID string) ([]chat.Envelope, error)
	// EnvelopeLookup returns the envelope instances the messages' envelopes
	// name, keyed by envelope ID.
	EnvelopeLookup(ctx context.Context, messages []store.Message) map[string]*store.EnvelopeInstance
}

// SessionEventLog is the event_log write DetectInterruptedTurn needs.
type SessionEventLog interface {
	LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string)
}

// SessionRuntimeWriter sets a session's subagent runtime on create.
type SessionRuntimeWriter interface {
	SetSessionSubagentRuntime(ctx context.Context, sessionID, runtime string) error
}

// SessionEnvelopeReader lists the envelope instances recorded for a session.
type SessionEnvelopeReader interface {
	ListEnvelopeInstancesBySession(ctx context.Context, sessionID string) ([]store.EnvelopeInstance, error)
}

// EnvelopeInstanceGetter fetches one envelope instance by ID.
type EnvelopeInstanceGetter interface {
	GetEnvelopeInstance(ctx context.Context, id string) (*store.EnvelopeInstance, error)
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
	events      EventEmitter           // may be nil
	runtime     SessionRuntimeWriter   // may be nil; required when Create sets a runtime
	eventLog    SessionEventLog        // may be nil
	envelopes   SessionEnvelopeReader  // may be nil
	instances   EnvelopeInstanceGetter // may be nil

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
	// Runtime backs CreateSessionOpts.SubagentRuntime. Optional.
	Runtime SessionRuntimeWriter
	// EventLog records interrupted-turn detections. Optional.
	EventLog SessionEventLog
	// Envelopes backs ListPendingEnvelopes. Optional.
	Envelopes SessionEnvelopeReader
	// EnvelopeInstances backs EnvelopeLookup. Optional.
	EnvelopeInstances EnvelopeInstanceGetter
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
		runtime:     deps.Runtime,
		eventLog:    deps.EventLog,
		envelopes:   deps.Envelopes,
		instances:   deps.EnvelopeInstances,
	}
}

// Create inserts the session, sets its subagent runtime, then binds the
// resolved agent as primary. The runtime is a separate write, not part of
// the insert's transaction: an error there leaves the session created.
// Store errors come back unwrapped so the HTTP body reads as it always has.
//
// Create emits no event. The HTTP handler emits activity session-created;
// session-start belongs to the first turn.
func (s *sessionServiceImpl) Create(ctx context.Context, opts CreateSessionOpts) (*store.Session, error) {
	sess := &store.Session{
		ProjectID: opts.ProjectID,
		Model:     opts.Model,
		Provider:  opts.Provider,
		Metadata:  opts.Metadata,
	}
	if err := s.writer.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	if opts.SubagentRuntime != "" {
		if s.runtime == nil {
			return nil, fmt.Errorf("session subagent runtime writer is not wired")
		}
		if err := s.runtime.SetSessionSubagentRuntime(ctx, sess.ID, opts.SubagentRuntime); err != nil {
			return nil, err
		}
	}
	if opts.SkipAgentBinding {
		return sess, nil
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

	return sess, nil
}

func (s *sessionServiceImpl) Get(ctx context.Context, id string) (*store.Session, error) {
	sess, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", id, err)
	}
	return sess, nil
}

func (s *sessionServiceImpl) List(ctx context.Context, includeArchived bool) ([]store.Session, error) {
	return s.sessions.ListSessions(ctx, includeArchived)
}

func (s *sessionServiceImpl) Update(ctx context.Context, sess *store.Session) error {
	return s.writer.UpdateSession(ctx, sess)
}

func (s *sessionServiceImpl) UpdateMetadata(ctx context.Context, id, metadataJSON string) error {
	return s.writer.UpdateSessionMetadata(ctx, id, metadataJSON)
}

// Archive marks the session archived, then closes its live agent runtime
// through the onArchive hook and emits session-end. The store error comes
// back unwrapped, like Create's, so an HTTP error body reads as it did when
// the handler called the store directly.
func (s *sessionServiceImpl) Archive(ctx context.Context, id string) error {
	if err := s.writer.ArchiveSession(ctx, id); err != nil {
		return err
	}

	if s.onArchive != nil {
		s.onArchive(ctx, id)
	}

	if s.events != nil {
		s.events.EmitSessionEnd(ctx, id)
	}

	return nil
}

func (s *sessionServiceImpl) Fork(ctx context.Context, sourceID string, opts ForkOpts) (*store.Session, error) {
	overrides := &store.Session{
		Provider: opts.Provider,
		Model:    opts.Model,
	}
	return s.writer.ForkSession(ctx, sourceID, overrides, opts.IncludeMessages)
}

func (s *sessionServiceImpl) ListMessages(ctx context.Context, sessionID string, limit int) ([]store.Message, error) {
	return s.sessions.ListMessages(ctx, sessionID, limit)
}

func (s *sessionServiceImpl) Search(ctx context.Context, query string, opts SearchOpts) ([]store.SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	return s.sessions.SearchMessages(ctx, query, opts.ProjectID, limit)
}

func (s *sessionServiceImpl) GetMessage(ctx context.Context, id string) (*store.Message, error) {
	return s.sessions.GetMessage(ctx, id)
}

func (s *sessionServiceImpl) CreateMessage(ctx context.Context, msg *store.Message) error {
	return s.writer.CreateMessage(ctx, msg)
}

func (s *sessionServiceImpl) ListMessagesPage(ctx context.Context, sessionID string, limit, offset int) (*store.MessagePage, error) {
	return s.sessions.ListMessagesPaginated(ctx, sessionID, limit, offset)
}

func (s *sessionServiceImpl) ListMessagesAround(ctx context.Context, sessionID, messageID string, before, after int) (*store.MessagePage, error) {
	return s.sessions.ListMessagesAroundID(ctx, sessionID, messageID, before, after)
}

// DetectInterruptedTurn reports whether the session has an in-flight turn
// whose backend agent is gone — the case where a service restart
// (deploy/reload) killed a turn mid-generation, leaving the GUI spinning
// forever with no indication anything went wrong (CW-20260518-0084).
//
// The signal is intentionally minimal and derived from existing state:
//
//   - The session's last persisted message is a `user` message. A completed
//     turn always ends with an `assistant` (or `tool`) row; a turn that
//     started but never produced a reply leaves the user message dangling.
//   - The process holds NO live in-memory stream for the session
//     (hasLiveStream, from the StreamManager). After a restart the
//     StreamManager is a fresh empty instance, so a turn that was generating
//     at restart time reads as having no live stream.
//
// Both conditions together mean "a turn was dispatched, no reply landed, and
// nothing in this process is producing one" — i.e. the agent process is
// gone. Reconciling the dead agent_runtime rows is a separate task
// (CW-20260518-0085); this only surfaces the state so the FE can stop the
// endless spinner.
//
// It probes the store for the chronologically-last message rather than
// taking a caller's slice (PR #213 review hardening): a caller holding a
// non-tail window would otherwise silently mis-trigger.
//
// Returns nil when the session is not in an interrupted state — the FE treats
// a null/absent field as "no interruption".
func (s *sessionServiceImpl) DetectInterruptedTurn(ctx context.Context, sess *store.Session, hasLiveStream bool) map[string]any {
	if sess == nil {
		return nil
	}
	// Only active sessions can have an in-flight turn; paused/archived ones
	// were deliberately put to rest.
	if sess.Status != "active" {
		return nil
	}
	// ListMessages returns DESC then reverses to ASC; with limit=1 the single
	// returned element is the absolute-latest row.
	tail, err := s.sessions.ListMessages(ctx, sess.ID, 1)
	if err != nil || len(tail) == 0 {
		return nil
	}
	last := tail[len(tail)-1]
	// The pure "dangling user turn + no live stream" decision lives in
	// internal/recovery (interrupted-turn detection, the fourth of the four
	// recovery mechanisms); this method's job is the lookups that feed it.
	result := recovery.DetectInterruptedTurn(last.Role, last.ID, last.CreatedAt, hasLiveStream)
	if result != nil {
		s.logInterruptedTurnDetected(ctx, sess.ID, last, result)
	}
	return result
}

// interruptedTurnDetectedMeta is the structured event_log.metadata payload
// for event_type="interrupted_turn_detected" — the session/turn context
// that triggered the heuristic, not a bare event-type string. Mirrors the
// shape convention chat_reflexes.go's "reflex_action" write established.
type interruptedTurnDetectedMeta struct {
	SessionID       string `json:"session_id"`
	LastMessageID   string `json:"last_message_id"`
	LastMessageRole string `json:"last_message_role"`
	LastActivityAt  string `json:"last_activity_at"`
	Reason          string `json:"reason"`
}

// logInterruptedTurnDetected writes the event_log postmortem row for a real
// interrupted-turn detection. Best-effort — LogEvent swallows its own DB
// errors; this only degrades to a skipped write when no event log is wired
// (never true in production wiring).
func (s *sessionServiceImpl) logInterruptedTurnDetected(ctx context.Context, sessionID string, last store.Message, result map[string]any) {
	if s.eventLog == nil {
		return
	}
	reason, _ := result["reason"].(string)
	meta := interruptedTurnDetectedMeta{
		SessionID:       sessionID,
		LastMessageID:   last.ID,
		LastMessageRole: last.Role,
		LastActivityAt:  last.CreatedAt,
		Reason:          reason,
	}
	blob, err := json.Marshal(meta)
	if err != nil {
		blob = []byte("{}")
	}
	s.eventLog.LogEvent(ctx, sessionID, "interrupted_turn_detected", "recovery",
		fmt.Sprintf("interrupted turn detected: last message %s (%s) has no reply and no live stream", last.ID, last.Role),
		string(blob))
}

// ListPendingEnvelopes returns the session's unanswered subagent-spawn
// approval and elicitation-prompt envelopes as action-required chat
// envelopes. A row whose stored JSON does not parse is skipped with a
// warning rather than failing the list.
func (s *sessionServiceImpl) ListPendingEnvelopes(ctx context.Context, sessionID string) ([]chat.Envelope, error) {
	if s.envelopes == nil {
		return nil, fmt.Errorf("session envelopes are not wired")
	}
	insts, err := s.envelopes.ListEnvelopeInstancesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	out := make([]chat.Envelope, 0, len(insts))
	for _, inst := range insts {
		if inst.RespondedAt != nil {
			continue
		}
		if inst.EnvelopeType != "subagent-spawn-approval" && inst.EnvelopeType != "elicitation-prompt" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(inst.EnvelopeJSON), &data); err != nil {
			slog.Warn("api: skip malformed plugin-envelope rehydrate row",
				"session_id", sessionID,
				"envelope_id", inst.ID,
				"type", inst.EnvelopeType,
				"err", err,
			)
			continue
		}
		out = append(out, chat.Envelope{
			Kind:         "envelope",
			Version:      1,
			Type:         inst.EnvelopeType,
			ID:           inst.ID,
			Data:         data,
			DisplayClass: string(EnvelopeDisplayClassActionRequired),
		})
	}
	return out, nil
}

// EnvelopeLookup fetches the EnvelopeInstance for every envelope ID named in
// the messages and returns them keyed by ID. A message's Envelope field may
// hold one envelope object or a JSON array of them; one that does not parse
// is skipped. A missing instance is skipped silently, and any other fetch
// error is logged and skipped, so the lookup is best-effort and never fails.
//
// Fetches run under the caller's ctx. Once the caller's request is canceled
// the remaining fetches fail and are skipped, which can only matter to a
// client that has already gone. With no EnvelopeInstances dependency the
// lookup is empty.
func (s *sessionServiceImpl) EnvelopeLookup(ctx context.Context, messages []store.Message) map[string]*store.EnvelopeInstance {
	lookup := make(map[string]*store.EnvelopeInstance)
	if s.instances == nil {
		return lookup
	}

	fetchID := func(id string) {
		if id == "" || lookup[id] != nil {
			return
		}
		inst, err := s.instances.GetEnvelopeInstance(ctx, id)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Warn("session: EnvelopeLookup: GetEnvelopeInstance failed",
					"envelope_id", id, "err", err)
			}
			return
		}
		lookup[id] = inst
	}

	for _, msg := range messages {
		if msg.Envelope == "" {
			continue
		}
		raw := json.RawMessage(msg.Envelope)
		trimmed := bytes.TrimLeft(raw, " \t\r\n")
		if len(trimmed) == 0 {
			continue
		}
		if trimmed[0] == '[' {
			var arr []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &arr); err != nil {
				continue
			}
			for _, e := range arr {
				fetchID(e.ID)
			}
		} else {
			var env struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				continue
			}
			fetchID(env.ID)
		}
	}
	return lookup
}
