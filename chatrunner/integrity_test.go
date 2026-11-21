package chatrunner_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/chat"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
	"github.com/hollis-labs/nanite/internal/store"
)

// corruptRunningTurn answers a call nobody made inside the turn the killed
// process left running.
func corruptRunningTurn(t *testing.T, dir string) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	running, err := db.RunningTurns(t.Context())
	if err != nil || len(running) != 1 {
		t.Fatalf("running turns %v, %v", running, err)
	}
	j, err := db.OpenJournal(t.Context(), running[0].ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := j.AppendMessage(store.Message{ID: store.NewID(now), TurnID: running[0].TurnID, Role: "tool", CreatedAt: now,
		Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "nobody", Content: "x"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
}

// With ResumeInterrupted, a turn whose conversation fails integrity checks
// is never resumed: RecoverTurns reports the refusal and offers the turn
// for discard only, Resume refuses synchronously, and Discard still works.
func TestRestartNeverResumesThroughAnUnsafeFinding(t *testing.T) {
	dir := crashedDir(t)
	corruptRunningTurn(t, dir)
	w := newWiredIn(t, dir, false, func(o *chatrunner.Options) { o.ResumeInterrupted = true })
	handled, err := w.runner.RecoverTurns(t.Context())
	if !errors.Is(err, agent.ErrIntegrity) || len(handled) != 0 {
		t.Fatalf("recover: %v, %v", handled, err)
	}
	var offered map[string]any
	w.until(t, "the discard-only offer", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityInterrupted {
			offered = activityValue(t, ev)
		}
		return ev.IsTerminal()
	})
	actions, _ := offered["actions"].([]any)
	if offered["integrity"] != "blocked" || !slices.Equal(actions, []any{"discard", "reset"}) {
		t.Fatalf("offer %v", offered)
	}
	var refused *agent.IntegrityError
	if err := w.runner.Resume(t.Context(), w.conv); !errors.As(err, &refused) || refused.Report.Unsafe()[0].Code != "orphan_tool_result" {
		t.Fatalf("resume: %v", err)
	}
	if n := w.brain.answers(resumeScript); n != 0 {
		t.Fatalf("the model was asked %d times", n)
	}
	if err := w.runner.Discard(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	if turn, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || turn != nil {
		t.Fatalf("still interrupted: %+v, %v", turn, err)
	}
}

// tearRunningTurn appends an incomplete record to the transcript of the turn
// the killed process left running, as a crash mid-write would.
func tearRunningTurn(t *testing.T, dir string) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	running, err := db.RunningTurns(t.Context())
	if err != nil || len(running) != 1 {
		t.Fatalf("running turns %v, %v", running, err)
	}
	_ = db.Close()
	segment := filepath.Join(dir, ".nanite", "transcripts", running[0].ConversationID, "00000000000000000001.jsonl")
	f, err := os.OpenFile(segment, os.O_WRONLY|os.O_APPEND, 0) // #nosec G304 -- The fixture's own transcript segment.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assis`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// offerSeen reads the chat until an interrupted-turn activity arrives and
// returns its value.
func offerSeen(t *testing.T, w *wired, what string) map[string]any {
	t.Helper()
	var offered map[string]any
	w.until(t, what, func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityInterrupted {
			if v := activityValue(t, ev); v["actions"] != nil { // an offer, not a "discarded" note
				offered = v
				return true
			}
		}
		return false
	})
	return offered
}

// In the default mode the owner is offered a blocked turn without resume.
// After the discard the chat is still blocked and is offered a reset; the
// reset moves the chat to a new conversation, which takes turns and is kept
// across a restart, while the old one stays on disk.
func TestBlockedChatIsResetToANewConversation(t *testing.T) {
	dir := crashedDir(t)
	corruptRunningTurn(t, dir)
	w := newWiredIn(t, dir, false, nil)
	if handled, err := w.runner.RecoverTurns(t.Context()); err != nil || !slices.Equal(handled, []string{w.conv}) {
		t.Fatalf("recover: %v, %v", handled, err)
	}
	offered := offerSeen(t, w, "the blocked offer")
	actions, _ := offered["actions"].([]any)
	if offered["integrity"] != "blocked" || offered["turn"] == nil || !slices.Equal(actions, []any{"discard", "reset"}) {
		t.Fatalf("offer %v", offered)
	}
	if _, err := w.runner.ResetChat(t.Context(), w.conv); !errors.Is(err, chatrunner.ErrResetInterrupted) {
		t.Fatalf("reset with an interrupted turn: %v", err)
	}
	if err := w.runner.Discard(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	offered = offerSeen(t, w, "the reset offer after the discard")
	actions, _ = offered["actions"].([]any)
	if offered["integrity"] != "blocked" || offered["turn"] != nil || !slices.Equal(actions, []any{"reset"}) {
		t.Fatalf("offer after discard %v", offered)
	}

	previous := w.runner.ConversationOf(w.conv)
	next, err := w.runner.ResetChat(t.Context(), w.conv)
	if err != nil {
		t.Fatal(err)
	}
	if next == previous || w.runner.ConversationOf(w.conv) != next {
		t.Fatalf("reset to %s from %s; serving %s", next, previous, w.runner.ConversationOf(w.conv))
	}
	var reset map[string]any
	w.until(t, "the reset activity", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityChatReset {
			reset = activityValue(t, ev)
			return true
		}
		return false
	})
	if reset["previous_conversation"] != previous || reset["conversation"] != next || reset["reason"] != "integrity" {
		t.Fatalf("reset activity %v", reset)
	}
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello again"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new conversation to answer", func() bool { return w.brain.answers("hello again") == 1 })
	if _, err := os.Stat(filepath.Join(dir, ".nanite", "transcripts", previous)); err != nil {
		t.Fatalf("the previous conversation is gone: %v", err)
	}

	if err := w.runner.Close(); err != nil {
		t.Fatal(err)
	}
	w = newWiredIn(t, dir, false, nil)
	if got := w.runner.ConversationOf(w.conv); got != next {
		t.Fatalf("after a restart the chat is served by %s, want %s", got, next)
	}
	if handled, err := w.runner.RecoverTurns(t.Context()); err != nil || len(handled) != 0 {
		t.Fatalf("recover after the reset: %v, %v", handled, err)
	}
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "after the restart"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "an answer after the restart", func() bool { return w.brain.answers("after the restart") == 1 })
}

