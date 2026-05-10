package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/permission"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/runtime/agent/recovery"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentDepsConfig holds the inputs the chat-service composition root uses to
// build *runtimeagent.Dependencies. Construction is deliberately a separate
// step from chatServiceImpl wiring so the deps can be re-used by the subagent
// runner and (eventually) the background dispatcher without re-creating the
// agentsessions.Manager singleton.
type AgentDepsConfig struct {
	Store           *store.Store
	PathGrants      *permission.PathGrants
	Streams         *StreamManager
	CLIAdapters     []provider.CLIAdapter
	WorkspacesRoot  string
	BinaryPath      string
	DBPath          string
	SandboxBaseProf sandbox.Profile

	// MCP, when non-nil, wires the recovery broker's MCP-transport
	// remediation adapter (recovery.MCPControl). The adapter forwards
	// RestartTransport to mcp.Manager.RestartStdioTransports so the broker
	// can recover from a wedged MCP stdio subprocess. Nil leaves
	// Dependencies.MCP unwired and the broker degrades to escalating
	// RemediationRefreshMCPTransport classifications as Permanent.
	MCP *mcp.Manager

	// Providers is the API provider registry the recovery broker's
	// credentials adapter consults to push refreshed keys onto cached
	// SDK clients. Optional — nil disables the credentials remediation
	// path entirely (Refresh returns an error and the broker escalates
	// to Permanent).
	Providers *provider.Registry
}

// AgentDepsBundle aggregates the artifacts BuildAgentDependencies returns.
// Distinct from a tuple return so callers can pluck the registry-bearing
// adapters they need without restructuring the signature each time a new
// composition-root adapter joins the broker wiring.
type AgentDepsBundle struct {
	// Deps is the composed runtime agent.Dependencies struct passed
	// into runtimeagent.Boot.
	Deps *runtimeagent.Dependencies

	// Manager is the singleton agentsessions.Manager held by the chat
	// service for daemon-bootstrap orphan sweep + Shutdown drain.
	Manager *agentsessions.Manager

	// Bridge is the agentEventBridge held by the chat service so
	// driveBootSession can bind per-session routers.
	Bridge *agentEventBridge

	// BootDirAdapter is the recovery.BootDirOps adapter wired into the
	// recovery broker. The chat service calls Track / Untrack on it so
	// the broker has bootDir + Options on hand when a remediation fires.
	BootDirAdapter *agentBootDirAdapter
}

