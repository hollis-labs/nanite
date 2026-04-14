package service

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
	sdkplugin "github.com/hollis-labs/plugin-sdk"
)

// StreamManager owns the concurrent state for message streams, SSE
// connections, and presence. Extracted from Engine's 6 sync.Map fields.
type StreamManager struct {
	streams        sync.Map // messageID -> chan chat.StreamEvent
	msgToSession   sync.Map // messageID -> sessionID
	sessionToMsgs  sync.Map // sessionID -> *sessionStreams (reverse index for session-scoped delivery)
	sessionSSE     sync.Map // sessionID -> *sseConn
	presenceClient sync.Map // clientID -> chan chat.PresenceEvent
	activePresence sync.Map // sessionID -> chat.PresenceEvent
	cliThrottle    sync.Map // sessionID -> time.Time

	// CLIActiveThrottleInterval controls the minimum gap between cli_active
	// presence events for the same session. Zero means use the default (5s).
	CLIActiveThrottleInterval time.Duration

	// pluginEnvelopeDrops counts plugin envelopes dropped because no active
	// chat SSE stream was attached for the target session at delivery time.
	// BLG-20260413-012 — surfaced via PluginEnvelopeDropCount for diagnostics.
	pluginEnvelopeDrops atomic.Uint64
}

// sessionStreams tracks the set of active message streams for a session so
// DeliverSessionEnvelopes can fan out without scanning msgToSession.
type sessionStreams struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

// sseConn tracks an active SSE connection for session-level deduplication.
type sseConn struct {
	done chan struct{}
}

// NewStreamManager creates a new StreamManager.
func NewStreamManager() *StreamManager {
	return &StreamManager{}
}

// --- Message streams ---

// CreateStream allocates a buffered stream channel for a message, maps the
// message to its session, and returns the channel. The caller (generateResponse)
// writes events to the channel; the SSE handler reads from it.
func (sm *StreamManager) CreateStream(messageID, sessionID string) chan chat.StreamEvent {
	ch := make(chan chat.StreamEvent, 128)
	sm.streams.Store(messageID, ch)
	sm.msgToSession.Store(messageID, sessionID)
	sm.addSessionMessage(sessionID, messageID)
	return ch
}

// addSessionMessage records a (sessionID, messageID) pair in the reverse
// index. Safe for concurrent callers.
func (sm *StreamManager) addSessionMessage(sessionID, messageID string) {
	val, _ := sm.sessionToMsgs.LoadOrStore(sessionID, &sessionStreams{ids: make(map[string]struct{})})
	ss := val.(*sessionStreams)
	ss.mu.Lock()
	ss.ids[messageID] = struct{}{}
	ss.mu.Unlock()
}

// removeSessionMessage drops messageID from sessionID's reverse index. When
// the index becomes empty the session entry is deleted to bound memory.
func (sm *StreamManager) removeSessionMessage(sessionID, messageID string) {
	val, ok := sm.sessionToMsgs.Load(sessionID)
	if !ok {
		return
	}
	ss := val.(*sessionStreams)
	ss.mu.Lock()
	delete(ss.ids, messageID)
	empty := len(ss.ids) == 0
	ss.mu.Unlock()
	if empty {
		sm.sessionToMsgs.CompareAndDelete(sessionID, val)
	}
}

// GetStream returns the event channel for a given message ID.
func (sm *StreamManager) GetStream(messageID string) (<-chan chat.StreamEvent, bool) {
	val, ok := sm.streams.Load(messageID)
	if !ok {
		return nil, false
	}
	return val.(chan chat.StreamEvent), true
}

// CloseStream removes the stream and message-to-session mapping. It does NOT
// close the channel — the producer (generateResponse) is responsible for that.
func (sm *StreamManager) CloseStream(messageID string) {
	if val, ok := sm.msgToSession.LoadAndDelete(messageID); ok {
		sm.removeSessionMessage(val.(string), messageID)
	}
	sm.streams.Delete(messageID)
}

// GetSessionForMessage returns the session ID associated with a message stream.
func (sm *StreamManager) GetSessionForMessage(messageID string) (string, bool) {
	val, ok := sm.msgToSession.Load(messageID)
	if !ok {
		return "", false
	}
	return val.(string), true
}

