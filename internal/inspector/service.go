package inspector

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultRingSize is the maximum number of TurnSnapshots retained per session.
	DefaultRingSize = 50

	// dropFull is returned by a non-blocking channel send when the channel is
	// at capacity. Record* calls are fire-and-forget when the ring is full.
	dropFull = "drop"
)

// Service is the inspector aggregator. Producers call Record* from hot paths;
// consumers call Snapshot / RecentSnapshots from API handlers.
//
// All Record* methods are safe to call concurrently and non-blocking: they
// acquire a per-session shard lock for O(1) writes and drop the event
// silently when the ring is full (dev-mode data loss is acceptable).
type Service struct {
	mu       sync.Mutex
	sessions map[string]*sessionBuffer
	ringSize int
	// turnCounters is an atomic int64 counter per session key for monotonic
	// turn ID generation. Stored as a sync.Map[string]*int64.
	turnCounters sync.Map
}

// NewService creates a new inspector Service with the default ring size.
func NewService() *Service {
	return &Service{
		sessions: make(map[string]*sessionBuffer),
		ringSize: DefaultRingSize,
	}
}

// NewServiceWithRingSize creates a Service with a custom ring buffer size.
// Useful in tests.
func NewServiceWithRingSize(n int) *Service {
	return &Service{
		sessions: make(map[string]*sessionBuffer),
		ringSize: n,
	}
}

// sessionBuffer holds the ring buffer for one session.
type sessionBuffer struct {
	mu     sync.Mutex
	turns  []*TurnSnapshot // ring — oldest at head
	cap    int
	// index → snapshot for O(1) lookup by turn ID
	index  map[string]*TurnSnapshot
}

func newSessionBuffer(cap int) *sessionBuffer {
	return &sessionBuffer{
		turns: make([]*TurnSnapshot, 0, cap),
		cap:   cap,
		index: make(map[string]*TurnSnapshot),
	}
}

// upsert finds or creates the TurnSnapshot for turnID, applies fn, and
// ensures the ring cap invariant. fn is called under the buffer lock.
func (sb *sessionBuffer) upsert(turnID string, fn func(*TurnSnapshot)) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	snap, ok := sb.index[turnID]
	if !ok {
		// New turn: evict oldest if at capacity.
		if len(sb.turns) >= sb.cap {
			oldest := sb.turns[0]
			sb.turns = sb.turns[1:]
			delete(sb.index, oldest.TurnID)
		}
		snap = &TurnSnapshot{TurnID: turnID, StartedAt: time.Now()}
		sb.turns = append(sb.turns, snap)
		sb.index[turnID] = snap
	}
	fn(snap)
}

// snapshot returns a deep copy of the TurnSnapshot for turnID, or nil if not
// found.
func (sb *sessionBuffer) snapshot(turnID string) *TurnSnapshot {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	snap, ok := sb.index[turnID]
	if !ok {
		return nil
	}
	return copySnapshot(snap)
}

// recent returns the last n snapshots (most recent last), copying each.
func (sb *sessionBuffer) recent(n int) []TurnSnapshot {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	src := sb.turns
	if n > 0 && len(src) > n {
		src = src[len(src)-n:]
	}
	out := make([]TurnSnapshot, 0, len(src))
	for _, s := range src {
		out = append(out, *copySnapshot(s))
	}
	return out
}

// copySnapshot performs a shallow-safe copy of a TurnSnapshot. Slice fields
// get new backing arrays so callers can't accidentally mutate the live data.
func copySnapshot(s *TurnSnapshot) *TurnSnapshot {
	c := *s // value copy
	c.Slots = append([]SlotSnapshot(nil), s.Slots...)
	c.LLMMessages = append([]LLMMessageRecord(nil), s.LLMMessages...)
	c.BrokerDecisions = append([]BrokerDecision(nil), s.BrokerDecisions...)
	c.ToolCalls = append([]ToolCallRecord(nil), s.ToolCalls...)
	c.MemoryHits = append([]MemoryRecord(nil), s.MemoryHits...)
	return &c
}

// getOrCreate returns the sessionBuffer for sessionID, creating one if needed.
func (s *Service) getOrCreate(sessionID string) *sessionBuffer {
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.sessions[sessionID]
	if !ok {
		sb = newSessionBuffer(s.ringSize)
		s.sessions[sessionID] = sb
	}
	return sb
}

