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
	streams        sync.Map // messageID -> *messageStream (CW-20260418-0100)
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

// ringBufferCapacity bounds the per-message event history used for replay on
// SSE reconnect. 256 covers a typical tool-heavy turn (many short deltas +
// tool_call/tool_result pairs) without unbounded memory growth. Older events
// are evicted first — a client that disconnects, sleeps past 256 events of
// activity, and then reconnects will miss the oldest events but still pick
// up from wherever in the buffer its cursor lands.
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

	// closed is set by the pump when produce closes and it has drained all
	// remaining events. Subscribers connecting after closed=true receive a
	// full replay followed by a closed channel.
	closed bool
}

// newMessageStream starts a pump goroutine for a fresh message. The caller
// receives the producer channel via CreateStream.
func newMessageStream(messageID, sessionID string) *messageStream {
	ms := &messageStream{
		messageID: messageID,
		sessionID: sessionID,
		produce:   make(chan chat.StreamEvent, 128),
		nextID:    1,
	}
	go ms.pump()
	return ms
}

// pump is the per-message dispatcher goroutine. It owns all mutations to
// buf/nextID/subscriber; Subscribe and the producer just push through it.
//
// The non-blocking send to the current subscriber happens while ms.mu is
// held. This serialises the write against subscribe()'s close(prev) (and
// the producer-end close below), eliminating the data race between pump
// fanout and subscriber replacement (CW-20260510-0002). The send is a
// `select default`, so holding the lock cannot block — at worst we take
// the overflow path and drop the slow subscriber under the same lock.
func (ms *messageStream) pump() {
	for evt := range ms.produce {
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
		var slowSub chan chat.StreamEvent
		if sub != nil {
			select {
			case sub <- evt:
			default:
				if ms.subscriber == sub {
					ms.subscriber = nil
					slowSub = sub
				}
			}
		}
		ms.mu.Unlock()
		if slowSub != nil {
			slog.Debug("stream: closing slow subscriber to force cursor-replay",
				"message_id", ms.messageID, "event_id", evt.EventID, "type", evt.Type)
			close(slowSub)
		}
	}

	// Producer closed. Mark closed and close the current subscriber so the
	// SSE handler sees EOF. Closing under the lock matches the in-loop
	// discipline so a concurrent subscribe() takeover cannot race the
	// pump-end close.
	ms.mu.Lock()
	ms.closed = true
	sub := ms.subscriber
	ms.subscriber = nil
	if sub != nil {
		close(sub)
	}
	ms.mu.Unlock()
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
	out := make(chan chat.StreamEvent, outCap)

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
	prev := ms.subscriber
	if ms.closed {
		// Pump already closed prev (or it was nil); nothing to do here.
		ms.mu.Unlock()
		_ = prev
		close(out)
		return out, true
	}
	ms.subscriber = out
	if prev != nil {
		close(prev)
	}
	ms.mu.Unlock()
	return out, false
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

// BroadcastSessionModeChanged fans a `session_mode_changed` presence event
// out to every connected presence client so a tab open on the same session
// updates its mode chip without manual refetch. Empty modeID/modeSlug
// signal a clear (session pointer reset to default chat mode). Reuses the
// existing presence channel — no new transport. F1 (CW-20260429-0001).
func (sm *StreamManager) BroadcastSessionModeChanged(sessionID, modeID, modeSlug string) {
	sm.BroadcastPresence(chat.PresenceEvent{
		Type:      "session_mode_changed",
		SessionID: sessionID,
		ModeID:    modeID,
		ModeSlug:  modeSlug,
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