// BuildAgentDependencies wires a *runtimeagent.Dependencies plus the singleton
// agentsessions.Manager. Returns an AgentDepsBundle aggregating the composed
// Dependencies struct, the Manager instance (for daemon-bootstrap orphan
// sweep + Shutdown drain), the agentEventBridge (held by the chat service
// so driveBootSession can bind per-turn routers), and the per-adapter
// registry handles (BootDirAdapter today; MCP / Credentials siblings land
// alongside as their adapters wire in).
//
// The composition root is the single point that:
//
//   - constructs *agentsessions.Manager with its sinks
//   - wires the Store-backed RuntimeStore
//   - wires the AgentProfileResolver
//   - resolves the per-provider CLIAdapter
//   - threads the EventFanout / TypedEventCallback factories the chat service
//     binds per-session.
func BuildAgentDependencies(cfg AgentDepsConfig) (AgentDepsBundle, error) {
	if cfg.Store == nil {
		return AgentDepsBundle{}, errors.New("BuildAgentDependencies: Store is required")
	}
	if cfg.Streams == nil {
		return AgentDepsBundle{}, errors.New("BuildAgentDependencies: Streams is required")
	}

	binPath := cfg.BinaryPath
	if binPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return AgentDepsBundle{}, fmt.Errorf("BuildAgentDependencies: resolve binary: %w", err)
		}
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			binPath = resolved
		} else {
			binPath = exe
		}
	}

	workspacesRoot := cfg.WorkspacesRoot
	if workspacesRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			workspacesRoot = filepath.Join(home, "."+brand.ID, "workspaces")
		}
	}

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = cfg.Store.DBPath()
	}

	stateSink := &agentRuntimeStateSink{store: cfg.Store}
	eventSink := &agentRuntimeEventSink{}
	manager := agentsessions.NewManager(stateSink).
		WithEventSink(eventSink)

	resolver := &agentProfileResolver{store: cfg.Store}
	runtimeStore := &agentRuntimeStore{store: cfg.Store}

	adapterIndex := indexAdapters(cfg.CLIAdapters)
	providerAdapter := func(name string) provider.CLIAdapter {
		if name == "" {
			return nil
		}
		// Strip nanite registry prefixes ("pty-", "sub-") so callers can
		// pass a session-side provider name verbatim. The adapter Name
		// itself is always the bare adapter (claude/codex/opencode/...).
		bare := stripRegistryPrefix(name)
		if a, ok := adapterIndex[bare]; ok {
			return a
		}
		return nil
	}

	telemetry := agentTelemetry{}

	bridge := &agentEventBridge{streams: cfg.Streams}

	deps := &runtimeagent.Dependencies{
		Agents:             resolver,
		SessionsManager:    manager,
		Store:              runtimeStore,
		PathGrants:         cfg.PathGrants,
		EventFanout:        bridge.fanout,
		TypedEventCallback: bridge.typedCallback,
		ProviderAdapter:    providerAdapter,
		MCPConfig: runtimeagent.MCPConfig{
			BinaryPath: binPath,
			DBPath:     dbPath,
			ServerID:   brand.ID,
		},
		WorkspacesRoot:     workspacesRoot,
		Telemetry:          telemetry,
		SandboxBaseProfile: cfg.SandboxBaseProf,
	}

	// BootDir adapter — satisfies recovery.BootDirOps by re-running the
	// per-provider sandbox-dir population logic against the existing
	// boot dir. The chat service calls bootDirAdapter.Track right after
	// each successful runtimeagent.Boot so the broker has bootDir +
	// Options on hand when a Repopulate / RegenerateCLAUDEMD remediation
	// fires.
	bootDirAdapter, err := newAgentBootDirAdapter(deps)
	if err != nil {
		return AgentDepsBundle{}, fmt.Errorf("BuildAgentDependencies: bootdir adapter: %w", err)
	}

	// Construct the in-process recovery broker and wire it into deps.
	// AgentBoot is a closure over `deps` so the broker dispatches
	// replacement sessions through the same composition root. BootDir
	// is wired here (Phase 9 — CW-20260510-0014): Repopulate /
	// RegenerateCLAUDEMD re-run the per-provider sandbox-dir population
	// logic against the existing boot dir. MCP is wired here when
	// cfg.MCP is non-nil (Phase 9 — CW-20260510-0015); otherwise it
	// stays nil and RemediationRefreshMCPTransport classifications
	// surface as broker errors handled by the orchestration layer.
	// Credentials is wired here (Phase 9, CW-20260510-0016): re-reads
	// the OS keychain via internal/secrets and pushes the fresh key
	// onto the cached internal/llm/{anthropic,openai}.Client via
	// SetAPIKey. CLI providers (claude/codex/opencode) intentionally
	// error from Refresh because their auth lives outside nanite's
	// reach — see recoveryCredentialsAdapter.Refresh for the full
	// disposition.
	brokerDeps := recovery.Dependencies{
		AgentBoot: &agentBootAdapter{deps: deps},
		BootDir:   bootDirAdapter,
		Store: &recoveryBrokerStore{
			store: cfg.Store,
		},
		Envelope: &recoveryEnvelopeSink{
			streams: cfg.Streams,
		},
		Credentials: newRecoveryCredentialsAdapter(resolver, cfg.Providers),
	}
	if cfg.MCP != nil {
		brokerDeps.MCP = &recoveryMCPAdapter{manager: cfg.MCP}
	}
	broker := recovery.NewBroker(brokerDeps)
	deps.Recovery = broker

	return AgentDepsBundle{
		Deps:           deps,
		Manager:        manager,
		Bridge:         bridge,
		BootDirAdapter: bootDirAdapter,
	}, nil
}

