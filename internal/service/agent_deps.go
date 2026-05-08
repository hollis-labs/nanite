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
	"sync/atomic"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/permission"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
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
}

// BuildAgentDependencies wires a *runtimeagent.Dependencies plus the singleton
// agentsessions.Manager. Returns the composed Dependencies struct, the
// Manager instance (for daemon-bootstrap orphan sweep + Shutdown drain), and
// the unbound EventBridge / TypedCallback factories.
//
// The composition root is the single point that:
//
//   - constructs *agentsessions.Manager with its sinks
//   - wires the Store-backed RuntimeStore
//   - wires the AgentProfileResolver
//   - resolves the per-provider CLIAdapter
//   - threads the EventFanout / TypedEventCallback factories the chat service
//     binds per-session.
func BuildAgentDependencies(cfg AgentDepsConfig) (*runtimeagent.Dependencies, *agentsessions.Manager, error) {
	if cfg.Store == nil {
		return nil, nil, errors.New("BuildAgentDependencies: Store is required")
	}
	if cfg.Streams == nil {
		return nil, nil, errors.New("BuildAgentDependencies: Streams is required")
	}

	binPath := cfg.BinaryPath
	if binPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, nil, fmt.Errorf("BuildAgentDependencies: resolve binary: %w", err)
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

	return deps, manager, nil
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
//  1. EventFanout — chan provider.StreamEvent (the legacy stream taxonomy
//     used by Provider.StreamChat). The chat-harness assembles deltas /
//     errors / usage onto this channel; the bridge translates each event
//     to chat.StreamEvent and forwards via streams.BroadcastSessionStreamEvent.
//  2. TypedEventCallback — provider.EventsCallback consuming the typed
//     events.Event taxonomy emitted by CLI adapters. ToolUse / ToolResult
//     drive the per-tool SSE pipeline (closes G-PTY-NO-TOOL-EVENTS).
//
// The bridge is stateless across sessions; per-session state lives in the
// closures returned by fanout/typedCallback.
type agentEventBridge struct {
	streams *StreamManager
	seq     atomic.Uint64
}

func (b *agentEventBridge) nextEventID() uint64 {
	return b.seq.Add(1)
}

// fanout returns the per-session StreamEvent channel. Closes naturally when
// the runtime drains its EventFanout at session-stop; the bridge goroutine
// exits at that point. Buffer is sized for typical burst rates from
// provider.StreamChat (deltas at ~10-30 Hz under load).
func (b *agentEventBridge) fanout(sessionID string) chan<- provider.StreamEvent {
	out := make(chan provider.StreamEvent, 64)
	go func() {
		for ev := range out {
			translated, ok := b.translateStreamEvent(ev)
			if !ok {
				continue
			}
			b.streams.BroadcastSessionStreamEvent(sessionID, translated)
		}
	}()
	return out
}

// translateStreamEvent maps a provider.StreamEvent to a chat.StreamEvent
// suitable for SSE broadcast. Returns (zero, false) when the event has no
// useful FE projection (e.g. EventSessionID is informational only — the
// chat service handles session-id persistence elsewhere).
func (b *agentEventBridge) translateStreamEvent(ev provider.StreamEvent) (chat.StreamEvent, bool) {
	switch ev.Type {
	case provider.EventDelta:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "delta",
			Content: ev.Content,
		}, true
	case provider.EventDone:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "stream_end",
		}, true
	case provider.EventError:
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "error",
			Error:   ev.Error,
		}, true
	case provider.EventUsage:
		// Usage rows feed the cost ledger upstream; surface as a stream_end
		// piggyback when present, otherwise drop.
		return chat.StreamEvent{}, false
	case provider.EventThinking:
		if ev.ThinkingBlock == nil {
			return chat.StreamEvent{}, false
		}
		return chat.StreamEvent{
			EventID: b.nextEventID(),
			Type:    "delta",
			Content: ev.ThinkingBlock.Thinking,
			Phase:   "thinking",
		}, true
	case provider.EventToolUse:
		// Tool invocations also surface via TypedEventCallback (richer
		// per-tool SSE); skip here to avoid double-emission.
		return chat.StreamEvent{}, false
	case provider.EventSessionID:
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
			// Lib-native delta is redundant with provider.StreamEvent
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
