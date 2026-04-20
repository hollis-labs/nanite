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

// SubordinateStreamEvent is the chat-SSE shape for a subordinate's
// parsed claudestream event. The chat engine's renderer maps these
// onto its existing StreamEvent wire format.
type SubordinateStreamEvent struct {
	Type         string          // subordinate_delta | subordinate_tool_use | subordinate_done
	SessionID    string          // subordinate agent-mux session ID
	Nickname     string          // human-readable label registered at launch
	Text         string          // populated for subordinate_delta
	ToolName     string          // populated for subordinate_tool_use
	ToolInput    json.RawMessage // populated for subordinate_tool_use
	InputTokens  int             // populated for subordinate_done
	OutputTokens int             // populated for subordinate_done
}

// Manager fans out claudestream events from a single shared
// StreamEvents subscription to per-session blocking waiters AND to
// the chat session's SSE sink for live rendering.
type Manager struct {
	stream  streamSource
	mu      sync.RWMutex
	chanFor map[string]chan claudestream.Event
	nickFor map[string]string
}

// NewManager constructs a Manager backed by the singleton Client.
func NewManager() *Manager {
	return NewManagerWithStream(Client())
}

// NewManagerWithStream is the test seam.
func NewManagerWithStream(s streamSource) *Manager {
	return &Manager{
		stream:  s,
		chanFor: make(map[string]chan claudestream.Event),
		nickFor: make(map[string]string),
	}
}

// Register allocates a per-session event channel and records a
// nickname for rendering. Returns the channel the caller reads from
// (e.g. a blocking mux_send waiter).
func (m *Manager) Register(sessionID, nickname string) <-chan claudestream.Event {
	ch := make(chan claudestream.Event, 64)
	m.mu.Lock()
	m.chanFor[sessionID] = ch
	m.nickFor[sessionID] = nickname
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
// waiter channel (non-blocking drop on full) AND the chat SSE sink
// (blocking — chat SSE backpressure applies).
func (m *Manager) Run(ctx context.Context, sink chan<- SubordinateStreamEvent) {
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
			cev, parseOK := parseOne(ev.PayloadJSON)
			if !parseOK {
				continue
			}
			m.dispatch(ctx, ev.SessionID, cev, sink)
		}
	}
}

func (m *Manager) dispatch(ctx context.Context, sessionID string, cev claudestream.Event, sink chan<- SubordinateStreamEvent) {
	m.mu.RLock()
	ch, known := m.chanFor[sessionID]
	nick := m.nickFor[sessionID]
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

	sev, emit := toSinkEvent(sessionID, nick, cev)
	if !emit {
		return
	}
	select {
	case sink <- sev:
	case <-ctx.Done():
	}
}

func toSinkEvent(sessionID, nickname string, cev claudestream.Event) (SubordinateStreamEvent, bool) {
	switch cev.Kind {
	case claudestream.KindDelta:
		return SubordinateStreamEvent{
			Type:      "subordinate_delta",
			SessionID: sessionID,
			Nickname:  nickname,
			Text:      cev.Text,
		}, true
	case claudestream.KindToolUse:
		if cev.ToolUse == nil {
			return SubordinateStreamEvent{}, false
		}
		raw, _ := json.Marshal(cev.ToolUse.Input)
		return SubordinateStreamEvent{
			Type:      "subordinate_tool_use",
			SessionID: sessionID,
			Nickname:  nickname,
			ToolName:  cev.ToolUse.Name,
			ToolInput: raw,
		}, true
	case claudestream.KindDone:
		sev := SubordinateStreamEvent{
			Type:      "subordinate_done",
			SessionID: sessionID,
			Nickname:  nickname,
		}
		if cev.Usage != nil {
			sev.InputTokens = cev.Usage.InputTokens
			sev.OutputTokens = cev.Usage.OutputTokens
		}
		return sev, true
	default:
		return SubordinateStreamEvent{}, false
	}
}