// A torn tail leaves its turn running, so there is nothing to resume or
// discard: the owner is still told, with a reset-only offer.
func TestTornRunningChatIsOfferedAReset(t *testing.T) {
	dir := crashedDir(t)
	tearRunningTurn(t, dir)
	w := newWiredIn(t, dir, false, nil)
	if handled, err := w.runner.RecoverTurns(t.Context()); err != nil || !slices.Equal(handled, []string{w.conv}) {
		t.Fatalf("recover: %v, %v", handled, err)
	}
	offered := offerSeen(t, w, "the reset offer")
	actions, _ := offered["actions"].([]any)
	if offered["integrity"] != "blocked" || offered["turn"] != nil || !slices.Equal(actions, []any{"reset"}) {
		t.Fatalf("offer %v", offered)
	}
	if _, err := w.runner.ResetChat(t.Context(), w.conv); err != nil {
		t.Fatalf("reset of a torn chat: %v", err)
	}
}

// When the hold runs out on a turn whose transcript was torn after it was
// interrupted, the discard cannot be written: the hold parks and offers a
// reset instead of retrying for ever.
func TestHoldParksWhenTheDiscardCannotBeWritten(t *testing.T) {
	dir := crashedDir(t)
	first := newWiredIn(t, dir, false, nil) // recovery marks the turn interrupted
	if err := first.runner.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var interrupted string
	for _, id := range []string{chatrunner.ConversationID(first.conv)} {
		if turn, err := db.Interrupted(t.Context(), id); err == nil && turn != nil {
			interrupted = id
		}
	}
	_ = db.Close()
	if interrupted == "" {
		t.Fatal("the fixture's turn was not interrupted")
	}
	f, err := os.OpenFile(filepath.Join(dir, ".nanite", "transcripts", interrupted, "00000000000000000001.jsonl"), os.O_WRONLY|os.O_APPEND, 0) // #nosec G304 -- The fixture's own transcript segment.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"sys`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w := newWiredIn(t, dir, false, func(o *chatrunner.Options) { o.InterruptedHold = 300 * time.Millisecond })
	offered := offerSeen(t, w, "the parked hold's reset offer")
	actions, _ := offered["actions"].([]any)
	if offered["integrity"] != "blocked" || !slices.Equal(actions, []any{"reset"}) {
		t.Fatalf("offer %v", offered)
	}
	if turn, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || turn == nil {
		t.Fatalf("the turn was discarded through a torn tail: %+v, %v", turn, err)
	}
	// The discard cannot be written, and the refusal says so.
	var refused *agent.IntegrityError
	if err := w.runner.Discard(t.Context(), w.conv); !errors.As(err, &refused) || refused.Interrupted ||
		strings.Contains(err.Error(), "can still be discarded") {
		t.Fatalf("discard through a torn tail: %v", err)
	}
	// The reset is the way on: the turn stays with the old conversation.
	next, err := w.runner.ResetChat(t.Context(), w.conv)
	if err != nil {
		t.Fatalf("reset with an interrupted turn the transcript cannot close: %v", err)
	}
	if chats, err := w.runner.InterruptedChats(t.Context()); err != nil || len(chats) != 0 {
		t.Fatalf("the old conversation's turn is still listed: %v, %v", chats, err)
	}
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "after the reset"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "an answer in the new conversation", func() bool { return w.brain.answers("after the reset") == 1 })
	if w.runner.ConversationOf(w.conv) != next {
		t.Fatalf("serving %s, want %s", w.runner.ConversationOf(w.conv), next)
	}
}