// agentBootAdapter satisfies recovery.AgentBoot by forwarding into
// agent.Boot with IsRelaunch=true so CreateRuntimeRow is skipped (the
// broker has already transitioned the runtime row via
// MarkAgentRuntimeRelaunching).
type agentBootAdapter struct {
	deps *runtimeagent.Dependencies
}

func (a *agentBootAdapter) Boot(ctx context.Context, opts runtimeagent.Options) (*runtimeagent.Session, error) {
	opts.IsRelaunch = true
	return runtimeagent.Boot(ctx, a.deps, opts)
}

// recoveryBrokerStore satisfies recovery.BrokerStore against the store
// package. MarkRuntimeRelaunching sets state="launching" with the
// broker's audit reason; WriteBreadcrumb persists into
// nanite_recovery_breadcrumbs (migration 054).
type recoveryBrokerStore struct {
	store *store.Store
}

func (s *recoveryBrokerStore) MarkRuntimeRelaunching(sessionID, reason string) error {
	return s.store.MarkAgentRuntimeRelaunching(sessionID, reason)
}

func (s *recoveryBrokerStore) WriteBreadcrumb(b recovery.Breadcrumb) error {
	return s.store.WriteRecoveryBreadcrumb(&store.RecoveryBreadcrumb{
		Timestamp:    b.Timestamp,
		SessionID:    b.SessionID,
		Class:        b.Class.String(),
		Cause:        b.Cause,
		Remediation:  b.Remediation.String(),
		Action:       b.Action.String(),
		Outcome:      b.Outcome.String(),
		AttemptCount: b.AttemptCount,
		DurationMs:   b.DurationFromFailure.Milliseconds(),
		Reason:       b.Reason,
	})
}

// mcpTransportRestarter is the narrow contract recoveryMCPAdapter needs
// from the host MCP manager. *mcp.Manager satisfies it via
// RestartStdioTransports. Defined as an interface (not a *mcp.Manager
// dependency) so unit tests can substitute a fake without spawning real
// stdio subprocesses.
type mcpTransportRestarter interface {
	RestartStdioTransports(ctx context.Context) error
}

// recoveryMCPAdapter satisfies recovery.MCPControl. Phase 9 (CW-20260510-0015)
// wiring: the broker's RemediationRefreshMCPTransport action lands here
// when the classifier observes MCPTransport.Down on a failure event.
//
// The adapter cycles every stdio MCP transport the manager owns. The
// transports use lazy start() — Close reaps the (possibly-wedged)
// subprocess; the next ListTools / CallTool from any caller spawns a
// fresh subprocess in its place. Non-stdio transports (HTTP / plugin /
// builtin) are skipped inside the manager since they don't have a
// subprocess to wedge.
//
// The agent CLI's own .mcp.json-spawned subprocess is owned by the agent
// process (claude / codex / opencode), not by the host. The broker's
// follow-up dispatch (replacement session via AgentBoot) handles that
// side: a relaunched session re-reads .mcp.json and respawns its own
// MCP subprocess from scratch. This adapter handles only the host-side
// stdio transports the manager itself spawned.
//
// Idempotency: relies on *mcp.StdioTransport.Close being a no-op when
// the subprocess is already reaped. Back-to-back RestartTransport calls
// during a still-restarting state cycle the second-call's no-op closes
// without panicking.
//
// Bounded by ctx — RestartStdioTransports returns ctx.Err() between
// transports so the broker's 10s remediation timeout is honoured.
type recoveryMCPAdapter struct {
	manager mcpTransportRestarter
}

func (a *recoveryMCPAdapter) RestartTransport(ctx context.Context, sessionID string) error {
	if a == nil || a.manager == nil {
		// Defensive: a fully-nil adapter would have been left out of
		// brokerDeps; this guards against a partially-constructed adapter
		// reaching the dispatch path.
		return errors.New("recovery: MCP adapter not wired")
	}
	if err := a.manager.RestartStdioTransports(ctx); err != nil {
		return fmt.Errorf("recovery: restart mcp transports: %w", err)
	}
	slog.Info("recovery: restarted mcp stdio transports", "session_id", sessionID)
	return nil
}


