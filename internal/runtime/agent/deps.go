package agent

import (
	"context"
	"time"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-sandbox/sandbox"
)

// Dependencies is the composition-root injection point. The chat service,
// scheduler, and background dispatcher each construct one of these and pass
// it into Boot per spawn.
//
// Concrete field types are deliberately interface-shaped where the
// surrounding nanite packages already define the abstraction (Store,
// PathGrants, EventFanout); they pin to lib types where the lib owns
// the contract (SessionsManager, ProviderAdapter).
type Dependencies struct {
	// Agents resolves an AgentProfile name to its full configuration
	// (provider, binary, args, env policy, role assembly inputs).
	Agents AgentProfiles

	// SessionsManager owns the runtime lifecycle (Start / SendInput / Stop /
	// Wait / Checkpoint / Resume / Attach). Boot is a thin orchestration
	// layer over this.
	SessionsManager *agentsessions.Manager

	// Store persists the session row + checkpoints. Boot writes the row
	// in state=launching prior to Start; the manager promotes it as the
	// runtime emits state events.
	Store SessionStore

	// PathGrants is the path-authority register. ModeSubagent registers
	// lineage here; cleanup defers to session stop.
	PathGrants PathGrants

	// EventFanout returns a per-session channel the runtime fans
	// provider.StreamEvent values onto. Production composition roots
	// allocate the channel and own the consumer goroutine; nanite reuses
	// the existing chat-stream sink here.
	EventFanout func(sessionID string) chan<- provider.StreamEvent

	// TypedEventCallback returns the per-line typed-event handler used to
	// surface CLI-internal tool calls as nanite SSE tool_call/tool_result
	// events. Closes G-PTY-NO-TOOL-EVENTS.
	TypedEventCallback func(sessionID string) provider.EventsCallback

	// ProviderAdapter resolves a provider name to its CLI adapter
	// (claude / codex / opencode / ...). The adapter advertises its
	// Caps and BootDirSpec.
	ProviderAdapter func(providerName string) provider.CLIAdapter

	// MCPLoopback exposes the per-session MCP loopback URL planted in the
	// agent's .mcp.json. Required for tools to reach back into nanite.
	MCPLoopback MCPLoopback

	// WorkspacesRoot is the persistent workspace base dir
	// (default ~/.nanite/workspaces).
	WorkspacesRoot string

	// Telemetry receives PTY restart and lifecycle observability events.
	Telemetry Telemetry

	// SandboxBaseProfile is the starting profile composed per-spawn with
	// AllowLoopback, FS allowlists, and ModeBackground/WideOpen overrides.
	SandboxBaseProfile sandbox.Profile
}

// AgentProfiles is the minimal interface Boot needs from the existing
// internal/agent package to resolve a profile name. Phase 3 will refine
// this as the chat-service composition root takes shape.
type AgentProfiles interface {
	GetOrDefault(name string) AgentProfile
}

// AgentProfile is a snapshot of the resolved agent configuration. Phase 3
// will switch this to a concrete type referencing internal/agent or
// internal/store; declared here so the skeleton compiles standalone.
type AgentProfile struct {
	Name     string
	Provider string
	Binary   string
	Args     []string
	Env      map[string]string
}

// SessionStore is the persistence contract Boot relies on. The full
// internal/store.Session shape lives in nanite's existing store package;
// this minimal interface lets the skeleton compile before Phase 3 wires
// the concrete dependency.
type SessionStore interface {
	CreateSession(s *StoreSession) error
	MarkSessionFailed(sessionID, reason string) error
	SetAgentSessionID(sessionID, providerSessionID string) error
	GetSessionCheckpoint(checkpointID string) (*StoreCheckpoint, error)
}

// StoreSession is the persistence shape Boot writes. Field set chosen to
// match the columns the existing internal/store.Session row owns. Phase 3
// will replace this with a type alias / direct dependency.
type StoreSession struct {
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

// StoreCheckpoint is the resume payload Boot consults for ModeResume.
type StoreCheckpoint struct {
	ID                string
	ProviderSessionID string
	CapturedAt        time.Time
}

// PathGrants is the path-authority lineage register. Phase 3 will pin
// this to internal/permission.PathGrants.
type PathGrants interface {
	RegisterLineage(childSessionID, parentSessionID string) error
	ReleaseLineage(sessionID string)
}

// MCPLoopback exposes the per-session loopback URL.
type MCPLoopback interface {
	URL(sessionID string) string
}

// Telemetry is the observability sink. Phase 3 will compose nanite's
// existing OpenTelemetry plumbing here.
type Telemetry interface {
	RecordPTYRestart(sessionID string, attempt int, prevExit *agentsessions.ExitError)
}

// noop helpers used by Phase 2 tests / future no-op composition
// roots; intentionally unexported so callers must construct
// Dependencies explicitly.
type noopPathGrants struct{}

func (noopPathGrants) RegisterLineage(string, string) error { return nil }
func (noopPathGrants) ReleaseLineage(string)                {}

type noopTelemetry struct{}

func (noopTelemetry) RecordPTYRestart(string, int, *agentsessions.ExitError) {}

var (
	_ PathGrants = noopPathGrants{}
	_ Telemetry  = noopTelemetry{}
	_           = context.Background // keep import in place for Phase 3 wiring
)