// --- SSE connection deduplication ---

// RegisterSSE registers a new SSE connection for a session. If another
// connection already exists, its done channel is closed (signaling
// session_takeover) before being replaced. Returns the new connection's
// done channel.
func (sm *StreamManager) RegisterSSE(sessionID string) <-chan struct{} {
	conn := &sseConn{done: make(chan struct{})}
	if prev, loaded := sm.sessionSSE.Swap(sessionID, conn); loaded {
		old := prev.(*sseConn)
		close(old.done)
		slog.Info("stream: SSE session takeover", "session_id", sessionID)
	}
	return conn.done
}

// UnregisterSSE removes the SSE connection for a session, but only if this
// is still the active connection (not already taken over).
func (sm *StreamManager) UnregisterSSE(sessionID string, done <-chan struct{}) {
	val, ok := sm.sessionSSE.Load(sessionID)
	if !ok {
		return
	}
	current := val.(*sseConn)
	// Only delete if the caller is still the active connection. If a takeover
	// happened, current.done differs from the caller's done channel — leave
	// the replacement's slot untouched.
	if current.done != done {
		return
	}
	sm.sessionSSE.CompareAndDelete(sessionID, val)
}

// --- Presence ---

// RegisterPresenceClient creates a new presence listener and returns a client
// ID and a read-only channel for receiving presence events.
func (sm *StreamManager) RegisterPresenceClient() (string, <-chan chat.PresenceEvent) {
	clientID := uuid.New().String()
	ch := make(chan chat.PresenceEvent, 32)
	sm.presenceClient.Store(clientID, ch)
	slog.Debug("presence: client registered", "client_id", clientID)
	return clientID, ch
}

// UnregisterPresenceClient removes a presence listener and closes its channel.
func (sm *StreamManager) UnregisterPresenceClient(clientID string) {
	if val, ok := sm.presenceClient.LoadAndDelete(clientID); ok {
		close(val.(chan chat.PresenceEvent))
		slog.Debug("presence: client unregistered", "client_id", clientID)
	}
}

// BroadcastPresence sends a presence event to all connected presence clients.
// Slow clients have the event dropped rather than blocking the broadcast.
func (sm *StreamManager) BroadcastPresence(event chat.PresenceEvent) {
	sm.presenceClient.Range(func(key, val any) bool {
		ch := val.(chan chat.PresenceEvent)
		select {
		case ch <- event:
		default:
			slog.Debug("presence: dropped event for slow client", "client_id", key.(string))
		}
		return true
	})
}

// SetActivePresence records that a session is currently streaming.
func (sm *StreamManager) SetActivePresence(sessionID string, event chat.PresenceEvent) {
	sm.activePresence.Store(sessionID, event)
}

// ClearActivePresence removes the active-streaming record for a session.
func (sm *StreamManager) ClearActivePresence(sessionID string) {
	sm.activePresence.Delete(sessionID)
}

// ActivePresenceState returns a snapshot of all currently-streaming sessions.
func (sm *StreamManager) ActivePresenceState() []chat.PresenceEvent {
	var events []chat.PresenceEvent
	sm.activePresence.Range(func(_, val any) bool {
		events = append(events, val.(chat.PresenceEvent))
		return true
	})
	return events
}

