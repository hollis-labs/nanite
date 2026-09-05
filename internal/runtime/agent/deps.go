package agent

import (
	"context"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
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
// types where the lib owns the contract (provider.CLIAdapter); they declare
// local interfaces only where Nanite owns persistence, profile lookup, or
// telemetry contracts (Store, Telemetry, AgentProfiles, MCPConfig).
type Dependencies struct {
	// Agents resolves an AgentProfile name to the persisted profile row.
	// Phase 4 wires this against internal/service.AgentService or the
	// store directly.
	Agents AgentProfiles

	// Manager is the single runtime-ID -> Wrapper handle registry shared by
	// Boot, chat lookup, orphan reconciliation, recovery replacement, and
	// daemon shutdown. It contains no independent process state; lifecycle
	// control delegates to wrapper.Wrapper and its shared ACP manager.
	Manager *SessionManager

	// Store persists the runtime-lifecycle row Boot writes prior to
	// constructing the wrapper.Wrapper that drives the session.
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

	// RuntimeEventSink optionally supplies Nanite's canonical normalized
	// event destination. Boot writes every runtime event to this sink before
	// the deliberately lossy legacy chat/SSE projection. Nil means no canonical
	// capture: raw events are never persisted by default. Callers that inject a
	// sink own its redaction, retention, bounds, and lifecycle. Unknown event
	// kinds remain internal; this does not expose them via API. isACP lets a
	// sink that deliberately owns the legacy stream projection preserve the
	// protocol's usage+terminal coalescing without inspecting adapter internals.
	RuntimeEventSink func(sessionID string, isACP bool) runtimeevents.Sink

	// Permissions and ApprovalRequestSink form the existing Nanite approval
	// layer used only when an ACP provider sends session/request_permission.
	// When either is nil Boot leaves the wrapper responder nil, retaining the
	// wrapper's safe default-cancel behavior. This is best-effort provider
	// cooperation, not a replacement for toolclient/RPC authorization.
	Permissions         *permission.Engine
	ApprovalRequestSink func(*permission.ApprovalRequest)

	// ProviderAdapter resolves a provider name to its CLI adapter
	// (claude / codex / opencode / ...). The adapter advertises its
	// Caps and BootDirSpec.
	ProviderAdapter func(providerName string) provider.CLIAdapter

	// ACPAdapterFactory is the test/composition seam for an ACP-backed
	// adapters.Adapter. Production leaves it nil and uses the five shipped
	// wrapper adapters selected by newACPAdapter.
	ACPAdapterFactory func(providerName string, transport adapters.Transport) (adapters.Adapter, error)

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

	// LiveSessions, when non-nil, lets orphansweep.RuntimeReaper.SweepOnce
	// probe the in-process session registry for runtime IDs whose persisted
	// PID is 0 (codex-style adapters never report a pid). Production wires
	// the same Manager used by chat/session control; tests pass a fake. Optional —
	// nil leaves orphansweep.RuntimeReaper.SweepOnce falling back to updated_at
	// staleness alone, which still reconciles pre-restart pid=0 rows once
	// they age past the grace window.
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

	// Skills resolves an agent's plantable skill set (grant + catalog
	// lookups) for skill_plant.go's CLI-hosted native skill delivery
	// (TASKS/skills/10). nil disables skill planting entirely — see
	// SetupParams.Skills' doc comment.
	Skills SkillStore

	// SkillVendor reads a plantable skill's vendored file tree for
	// skill_plant.go. nil disables skill planting, same as a nil Skills.
	SkillVendor SkillVendorReader
}

// LiveSessionChecker reports whether the in-process session registry has
// an entry for runtimeID. Production wires this against SessionManager, the
// same binding registry used by Boot and chat control; tests pass a fake.
// Optional in Dependencies — nil means orphansweep.RuntimeReaper.SweepOnce falls back
// to PID + updated_at staleness only, which still catches the common
// post-restart case (a fresh process has an empty registry, so every
// persisted row is "no live session").
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
	// before constructing the wrapper.Wrapper that drives this session.
	// The id is the agent runtime id (typically equal to opts.SessionID
	// for chat ModeLongLived).
	CreateRuntimeRow(row *RuntimeRow) error

	// MarkRuntimeFailed transitions the row to state="failed" with reason.
	MarkRuntimeFailed(runtimeID, reason string) error

	// UpdateState records a session lifecycle state transition
	// ("running" once wrapper.Wrapper.Run's session is confirmed started;
	// "done" or "failed" once it exits) together with the process's
	// current PID (0 when the runtime kind doesn't expose one, e.g.
	// Codex/OpenCode's subprocess-per-turn shape between turns).
	//
	// wrapper.Wrapper owns runtime construction and drives it to completion;
	// Boot and the session's completion path call UpdateState at the wrapper
	// readiness and exit boundaries. orphansweep.RuntimeReaper.SweepOnce and
	// any UI/API surface reading agent_runtime.state depend on this not
	// regressing to a permanent "launching" row.
	UpdateState(runtimeID, state string, pid int) error

	// SetProviderSessionID records the provider-side session id (claude's
	// session_id, codex's, etc.) once the adapter reports it via
	// StartOptions.OnSessionID.
	SetProviderSessionID(runtimeID, providerSessionID string) error

	// GetCheckpoint loads a runtime checkpoint payload for ModeResume.
	GetCheckpoint(checkpointID string) (*RuntimeCheckpoint, error)

	// ListRunningRows returns the persisted lifecycle rows currently in
	// state="launching" or state="running". Used by orphansweep.RuntimeReaper.SweepOnce at
	// daemon bootstrap to reconcile rows whose PID is no longer alive. The
	// caller's context must reach the database query so shutdown can cancel it.
	ListRunningRows(ctx context.Context) ([]*RuntimeRow, error)

	// MarkRuntimeOrphaned transitions a runtime row to state="orphaned"
	// with the supplied reason. orphansweep.RuntimeReaper.SweepOnce calls this for rows whose
	// persisted PID is no longer alive; ctx governs that persistence call.
	MarkRuntimeOrphaned(ctx context.Context, runtimeID, reason string) error

	// LogEvent appends a row to the shared event_log postmortem trail.
	// orphansweep.RuntimeReaper.SweepOnce calls this alongside MarkRuntimeOrphaned so
	// every reconciliation leaves a queryable, reasoning-populated record
	// (docs/engineering/architecture/06-session-lifecycle-and-recovery.md:
	// "extend event_log logging to all four [recovery mechanisms]").
	// sessionID is the runtime row's ID (equal to the chat session ID for
	// ModeLongLived rows; a scoped subagent/background run ID otherwise —
	// event_log.session_id carries no FK constraint, so this is always
	// safe to write). metadata should be a JSON object, not a bare string.
	LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string)
}

// RuntimeRow is the lifecycle-tracking row Boot writes. Distinct from
// store.Session (which tracks chat sessions) because not every Boot is a
// chat session: ModeBackground tasks, scheduler-dispatched executors, and
// nested subagents all create runtime rows without owning a chat row.
//
// UpdatedAt is the last persistence-side modification timestamp. orphansweep.RuntimeReaper.SweepOnce
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
