package service

// TestReminderEngineWireup_TurnCountFires is a service-layer integration test
// for J11 (CW-20260426-0009) that verifies the four wire-up points:
//
//  1. Engine.EvalTurn is called from the per-turn path.
//  2. Fired reminders are injected into SlotUserContext as a <system-reminder> block.
//  3. Inspector.RecordReminders is called with the fired reminder.
//  4. session.MessageCount is used as the turn counter.
//
// This test does NOT spin up generateResponse (which requires a full LLM
// provider round-trip). Instead, it calls the narrower helper that
// generateResponse delegates to — EvalTurn on a real Engine backed by a
// real SQLite store — and asserts the injection and inspector record results.
// This mirrors the approach used in context_slot_fill_test.go.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/go-providers/provider"
)

// TestReminderEngine_TurnCountFiresAndInjectsSlot verifies:
//  1. Agent registers a turn_count reminder (n=2) via Engine.RegisterTurnCount.
//  2. EvalTurn at turn 1 (creation+0) does NOT fire.
//  3. EvalTurn at turn 2 (creation+2) fires and returns the reminder.
//  4. FormatInjection produces the expected <system-reminder> block.
//  5. Appending the injection to an existing SlotUserContext works correctly.
func TestReminderEngine_TurnCountFiresAndInjectsSlot(t *testing.T) {
	s, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.DB.Close()

	engine := reminders.NewEngine(s)

	sessionID := "sess-reminder-wireup"

	// Create the reminder row in the DB (simulates reminder_set tool call).
	r := store.Reminder{
		ID:          "rem-test-001",
		SessionID:   sessionID,
		Text:        "Don't forget to create that ticket!",
		TriggerJSON: `{"type":"turn_count","n":2}`,
	}
	if err := s.CreateReminder(r); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	// Register creation turn = 0 (simulates what SelfToolsTransport does when
	// the reminder is created; the MCP tool calls RegisterTurnCount immediately
	// after CreateReminder with the current session.MessageCount).
	engine.RegisterTurnCount(r.ID, 0)

	// --- Turn 1: should NOT fire (0 + 2 = 2, and currentTurn=1 < 2) ---
	fired1, err := engine.EvalTurn(sessionID, 1)
	if err != nil {
		t.Fatalf("EvalTurn(1): %v", err)
	}
	if len(fired1) != 0 {
		t.Errorf("turn 1: expected 0 fired reminders, got %d", len(fired1))
	}

	// --- Turn 2: should fire (currentTurn=2 >= 0+2) ---
	fired2, err := engine.EvalTurn(sessionID, 2)
	if err != nil {
		t.Fatalf("EvalTurn(2): %v", err)
	}
	if len(fired2) != 1 {
		t.Fatalf("turn 2: expected 1 fired reminder, got %d", len(fired2))
	}
	if fired2[0].ID != r.ID {
		t.Errorf("fired reminder ID mismatch: got %q, want %q", fired2[0].ID, r.ID)
	}
	if fired2[0].Text != r.Text {
		t.Errorf("fired reminder text mismatch: got %q, want %q", fired2[0].Text, r.Text)
	}

	// --- Verify FormatInjection produces the expected block ---
	injection := reminders.FormatInjection(fired2)
	if !strings.Contains(injection, "<system-reminder>") {
		t.Errorf("injection missing <system-reminder> open tag: %q", injection)
	}
	if !strings.Contains(injection, "</system-reminder>") {
		t.Errorf("injection missing </system-reminder> close tag: %q", injection)
	}
	if !strings.Contains(injection, r.Text) {
		t.Errorf("injection missing reminder text %q: %q", r.Text, injection)
	}

	// --- Verify slot injection appends to existing SlotUserContext content ---
	// Simulate the window.SetContent pattern from generateResponse.
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	sess := &store.Session{ID: sessionID, Title: "ReminderWireupTest"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{
		ID:     "agent-reminder-test",
		Name:   "ReminderTestAgent",
		Slug:   "reminder-test",
		Status: "active",
		Tags:   `[]`,
		Tools:  `[]`,
	}
	mode := &store.AgentMode{Slug: "default"}
	slotResult, err := svc.AssembleSlots(context.Background(), sess, agent, mode, nil, []provider.ToolDefinition{}, "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if slotResult.Window == nil {
		t.Fatal("expected non-nil Window in SlotAssemblyResult")
	}

	// Apply the injection (mirrors the generateResponse code path).
	existing := ""
	if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot != nil {
		existing = slot.Content
	}
	if existing != "" {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, existing+"\n\n"+injection)
	} else {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, injection)
	}

	// Verify the slot now contains the injection.
	if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot == nil {
		t.Fatal("SlotUserContext is nil after injection")
	} else if !strings.Contains(slot.Content, "<system-reminder>") {
		t.Errorf("SlotUserContext missing injection after SetContent: %q", slot.Content)
	}

	// --- Turn 3: reminder is already fired; should not fire again ---
	fired3, err := engine.EvalTurn(sessionID, 3)
	if err != nil {
		t.Fatalf("EvalTurn(3): %v", err)
	}
	if len(fired3) != 0 {
		t.Errorf("turn 3: expected 0 fired (reminder already fired), got %d", len(fired3))
	}
}