// NextTurnID returns a monotonically-increasing string turn ID for sessionID.
// Format: "1", "2", …
func (s *Service) NextTurnID(sessionID string) string {
	key := sessionID
	var ctr *int64
	if v, ok := s.turnCounters.Load(key); ok {
		ctr = v.(*int64)
	} else {
		n := int64(0)
		actual, _ := s.turnCounters.LoadOrStore(key, &n)
		ctr = actual.(*int64)
	}
	next := atomic.AddInt64(ctr, 1)
	return fmt.Sprintf("%d", next)
}

// EnsureTurn guarantees the snapshot for (sessionID, turnID) exists with the
// given sessionID populated. Safe to call multiple times.
func (s *Service) EnsureTurn(sessionID, turnID string) {
	sb := s.getOrCreate(sessionID)
	sb.upsert(turnID, func(snap *TurnSnapshot) {
		snap.SessionID = sessionID
	})
}

// RecordSlots sets all 8 slot snapshots for a turn. Overwrites any
// previously-recorded slots for this turn.
func (s *Service) RecordSlots(sessionID, turnID string, slots []SlotSnapshot) {
	sb := s.getOrCreate(sessionID)
	sb.upsert(turnID, func(snap *TurnSnapshot) {
		snap.SessionID = sessionID
		snap.Slots = slots
	})
}

// RecordLLMMessages sets the LLM-facing message list for a turn.
func (s *Service) RecordLLMMessages(sessionID, turnID string, msgs []LLMMessageRecord) {
	sb := s.getOrCreate(sessionID)
	sb.upsert(turnID, func(snap *TurnSnapshot) {
		snap.SessionID = sessionID
		snap.LLMMessages = msgs
	})
}

// RecordBrokerDecision appends one broker decision to the turn.
func (s *Service) RecordBrokerDecision(sessionID, turnID string, d BrokerDecision) {
	sb := s.getOrCreate(sessionID)
	sb.upsert(turnID, func(snap *TurnSnapshot) {
		snap.SessionID = sessionID
		snap.BrokerDecisions = append(snap.BrokerDecisions, d)
	})
}

// RecordToolCall appends one tool-call record to the turn.
func (s *Service) RecordToolCall(sessionID, turnID string, call ToolCallRecord) {
	sb := s.getOrCreate(sessionID)
	sb.upsert(turnID, func(snap *TurnSnapshot) {
		snap.SessionID = sessionID
		snap.ToolCalls = append(snap.ToolCalls, call)
	})
}

// RecordScopeTier records the B2 scope-tier classification for the turn.
func (s *Service) RecordScopeTier(sessionID, turnID string, tier string) {
	sb := s.getOrCreate(sessionID)
	sb.upsert(turnID, func(snap *TurnSnapshot) {
		snap.SessionID = sessionID
		snap.ScopeTier = tier
	})
}

// RecordLoopStatus records the I2 loop-detection result for the turn.
// Overwrites any previously-recorded LoopStatus for this turn.
func (s *Service) RecordLoopStatus(sessionID, turnID string, rec *LoopRecord) {
	sb := s.getOrCreate(sessionID)
	sb.upsert(turnID, func(snap *TurnSnapshot) {
		snap.SessionID = sessionID
		snap.LoopStatus = rec
	})
}

// Snapshot returns the TurnSnapshot for (sessionID, turnID), or nil if not
// found.
func (s *Service) Snapshot(sessionID, turnID string) *TurnSnapshot {
	s.mu.Lock()
	sb, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return sb.snapshot(turnID)
}

// RecentSnapshots returns up to limit TurnSnapshots for sessionID, ordered
// oldest first. limit <= 0 returns all retained snapshots.
func (s *Service) RecentSnapshots(sessionID string, limit int) []TurnSnapshot {
	s.mu.Lock()
	sb, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return sb.recent(limit)
}

// trafficLight returns "green" (cached), "yellow" (has content, not cached),
// or "red" (empty) for a slot.
func trafficLight(tokens int, cached bool) string {
	if tokens == 0 {
		return "red"
	}
	if cached {
		return "green"
	}
	return "yellow"
}
