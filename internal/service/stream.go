package service

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	sdkplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// StreamManager owns the concurrent state for message streams, SSE
// connections, and presence. Extracted from Engine's 6 sync.Map fields.
type StreamManager struct {
	turnsOnce      sync.Once
	turns          *CognitiveTurns
	turnStore      *store.Store
	sseMu          sync.Mutex // serialize connection takeover with subscriber replacement
	streams        sync.Map   // messageID -> *messageStream (CW-20260418-0100)
	msgToSession   sync.Map   // messageID -> sessionID
	sessionToMsgs  sync.Map   // sessionID -> *sessionStreams (reverse index for session-scoped delivery)
	messageSSE     sync.Map   // messageID -> *sseConn
	presenceClient sync.Map   // clientID -> chan chat.PresenceEvent
	activePresence sync.Map   // sessionID -> chat.PresenceEvent
	cliThrottle    sync.Map   // sessionID -> time.Time

	// CLIActiveThrottleInterval controls the minimum gap between cli_active
	// presence events for the same session. Zero means use the default (5s).
	CLIActiveThrottleInterval time.Duration

	// pluginEnvelopeDrops counts plugin envelopes dropped because no active
	// chat SSE stream was attached for the target session at delivery time.
	// BLG-20260413-012 — surfaced via PluginEnvelopeDropCount for diagnostics.
	pluginEnvelopeDrops atomic.Uint64
}

// ringBufferCapacity bounds the per-message event history used for replay on
// SSE reconnect. 256 covers a typical tool-heavy turn (many short deltas +
// tool_call/tool_result pairs) without unbounded memory growth. Older events
// are evicted first — a client that disconnects, sleeps past 256 events of
// activity receives an explicit gap before the retained replay suffix on
// the HTTP transport. The missing range requires durable message recovery.
// CW-20260418-0100.
const ringBufferCapacity = 256

// messageStream represents the per-message fan-out pipeline: a producer
// channel written to by generateResponse, a ring buffer of recent events
// with monotonically-assigned EventIDs, and the current SSE subscriber.
//
// The pump goroutine reads from produce, assigns the next EventID, appends
// to the buffer (evicting oldest when full), and forwards to the current
// subscriber channel (non-blocking — if the subscriber is slow or absent
// the event still lives in the buffer for replay).
//
// CW-20260418-0100.
type messageStream struct {
	cognitive *cognitiveRun
	messageID string
	sessionID string

	// produce is returned by CreateStream and written by generateResponse.
	// Exactly one producer; closed by the producer's defer when the stream
	// ends. The pump goroutine reads until produce closes.
	produce chan chat.StreamEvent

	mu     sync.Mutex
	buf    []chat.StreamEvent // ring; at most ringBufferCapacity entries. buf[len-1] is the newest.
	nextID uint64             // next EventID to assign (starts at 1)

	// subscriber is the current live SSE receiver. nil when no client is
	// connected. Swapped atomically under mu. When the pump forwards an
	// event to a nil subscriber, the event stays only in the buffer — the
	// client will replay it when (or if) they connect via Subscribe.
	subscriber chan chat.StreamEvent
	closure    chan chat.StreamTermination

	// closed is set by the pump when produce closes and it has drained all
	// remaining events. Subscribers connecting after closed=true receive a
	// full replay followed by a closed channel.
	closed bool
}

// newMessageStream starts a pump goroutine for a fresh message. The caller
// receives the producer channel via CreateStream.
func newMessageStream(messageID, sessionID string, cognitive ...*cognitiveRun) *messageStream {
	ms := &messageStream{
		messageID: messageID,
		sessionID: sessionID,
		produce:   make(chan chat.StreamEvent, 128),
		nextID:    1,
	}
	if len(cognitive) > 0 {
		ms.cognitive = cognitive[0]
	}
	go ms.pump()
	return ms
}