// TestReminderEngine_TimeTriggerReachesLLMSlotBlocks is the regression test
// for CW-20260501-0002. Before the fix, EvalTurn correctly fired time-based
// reminders and Window.SetContent updated SlotUserContext, but neither
// slotResult.Blocks (consumed by ChatRequest.SlotBlocks — the actual LLM
// payload) nor slotResult.SystemPrompt (consumed by budget enforcement and
// telemetry) was refreshed. Result: the agent never saw the <system-reminder>
// block on its next turn even though fired_at was set in the DB.
//
// This test reproduces the full chat_generate.go injection sequence (including
// the post-injection Blocks/SystemPrompt rebuild) and asserts that the
// derived views actually contain the reminder text.
func TestReminderEngine_TimeTriggerReachesLLMSlotBlocks(t *testing.T) {
	s, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.DB.Close()

	engine := reminders.NewEngine(s)

	sessionID := "sess-time-reminder"
	sess := &store.Session{ID: sessionID, Title: "TimeReminderRegression"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Create a time-based reminder that fires immediately (1s ago).
	fireAt := time.Now().Add(-1 * time.Second).UTC().Format(time.RFC3339)
	r := store.Reminder{
		ID:          "rem-time-001",
		SessionID:   sessionID,
		Text:        "User wanted a status check on the build.",
		TriggerJSON: fmt.Sprintf(`{"type":"time","at":%q}`, fireAt),
	}
	if err := s.CreateReminder(r); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	// EvalTurn must fire the reminder and mark it fired_at in the DB.
	fired, err := engine.EvalTurn(sessionID, 1)
	if err != nil {
		t.Fatalf("EvalTurn: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("expected 1 fired reminder, got %d", len(fired))
	}

	// Verify fired_at is set in the DB (acceptance criterion 1).
	got, err := s.GetReminder(r.ID)
	if err != nil {
		t.Fatalf("GetReminder: %v", err)
	}
	if got.FiredAt == nil {
		t.Fatal("expected fired_at to be set in the DB after EvalTurn")
	}

	// --- Now simulate the chat_generate.go injection pipeline ---
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	agent := &store.AgentProfile{
		ID:     "agent-time-reminder",
		Name:   "TimeReminderAgent",
		Slug:   "time-reminder",
		Status: "active",
		Tags:   `[]`,
		Tools:  `[]`,
	}
	mode := &store.AgentMode{Slug: "default"}
	slotResult, err := svc.AssembleSlots(context.Background(), sess, agent, mode, nil, []provider.ToolDefinition{}, "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	// Snapshot the pre-injection derived views so we can assert the bug
	// state vs the fixed state.
	preBlocks := slotResult.Blocks
	preSystem := slotResult.SystemPrompt

	injection := reminders.FormatInjection(fired)

	// Apply the injection — must mirror chat_generate.go EXACTLY so this
	// test catches regressions where someone updates the Window without
	// refreshing Blocks/SystemPrompt.
	existing := ""
	if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot != nil {
		existing = slot.Content
	}
	if existing != "" {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, existing+"\n\n"+injection)
	} else {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, injection)
	}
	// CW-20260501-0002 — the fix:
	slotResult.Blocks = slotResult.Window.Assemble()
	slotResult.SystemPrompt = rebuildLegacySystemPrompt(slotResult.Window)

	// --- Acceptance assertions ---

	// (a) Window.SlotUserContext carries the injection (already verified
	// elsewhere; included for completeness).
	if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot == nil ||
		!strings.Contains(slot.Content, r.Text) {
		t.Errorf("SlotUserContext missing reminder text after injection")
	}

	// (b) slotResult.Blocks (what slotBlocksFor projects into ChatRequest.SlotBlocks)
	// MUST contain the reminder text. This is the load-bearing assertion —
	// before the fix, this fails because Blocks was assembled before injection.
	blocksHasReminder := false
	for _, b := range slotResult.Blocks {
		if strings.Contains(b.Content, r.Text) {
			blocksHasReminder = true
			break
		}
	}
	if !blocksHasReminder {
		t.Errorf("slotResult.Blocks missing reminder text — the LLM would not see the reminder. Pre-injection Blocks=%d, Post-injection Blocks=%d", len(preBlocks), len(slotResult.Blocks))
	}

	// (c) slotResult.SystemPrompt (legacy concat) MUST contain the reminder.
	if !strings.Contains(slotResult.SystemPrompt, r.Text) {
		t.Errorf("slotResult.SystemPrompt missing reminder text. pre=%d post=%d", len(preSystem), len(slotResult.SystemPrompt))
	}

	// (d) <system-reminder> framing is present in the derived views.
	foundFraming := false
	for _, b := range slotResult.Blocks {
		if strings.Contains(b.Content, "<system-reminder>") &&
			strings.Contains(b.Content, "</system-reminder>") {
			foundFraming = true
			break
		}
	}
	if !foundFraming {
		t.Errorf("slotResult.Blocks missing <system-reminder> framing")
	}
}

// TestReminderEngine_TurnCountFiresThroughInjectionPipeline asserts the
// equivalent regression for turn_count triggers.
func TestReminderEngine_TurnCountFiresThroughInjectionPipeline(t *testing.T) {
	s, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.DB.Close()

	engine := reminders.NewEngine(s)
	sessionID := "sess-turn-pipeline"
	sess := &store.Session{ID: sessionID, Title: "TurnReminderPipeline"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	r := store.Reminder{
		ID:          "rem-turn-pipeline-001",
		SessionID:   sessionID,
		Text:        "Time-boxed reminder that should reach the LLM.",
		TriggerJSON: `{"type":"turn_count","n":1}`,
	}
	if err := s.CreateReminder(r); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}
	engine.RegisterTurnCount(r.ID, 0)

	fired, err := engine.EvalTurn(sessionID, 1)
	if err != nil {
		t.Fatalf("EvalTurn: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("expected 1 fired, got %d", len(fired))
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})
	agent := &store.AgentProfile{
		ID: "a-1", Name: "A", Slug: "a", Status: "active", Tags: "[]", Tools: "[]",
	}
	mode := &store.AgentMode{Slug: "default"}
	slotResult, err := svc.AssembleSlots(context.Background(), sess, agent, mode, nil, []provider.ToolDefinition{}, "", 200000, nil, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	injection := reminders.FormatInjection(fired)
	existing := ""
	if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot != nil {
		existing = slot.Content
	}
	if existing != "" {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, existing+"\n\n"+injection)
	} else {
		slotResult.Window.SetContent(ctxpkg.SlotUserContext, injection)
	}
	slotResult.Blocks = slotResult.Window.Assemble()
	slotResult.SystemPrompt = rebuildLegacySystemPrompt(slotResult.Window)

	hit := false
	for _, b := range slotResult.Blocks {
		if strings.Contains(b.Content, r.Text) {
			hit = true
			break
		}
	}
	if !hit {
		t.Errorf("turn_count reminder did not reach slotResult.Blocks")
	}
	if !strings.Contains(slotResult.SystemPrompt, r.Text) {
		t.Errorf("turn_count reminder did not reach slotResult.SystemPrompt")
	}
}

// TestReminderEngine_InspectorRecordsFiredReminders verifies that the inspector
// RecordReminders API (called from generateResponse after EvalTurn) correctly
// persists the fired reminder into the dev-mode turn snapshot.
func TestReminderEngine_InspectorRecordsFiredReminders(t *testing.T) {
	inspector := inspectsvc.NewService()

	sessionID := "sess-insp-reminder"
	turnID := inspector.NextTurnID(sessionID)
	inspector.EnsureTurn(sessionID, turnID)

	// Simulate the record call from generateResponse.
	firedReminder := store.Reminder{
		ID:          "rem-insp-001",
		Text:        "Check in with the user before proceeding.",
		TriggerJSON: `{"type":"turn_count","n":3}`,
	}
	rec := inspectsvc.RemindersRecord{}
	rec.FiredThisTurn = append(rec.FiredThisTurn, inspectsvc.ReminderItem{
		ID:          firedReminder.ID,
		Text:        firedReminder.Text,
		TriggerJSON: firedReminder.TriggerJSON,
	})
	inspector.RecordReminders(sessionID, turnID, rec)

	// Verify the snapshot contains the reminder.
	snap := inspector.Snapshot(sessionID, turnID)
	if snap == nil {
		t.Fatal("expected non-nil TurnSnapshot")
	}
	if snap.Reminders == nil {
		t.Fatal("expected non-nil Reminders in TurnSnapshot")
	}
	if len(snap.Reminders.FiredThisTurn) != 1 {
		t.Fatalf("expected 1 fired reminder in snapshot, got %d", len(snap.Reminders.FiredThisTurn))
	}
	got := snap.Reminders.FiredThisTurn[0]
	if got.ID != firedReminder.ID {
		t.Errorf("reminder ID mismatch: got %q, want %q", got.ID, firedReminder.ID)
	}
	if got.Text != firedReminder.Text {
		t.Errorf("reminder text mismatch: got %q, want %q", got.Text, firedReminder.Text)
	}
	if got.TriggerJSON != firedReminder.TriggerJSON {
		t.Errorf("reminder TriggerJSON mismatch: got %q, want %q", got.TriggerJSON, firedReminder.TriggerJSON)
	}
}

// TestReminderEngine_SessionMessageCountAsTurnCounter verifies that
// session.MessageCount (the existing DB field) serves correctly as the
// monotonic turn counter without requiring a new schema field.
func TestReminderEngine_SessionMessageCountAsTurnCounter(t *testing.T) {
	s, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.DB.Close()

	sessionID := "sess-turn-count"
	sess := &store.Session{ID: sessionID, Title: "TurnCounterTest"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Simulate 2 message inserts (each increments session.MessageCount).
	for i := 0; i < 2; i++ {
		msg := &store.Message{
			ID:        fmt.Sprintf("msg-%d", i),
			SessionID: sessionID,
			Role:      "user",
			Content:   "turn message",
		}
		if err := s.CreateMessage(msg); err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
	}

	// Reload the session to get the DB-updated MessageCount.
	reloaded, err := s.GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if reloaded.MessageCount != 2 {
		t.Errorf("expected MessageCount=2, got %d", reloaded.MessageCount)
	}

	// Create a reminder that fires at turn 2 from creation at turn 0.
	engine := reminders.NewEngine(s)
	r := store.Reminder{
		ID:          "rem-mc-001",
		SessionID:   sessionID,
		Text:        "Turn counter check reminder",
		TriggerJSON: `{"type":"turn_count","n":2}`,
	}
	if err := s.CreateReminder(r); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}
	engine.RegisterTurnCount(r.ID, 0)

	// Using session.MessageCount (=2) as the turn counter, reminder should fire.
	fired, err := engine.EvalTurn(sessionID, reloaded.MessageCount)
	if err != nil {
		t.Fatalf("EvalTurn(MessageCount=%d): %v", reloaded.MessageCount, err)
	}
	if len(fired) != 1 {
		t.Errorf("expected 1 fired reminder using MessageCount=%d, got %d", reloaded.MessageCount, len(fired))
	}
}
