package service

import (
	"context"
	"time"

	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/store"
)

// NewSelfToolsTransport wires both daemon and standalone MCP tools through
// domain services without importing service back into selftools.
func NewSelfToolsTransport(st *store.Store) *selftools.SelfToolsTransport {
	transport := selftools.NewSelfToolsTransport(st, selftools.ReadServices{
		Skills:     NewSkillService(SkillServiceConfig{Skills: st}),
		Sessions:   &selfToolSessionReader{sessions: NewSessionService(SessionServiceDeps{Sessions: st}), shortCodes: st},
		Procedures: NewAgentCapabilitiesService(st),
		Handoffs:   NewHandoffService(st),
	}, selftools.WriteServices{
		Pins: NewPinService(st), Reminders: NewReminderService(st), Schedules: NewScheduleService(st),
		Membership: NewAgentMembershipService(st, st), Dispatch: &selfToolDispatchService{store: st, reflexes: NewReflexService(st)}, Events: &selfToolEventService{store: st},
	})
	if st != nil {
		transport.WorkTrackingTools.Updater = &selfToolTodoUpdater{NewTodoService(TodoServiceConfig{Todos: st, Plans: st})}
	}
	return transport
}

// NewWorkTrackingTools wires todo updates through the same service as HTTP.
func NewWorkTrackingTools(st *store.Store, broadcaster selftools.WorkBroadcaster) *selftools.WorkTrackingTools {
	tools := selftools.NewWorkTrackingTools(st, st, broadcaster)
	if st != nil {
		tools.Updater = &selfToolTodoUpdater{NewTodoService(TodoServiceConfig{Todos: st, Plans: st})}
	}
	return tools
}

type selfToolTodoUpdater struct{ todos TodoService }

func (s *selfToolTodoUpdater) UpdateTodoFields(ctx context.Context, id string, fields selftools.TodoUpdateFields) (*store.Todo, error) {
	return s.todos.UpdateTodo(ctx, id, TodoUpdates(fields))
}

type selfToolSessionReader struct {
	sessions   SessionService
	shortCodes interface {
		GetSessionByShortCode(context.Context, string) (*store.Session, error)
		GetSession(context.Context, string) (*store.Session, error)
	}
}

func (s *selfToolSessionReader) Get(ctx context.Context, id string) (*store.Session, error) {
	return s.shortCodes.GetSession(ctx, id)
}
func (s *selfToolSessionReader) GetByShortCode(ctx context.Context, code string) (*store.Session, error) {
	return s.shortCodes.GetSessionByShortCode(ctx, code)
}
func (s *selfToolSessionReader) ListMessages(ctx context.Context, id string, limit int) ([]store.Message, error) {
	return s.sessions.ListMessages(ctx, id, limit)
}
func (s *selfToolSessionReader) ListMessagesPage(ctx context.Context, id string, limit, offset int) (*store.MessagePage, error) {
	return s.sessions.ListMessagesPage(ctx, id, limit, offset)
}

// HandoffService persists already-validated stash envelopes. Envelope parsing
// and MCP response formatting remain owned by the caller.
type HandoffService struct{ store HandoffStashStore }

func NewHandoffService(st HandoffStashStore) *HandoffService { return &HandoffService{store: st} }
func (s *HandoffService) Upsert(ctx context.Context, row store.HandoffStash) error {
	return s.store.UpsertHandoffStash(ctx, row)
}
func (s *HandoffService) Get(ctx context.Context, sessionID, id string) (store.HandoffStash, error) {
	return s.store.GetHandoffStash(ctx, sessionID, id)
}

// selfToolDispatchService supplies the authoritative dispatch facts; matching
// and cooldown resolution remain in the shared reflex resolver.
type selfToolDispatchService struct {
	store    *store.Store
	reflexes *ReflexService
}

func (s *selfToolDispatchService) IsSubagentSession(ctx context.Context, id string) (bool, error) {
	return s.store.IsSubagentSession(ctx, id)
}
func (s *selfToolDispatchService) GetAgent(ctx context.Context, id string) (*store.AgentProfile, error) {
	return s.store.GetAgent(ctx, id)
}
func (s *selfToolDispatchService) ListAgentReflexesForAgent(ctx context.Context, id, class string) ([]store.AgentReflex, error) {
	return s.reflexes.ListForAgent(ctx, id, class)
}
func (s *selfToolDispatchService) ResolveWorkflowRunIDForSession(ctx context.Context, id string) (string, bool, error) {
	return s.store.ResolveWorkflowRunIDForSession(ctx, id)
}
func (s *selfToolDispatchService) ListAgentReflexesForWorkflowRun(ctx context.Context, runID, id, class string) ([]store.AgentReflex, error) {
	return s.store.ListAgentReflexesForWorkflowRun(ctx, runID, id, class)
}
func (s *selfToolDispatchService) GetReflexActionKind(ctx context.Context, kind string) (*store.ReflexActionKind, error) {
	return s.store.GetReflexActionKind(ctx, kind)
}

// selfToolEventService keeps broker, reflex and reaction telemetry on the
// same event log, preserving each caller's cancellation policy.
type selfToolEventService struct{ store *store.Store }

func (s *selfToolEventService) LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string) {
	s.store.LogEvent(ctx, sessionID, eventType, category, detail, metadata)
}
func (s *selfToolEventService) BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error {
	return s.store.BumpAgentReflexFired(ctx, id, now)
}