// pump is the per-message dispatcher goroutine. It owns all mutations to
// buf/nextID/subscriber; Subscribe and the producer just push through it.
//
// The non-blocking send to the current subscriber happens while ms.mu is
// held. This serializes the write against subscribe()'s close(prev) (and
// the producer-end close below), eliminating the data race between pump
// fanout and subscriber replacement (CW-20260510-0002). The send is a
// `select default`, so holding the lock cannot block — at worst we take
// the overflow path and drop the slow subscriber under the same lock.
func (ms *messageStream) pump() {
	for evt := range ms.produce {
		// Explicit fallback markers belong to the retained wire. The native
		// service finalizes from its committed message and Ending intent instead.
		if ms.cognitive != nil && !evt.RetainedTerminal {
			ms.cognitive.consume(evt)
		}
		if evt.Type == "stream_end" && evt.Termination == nil {
			evt.Termination = &chat.StreamTermination{Reason: "completed", Outcome: "success"}
		}
		ms.mu.Lock()
		evt.EventID = ms.nextID
		ms.nextID++
		if len(ms.buf) >= ringBufferCapacity {
			ms.buf = ms.buf[1:]
		}
		ms.buf = append(ms.buf, evt)
		sub := ms.subscriber

		// Forward to subscriber non-blocking. If the subscriber's channel
		// is full (slow consumer), close it so the SSE handler sees EOF
		// and the client reconnects with its EventID cursor — the ring
		// buffer still holds the event and replay will cover the gap.
		// Silently dropping would lose events to an actively-connected
		// client with no recovery path (PR #66 review #5).
		if sub != nil {
			select {
			case sub <- evt:
			default:
				if ms.subscriber == sub {
					ms.closeSubscriberLocked(chat.StreamTermination{Reason: "slow_consumer", Retryable: true})
				}
			}
		}
		ms.mu.Unlock()
	}
	if ms.cognitive != nil {
		ms.cognitive.end()
	}

	// Producer closed. Mark closed and close the current subscriber so the
	// SSE handler sees EOF. Closing under the lock matches the in-loop
	// discipline so a concurrent subscribe() takeover cannot race the
	// pump-end close.
	ms.mu.Lock()
	ms.closed = true
	ms.closeSubscriberLocked(chat.StreamTermination{Reason: "producer_closed"})
	ms.mu.Unlock()
}

// closeSubscriberLocked publishes the reason before EOF. Both channel closure
// and pump delivery are serialized by mu, including cancellation and takeover.
func (ms *messageStream) closeSubscriberLocked(end chat.StreamTermination) {
	if ms.subscriber == nil {
		return
	}
	if ms.closure != nil {
		ms.closure <- end
		close(ms.closure)
	}
	close(ms.subscriber)
	ms.subscriber, ms.closure = nil, nil
}

// subscribe replays buffered events with EventID > fromEventID then registers
// the caller as the live subscriber. If an older subscriber is already
// registered, it is replaced (and closed) — the SSE dedup at the handler
// layer already enforces a single subscriber per session, so this is the
// dedup's enforcement inside the pump.
//
// Returns a read-only channel of live events (replay events are pre-drained
// into the returned channel before return), plus a boolean indicating
// whether the stream has already closed. When closed=true the caller
// receives the replay events and the channel is closed.
func (ms *messageStream) subscribe(fromEventID uint64) (<-chan chat.StreamEvent, bool) {
	ch, _, closed := ms.subscribeTransport(fromEventID, false)
	return ch, closed
}

func (ms *messageStream) subscribeTransport(fromEventID uint64, reportGap bool) (<-chan chat.StreamEvent, <-chan chat.StreamTermination, bool) {
	ms.mu.Lock()
	// Size out to fit every possible replay event plus a normal live-event
	// headroom. The buffer can hold up to ringBufferCapacity entries;
	// sizing out to max(128, len(buf)) means the pre-fill below never
	// drops (PR #66 review #4). A fixed-128 channel was the original
	// implementation and caused a silent loss of replay data whenever
	// the client's cursor was more than 128 events behind.
	outCap := 128
	if len(ms.buf) > outCap {
		outCap = len(ms.buf)
	}
	out := make(chan chat.StreamEvent, outCap+1)
	closure := make(chan chat.StreamTermination, 1)
	if reportGap {
		latest := ms.nextID - 1
		if fromEventID > latest {
			out <- chat.StreamEvent{Type: "gap", Gap: &chat.StreamGap{From: latest + 1, To: fromEventID, Reason: "cursor_ahead"}}
		} else if len(ms.buf) > 0 && fromEventID < ms.buf[0].EventID-1 {
			out <- chat.StreamEvent{Type: "gap", Gap: &chat.StreamGap{From: fromEventID + 1, To: ms.buf[0].EventID - 1, Reason: "retention"}}
		}
	}

	// Pre-fill out with buffered events the caller hasn't seen yet. This
	// is now a plain send (not a select-with-default) because outCap is
	// guaranteed ≥ len(ms.buf).
	for _, evt := range ms.buf {
		if evt.EventID > fromEventID {
			out <- evt
		}
	}

	// Replace any existing subscriber. Close the old one so the previous
	// client sees EOF rather than a silently-abandoned channel.
	//
	// close(prev) happens while ms.mu is held so it cannot race the pump's
	// non-blocking send (which also runs under ms.mu — see pump()). Without
	// this, a write to prev in the pump and close(prev) here could interleave
	// on the same channel, which the race detector flags as a data race
	// (CW-20260510-0002).
	if ms.closed {
		closure <- chat.StreamTermination{Reason: "producer_closed"}
		close(closure)
		ms.mu.Unlock()
		close(out)
		return out, closure, true
	}
	ms.closeSubscriberLocked(chat.StreamTermination{Reason: "session_takeover"})
	ms.subscriber = out
	ms.closure = closure
	ms.mu.Unlock()
	return out, closure, false
}