// BroadcastSessionArchived clears active presence for a session and sends a
// session_archived event to all presence clients.
func (sm *StreamManager) BroadcastSessionArchived(sessionID string) {
	sm.activePresence.Delete(sessionID)
	sm.BroadcastPresence(chat.PresenceEvent{
		Type:      "session_archived",
		SessionID: sessionID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// ThrottledCLIPresence emits a cli_active presence event at most once per the
// configured throttle interval per session.
func (sm *StreamManager) ThrottledCLIPresence(sessionID string) {
	throttle := sm.CLIActiveThrottleInterval
	if throttle <= 0 {
		throttle = 5 * time.Second
	}

	now := time.Now()
	if last, ok := sm.cliThrottle.Load(sessionID); ok {
		if now.Sub(last.(time.Time)) < throttle {
			return
		}
	}
	sm.cliThrottle.Store(sessionID, now)

	sm.BroadcastPresence(chat.PresenceEvent{
		Type:      "cli_active",
		SessionID: sessionID,
		Timestamp: now.UTC().Format(time.RFC3339),
	})
}

// --- Plugin envelope delivery (BLG-20260413-012) ---

// Deliver fans validated plugin-emitted envelopes into every active chat
// message stream for sessionID as StreamEvents of type "plugin_envelope".
// Returns true when at least one envelope reached at least one active stream;
// false when no active stream was attached for sessionID, in which case the
// envelope batch is counted as a drop (PluginEnvelopeDropCount) and logged.
//
// Delivery is non-blocking: if a stream's buffered channel is full the
// envelope for that stream is dropped (same back-pressure contract the
// engine's own writes assume). This matches BLG-012's "log and drop, don't
// block the event dispatch" requirement — plugin event hooks run inside the
// host event dispatcher goroutine and must never stall the pipeline.
//
// pluginID is optional; when provided it is stamped on each StreamEvent so
// the frontend can route rendering to the emitting plugin.
func (sm *StreamManager) Deliver(sessionID string, envs []sdkplugin.EnvelopeOut) bool {
	return sm.DeliverSessionEnvelopes(sessionID, "", envs)
}

// DeliverSessionEnvelopes is the pluginID-aware form of Deliver. It is the
// primary entry point used by the subprocess event-hook proxy; Deliver is the
// zero-pluginID convenience matching the subprocess.EnvelopeConsumer interface
// for call sites that don't thread pluginID through (e.g. test fakes).
func (sm *StreamManager) DeliverSessionEnvelopes(sessionID, pluginID string, envs []sdkplugin.EnvelopeOut) bool {
	if sessionID == "" || len(envs) == 0 {
		return false
	}

	val, ok := sm.sessionToMsgs.Load(sessionID)
	if !ok {
		sm.pluginEnvelopeDrops.Add(uint64(len(envs)))
		slog.Warn("stream: dropped plugin envelopes — no active chat stream for session",
			"session_id", sessionID, "plugin_id", pluginID, "count", len(envs))
		return false
	}

	ss := val.(*sessionStreams)
	ss.mu.Lock()
	targets := make([]string, 0, len(ss.ids))
	for id := range ss.ids {
		targets = append(targets, id)
	}
	ss.mu.Unlock()

	if len(targets) == 0 {
		sm.pluginEnvelopeDrops.Add(uint64(len(envs)))
		slog.Warn("stream: dropped plugin envelopes — no active chat stream for session",
			"session_id", sessionID, "plugin_id", pluginID, "count", len(envs))
		return false
	}

	anyDelivered := false
	for _, env := range envs {
		payload, err := json.Marshal(env)
		if err != nil {
			// A non-marshalable envelope is a plugin bug; the B.11 filter
			// should have dropped it already. Count as a drop and continue.
			sm.pluginEnvelopeDrops.Add(1)
			slog.Warn("stream: dropped plugin envelope — marshal failed",
				"session_id", sessionID, "plugin_id", pluginID, "error", err)
			continue
		}
		evt := chat.StreamEvent{
			Type:     "plugin_envelope",
			Envelope: string(payload),
			PluginID: pluginID,
		}
		for _, msgID := range targets {
			chVal, ok := sm.streams.Load(msgID)
			if !ok {
				continue
			}
			ch := chVal.(chan chat.StreamEvent)
			select {
			case ch <- evt:
				anyDelivered = true
			default:
				// Slow consumer — drop for this stream but keep going for the
				// others. This mirrors presence broadcast semantics.
				sm.pluginEnvelopeDrops.Add(1)
				slog.Warn("stream: dropped plugin envelope — chat stream full",
					"session_id", sessionID, "plugin_id", pluginID, "message_id", msgID)
			}
		}
	}
	return anyDelivered
}

// PluginEnvelopeDropCount returns the total number of plugin envelopes
// dropped since process start (no active chat stream, marshal failure, or
// buffered-channel full). Monotonic counter surfaced for diagnostics.
func (sm *StreamManager) PluginEnvelopeDropCount() uint64 {
	return sm.pluginEnvelopeDrops.Load()
}
