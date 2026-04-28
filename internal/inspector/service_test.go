package inspector

import (
	"testing"
)

// TestServiceRecordAndSnapshot verifies that Record* calls are readable via
// Snapshot and RecentSnapshots.
func TestServiceRecordAndSnapshot(t *testing.T) {
	svc := NewServiceWithRingSize(10)
	sessionID := "sess-001"

	turnID := svc.NextTurnID(sessionID)
	if turnID != "1" {
		t.Fatalf("expected first turn ID to be '1', got %q", turnID)
	}

	svc.EnsureTurn(sessionID, turnID)

	// Record 8 slots.
	slots := make([]SlotSnapshot, 8)
	for i, name := range []string{"system", "memory", "agent", "rules", "tools", "session", "context", "conversation"} {
		slots[i] = SlotSnapshot{
			Name:         name,
			Tokens:       (i + 1) * 100,
			Cached:       i%2 == 0,
			Content:      "content-" + name,
			TrafficLight: trafficLight((i+1)*100, i%2 == 0),
		}
	}
	svc.RecordSlots(sessionID, turnID, slots)

	// Record LLM messages.
	msgs := []LLMMessageRecord{
		{Role: "system", Content: "sys", Tokens: 50, Classification: "system_prompt"},
		{Role: "user", Content: "hello", Tokens: 10, Classification: "user_turn"},
	}
	svc.RecordLLMMessages(sessionID, turnID, msgs)

	// Record a broker decision.
	bd := BrokerDecision{
		Intent:        "find tools",
		Outcome:       "loaded",
		SelectedTools: []string{"nanite_read", "nanite_write"},
		TotalCalls:    1,
	}
	svc.RecordBrokerDecision(sessionID, turnID, bd)

	// Record a tool call.
	tc := ToolCallRecord{
		ToolID:    "tc-001",
		Name:      "nanite_read",
		Arguments: `{"path":"/tmp/foo"}`,
		Result:    "file content",
		LatencyMs: 42,
	}
	svc.RecordToolCall(sessionID, turnID, tc)

	// Record scope tier.
	svc.RecordScopeTier(sessionID, turnID, "small")

	// Read back via Snapshot.
	snap := svc.Snapshot(sessionID, turnID)
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}

	if snap.SessionID != sessionID {
		t.Errorf("SessionID: got %q, want %q", snap.SessionID, sessionID)
	}
	if snap.TurnID != turnID {
		t.Errorf("TurnID: got %q, want %q", snap.TurnID, turnID)
	}
	if len(snap.Slots) != 8 {
		t.Errorf("Slots: got %d, want 8", len(snap.Slots))
	}
	if len(snap.LLMMessages) != 2 {
		t.Errorf("LLMMessages: got %d, want 2", len(snap.LLMMessages))
	}
	if len(snap.BrokerDecisions) != 1 {
		t.Errorf("BrokerDecisions: got %d, want 1", len(snap.BrokerDecisions))
	}
	if snap.BrokerDecisions[0].Intent != "find tools" {
		t.Errorf("BrokerDecision.Intent: got %q, want %q", snap.BrokerDecisions[0].Intent, "find tools")
	}
	if len(snap.ToolCalls) != 1 {
		t.Errorf("ToolCalls: got %d, want 1", len(snap.ToolCalls))
	}
	if snap.ToolCalls[0].Name != "nanite_read" {
		t.Errorf("ToolCall.Name: got %q, want %q", snap.ToolCalls[0].Name, "nanite_read")
	}
	if snap.ScopeTier != "small" {
		t.Errorf("ScopeTier: got %q, want %q", snap.ScopeTier, "small")
	}
}

// TestServiceMultipleTurns verifies that multiple turns are tracked correctly.
func TestServiceMultipleTurns(t *testing.T) {
	svc := NewServiceWithRingSize(10)
	sessionID := "sess-002"

	for i := 0; i < 3; i++ {
		turnID := svc.NextTurnID(sessionID)
		svc.EnsureTurn(sessionID, turnID)
		svc.RecordScopeTier(sessionID, turnID, "trivial")
	}

	turns := svc.RecentSnapshots(sessionID, 10)
	if len(turns) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(turns))
	}
	if turns[0].TurnID != "1" || turns[1].TurnID != "2" || turns[2].TurnID != "3" {
		t.Errorf("unexpected turn IDs: %v %v %v", turns[0].TurnID, turns[1].TurnID, turns[2].TurnID)
	}
}

