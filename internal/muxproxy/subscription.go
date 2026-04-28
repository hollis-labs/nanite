//go:build devmode

package muxproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"

	"github.com/chrispian/agent-mux/pkg/claudestream"
	agentmux "github.com/hollis-labs/go-agentmux-client"
)

// streamSource is the subset of *agentmux.Client the Manager consumes.
// Exists so tests can inject a fake.
//
// StreamEvents is retained for backwards compatibility with existing
// tests but is no longer used at runtime — claudestream CLI events
// ride the /sessions/{id}/attach stream (ADR 0017 in agent-mux), not
// the daemon event bus. Production dispatch uses AttachSession.
type streamSource interface {
	StreamEvents(ctx context.Context, opts agentmux.StreamEventsOptions) (<-chan agentmux.StreamEvent, <-chan error)
	AttachSession(ctx context.Context, sessionID string, w io.Writer, sinceSeq int64) error
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

// Manager fans out claudestream events per subordinate session to
// both a blocking waiter channel (read by mux_send) and the owning
// chat session's SSE stream (via StreamPublisher).
//
// Architecture: one per-session AttachSession goroutine, spawned in
// Register, torn down in Unregister. The daemon event bus (StreamEvents)
// is NOT used — claudestream CLI events ride attach streams.
type Manager struct {
	stream    streamSource
	publisher StreamPublisher // may be nil; Manager works without one
	ambient   context.Context // long-lived ctx for attach goroutines; set by Run
	mu        sync.RWMutex
	chanFor   map[string]chan claudestream.Event
	nickFor   map[string]string
	chatOwner map[string]string  // subordinate session ID → chat session ID
	cancelFor map[string]context.CancelFunc // per-session attach-goroutine cancel
}

// NewManager constructs a Manager backed by the singleton Client.
func NewManager() *Manager {
	return NewManagerWithStream(Client())
}

// NewManagerWithStream is the test seam.
func NewManagerWithStream(s streamSource) *Manager {
	return &Manager{
		stream:    s,
		ambient:   context.Background(),
		chanFor:   make(map[string]chan claudestream.Event),
		nickFor:   make(map[string]string),
		chatOwner: make(map[string]string),
		cancelFor: make(map[string]context.CancelFunc),
	}
}

// SetPublisher wires in a StreamPublisher for live SSE rendering.
// Safe to call after construction and before Run.
func (m *Manager) SetPublisher(p StreamPublisher) {
	m.mu.Lock()
	m.publisher = p
	m.mu.Unlock()
}

// Register allocates a per-session event channel, records a nickname,
// records the owning chat session ID, and spawns a per-session
// attach-stream goroutine that drains the subordinate's stdout into
// the channel.
//
// chatSessionID is the Nanite chat session that owns this subordinate;
// pass "" when the chat session ID is not available (e.g. in tests).
func (m *Manager) Register(chatSessionID, subordinateSessionID, nickname string) <-chan claudestream.Event {
	ch := make(chan claudestream.Event, 64)
	attachCtx, cancel := context.WithCancel(m.ambient)

	m.mu.Lock()
	m.chanFor[subordinateSessionID] = ch
	m.nickFor[subordinateSessionID] = nickname
	m.chatOwner[subordinateSessionID] = chatSessionID
	m.cancelFor[subordinateSessionID] = cancel
	m.mu.Unlock()

	// Spawn the per-session attach goroutine only when the stream
	// source actually supports attach. Tests that use the zero-value
	// fakeStream rely on direct writes via WaiterChannel.
	if m.stream != nil {
		go m.attachStream(attachCtx, subordinateSessionID)
	}

	slog.Info("muxproxy: subordinate registered",
		"chat_session", chatSessionID,
		"subordinate_session", subordinateSessionID,
		"nickname", nickname,
	)
	return ch
}

// Unregister removes a session's entries and cancels its attach
// goroutine. Safe to call for unknown IDs.
func (m *Manager) Unregister(sessionID string) {
	m.mu.Lock()
	nick := m.nickFor[sessionID]
	if ch, ok := m.chanFor[sessionID]; ok {
		close(ch)
	}
	if cancel, ok := m.cancelFor[sessionID]; ok {
		cancel()
	}
	delete(m.chanFor, sessionID)
	delete(m.nickFor, sessionID)
	delete(m.chatOwner, sessionID)
	delete(m.cancelFor, sessionID)
	m.mu.Unlock()
	slog.Info("muxproxy: subordinate unregistered", "session_id", sessionID, "nickname", nick)
}

// Nickname returns the label for a session, or "" if unregistered.
func (m *Manager) Nickname(sessionID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nickFor[sessionID]
}

// WaiterChannel returns the per-session channel for direct writes.
// Only intended for tests that bypass the real attach path.
func (m *Manager) WaiterChannel(sessionID string) chan claudestream.Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.chanFor[sessionID]
}