// MessageStreamSubscription carries observer-local termination independently
// from producer events. Cancel detaches exactly this observer, never execution.
type MessageStreamSubscription struct {
	Events   <-chan chat.StreamEvent
	Closure  <-chan chat.StreamTermination
	Takeover <-chan struct{}
	Cancel   func()
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
func NewStreamManager(backing ...*store.Store) *StreamManager {
	sm := &StreamManager{}
	if len(backing) > 0 {
		sm.turnStore = backing[0]
	}
	return sm
}

// CognitiveTurns is the native per-run command/status/subscription source.
// Retained product streams keep their existing delivery path.
func (sm *StreamManager) CognitiveTurns() *CognitiveTurns {
	sm.turnsOnce.Do(func() { sm.turns = NewCognitiveTurns(sm.turnStore) })
	return sm.turns
}

func (sm *StreamManager) createCognitiveStream(messageID, sessionID string, run *cognitiveRun) chan chat.StreamEvent {
	ms := newMessageStream(messageID, sessionID, run)
	sm.streams.Store(messageID, ms)
	sm.msgToSession.Store(messageID, sessionID)
	sm.addSessionMessage(sessionID, messageID)
	return ms.produce
}

// --- Message streams ---

// CreateStream allocates a message stream (producer channel + ring buffer
// + pump goroutine) and registers it. The caller (generateResponse) writes
// events to the returned channel; the pump assigns EventIDs, preserves the
// last ringBufferCapacity events for replay, and forwards to the current
// SSE subscriber registered via Subscribe.
//
// The returned channel must be closed by the producer when the stream
// ends; the pump exits on that close.
func (sm *StreamManager) CreateStream(messageID, sessionID string) chan chat.StreamEvent {
	ms := newMessageStream(messageID, sessionID)
	sm.streams.Store(messageID, ms)
	sm.msgToSession.Store(messageID, sessionID)
	sm.addSessionMessage(sessionID, messageID)
	return ms.produce
}

// addSessionMessage records a (sessionID, messageID) pair in the reverse
// index. Safe for concurrent callers.
//
// Retries via LoadOrStore to close the add/remove race Copilot flagged on
// PR #36: if a concurrent removeSessionMessage CompareAndDeletes the entry
// we obtained from LoadOrStore before we finish Lock+add, the re-check
// under ss.mu detects that our pointer is no longer the canonical one and
// loops. Pairs with removeSessionMessage, which holds ss.mu across its own
// CompareAndDelete so the re-check here is well-ordered.
func (sm *StreamManager) addSessionMessage(sessionID, messageID string) {
	for {
		val, _ := sm.sessionToMsgs.LoadOrStore(sessionID, &sessionStreams{ids: make(map[string]struct{})})
		ss := val.(*sessionStreams)
		ss.mu.Lock()
		// Re-check: did a concurrent removeSessionMessage evict our pointer
		// from sessionToMsgs between LoadOrStore and Lock? If so, this
		// *sessionStreams is orphaned — loop to LoadOrStore a fresh one.
		cur, ok := sm.sessionToMsgs.Load(sessionID)
		if !ok || cur.(*sessionStreams) != ss {
			ss.mu.Unlock()
			continue
		}
		ss.ids[messageID] = struct{}{}
		ss.mu.Unlock()
		return
	}
}

// removeSessionMessage drops messageID from sessionID's reverse index. When
// the index becomes empty the session entry is deleted to bound memory.
//
// Holds ss.mu across CompareAndDelete so addSessionMessage's re-check can
// observe either the pre-delete or post-delete state coherently — never an
// interleaving where a new id is written into a *sessionStreams that is
// about to be unmapped.
func (sm *StreamManager) removeSessionMessage(sessionID, messageID string) {
	val, ok := sm.sessionToMsgs.Load(sessionID)
	if !ok {
		return
	}
	ss := val.(*sessionStreams)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.ids, messageID)
	if len(ss.ids) == 0 {
		sm.sessionToMsgs.CompareAndDelete(sessionID, val)
	}
}