// A message arriving while a chat is reset either runs before the reset
// (which then refuses as busy) or is refused: no run is ever left on the
// conversation the chat no longer uses.
func TestResetNeverStrandsARunOnTheOldConversation(t *testing.T) {
	dir := t.TempDir()
	w := newWiredIn(t, dir, false, nil)
	for i := range 150 {
		view, err := w.svc.EnsureConversation(t.Context(), fmt.Sprintf("c%d", i))
		if err != nil {
			t.Fatal(err)
		}
		chatID := view.ConversationID
		var wg sync.WaitGroup
		var resetErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = w.svc.Submit(t.Context(), chatID, chat.Turn{Text: "long"}) }()
		go func() { defer wg.Done(); _, resetErr = w.runner.ResetChat(t.Context(), chatID) }()
		wg.Wait()
		if resetErr != nil {
			if !errors.Is(resetErr, agent.ErrBusy) {
				t.Fatalf("reset %d: %v", i, resetErr)
			}
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, ".nanite", "transcripts", chatrunner.ConversationID(chatID), "*.jsonl"))
		for _, m := range matches {
			raw, _ := os.ReadFile(m) // #nosec G304 -- A transcript under the test's temporary directory.
			if strings.Contains(string(raw), `"long"`) {
				t.Fatalf("try %d: a turn ran on the old conversation after the reset succeeded", i)
			}
		}
	}
}

// A reset refuses while a run holds the chat.
func TestResetRefusesWhileARunHoldsTheChat(t *testing.T) {
	w := newWiredIn(t, t.TempDir(), false, nil)
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "long"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to reach the model", func() bool { return w.brain.answers("long") == 1 })
	if _, err := w.runner.ResetChat(t.Context(), w.conv); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("reset during a run: %v", err)
	}
}

// The desk's saved state on the conversation a chat used before a reset is
// skipped quietly on restart, not reported as unreadable.
func TestDeskStateOfTheOldConversationIsSkipped(t *testing.T) {
	dir := crashedDeskDir(t)
	planner := &messagePlanner{}
	w := newWiredIn(t, dir, false, withDesk(planner, nil))
	if err := w.runner.Discard(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	if _, err := w.runner.ResetChat(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	if err := w.runner.Close(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(lockedLog{&logs, &mu}, nil))
	newWiredIn(t, dir, false, withDesk(planner, func(o *chatrunner.Options) { o.Agent.Logger = logger }))
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(logs.String(), "saved desk state unreadable") {
		t.Fatalf("the old conversation's desk state was reported:\n%s", logs.String())
	}
}

type lockedLog struct {
	b  *strings.Builder
	mu *sync.Mutex
}

func (l lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