// Run records the long-lived application context used by future
// Register calls as the parent of their attach goroutines. It then
// blocks until ctx is cancelled, at which point all attach goroutines
// will exit on their own.
//
// Callers should call Run in a goroutine once at app start, passing
// the application-lifetime context. The choice to use an ambient ctx
// (rather than a ctx-per-attach) means subordinate streams survive
// individual chat-turn cancellations.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.ambient = ctx
	m.mu.Unlock()
	<-ctx.Done()
}

// attachStream is the per-session goroutine. It opens the attach HTTP
// stream, drains NDJSON lines, parses them via parseAll, and fans
// events out to both the waiter channel and the chat SSE publisher.
func (m *Manager) attachStream(ctx context.Context, sessionID string) {
	pr, pw := io.Pipe()

	// AttachSession runs until the server closes the stream or ctx
	// cancels. It writes raw bytes into pw; pr reads them line-by-line.
	// We run AttachSession in its own goroutine so the bufio.Scanner
	// loop below can reach io.EOF when the session ends and exit.
	go func() {
		defer func() { _ = pw.Close() }()
		err := m.stream.AttachSession(ctx, sessionID, pw, 0)
		if err != nil && ctx.Err() == nil {
			slog.Debug("muxproxy: attach ended", "session", sessionID, "err", err)
		}
	}()

	scanner := bufio.NewScanner(pr)
	// claude can emit ~100 KB assistant blocks; bump default 64 KB cap.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		for _, cev := range parseAll(line) {
			m.dispatch(sessionID, cev)
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		slog.Debug("muxproxy: attach scan error", "session", sessionID, "err", err)
	}
}

func (m *Manager) dispatch(sessionID string, cev claudestream.Event) {
	m.mu.RLock()
	ch, known := m.chanFor[sessionID]
	nick := m.nickFor[sessionID]
	chatID := m.chatOwner[sessionID]
	publisher := m.publisher
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

	if publisher == nil || chatID == "" {
		return
	}
	sev, emit := toSubEvent(nick, cev)
	if !emit {
		return
	}
	publisher.PublishSubEvent(chatID, sev)
}

func toSubEvent(nickname string, cev claudestream.Event) (SubEvent, bool) {
	switch cev.Kind {
	case claudestream.KindDelta:
		return SubEvent{
			Type:    "subordinate_delta",
			AgentID: nickname,
			Content: cev.Text,
		}, true
	case claudestream.KindToolUse:
		if cev.ToolUse == nil {
			return SubEvent{}, false
		}
		raw, _ := json.Marshal(cev.ToolUse.Input)
		return SubEvent{
			Type:    "subordinate_tool_use",
			AgentID: nickname,
			Tool:    cev.ToolUse.Name,
			Content: string(raw),
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
		if cancel, ok := m.cancelFor[subID]; ok {
			cancel()
		}
		delete(m.chanFor, subID)
		delete(m.nickFor, subID)
		delete(m.chatOwner, subID)
		delete(m.cancelFor, subID)
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
		if cancel, ok := m.cancelFor[subID]; ok {
			cancel()
		}
		ids = append(ids, subID)
	}
	m.chanFor = make(map[string]chan claudestream.Event)
	m.nickFor = make(map[string]string)
	m.chatOwner = make(map[string]string)
	m.cancelFor = make(map[string]context.CancelFunc)
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