// Subscribe registers the caller as the live SSE reader for a message and
// replays buffered events with EventID > fromEventID before the channel
// delivers live events. Returns (channel, closed). When closed=true the
// stream has already ended — the channel contains any replay events and is
// already closed. Returns (nil, false, false) if no such message exists.
//
// Passing fromEventID=0 is the normal "first connection" case; replay is
// bounded by the ring-buffer retention (most recent ringBufferCapacity
// events). Larger cursors are used when a client reconnects after an SSE
// drop — replay catches them up to the latest event.
//
// CW-20260418-0100.
func (sm *StreamManager) Subscribe(messageID string, fromEventID uint64) (<-chan chat.StreamEvent, bool, bool) {
	val, ok := sm.streams.Load(messageID)
	if !ok {
		return nil, false, false
	}
	ms, ok := val.(*messageStream)
	if !ok {
		return nil, false, false
	}
	ch, closed := ms.subscribe(fromEventID)
	return ch, closed, true
}

// SubscribeSSE replaces a browser connection and its subscriber together.
// Mark the old connection taken over before closing its event channel, so an
// automatic reconnect cannot mistake a deliberate takeover for a network drop.
//
// Takeover is per message: a second connection to the same message (another
// tab) replaces the first. Connections to different messages of one session
// coexist — a turn queued behind a running one (CW-20261001-0072) must not cut
// the running turn's stream off when its own stream is opened.
func (sm *StreamManager) SubscribeSSE(messageID string, fromEventID uint64) (<-chan chat.StreamEvent, <-chan struct{}, bool) {
	sub, ok := sm.SubscribeSSETransport(messageID, fromEventID)
	return sub.Events, sub.Takeover, ok
}

// SubscribeSSETransport reports cursor gaps and connection closure without
// conflating them with the producer's stream_end.
func (sm *StreamManager) SubscribeSSETransport(messageID string, fromEventID uint64) (MessageStreamSubscription, bool) {
	sm.sseMu.Lock()
	defer sm.sseMu.Unlock()
	val, ok := sm.streams.Load(messageID)
	if !ok {
		return MessageStreamSubscription{}, false
	}
	ms := val.(*messageStream)
	done := sm.RegisterSSE(messageID)
	ch, closure, _ := ms.subscribeTransport(fromEventID, true)
	return MessageStreamSubscription{Events: ch, Closure: closure, Takeover: done, Cancel: func() {
		ms.mu.Lock()
		if ms.subscriber == ch {
			ms.closeSubscriberLocked(chat.StreamTermination{Reason: "observer_detached", Retryable: true})
		}
		ms.mu.Unlock()
		sm.UnregisterSSE(messageID, done)
	}}, true
}

// DetachFromSession takes messageID out of its session's set of live streams
// without closing it: it stays subscribable, but session-scoped broadcasts no
// longer reach it and ActiveMessageForSession no longer returns it. A turn
// queued behind a running one is detached until it starts, so the running
// turn's tool events and reconnects are not misattributed to it.
func (sm *StreamManager) DetachFromSession(messageID string) {
	if sid, ok := sm.msgToSession.Load(messageID); ok {
		sm.removeSessionMessage(sid.(string), messageID)
	}
}

// AttachToSession returns a detached stream to its session's live set.
// Idempotent; a no-op once the stream has been cleaned up.
func (sm *StreamManager) AttachToSession(messageID string) {
	if sid, ok := sm.msgToSession.Load(messageID); ok {
		sm.addSessionMessage(sid.(string), messageID)
	}
}

// GetStream returns a subscription to the event stream for a given message
// ID, starting from EventID 0. Any events currently in the ring buffer are
// replayed before live events begin — equivalent to Subscribe(messageID, 0).
// Retained so call sites that never need a cursor don't have to thread one.
// PR #66 review #6: the "no replay" claim from the old docstring was wrong
// under the ring-buffer refactor.
func (sm *StreamManager) GetStream(messageID string) (<-chan chat.StreamEvent, bool) {
	ch, _, ok := sm.Subscribe(messageID, 0)
	if !ok {
		return nil, false
	}
	return ch, true
}

