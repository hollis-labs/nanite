package agent

import (
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
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
	// llmtypes.StreamEvent onto for downstream consumers (cost ledger,
	// chat-stream sink). The composition root translates StreamEvent ->
	// chat.StreamEvent before forwarding to the FE; that bridge is
	// outside the agent package.
	EventFanout func(sessionID string) chan<- llmtypes.StreamEvent

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

	// CLIWritableRoots is the allow-list of directories a CLI-launch
	// agent (codex / claude) may write to beyond its throwaway boot
	// dir. The composition root populates it from the nanite
	// dev_tools_allowed_paths config setting; composeBootdirParams
	// copies it into SetupParams so the boot-dir layouts can thread it
	// into the planted provider config (codex [sandbox_workspace_write]
	// writable_roots, claude permissions.additionalDirectories).
	// Empty leaves CLI agents confined to their boot dir cwd
	// (CW-20260518-0075).
	CLIWritableRoots []string

	// Telemetry receives PTY restart and lifecycle observability events.
	Telemetry Telemetry

	// LiveSessions, when non-nil, lets orphansweep.SweepOrphans probe the in-process
	// session registry for runtime IDs whose persisted PID is 0 (codex-
	// style adapters never report a pid). Production wires this against
	// agentsessions.Manager.Get; tests pass a fake. Optional — nil leaves
	// orphansweep.SweepOrphans falling back to updated_at staleness alone, which
	// still reconciles pre-restart pid=0 rows once they age past the
	// grace window.
	LiveSessions LiveSessionChecker

	// Recovery, when non-nil, observes lib-level restart attempts and
	// terminal session exits. Implemented by the in-process recovery
	// broker (internal/recovery/broker). Optional — nil leaves
	// the chat harness's existing per-turn recoverable-error handling
	// as the only remediation path.
	Recovery RecoveryHooks

	// SandboxBaseProfile is the starting profile composed per-spawn with
	// AllowLoopback, FS allowlists, and ModeBackground/WideOpen overrides.
	SandboxBaseProfile sandbox.Profile
}

// LiveSessionChecker reports whether the in-process session registry has
// an entry for runtimeID. Production wires this against
// agentsessions.Manager.Get; tests pass a fake. Optional in Dependencies —
// nil means orphansweep.SweepOrphans falls back to PID + updated_at
// staleness only, which still catches the common post-restart case (a
// fresh process has an empty registry, so every persisted row is "no live
// session").
//
// Declared here (rather than in internal/recovery/orphansweep, which
// implements the sweep logic that consumes it) because Dependencies.
// LiveSessions references it directly — moving it out would force this
// package to import orphansweep, which itself must import this package
// for agent.Dependencies/agent.RuntimeRow, creating a cycle.
type LiveSessionChecker interface {
	IsLive(runtimeID string) bool
}

// RecoveryHooks is the in-process subagent recovery broker's hook
// surface. Implemented by broker.Broker; declared here so the agent
// package can hold the contract without importing broker (which
// would create an import cycle).
//
// OnRestart is invoked from agent.Boot's SupervisorOptions.OnRestart
// closure when the lib-level supervisor triggers a restart. The broker
// observes (records breadcrumb, optionally overlays an info-card) but
// does not re-dispatch — the lib has already handled the restart.
//
// OnSessionExit is invoked from the chat composition root's
// Wait-observer goroutine when a session's terminal *ExitError lands.
// The meta bag carries chat-side context (stderr tail, sandbox state,
// MCP transport health, etc.) the classifier consults; missing keys
// degrade to zero-valued FailureEvent fields.
type RecoveryHooks interface {
	OnRestart(sessionID string, attempt int, prevExit *agentsessions.ExitError)
	OnSessionExit(sessionID string, exit *agentsessions.ExitError, meta map[string]any)
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

	// ListRunningRows returns the persisted lifecycle rows currently in
	// state="launching" or state="running". Used by orphansweep.SweepOrphans at
	// daemon bootstrap to reconcile rows whose PID is no longer alive.
	ListRunningRows() ([]*RuntimeRow, error)

	// MarkRuntimeOrphaned transitions a runtime row to state="orphaned"
	// with the supplied reason. orphansweep.SweepOrphans calls this for rows whose
	// persisted PID is no longer alive.
	MarkRuntimeOrphaned(runtimeID, reason string) error
}

// RuntimeRow is the lifecycle-tracking row Boot writes. Distinct from
// store.Session (which tracks chat sessions) because not every Boot is a
// chat session: ModeBackground tasks, scheduler-dispatched executors, and
// nested subagents all create runtime rows without owning a chat row.
//
// UpdatedAt is the last persistence-side modification timestamp. orphansweep.SweepOrphans
// uses it as the staleness signal for rows where PID == 0 — codex-style
// runtimes never persist a pid, so signal-0 liveness can't speak for them;
// the in-memory session-liveness probe + an `updated_at` age threshold do
// the work instead.
type RuntimeRow struct {
	ID              string
	AgentProfile    string
	Provider        string
	Mode            string
	Workdir         string
	State           string
	PID             int
	ParentSessionID *string
	StartedAt       time.Time
	UpdatedAt       time.Time
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

	// APIBaseURL, when set, is the base URL of the live nanite API server
	// (e.g. "http://127.0.0.1:8090"). It is planted into the boot dir's
	// .mcp.json as the NANITE_API_URL env var so the spawned `nanite mcp`
	// subprocess forwards self-tool calls to the running harness instead
	// of dispatching them against its own bare store. Empty leaves the
	// subprocess in local-only mode (store-backed self-tools, no live
	// services).
	APIBaseURL string
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
