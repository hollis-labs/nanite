package selftools

import (
	"context"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"

	"github.com/hollis-labs/nanite/internal/store"
)

// ReadServices are cycle-safe collaborators supplied by the composition root.
// selftools cannot import service; production implementations live there.
type ReadServices struct {
	Skills     SkillReader
	Sessions   SessionReader
	Procedures ProcedureReader
	Handoffs   HandoffService
}
type SkillReader interface {
	List(context.Context) ([]store.Skill, error)
	Get(context.Context, string) (*store.Skill, error)
}
type SessionReader interface {
	Get(context.Context, string) (*store.Session, error)
	GetByShortCode(context.Context, string) (*store.Session, error)
	ListMessages(context.Context, string, int) ([]store.Message, error)
	ListMessagesPage(context.Context, string, int, int) (*store.MessagePage, error)
}
type ProcedureReader interface {
	GetProcedure(context.Context, string, string) (*store.AgentProcedure, error)
}
type HandoffService interface {
	Upsert(context.Context, store.HandoffStash) error
	Get(context.Context, string, string) (store.HandoffStash, error)
}

// WriteServices keep dispatch state and mutations behind service collaborators.
type WriteServices struct {
	Schedules  ScheduleWriter
	Membership PrimaryAgentReader
	Dispatch   DispatchReader
	Events     reflexes.TraceStore
}
type ScheduleWriter interface {
	InsertPrepared(context.Context, store.AgentSchedule) error
}
type PrimaryAgentReader interface {
	GetSessionPrimaryAgent(context.Context, string) (*store.SessionAgent, error)
}
type DispatchReader interface {
	IsSubagentSession(context.Context, string) (bool, error)
	GetAgent(context.Context, string) (*store.AgentProfile, error)
	ListAgentReflexesForAgent(context.Context, string, string) ([]store.AgentReflex, error)
	ResolveWorkflowRunIDForSession(context.Context, string) (string, bool, error)
	ListAgentReflexesForWorkflowRun(context.Context, string, string, string) ([]store.AgentReflex, error)
	GetReflexActionKind(context.Context, string) (*store.ReflexActionKind, error)
}