// stripRegistryPrefix drops the nanite registry-side prefix
// ("pty-claude" → "claude", "sub-codex" → "codex"). Callers that already
// pass the bare adapter name see no change.
func stripRegistryPrefix(name string) string {
	if strings.HasPrefix(name, "pty-") {
		return strings.TrimPrefix(name, "pty-")
	}
	if strings.HasPrefix(name, "sub-") {
		return strings.TrimPrefix(name, "sub-")
	}
	return name
}

// indexAdapters builds the providerName → CLIAdapter resolution map from
// the slice threaded through ContainerConfig.
func indexAdapters(in []provider.CLIAdapter) map[string]provider.CLIAdapter {
	out := make(map[string]provider.CLIAdapter, len(in))
	for _, a := range in {
		if a == nil {
			continue
		}
		out[a.Name()] = a
	}
	return out
}

// --- AgentProfiles adapter ---

// agentProfileResolver implements runtimeagent.AgentProfiles backed by the
// store. GetOrDefault is the only contract; Boot consults it to resolve the
// caller-supplied AgentProfile slug into a concrete row. An empty slug or an
// unknown slug falls back to a synthetic default profile (claude provider,
// no system prompt) so Boot can still spawn — the chat-service does not
// hand-craft profiles for ad-hoc sessions.
type agentProfileResolver struct {
	store *store.Store
}

func (r *agentProfileResolver) GetOrDefault(name string) (*store.AgentProfile, error) {
	if name != "" {
		if p, err := r.store.GetAgentBySlug(name); err == nil && p != nil {
			return p, nil
		}
		if p, err := r.store.GetAgent(name); err == nil && p != nil {
			return p, nil
		}
	}
	return &store.AgentProfile{
		ID:              "agent-runtime-default",
		Name:            "default",
		Slug:            "default",
		DefaultProvider: "claude",
	}, nil
}

// --- RuntimeStore adapter ---

// agentRuntimeStore implements runtimeagent.RuntimeStore against the store's
// agent_runtime table (migration 050).
type agentRuntimeStore struct {
	store *store.Store
}

func (s *agentRuntimeStore) CreateRuntimeRow(row *runtimeagent.RuntimeRow) error {
	if row == nil {
		return errors.New("agentRuntimeStore.CreateRuntimeRow: nil row")
	}
	parent := ""
	if row.ParentSessionID != nil {
		parent = *row.ParentSessionID
	}
	return s.store.CreateAgentRuntimeRow(&store.AgentRuntimeRow{
		ID:              row.ID,
		AgentProfile:    row.AgentProfile,
		Provider:        row.Provider,
		Mode:            row.Mode,
		Workdir:         row.Workdir,
		State:           row.State,
		PID:             row.PID,
		ParentSessionID: parent,
		MetaJSON:        marshalMeta(row.Meta),
		StartedAt:       row.StartedAt,
	})
}

func (s *agentRuntimeStore) MarkRuntimeFailed(id, reason string) error {
	return s.store.MarkAgentRuntimeFailed(id, reason)
}

func (s *agentRuntimeStore) SetProviderSessionID(id, providerSessionID string) error {
	return s.store.SetAgentRuntimeProviderSessionID(id, providerSessionID)
}

func (s *agentRuntimeStore) GetCheckpoint(id string) (*runtimeagent.RuntimeCheckpoint, error) {
	cp, err := s.store.GetAgentRuntimeCheckpoint(id)
	if err != nil {
		return nil, err
	}
	return &runtimeagent.RuntimeCheckpoint{
		ID:                cp.ID,
		ProviderSessionID: cp.ProviderSessionID,
		CapturedAt:        cp.CapturedAt,
	}, nil
}

