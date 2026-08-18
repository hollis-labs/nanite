package store

import (
	"context"
	"time"
)

// AgentStateStore is the indirection seam over the five per-agent durable
// state tables introduced by migration 065. Today *Store satisfies the
// interface against the central agridd SQLite DB sharded by agent_id; a
// future per-agent-file backend (one SQLite file per agent, or per-agent
// JSON files on disk) can implement the same surface and swap in without
// touching call sites.
//
// Methods are grouped by table to keep the interface scannable. Adding a
// new table here means adding a new group below, then making both the
// central-DB backend and any per-agent-file backend implement the new
// methods in the same commit.
type AgentStateStore interface {
	// agent_known_tools — per-agent tool roster with activation telemetry.
	InsertAgentKnownTool(ctx context.Context, row AgentKnownTool) error
	ListAgentKnownTools(ctx context.Context, agentID string) ([]AgentKnownTool, error)
	GetAgentKnownTool(ctx context.Context, agentID, toolName string) (*AgentKnownTool, error)
	DeleteAgentKnownTool(ctx context.Context, agentID, toolName string) error
	BumpActivation(ctx context.Context, agentID, toolName string) error

	// agent_known_skills — identical shape to known tools, swapping the
	// secondary key for skill_name.
	InsertAgentKnownSkill(ctx context.Context, row AgentKnownSkill) error
	ListAgentKnownSkills(ctx context.Context, agentID string) ([]AgentKnownSkill, error)
	GetAgentKnownSkill(ctx context.Context, agentID, skillName string) (*AgentKnownSkill, error)
	DeleteAgentKnownSkill(ctx context.Context, agentID, skillName string) error

	// agent_procedures — named procedure bodies recorded per agent. Scope
	// is "agent" today; "shared" and future scopes are valid column
	// values.
	InsertAgentProcedure(ctx context.Context, row AgentProcedure) error
	ListAgentProcedures(ctx context.Context, agentID string) ([]AgentProcedure, error)
	GetAgentProcedure(ctx context.Context, agentID, name string) (*AgentProcedure, error)
	DeleteAgentProcedure(ctx context.Context, agentID, name string) error

	// agent_log — append-only per-agent log (passes, lessons, etc.).
	AppendLog(ctx context.Context, row AgentLogEntry) error
	ListLog(ctx context.Context, agentID string, limit int) ([]AgentLogEntry, error)

	// agent_knowledge_seed — manifest of memory_keys to seed into
	// Tesseract on first activation (consumer wiring lands in FU-7f).
	InsertAgentKnowledgeSeed(ctx context.Context, row AgentKnowledgeSeed) error
	ListAgentKnowledgeSeeds(ctx context.Context, agentID string) ([]AgentKnowledgeSeed, error)
	GetAgentKnowledgeSeed(ctx context.Context, agentID, seedKey string) (*AgentKnowledgeSeed, error)
	DeleteAgentKnowledgeSeed(ctx context.Context, agentID, seedKey string) error
	// MarkAgentKnowledgeSeedApplied stamps applied_at on a seed row once
	// the body has been written to the target namespace by the FU-7f boot
	// hook.
	MarkAgentKnowledgeSeedApplied(ctx context.Context, agentID, seedKey string) error

	// agent_schedules — directive rows the composer (FU-27) folds into
	// the per-tick procedure body. Authored by operator, agent (self),
	// or system.
	InsertAgentSchedule(ctx context.Context, row AgentSchedule) error
	GetAgentSchedule(ctx context.Context, id string) (*AgentSchedule, error)
	ListAgentSchedules(ctx context.Context, agentID string) ([]AgentSchedule, error)
	DeleteAgentSchedule(ctx context.Context, id string) error
	UpdateAgentScheduleStatus(ctx context.Context, id, status string) error
	BumpAgentScheduleFireCount(ctx context.Context, id string, now time.Time) error
	// GetDueSchedules returns the active schedules whose firing criteria
	// match the given tick context, sorted priority DESC, created_at ASC.
	GetDueSchedules(ctx context.Context, agentID, sessionID string, tickN int, now time.Time) ([]AgentSchedule, error)
}

// Compile-time assertion that the central-DB *Store satisfies the seam.
// The per-agent-file backend will add a parallel assertion in its own
// file when it lands.
var _ AgentStateStore = (*Store)(nil)
