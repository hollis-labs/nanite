package muxproxy

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/chrispian/agent-mux/pkg/claudestream"
	agentmux "github.com/hollis-labs/go-agentmux-client"
)

// streamSource is the subset of *agentmux.Client the Manager consumes.
// Exists so tests can inject a fake.
type streamSource interface {
	StreamEvents(ctx context.Context, opts agentmux.StreamEventsOptions) (<-chan agentmux.StreamEvent, <-chan error)
}

// SubEvent is a subordinate stream event emitted from the Manager for
// live SSE rendering. It mirrors the fields of chat.StreamEvent that
// subordinate events populate, without importing the chat package
// (which would create a dependency cycle via chat→mcp→muxproxy).
// CW-20260420-0047.
type SubEvent struct {
	Type         string // subordinate_delta | subordinate_tool_use | subordinate_done
	Content      string // text delta (subordinate_delta) or JSON tool input (subordinate_tool_use)
	AgentID      string // nickname
	InputTokens  int    // subordinate_done only
	OutputTokens int    // subordinate_done only
	Tool         string // tool name (subordinate_tool_use only)
}

// StreamPublisher publishes subordinate events into an active chat
// session's SSE stream. The caller (main.go) provides an adapter that
// converts SubEvent → chat.StreamEvent and calls
// StreamManager.BroadcastSessionStreamEvent. CW-20260420-0047.
type StreamPublisher interface {
	PublishSubEvent(sessionID string, evt SubEvent)
}

// Manager fans out claudestream events from a single shared
// StreamEvents subscription to per-session blocking waiters AND to
// the chat session's SSE stream via StreamPublisher.
type Manager struct {
	stream    streamSource
	publisher StreamPublisher // may be nil; Manager works without one
	mu        sync.RWMutex
	chanFor   map[string]chan claudestream.Event
	nickFor   map[string]string
	chatOwner map[string]string // subordinate session ID → chat session ID
}

// NewManager constructs a Manager backed by the singleton Client.
func NewManager() *Manager {
	return NewManagerWithStream(Client())
}

// NewManagerWithStream is the test seam.
func NewManagerWithStream(s streamSource) *Manager {
	return &Manager{
		stream:    s,
		chanFor:   make(map[string]chan claudestream.Event),
		nickFor:   make(map[string]string),
		chatOwner: make(map[string]string),
	}
}

// SetPublisher wires in a StreamPublisher for live SSE rendering.
// Safe to call after construction and before Run.
func (m *Manager) SetPublisher(p StreamPublisher) {
	m.mu.Lock()
	m.publisher = p
	m.mu.Unlock()
}

// Register allocates a per-session event channel, records a nickname and
// the owning chat session ID. Returns the channel the caller reads from
// (e.g. a blocking mux_send waiter).
// chatSessionID is the Nanite chat session that owns this subordinate;
// pass "" when the chat session ID is not available (e.g. in tests).
func (m *Manager) Register(chatSessionID, subordinateSessionID, nickname string) <-chan claudestream.Event {
	ch := make(chan claudestream.Event, 64)
	m.mu.Lock()
	m.chanFor[subordinateSessionID] = ch
	m.nickFor[subordinateSessionID] = nickname
	m.chatOwner[subordinateSessionID] = chatSessionID
	m.mu.Unlock()
	return ch
}

// Unregister removes a session's entries. Safe to call for unknown IDs.
func (m *Manager) Unregister(sessionID string) {
	m.mu.Lock()
	if ch, ok := m.chanFor[sessionID]; ok {
		close(ch)
	}
	delete(m.chanFor, sessionID)
	delete(m.nickFor, sessionID)
	delete(m.chatOwner, sessionID)
	m.mu.Unlock()
}

// Nickname returns the label for a session, or "" if unregistered.
func (m *Manager) Nickname(sessionID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nickFor[sessionID]
}

// WaiterChannel returns the per-session channel for direct writes.
// Only intended for tests that bypass the real StreamEvents path.
func (m *Manager) WaiterChannel(sessionID string) chan claudestream.Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.chanFor[sessionID]
}