func (s *agentRuntimeStore) ListRunningRows() ([]*runtimeagent.RuntimeRow, error) {
	rows, err := s.store.ListRunningAgentRuntimeRows()
	if err != nil {
		return nil, err
	}
	out := make([]*runtimeagent.RuntimeRow, 0, len(rows))
	for _, r := range rows {
		var parent *string
		if r.ParentSessionID != "" {
			p := r.ParentSessionID
			parent = &p
		}
		out = append(out, &runtimeagent.RuntimeRow{
			ID:              r.ID,
			AgentProfile:    r.AgentProfile,
			Provider:        r.Provider,
			Mode:            r.Mode,
			Workdir:         r.Workdir,
			State:           r.State,
			PID:             r.PID,
			ParentSessionID: parent,
			StartedAt:       r.StartedAt,
			Meta:            r.MetaMap(),
		})
	}
	return out, nil
}

func (s *agentRuntimeStore) MarkRuntimeOrphaned(id, reason string) error {
	return s.store.MarkAgentRuntimeOrphaned(id, reason)
}

// marshalMeta projects an arbitrary map into a JSON string suitable for
// agent_runtime.meta_json. nil / empty → "{}". Encoding errors collapse to
// "{}" so the runtime row is never refused on metadata-only failures.
func marshalMeta(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// --- StateSink adapter ---

// agentRuntimeStateSink translates agentsessions.Manager state events into
// agent_runtime row updates. The lib emits launching → running → done|failed;
// orphaned is set separately by SweepOrphans.
type agentRuntimeStateSink struct {
	store *store.Store
}

func (s *agentRuntimeStateSink) UpdateSessionState(id string, state agentsessions.State, pid int, exit *int) error {
	return s.store.SetAgentRuntimeState(id, string(state), pid)
}

// --- EventSink adapter ---

// agentRuntimeEventSink forwards lifecycle events to slog at debug level.
// Production observability (OTEL spans, counters) lands once the cross-app
// telemetry seam stabilizes; the sink shape is preserved so the upgrade is
// drop-in.
type agentRuntimeEventSink struct{}

func (s *agentRuntimeEventSink) Emit(ctx context.Context, ev agentsessions.LifecycleEvent) {
	exit := -1
	if ev.ExitCode != nil {
		exit = *ev.ExitCode
	}
	slog.Debug("agent_runtime: lifecycle",
		"session_id", ev.SessionID,
		"kind", string(ev.Kind),
		"from", string(ev.From),
		"to", string(ev.To),
		"exit", exit,
		"reason", ev.Reason,
	)
}

// --- Telemetry ---

// agentTelemetry forwards PTY restart events to slog. The shape matches
// runtimeagent.Telemetry so the production root can drop in an OTEL-backed
// implementation without re-wiring the deps.
type agentTelemetry struct{}

func (agentTelemetry) RecordPTYRestart(sessionID string, attempt int, prevExit *agentsessions.ExitError) {
	exit := -1
	reason := ""
	if prevExit != nil {
		exit = prevExit.Code
		reason = prevExit.Error()
	}
	slog.Warn("agent_runtime: pty restart",
		"session_id", sessionID,
		"attempt", attempt,
		"prev_exit", exit,
		"reason", reason,
	)
}

// --- Event bridge (Phase 4c.2 + Phase 5) ---

// agentEventBridge translates lib-native event surfaces into nanite SSE
// chat.StreamEvent payloads, broadcasting to the chat session's stream.
//
// Two distinct surfaces feed the bridge:
//
//  1. EventFanout — chan llmtypes.StreamEvent (the legacy stream taxonomy
//     used by Provider.StreamChat). The chat-harness assembles deltas /
//     errors / usage onto this channel; the bridge translates each event
//     to chat.StreamEvent and forwards via streams.BroadcastSessionStreamEvent.
//  2. TypedEventCallback — provider.EventsCallback consuming the typed
//     events.Event taxonomy emitted by CLI adapters. ToolUse / ToolResult
//     drive the per-tool SSE pipeline (closes G-PTY-NO-TOOL-EVENTS).
//
// Per-session routers (Phase 4c.4) let driveBootSession redirect a turn's
// runtime stream events into a dedicated chat-harness consumer chan instead
// of broadcasting them as SSE. When no router is bound for a session id, the
// fanout falls back to the SSE-broadcast path used by spawned agents and
// background tasks.
type agentEventBridge struct {
	streams *StreamManager
	seq     atomic.Uint64
	routers sync.Map // sessionID -> *sessionRouter
}

// sessionRouter wraps a per-turn turnCh with a close-once guard so the bridge
// fanout goroutine, the chat-harness ctx-cancel watcher, and explicit
// SetPerSessionRouter(nil) calls can all race to release the chan without
// double-close panics.
type sessionRouter struct {
	ch     chan llmtypes.StreamEvent
	closed atomic.Bool
}

func (r *sessionRouter) closeOnce() {
	if r.closed.CompareAndSwap(false, true) {
		close(r.ch)
	}
}

func (b *agentEventBridge) nextEventID() uint64 {
	return b.seq.Add(1)
}

// SetPerSessionRouter binds (or unbinds) a per-turn chan that the fanout
// goroutine forwards runtime events to. Passing nil unbinds + closes the
// previously bound chan. The bridge owns the close lifecycle so callers don't
// race against in-flight sends.
//
// Phase 4c.4: driveBootSession binds turnCh before SendInput; the bridge
// unbinds + closes when EventDone or EventError flows through, or when the
// chat-harness explicitly clears the router on ctx cancel.
func (b *agentEventBridge) SetPerSessionRouter(sessionID string, ch chan llmtypes.StreamEvent) {
	if ch == nil {
		if v, ok := b.routers.LoadAndDelete(sessionID); ok {
			v.(*sessionRouter).closeOnce()
		}
		return
	}
	router := &sessionRouter{ch: ch}
	if prev, loaded := b.routers.Swap(sessionID, router); loaded {
		// Phase 4c.7 (CW-20260508-0002): session takeover detected. The
		// prior turn's Done hadn't arrived yet (or its ctx-cancel watcher
		// hadn't run) when the chat-harness bound a fresh turnCh — most
		// likely a user-driven retry / new message before the prior
		// generateResponse drained.
		//
		// Conservative semantics: release the stale router (close-once
		// terminates the prior streamLoop), then let the new turn proceed.
		// The Boot'd CLI process is still running; its mid-turn output may
		// interleave with the new turn's response.
		//
		// Known limitation: claude-code's PTY surface doesn't expose a
		// mid-turn interrupt today, so we can't tell the agent to abort
		// the prior turn before delivering new input. Phase 5+ adds
		// Session.Interrupt(ctx) once go-agent-sessions surfaces a
		// non-blocking interrupt (follow-up ticket).
		slog.Warn("agent_event_bridge: session takeover — closing stale per-turn chan",
			"session_id", sessionID)
		prev.(*sessionRouter).closeOnce()
	}
}

// fanout returns the per-session StreamEvent channel. Closes naturally when
// the runtime drains its EventFanout at session-stop; the bridge goroutine
// exits at that point. Buffer is sized for typical burst rates from
// provider.StreamChat (deltas at ~10-30 Hz under load).
//
// When a router is bound for sessionID via SetPerSessionRouter, runtime
// events forward to the bound turnCh instead of broadcasting SSE. Done /
// Error close the turnCh and unbind the router so subsequent inter-turn
// events fall back to SSE broadcast.
func (b *agentEventBridge) fanout(sessionID string) chan<- llmtypes.StreamEvent {
	out := make(chan llmtypes.StreamEvent, 64)
	go func() {
		for ev := range out {
			if v, ok := b.routers.Load(sessionID); ok {
				router := v.(*sessionRouter)
				if !router.closed.Load() {
					select {
					case router.ch <- ev:
					default:
						// Drop on full to avoid stalling the runtime; the
						// chat-harness consumer is expected to keep up.
					}
				}
				if ev.Type == llmtypes.EventDone || ev.Type == llmtypes.EventError {
					b.routers.CompareAndDelete(sessionID, router)
					router.closeOnce()
				}
				continue
			}
			translated, ok := b.translateStreamEvent(ev)
			if !ok {
				continue
			}
			b.streams.BroadcastSessionStreamEvent(sessionID, translated)
		}
	}()
	return out
}

// translateStreamEvent maps a llmtypes.StreamEvent to a chat.StreamEvent
// suitable for SSE broadcast. Returns (zero, false) when the event has no
// useful FE projection (e.g. EventSessionID is informational only — the
// chat service handles session-id persistence elsewhere).
func (b *agentEventBridge) translateStreamEvent(ev llmtypes.StreamEvent) (chat.StreamEvent, bool) {
	switch ev.Type {
	case llmtypes.EventDelta:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "delta",
			Content: ev.Content,
		}, true
	case llmtypes.EventDone:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "stream_end",
		}, true
	case llmtypes.EventError:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "error",
			Error:   ev.Error,
		}, true
	case llmtypes.EventUsage:
		// Usage rows feed the cost ledger upstream; surface as a stream_end
		// piggyback when present, otherwise drop.
		return chat.StreamEvent{}, false
	case llmtypes.EventThinking:
		if ev.ThinkingBlock == nil {
			return chat.StreamEvent{}, false
		}
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "delta",
			Content: ev.ThinkingBlock.Thinking,
			Phase:   "thinking",
		}, true
	case llmtypes.EventToolUse:
		// Tool invocations also surface via TypedEventCallback (richer
		// per-tool SSE); skip here to avoid double-emission.
		return chat.StreamEvent{}, false
	case llmtypes.EventSessionID:
		// Provider session id propagates through Boot's OnSessionID path.
		return chat.StreamEvent{}, false
	default:
		return chat.StreamEvent{}, false
	}
}