// TestServiceRingEviction verifies that the ring buffer evicts oldest turns
// when the cap is reached.
func TestServiceRingEviction(t *testing.T) {
	const ringSize = 3
	svc := NewServiceWithRingSize(ringSize)
	sessionID := "sess-ring"

	for i := 0; i < ringSize+2; i++ {
		turnID := svc.NextTurnID(sessionID)
		svc.EnsureTurn(sessionID, turnID)
	}

	turns := svc.RecentSnapshots(sessionID, 0) // 0 → all
	if len(turns) != ringSize {
		t.Fatalf("expected ring size %d turns, got %d", ringSize, len(turns))
	}

	// Oldest 2 turns should have been evicted.
	if turns[0].TurnID != "3" {
		t.Errorf("expected oldest retained turn to be '3', got %q", turns[0].TurnID)
	}
}

// TestServiceUnknownSession returns nil/empty for unknown sessions.
func TestServiceUnknownSession(t *testing.T) {
	svc := NewService()
	snap := svc.Snapshot("no-such-session", "1")
	if snap != nil {
		t.Error("expected nil snapshot for unknown session")
	}
	turns := svc.RecentSnapshots("no-such-session", 10)
	if turns != nil {
		t.Error("expected nil for unknown session recent turns")
	}
}

// TestServiceCopyIsolation verifies that mutating a returned snapshot does not
// affect the internal ring buffer.
func TestServiceCopyIsolation(t *testing.T) {
	svc := NewService()
	sessionID := "sess-copy"
	turnID := svc.NextTurnID(sessionID)
	svc.EnsureTurn(sessionID, turnID)
	svc.RecordSlots(sessionID, turnID, []SlotSnapshot{{Name: "system", Tokens: 42}})

	snap := svc.Snapshot(sessionID, turnID)
	snap.Slots[0].Tokens = 9999 // mutate the copy

	// Re-fetch — internal state should be unchanged.
	snap2 := svc.Snapshot(sessionID, turnID)
	if snap2.Slots[0].Tokens == 9999 {
		t.Error("snapshot isolation violated: internal ring buffer was mutated via returned copy")
	}
}

// TestTrafficLight verifies the colour logic.
func TestTrafficLight(t *testing.T) {
	cases := []struct {
		tokens int
		cached bool
		want   string
	}{
		{0, false, "red"},
		{0, true, "red"},
		{100, true, "green"},
		{100, false, "yellow"},
	}
	for _, c := range cases {
		got := trafficLight(c.tokens, c.cached)
		if got != c.want {
			t.Errorf("trafficLight(%d, %v) = %q, want %q", c.tokens, c.cached, got, c.want)
		}
	}
}

// TestNextTurnIDMonotonic verifies monotonic turn ID generation.
func TestNextTurnIDMonotonic(t *testing.T) {
	svc := NewService()
	sessionID := "sess-mono"
	for i := 1; i <= 5; i++ {
		id := svc.NextTurnID(sessionID)
		if id != string(rune('0'+i)) {
			t.Errorf("turn %d: got ID %q, want %q", i, id, string(rune('0'+i)))
		}
	}
}

// TestBrokerDecisionMultiple verifies multiple broker decisions accumulate.
func TestBrokerDecisionMultiple(t *testing.T) {
	svc := NewService()
	sessionID := "sess-broker"
	turnID := svc.NextTurnID(sessionID)
	svc.EnsureTurn(sessionID, turnID)

	for _, outcome := range []string{"loaded", "empty", "halted"} {
		svc.RecordBrokerDecision(sessionID, turnID, BrokerDecision{Outcome: outcome})
	}

	snap := svc.Snapshot(sessionID, turnID)
	if len(snap.BrokerDecisions) != 3 {
		t.Fatalf("expected 3 broker decisions, got %d", len(snap.BrokerDecisions))
	}
}