// Run drives the shared StreamEvents subscription until ctx is done.
// Every claudestream event is dispatched to BOTH the per-session
// waiter channel (non-blocking drop on full) AND the chat SSE stream
// via the configured StreamPublisher (if set).
func (m *Manager) Run(ctx context.Context) {
	events, errs := m.stream.StreamEvents(ctx, agentmux.StreamEventsOptions{
		Scopes: []string{"session"},
	})
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errs:
			if !ok {
				return
			}
			if err != nil {
				slog.Error("muxproxy: stream error", "err", err)
				return
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			for _, cev := range parseAll(ev.PayloadJSON) {
				m.dispatch(ev.SessionID, cev)
			}
		}
	}
}

func (m *Manager) dispatch(sessionID string, cev claudestream.Event) {
	m.mu.RLock()
	ch, known := m.chanFor[sessionID]
	nick := m.nickFor[sessionID]
	chatID := m.chatOwner[sessionID]
	pub := m.publisher
	m.mu.RUnlock()
	if !known {
		return
	}

	// Non-blocking send to waiter.
	select {
	case ch <- cev:
	default:
		slog.Debug("muxproxy: waiter channel full, dropping event",
			"session", sessionID, "kind", cev.Kind)
	}

	// Live-render path: publish to chat SSE if publisher and chatID are set.
	if pub == nil || chatID == "" {
		return
	}
	sev, emit := toSubEvent(nick, cev)
	if !emit {
		return
	}
	pub.PublishSubEvent(chatID, sev)
}

// toSubEvent converts a claudestream.Event into a SubEvent for live SSE
// rendering. Returns (event, true) when the event kind is renderable,
// (zero, false) otherwise.
func toSubEvent(nickname string, cev claudestream.Event) (SubEvent, bool) {
	switch cev.Kind {
	case claudestream.KindDelta:
		return SubEvent{
			Type:    "subordinate_delta",
			Content: cev.Text,
			AgentID: nickname,
		}, true
	case claudestream.KindToolUse:
		if cev.ToolUse == nil {
			return SubEvent{}, false
		}
		raw, _ := json.Marshal(cev.ToolUse.Input)
		return SubEvent{
			Type:    "subordinate_tool_use",
			Tool:    cev.ToolUse.Name,
			Content: string(raw),
			AgentID: nickname,
		}, true
	case claudestream.KindDone:
		sev := SubEvent{
			Type:    "subordinate_done",
			AgentID: nickname,
		}
		if cev.Usage != nil {
			sev.InputTokens = cev.Usage.InputTokens
			sev.OutputTokens = cev.Usage.OutputTokens
		}
		return sev, true
	default:
		return SubEvent{}, false
	}
}

// StopAllForChat unregisters every subordinate session owned by the
// given chat session and returns their IDs. The caller is responsible
// for invoking client.StopSession on each to actually terminate the
// daemon-side process.
func (m *Manager) StopAllForChat(chatSessionID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var ids []string
	for subID, owner := range m.chatOwner {
		if owner == chatSessionID {
			ids = append(ids, subID)
		}
	}
	for _, subID := range ids {
		if ch, ok := m.chanFor[subID]; ok {
			close(ch)
		}
		delete(m.chanFor, subID)
		delete(m.nickFor, subID)
		delete(m.chatOwner, subID)
	}
	return ids
}

// StopAll unregisters every live subordinate session and returns their
// IDs. Used at process-exit cleanup.
func (m *Manager) StopAll() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.chanFor))
	for subID, ch := range m.chanFor {
		close(ch)
		ids = append(ids, subID)
	}
	m.chanFor = make(map[string]chan claudestream.Event)
	m.nickFor = make(map[string]string)
	m.chatOwner = make(map[string]string)
	return ids
}

// LaunchSummary mirrors service.LaunchSummary to avoid a package
// cycle: transport.go lives in muxproxy, service in service,
// transport dispatches *into* service.
type LaunchSummary struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Agent    string `json:"agent"`
	Provider string `json:"provider"`
}

// LaunchResult mirrors service.LaunchResult (see LaunchSummary note).
type LaunchResult struct {
	SessionID  string `json:"session_id"`
	ProviderID string `json:"provider_id"`
	Nickname   string `json:"nickname"`
}

// SendToolUse mirrors service.SendToolUse.
type SendToolUse struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// SendResult mirrors service.SendResult.
type SendResult struct {
	Transcript   string        `json:"transcript"`
	ToolUses     []SendToolUse `json:"tool_uses"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
	ExitStatus   string        `json:"exit_status"`
	Error        string        `json:"error,omitempty"`
}