// CloseStream removes the stream and message-to-session mapping immediately.
// Prefer ScheduleCleanup in generateResponse-like producers so the ring
// buffer stays available for post-completion cursor reconnects.
//
// Does NOT close the producer channel — generateResponse's defer owns that.
// The pump goroutine exits when the producer closes.
func (sm *StreamManager) CloseStream(messageID string) {
	if val, ok := sm.msgToSession.LoadAndDelete(messageID); ok {
		sm.removeSessionMessage(val.(string), messageID)
	}
	sm.streams.Delete(messageID)
}

// defaultPostCompletionGrace is how long after the producer closes we keep
// the messageStream around for late SSE reconnects (CW-20260418-0100). Long
// enough that a tab that was backgrounded while the generation completed
// can come back, reconnect with its EventID cursor, and replay the final
// events including stream_end; short enough that process memory stays
// bounded under heavy session churn.
const defaultPostCompletionGrace = 60 * time.Second

// ScheduleCleanup arranges for CloseStream to run after `delay`. This is the
// cleanup call sites like generateResponse should use instead of invoking
// CloseStream directly, so SSE clients that disconnect near the end of a
// generation still get a replay window when they reconnect with an EventID
// cursor. PR #66 review #7: without this grace period, the "reconnect to
// a completed message" half of CW-20260418-0100 was unreachable in prod.
//
// Fire-and-forget; callers do not block on the timer.
func (sm *StreamManager) ScheduleCleanup(messageID string, delay time.Duration) {
	if delay <= 0 {
		sm.CloseStream(messageID)
		return
	}
	time.AfterFunc(delay, func() {
		sm.CloseStream(messageID)
	})
}

// GetSessionForMessage returns the session ID associated with a message stream.
func (sm *StreamManager) GetSessionForMessage(messageID string) (string, bool) {
	val, ok := sm.msgToSession.Load(messageID)
	if !ok {
		return "", false
	}
	return val.(string), true
}

// ActiveMessageForSession finds a turn that can still produce events. Closed
// streams retained for replay must not mask a newer, in-flight turn.
func (sm *StreamManager) ActiveMessageForSession(sessionID string) string {
	val, ok := sm.sessionToMsgs.Load(sessionID)
	if !ok {
		return ""
	}
	ss := val.(*sessionStreams)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for id := range ss.ids {
		if value, found := sm.streams.Load(id); found {
			ms := value.(*messageStream)
			ms.mu.Lock()
			closed := ms.closed
			ms.mu.Unlock()
			if !closed {
				return id
			}
		}
	}
	return ""
}

// HasLiveStreamForSession reports whether the process is currently holding an
// in-memory message stream for sessionID — i.e. a turn that this process is
// actively generating (or has just finished, still inside the post-completion
// grace window). The reverse index is purged by removeSessionMessage once a
// session's last stream is cleaned up.
//
// CW-20260518-0084: this is the discriminator the FE needs to tell a genuinely
// in-flight turn apart from one whose backend agent was killed by a service
// restart. After a restart the StreamManager is a fresh, empty instance, so
// every prior turn reads as "no live stream" — which, combined with a session
// whose last persisted message is a still-unanswered user turn, is the
// "interrupted by restart" signal.
func (sm *StreamManager) HasLiveStreamForSession(sessionID string) bool {
	val, ok := sm.sessionToMsgs.Load(sessionID)
	if !ok {
		return false
	}
	ss := val.(*sessionStreams)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.ids) > 0
}

// --- SSE connection deduplication ---

// RegisterSSE registers a new SSE connection for a message. If another
// connection to that message already exists, its done channel is closed
// (signaling session_takeover) before being replaced. Returns the new
// connection's done channel.
func (sm *StreamManager) RegisterSSE(messageID string) <-chan struct{} {
	conn := &sseConn{done: make(chan struct{})}
	if prev, loaded := sm.messageSSE.Swap(messageID, conn); loaded {
		old := prev.(*sseConn)
		close(old.done)
		slog.Info("stream: SSE connection takeover", "message_id", messageID)
	}
	return conn.done
}

