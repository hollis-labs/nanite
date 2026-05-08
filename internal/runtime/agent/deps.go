package agent

import (
	"time"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// Dependencies is the composition-root injection point. The chat service,
// scheduler, and background dispatcher each construct one and pass it into
// Boot per spawn.
//
// Field types reference real nanite packages where the abstraction already
// exists (*store.AgentProfile, *permission.PathGrants); they reference lib
// types where the lib owns the contract (*agentsessions.Manager,
// provider.CLIAdapter); they declare local interfaces only where nanite
// currently spreads the responsibility across many call sites that Phase 4
// will normalize (Store, Telemetry, AgentProfiles, MCPConfig).
type Dependencies struct {
	// Agents resolves an AgentProfile name to the persisted profile row.
	// Phase 4 wires this against internal/service.AgentService or the
	// store directly.
	Agents AgentProfiles

	// SessionsManager owns the runtime lifecycle (Start / SendInput / Stop /
	// Wait / Checkpoint / Resume / Attach). Boot is a thin orchestration
	// layer over this. Constructed in the composition root; nanite has no
	// existing Manager wiring, so this is a fresh dependency for Phase 4.
	SessionsManager *agentsessions.Manager

	// Store persists the runtime-lifecycle row Boot writes prior to Start.
	// Phase 4 backs this against store.Session, subagent_runs, or a new
	// dedicated table — kept as an interface here so the composition root
	// chooses without rippling through the agent package.
	Store RuntimeStore

	// PathGrants registers and clears subagent lineage. ModeSubagent
	// registers <child, parent> on Boot; the manager clears on session
	// stop via the lifecycle hook.
	PathGrants *permission.PathGrants

	// EventFanout returns the channel the runtime fans
	// provider.StreamEvent onto for downstream consumers (cost ledger,
	// chat-stream sink). The composition root translates StreamEvent ->
	// chat.StreamEvent before forwarding to the FE; that bridge is
	// outside the agent package.
	EventFanout func(sessionID string) chan<- provider.StreamEvent

	// TypedEventCallback returns the per-line typed-event handler used to
	// surface CLI-internal tool calls as nanite SSE tool_call /
	// tool_result events. Closes G-PTY-NO-TOOL-EVENTS.
	TypedEventCallback func(sessionID string) provider.EventsCallback

	// ProviderAdapter resolves a provider name to its CLI adapter
	// (claude / codex / opencode / ...). The adapter advertises its
	// Caps and BootDirSpec.
	ProviderAdapter func(providerName string) provider.CLIAdapter

	// MCPConfig describes how to plant the per-session .mcp.json. Nanite
	// MCP transport is subprocess-spawn-based: the planted config names
	// the nanite binary and the per-session args ("mcp --db <db> --session
	// <sessID>"). The composition root constructs an MCPConfig with the
	// resolved binary path and the active store DB path.
	MCPConfig MCPConfig

	// WorkspacesRoot is the persistent workspace base dir
	// (default ~/.nanite/workspaces).
	WorkspacesRoot string

	// Telemetry receives PTY restart and lifecycle observability events.
	Telemetry Telemetry

	// SandboxBaseProfile is the starting profile composed per-spawn with
	// AllowLoopback, FS allowlists, and ModeBackground/WideOpen overrides.
	SandboxBaseProfile sandbox.Profile
}

// AgentProfiles resolves agent profile names. Backed by
// internal/store.GetAgentBySlug + a service-layer fallback, or by an in-memory
// registry for tests.
type AgentProfiles interface {
	// GetOrDefault returns the named profile or a sensible default when
	// name is empty / unresolved.
	GetOrDefault(name string) (*store.AgentProfile, error)
}

// RuntimeStore is the persistence contract Boot relies on for the runtime
// lifecycle row. Backed by internal/store in production; the row may live
// in store.sessions, subagent_runs, or a dedicated agent_runtime table —
// the composition root decides.
type RuntimeStore interface {
	// CreateRuntimeRow persists the launching-state row Boot creates
	// before SessionsManager.Start. The id is the agent runtime id
	// (typically equal to opts.SessionID for chat ModeLongLived).
	CreateRuntimeRow(row *RuntimeRow) error

	// MarkRuntimeFailed transitions the row to state="failed" with reason.
	MarkRuntimeFailed(runtimeID, reason string) error

	// SetProviderSessionID records the provider-side session id (claude's
	// session_id, codex's, etc.) once the adapter reports it via
	// StartOptions.OnSessionID.
	SetProviderSessionID(runtimeID, providerSessionID string) error

	// GetCheckpoint loads a runtime checkpoint payload for ModeResume.
	GetCheckpoint(checkpointID string) (*RuntimeCheckpoint, error)
}

// RuntimeRow is the lifecycle-tracking row Boot writes. Distinct from
// store.Session (which tracks chat sessions) because not every Boot is a
// chat session: ModeBackground tasks, scheduler-dispatched executors, and
// nested subagents all create runtime rows without owning a chat row.
type RuntimeRow struct {
	ID              string
	AgentProfile    string
	Provider        string
	Mode            string
	Workdir         string
	State           string
	ParentSessionID *string
	StartedAt       time.Time
	Meta            map[string]any
}

// RuntimeCheckpoint is the resume payload Boot consults for ModeResume.
type RuntimeCheckpoint struct {
	ID                string
	ProviderSessionID string
	CapturedAt        time.Time
}

// MCPConfig carries the inputs the bootdir layouts need to plant a valid
// .mcp.json subprocess descriptor.
type MCPConfig struct {
	// BinaryPath is the absolute path to the nanite binary. Resolved once
	// at composition root; symlinks already evaluated.
	BinaryPath string

	// DBPath is the active store DB the spawned MCP subprocess should
	// open. Empty disables MCP planting (some tests / standalone runs).
	DBPath string

	// ServerID identifies the planted MCP server entry. Defaults to
	// brand.ID when empty.
	ServerID string
}

// Telemetry is the observability sink. The production composition root
// wires this against nanite's existing OpenTelemetry plumbing.
type Telemetry interface {
	RecordPTYRestart(sessionID string, attempt int, prevExit *agentsessions.ExitError)
}

// noopTelemetry is the test-friendly default when no observability is wired.
type noopTelemetry struct{}

func (noopTelemetry) RecordPTYRestart(string, int, *agentsessions.ExitError) {}

// Compile-time interface compliance assertions.
var _ Telemetry = noopTelemetry{}