// typedCallback returns the per-session callback Boot wires onto
// StartOptions.TypedEventCallback. Drives the per-tool SSE pipeline that
// closes G-PTY-NO-TOOL-EVENTS.
func (b *agentEventBridge) typedCallback(sessionID string) provider.EventsCallback {
	return func(e events.Event) {
		switch evt := e.(type) {
		case events.ToolUse:
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "tool_call",
				Tool:    evt.Name,
				ToolID:  evt.ID,
				Detail:  toolDetailFromArgs(evt.Args),
			})
		case events.ToolResult:
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "tool_result",
				ToolID:  evt.ID,
				Summary: evt.ContentPreview,
				Error: func() string {
					if evt.IsError {
						return evt.ContentPreview
					}
					return ""
				}(),
			})
		case events.Thinking:
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "delta",
				Content: evt.Text,
				Phase:   "thinking",
			})
		case events.Error:
			msg := evt.Message
			if msg == "" && evt.Err != nil {
				msg = evt.Err.Error()
			}
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "error",
				Error:   msg,
			})
		case events.SubagentSpawn:
			// Synthesized status event for FE awareness; no payload
			// schema yet, so we use the Detail field to surface the tool.
			b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
				EventID: b.nextEventID(),
				Type:    "status",
				Detail:  "subagent: " + evt.Tool,
			})
		case events.Heartbeat:
			// Optional: surface as a no-op presence ping. Drop for now;
			// the FE stalled-stream watchdog uses other signals.
		case events.SessionID:
			// Provider session id flows via Boot.OnSessionID; drop here.
		case events.Delta:
			// Lib-native delta is redundant with llmtypes.StreamEvent
			// EventDelta (which the EventFanout bridge already forwards).
			// Drop to avoid double-emission.
		case events.Usage, events.Done, events.SubprocessStderr:
			// Usage rows feed cost-ledger upstream; Done is a turn boundary;
			// SubprocessStderr is an observability surface, not a chat event.
		}
	}
}

// toolDetailFromArgs picks a short human-readable detail string from a tool
// args map. Today's heuristic: prefer "command" / "path" / "name" / "url"
// fields when present; fall back to the empty string.
func toolDetailFromArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"command", "path", "file_path", "name", "url", "query"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// _ context.Context retained so the EventSink's ctx parameter is part of
// the package's compile graph even when no in-tree caller threads it
// further (the lib's Manager.emit is the canonical caller).
var _ = context.TODO