// UnregisterSSE removes the SSE connection for a message, but only if this
// is still the active connection (not already taken over).
func (sm *StreamManager) UnregisterSSE(messageID string, done <-chan struct{}) {
	val, ok := sm.messageSSE.Load(messageID)
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
	sm.messageSSE.CompareAndDelete(messageID, val)
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

// BroadcastWorkChanged notifies all connected clients that session-scoped
// todos or plans have changed, so the Work drawer can refresh. Covers every
// mutation entrypoint — REST handlers, MCP self-tools, and the work-sync
// endpoint — so the drawer stays consistent regardless of who wrote the
// change. CW-20260418-0044.
func (sm *StreamManager) BroadcastWorkChanged() {
	sm.BroadcastPresence(chat.PresenceEvent{
		Type:      "work_changed",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
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

// BroadcastSessionStreamEvent fans a single chat.StreamEvent out to every
// active message stream for sessionID. Non-blocking — drops on full buffers
// and on the closed-channel race window the same way DeliverSessionEnvelopes
// does. Returns the count of streams that accepted the event so callers can
// detect "no active stream" and choose to surface a different signal.
func (sm *StreamManager) BroadcastSessionStreamEvent(sessionID string, evt chat.StreamEvent) int {
	if sessionID == "" {
		return 0
	}
	val, ok := sm.sessionToMsgs.Load(sessionID)
	if !ok {
		return 0
	}
	ss := val.(*sessionStreams)
	ss.mu.Lock()
	targets := make([]string, 0, len(ss.ids))
	for id := range ss.ids {
		targets = append(targets, id)
	}
	ss.mu.Unlock()

	delivered := 0
	for _, msgID := range targets {
		chVal, ok := sm.streams.Load(msgID)
		if !ok {
			continue
		}
		ms, ok := chVal.(*messageStream)
		if !ok {
			continue
		}
		if trySendEnvelope(ms.produce, evt) == sendDelivered {
			delivered++
		}
	}
	return delivered
}

// BroadcastPanelSignal adapts mcp.PanelSignalSink to the chat.StreamEvent
// shape. Used by J8 v1 panel-control tools (panel_open / panel_close /
// signal_mode) so the mcp package can push panel signals without importing
// chat (which would cycle through chat → toolclient → mcp).
//
// signalType is the SSE event type ("panel_signal"); jsonPayload is the
// already-marshaled PanelSignal JSON. Both ride on the existing Envelope
// field on chat.StreamEvent so no new wire field is introduced.
//
// CW-20260426-0006.
func (sm *StreamManager) BroadcastPanelSignal(sessionID, signalType, jsonPayload string) int {
	return sm.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
		Type:     signalType,
		Envelope: jsonPayload,
	})
}

// --- Plugin envelope delivery (BLG-20260413-012) ---

// sendOutcome classifies the result of a non-blocking envelope send so
// DeliverSessionEnvelopes can account for drops without conflating the
// "slow consumer" and "already closed" cases in logs or metrics.
type sendOutcome int

const (
	sendDelivered sendOutcome = iota
	sendFull
	sendClosed
)

// trySendEnvelope attempts a non-blocking send on ch and recovers from the
// "send on closed channel" panic that would otherwise crash the host event
// dispatcher. The recover is narrowly scoped to this function so it cannot
// mask unrelated bugs. See Copilot review on PR #36 — the producer
// (generateResponse) closes the channel before calling CloseStream, so a
// concurrent Deliver can observe an entry in sm.streams whose channel has
// already been closed.
func trySendEnvelope(ch chan chat.StreamEvent, evt chat.StreamEvent) (outcome sendOutcome) {
	defer func() {
		if r := recover(); r != nil {
			outcome = sendClosed
		}
	}()
	select {
	case ch <- evt:
		return sendDelivered
	default:
		return sendFull
	}
}

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
			ms, ok := chVal.(*messageStream)
			if !ok {
				continue
			}
			outcome := trySendEnvelope(ms.produce, evt)
			switch outcome {
			case sendDelivered:
				anyDelivered = true
			case sendFull:
				sm.pluginEnvelopeDrops.Add(1)
				slog.Warn("stream: dropped plugin envelope — chat stream full",
					"session_id", sessionID, "plugin_id", pluginID, "message_id", msgID)
			case sendClosed:
				// Window between the producer's close(ch) and CloseStream's
				// sm.streams.Delete. The producer owns channel closure
				// (see CloseStream's doc comment) so we can observe a
				// Load hit on a channel that has already been closed.
				// Count as a drop — panicking the host event dispatch
				// would violate BLG-012's "don't block the event
				// dispatch" contract. Copilot PR #36 flagged this race.
				sm.pluginEnvelopeDrops.Add(1)
				slog.Warn("stream: dropped plugin envelope — chat stream closed",
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
