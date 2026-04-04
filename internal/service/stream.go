package service

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/chat"
)

// StreamManager owns the concurrent state for message streams, SSE
// connections, and presence. Extracted from Engine's 6 sync.Map fields.
type StreamManager struct {
	streams        sync.Map // messageID -> chan chat.StreamEvent
	msgToSession   sync.Map // messageID -> sessionID
	sessionSSE     sync.Map // sessionID -> *sseConn
	presenceClient sync.Map // clientID -> chan chat.PresenceEvent
	activePresence sync.Map // sessionID -> chat.PresenceEvent
	cliThrottle    sync.Map // sessionID -> time.Time

	// CLIActiveThrottleInterval controls the minimum gap between cli_active
	// presence events for the same session. Zero means use the default (5s).
	CLIActiveThrottleInterval time.Duration
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
	return ch
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
	sm.streams.Delete(messageID)
	sm.msgToSession.Delete(messageID)
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
		log.Printf("stream: SSE session takeover for session %s", sessionID)
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
	log.Printf("presence: client %s registered", clientID)
	return clientID, ch
}

// UnregisterPresenceClient removes a presence listener and closes its channel.
func (sm *StreamManager) UnregisterPresenceClient(clientID string) {
	if val, ok := sm.presenceClient.LoadAndDelete(clientID); ok {
		close(val.(chan chat.PresenceEvent))
		log.Printf("presence: client %s unregistered", clientID)
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
			log.Printf("presence: dropped event for slow client %s", key.(string))
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
