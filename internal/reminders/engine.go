// Package reminders implements the deterministic trigger engine for agent-set
// reminders (J11, CW-20260426-0009).
//
// # Design
//
// The engine is rule-based only — no LLM judge. v1 supports two trigger types:
//
//   - time-based: fires when the wall clock is at or past the trigger.At time.
//   - turn_count-based: fires when the session turn counter reaches the trigger.N
//     turns since the reminder was created.
//
// Keyword-mention triggers are deferred to a follow-up
// (followups_j11_keyword_mention_trigger) because they require classifier
// integration.
//
// # Injection
//
// When a trigger fires, the reminder text is injected into the next turn's
// SlotUserContext as a <system-reminder> block (Anthropic-style). The block
// rides in the existing 2000-token SlotUserContext budget (per J10/J11 contract).
// There is no UI toast in v1 (captured as followups_j11_reminder_toast_ui).
//
// # Inspector visibility
//
// The engine records RemindersSet and RemindersFired into the inspector snapshot
// via inspector.Service.RecordReminders so the I1 dev-mode panel can surface them.
package reminders

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// TriggerType identifies the v1 trigger condition.
type TriggerType string

const (
	TriggerTypeTime      TriggerType = "time"
	TriggerTypeTurnCount TriggerType = "turn_count"
)

// Trigger is the decoded representation of a reminder trigger JSON blob.
type Trigger struct {
	// Type is "time" or "turn_count".
	Type TriggerType `json:"type"`
	// At is the RFC3339 fire-time for time-based triggers.
	At string `json:"at,omitempty"`
	// N is the turn count delta for turn_count triggers.
	// The reminder fires when the session's turn counter >= CreationTurn+N.
	N int `json:"n,omitempty"`
}

// ParseTrigger decodes a reminder's TriggerJSON into a Trigger.
// Returns an error when the JSON is malformed or the type is unrecognised.
func ParseTrigger(raw string) (Trigger, error) {
	var t Trigger
	if raw == "" || raw == "{}" {
		return t, fmt.Errorf("reminders: empty trigger JSON")
	}
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return t, fmt.Errorf("reminders: parse trigger JSON: %w", err)
	}
	switch t.Type {
	case TriggerTypeTime, TriggerTypeTurnCount:
	default:
		return t, fmt.Errorf("reminders: unknown trigger type %q", t.Type)
	}
	return t, nil
}

// Engine evaluates unfired reminders on each turn and returns the ones that
// should fire. It is designed for per-session use: one Engine per session
// (or shared via the Store + session ID).
//
// Engine is safe for concurrent use.
type Engine struct {
	store *store.Store

	mu sync.Mutex
	// turnCounters tracks the creation-turn for each reminder, keyed by
	// reminderID. Only turn_count triggers use this.
	turnCounters map[string]int
}

// NewEngine creates a new reminder Engine backed by the given store.
func NewEngine(s *store.Store) *Engine {
	return &Engine{
		store:        s,
		turnCounters: make(map[string]int),
	}
}

// RegisterTurnCount records the creation turn for a newly created reminder so
// turn_count triggers can compute the fire turn correctly. This should be called
// immediately after CreateReminder with the current session turn counter.
func (e *Engine) RegisterTurnCount(reminderID string, creationTurn int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.turnCounters[reminderID] = creationTurn
}

// EvalTurn evaluates all unfired reminders for the session and returns the
// ones whose trigger conditions are met. It marks fired reminders in the DB
// and removes them from the in-memory counter map.
//
// currentTurn is the monotonic session turn counter at the start of this turn.
// Callers must increment the turn counter before calling EvalTurn.
func (e *Engine) EvalTurn(sessionID string, currentTurn int) ([]store.Reminder, error) {
	unfired, err := e.store.ListUnfiredReminders(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID)
	if err != nil {
		return nil, fmt.Errorf("reminders engine: %w", err)
	}

	var fired []store.Reminder
	now := time.Now()

	for _, r := range unfired {
		trig, err := ParseTrigger(r.TriggerJSON)
		if err != nil {
			slog.Warn("reminders engine: skip malformed trigger",
				"reminder_id", r.ID, "err", err)
			continue
		}
		shouldFire := false
		switch trig.Type {
		case TriggerTypeTime:
			if trig.At != "" {
				fireAt, parseErr := time.Parse(time.RFC3339, trig.At)
				if parseErr != nil {
					slog.Warn("reminders engine: skip bad time trigger",
						"reminder_id", r.ID, "at", trig.At, "err", parseErr)
					continue
				}
				shouldFire = !now.Before(fireAt)
			}
		case TriggerTypeTurnCount:
			e.mu.Lock()
			creationTurn, ok := e.turnCounters[r.ID]
			e.mu.Unlock()
			if !ok {
				// Fallback: no in-memory record (e.g. after restart). Use turn
				// 0 as creation turn so n turns from session start fire.
				creationTurn = 0
			}
			shouldFire = currentTurn >= creationTurn+trig.N
		}

		if !shouldFire {
			continue
		}

		if markErr := e.store.MarkReminderFired(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, r.ID); markErr != nil {
			slog.Warn("reminders engine: failed to mark reminder fired",
				"reminder_id", r.ID, "err", markErr)
			continue
		}
		e.mu.Lock()
		delete(e.turnCounters, r.ID)
		e.mu.Unlock()
		fired = append(fired, r)
	}
	return fired, nil
}

// FormatInjection renders fired reminders as a <system-reminder> block for
// injection into SlotUserContext. Returns an empty string when fired is empty.
//
// The injection format follows the Anthropic system-reminder pattern:
//
//	<system-reminder>
//	Reminder: text
//	</system-reminder>
func FormatInjection(fired []store.Reminder) string {
	if len(fired) == 0 {
		return ""
	}
	out := "<system-reminder>\n"
	for _, r := range fired {
		out += "Reminder: " + r.Text + "\n"
	}
	out += "</system-reminder>"
	return out
}
