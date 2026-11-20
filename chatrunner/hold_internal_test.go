package chatrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/desk"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// interruptedRunner seeds the same durable records a crashed process leaves,
// without a model call or a timer running before the test is ready.
func interruptedRunner(t *testing.T) (*Runner, *agent.Conversation, string) {
	t.Helper()
	r := openRunner(t)
	const chatID = "held-chat"
	c, err := r.conversation(t.Context(), chatID)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), filepath.Join(r.opts.Agent.Dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	j, err := db.OpenJournal(t.Context(), ConversationID(chatID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	if err := j.StartTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	if err := j.AppendMessage(store.Message{ID: turn.UserMessageID, TurnID: turn.ID, Role: "user", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "continue"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := j.InterruptTurn(turn.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if c.Interrupted() == nil {
		t.Fatal("fixture is not interrupted")
	}
	r.opts.InterruptedHold = time.Hour
	return r, c, chatID
}

func expiredHold(t *testing.T, r *Runner, chatID string) string {
	t.Helper()
	r.startHold(t.Context(), chatID)
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.holds[chatID]
	if h == nil {
		t.Fatal("missing hold")
	}
	h.timer.Stop()
	h.deadline = time.Now().Add(-time.Second)
	return h.turn
}

func waitDiscarded(t *testing.T, c *agent.Conversation) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for c.Interrupted() != nil {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("hold never retried its discard")
		}
	}
}

func TestHoldRearmedAfterResumeFailure(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		name := "rejected"
		if cancel {
			name = "cancelled-before-start"
		}
		t.Run(name, func(t *testing.T) {
			r, c, chatID := interruptedRunner(t)
			turnID := expiredHold(t, r, chatID)
			rejected := errors.New("observe rejected")
			r.opts.Observe = func(_ context.Context, _ string, _ chat.Turn, run chat.Run) error {
				// The hold expires while Resume owns the claim. It must retry when
				// that claim is released without starting the resumed turn.
				r.expireHold(chatID, turnID)
				if !cancel {
					return rejected
				}
				run.Cancel()
				for range run.Events() {
				}
				return nil
			}
			err := r.Resume(t.Context(), chatID)
			if !cancel && !errors.Is(err, rejected) || cancel && err != nil {
				t.Fatalf("Resume: %v", err)
			}
			waitDiscarded(t, c)
		})
	}
}

func TestHoldRearmedAfterDiscardFailure(t *testing.T) {
	r, c, chatID := interruptedRunner(t)
	turnID := expiredHold(t, r, chatID)
	// A directory where the journal expects its lock file makes Discard fail
	// before writing anything, while reads of the interrupted turn still work.
	lock := filepath.Join(r.opts.Agent.Dir, ".nanite", "transcripts", ConversationID(chatID), ".writer.lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	r.expireHold(chatID, turnID)
	if c.Interrupted() == nil {
		t.Fatal("discard unexpectedly succeeded")
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	waitDiscarded(t, c)
}

func TestHoldTimestampBelongsToInterruptedTurn(t *testing.T) {
	for _, match := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale-turn", true: "same-turn"}[match], func(t *testing.T) {
			r, c, chatID := interruptedRunner(t)
			r.opts.InterruptedHold = -1
			id := c.Interrupted().ID
			savedID := "older-turn"
			if match {
				savedID = id
			}
			old := time.Now().Add(-time.Hour).UTC()
			raw, _ := json.Marshal(map[string]any{"turn": savedID, "since": old})
			if err := c.SetHostState(t.Context(), holdHostState, string(raw)); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			r.startHold(t.Context(), chatID)
			got, err := c.HostState(t.Context(), holdHostState)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Turn  string    `json:"turn"`
				Since time.Time `json:"since"`
			}
			if err := json.Unmarshal([]byte(got), &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Turn != id || match && !saved.Since.Equal(old) || !match && saved.Since.Before(before) {
				t.Fatalf("hold stamp %+v", saved)
			}
		})
	}
}

func TestConcurrentOwnerAndTimeoutDiscard(t *testing.T) {
	r, c, chatID := interruptedRunner(t)
	turnID := expiredHold(t, r, chatID)
	announcements := make(chan struct{}, 12)
	r.opts.Observe = func(_ context.Context, _ string, _ chat.Turn, run chat.Run) error {
		for ev := range run.Events() {
			if ev.Kind == ActivityInterrupted && bytes.Contains(ev.Value, []byte(`"discarded"`)) {
				announcements <- struct{}{}
			}
		}
		return nil
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 10 {
		wg.Go(func() {
			<-start
			if err := r.Discard(t.Context(), chatID); err != nil {
				t.Errorf("owner discard: %v", err)
			}
		})
	}
	wg.Go(func() { <-start; r.expireHold(chatID, turnID) })
	close(start)
	wg.Wait()
	select {
	case <-announcements:
	case <-time.After(5 * time.Second):
		t.Fatal("no discard announcement")
	}
	select {
	case <-announcements:
		t.Fatal("duplicate discard announcement")
	case <-time.After(100 * time.Millisecond):
	}
	if err := c.Discard(t.Context()); !errors.Is(err, agent.ErrNoInterrupted) {
		t.Fatalf("already discarded: %v", err)
	}
}

func TestHoldEndWithdrawsWaitingItem(t *testing.T) {
	for _, release := range []string{"resume", "discard", "timeout"} {
		t.Run(release, func(t *testing.T) {
			r, c, chatID := interruptedRunner(t)
			turnID := expiredHold(t, r, chatID)
			d, err := newDesk(r, DeskOptions{})
			if err != nil {
				t.Fatal(err)
			}
			r.desk = d
			ed := d.chat(chatID).editor
			ed.Submit(desk.Item{ID: "interrupted-wait-" + turnID, Kind: desk.ItemNotice, Text: "Work is waiting", Prefer: desk.Digest}, desk.State{})
			if len(ed.Pending()) != 1 {
				t.Fatal("waiting item not queued")
			}
			switch release {
			case "resume":
				r.opts.Observe = func(_ context.Context, _ string, _ chat.Turn, run chat.Run) error {
					for _, err := range run.Events() {
						if err != nil {
							return err
						}
					}
					return nil
				}
				if err := r.Resume(t.Context(), chatID); err != nil {
					t.Fatal(err)
				}
			case "discard":
				if err := r.Discard(t.Context(), chatID); err != nil {
					t.Fatal(err)
				}
			case "timeout":
				r.expireHold(chatID, turnID)
			}
			if len(ed.Pending()) != 0 {
				t.Fatal("obsolete waiting item remains queued")
			}
			if c.Interrupted() != nil {
				t.Fatal("hold did not end")
			}
		})
	}
}

func TestLateWaitingNoticeCannotReturnAfterHoldEnds(t *testing.T) {
	r, _, chatID := interruptedRunner(t)
	expiredHold(t, r, chatID)
	d, err := newDesk(r, DeskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.desk = d
	h := r.holds[chatID]
	r.endHold(chatID)
	// Model a tellWaiting goroutine scheduled before the hold ended but
	// not run until afterwards. It must not put the withdrawn item back.
	r.tellHeld(chatID, h)
	if len(d.chat(chatID).editor.Pending()) != 0 {
		t.Fatal("late waiting notice was queued")
	}
}

func TestHoldRetryBackoffCapsAndLogsOncePerLevel(t *testing.T) {
	r, _, chatID := interruptedRunner(t)
	expiredHold(t, r, chatID)
	var logs bytes.Buffer
	r.opts.Agent.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	want := 100 * time.Millisecond
	for range 15 {
		r.rearmHold(chatID, errors.New("store unavailable"))
		r.mu.Lock()
		h := r.holds[chatID]
		h.timer.Stop()
		got := h.retryDelay
		r.mu.Unlock()
		if got != want {
			t.Fatalf("retry delay %s, want %s", got, want)
		}
		want = min(want*2, 30*time.Second)
	}
	if got := strings.Count(logs.String(), "retry interrupted hold"); got != 10 {
		t.Fatalf("logged %d times, want 10 levels", got)
	}
}

func TestLegacyHoldStampStartsFreshOnce(t *testing.T) {
	r, c, chatID := interruptedRunner(t)
	r.opts.InterruptedHold = -1
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if err := c.SetHostState(t.Context(), holdHostState, old); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	r.startHold(t.Context(), chatID)
	raw, err := c.HostState(t.Context(), holdHostState)
	if err != nil {
		t.Fatal(err)
	}
	var stamp struct {
		Turn  string
		Since time.Time
	}
	if err := json.Unmarshal([]byte(raw), &stamp); err != nil {
		t.Fatal(err)
	}
	if stamp.Turn != c.Interrupted().ID || stamp.Since.Before(before) {
		t.Fatalf("legacy stamp not reset: %+v", stamp)
	}
	r.startHold(t.Context(), chatID)
	again, err := c.HostState(t.Context(), holdHostState)
	if err != nil || again != raw {
		t.Fatalf("hold reset twice: %q %v", again, err)
	}
}
