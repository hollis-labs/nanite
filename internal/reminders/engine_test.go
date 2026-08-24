package reminders_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/reminders"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// openTestStore creates an in-memory SQLite store for testing.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	s, err := storetest.New(t, context.Background(), f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}

func TestParseTrigger_Time(t *testing.T) {
	raw := `{"type":"time","at":"2026-01-01T00:00:00Z"}`
	trig, err := reminders.ParseTrigger(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trig.Type != reminders.TriggerTypeTime {
		t.Errorf("expected TriggerTypeTime, got %q", trig.Type)
	}
	if trig.At != "2026-01-01T00:00:00Z" {
		t.Errorf("unexpected At: %q", trig.At)
	}
}

func TestParseTrigger_TurnCount(t *testing.T) {
	raw := `{"type":"turn_count","n":5}`
	trig, err := reminders.ParseTrigger(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trig.Type != reminders.TriggerTypeTurnCount {
		t.Errorf("expected TriggerTypeTurnCount, got %q", trig.Type)
	}
	if trig.N != 5 {
		t.Errorf("expected N=5, got %d", trig.N)
	}
}

func TestParseTrigger_Empty(t *testing.T) {
	_, err := reminders.ParseTrigger("")
	if err == nil {
		t.Fatal("expected error for empty trigger")
	}
}

func TestParseTrigger_UnknownType(t *testing.T) {
	_, err := reminders.ParseTrigger(`{"type":"keyword_mention"}`)
	if err == nil {
		t.Fatal("expected error for unknown trigger type")
	}
}

func TestEngine_TurnCount(t *testing.T) {
	s := openTestStore(t)
	eng := reminders.NewEngine(s)
	sessionID := "sess-tc-test"

	trigJSON, _ := json.Marshal(reminders.Trigger{Type: reminders.TriggerTypeTurnCount, N: 3})
	err := s.CreateReminder(context.Background(), store.Reminder{
		ID:          "r1",
		SessionID:   sessionID,
		Text:        "Review the plan",
		TriggerJSON: string(trigJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterTurnCount("r1", 0) // creation turn = 0

	// Turns 1, 2 — should not fire.
	for turn := 1; turn <= 2; turn++ {
		fired, err := eng.EvalTurn(sessionID, turn)
		if err != nil {
			t.Fatal(err)
		}
		if len(fired) != 0 {
			t.Errorf("turn %d: expected no fired reminders, got %d", turn, len(fired))
		}
	}

	// Turn 3 — should fire.
	fired, err := eng.EvalTurn(sessionID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 {
		t.Fatalf("turn 3: expected 1 fired reminder, got %d", len(fired))
	}
	if fired[0].ID != "r1" {
		t.Errorf("unexpected fired reminder ID: %q", fired[0].ID)
	}

	// Turn 4 — already fired, should not fire again.
	fired2, err := eng.EvalTurn(sessionID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired2) != 0 {
		t.Errorf("turn 4: expected no fired reminders after already fired, got %d", len(fired2))
	}
}

func TestEngine_TimeTriggger_Past(t *testing.T) {
	s := openTestStore(t)
	eng := reminders.NewEngine(s)
	sessionID := "sess-time-past"

	// At time in the past — should fire immediately on next EvalTurn.
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	trigJSON, _ := json.Marshal(reminders.Trigger{Type: reminders.TriggerTypeTime, At: past})
	err := s.CreateReminder(context.Background(), store.Reminder{
		ID:          "r-past",
		SessionID:   sessionID,
		Text:        "Past reminder",
		TriggerJSON: string(trigJSON),
	})
	if err != nil {
		t.Fatal(err)
	}

	fired, err := eng.EvalTurn(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 {
		t.Fatalf("expected 1 fired reminder, got %d", len(fired))
	}
}

func TestEngine_TimeTrigger_Future(t *testing.T) {
	s := openTestStore(t)
	eng := reminders.NewEngine(s)
	sessionID := "sess-time-future"

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	trigJSON, _ := json.Marshal(reminders.Trigger{Type: reminders.TriggerTypeTime, At: future})
	err := s.CreateReminder(context.Background(), store.Reminder{
		ID:          "r-future",
		SessionID:   sessionID,
		Text:        "Future reminder",
		TriggerJSON: string(trigJSON),
	})
	if err != nil {
		t.Fatal(err)
	}

	fired, err := eng.EvalTurn(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 0 {
		t.Errorf("expected 0 fired reminders (future), got %d", len(fired))
	}
}

func TestFormatInjection_Empty(t *testing.T) {
	out := reminders.FormatInjection(nil)
	if out != "" {
		t.Errorf("expected empty string for nil fired, got %q", out)
	}
}

func TestFormatInjection_Single(t *testing.T) {
	fired := []store.Reminder{{Text: "Don't forget the ticket"}}
	out := reminders.FormatInjection(fired)
	if out == "" {
		t.Fatal("expected non-empty injection block")
	}
	if out != "<system-reminder>\nReminder: Don't forget the ticket\n</system-reminder>" {
		t.Errorf("unexpected injection format: %q", out)
	}
}

func TestFormatInjection_Multiple(t *testing.T) {
	fired := []store.Reminder{
		{Text: "First reminder"},
		{Text: "Second reminder"},
	}
	out := reminders.FormatInjection(fired)
	for _, want := range []string{"First reminder", "Second reminder", "<system-reminder>", "</system-reminder>"} {
		if !containsStr(out, want) {
			t.Errorf("expected %q in injection output, got: %q", want, out)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
