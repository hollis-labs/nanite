package chatrunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/chat/memstore"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
)

// wired is a runner behind a real chat Service, with Observe set.
type wired struct {
	runner *chatrunner.Runner
	brain  *brain
	svc    *chat.Service
	conv   string
	att    string // the controller attachment's ID
	stream *chat.Stream
	// hold, when set, is called before each Observe with the chat
	// conversation it publishes on.
	hold atomic.Pointer[func(string)]
}

func newWired(t *testing.T, noticeTurns bool) *wired {
	t.Helper()
	return newWiredWith(t, noticeTurns, nil)
}

func newWiredWith(t *testing.T, noticeTurns bool, desk *chatrunner.DeskOptions) *wired {
	t.Helper()
	return newWiredOpts(t, noticeTurns, desk, nil)
}

// newWiredOpts is newWiredWith with tweak applied to the runner's options.
func newWiredOpts(t *testing.T, noticeTurns bool, desk *chatrunner.DeskOptions, tweak func(*chatrunner.Options)) *wired {
	t.Helper()
	dir := t.TempDir()
	definition := "---\nschema_version: \"2\"\ndefinition_id: def:chat-test\nrevision: \"1\"\nname: chat-test\ndescription: Chat test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest instructions.\n"
	profile := "kind: embedded\nlifetime: application\napplication_id: chat-test\nruntime:\n  provider: anthropic\n  model: test-model\n"
	for name, text := range map[string]string{"agent.md": definition, "host.yaml": profile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	w := &wired{brain: &brain{}}
	var svc atomic.Pointer[chat.Service]
	opts := chatrunner.Options{
		Agent: agent.Options{Dir: dir, Provider: w.brain, GlobalSkillsDir: "-", Builtins: []agent.BuiltinGroup{agent.BuiltinSubtasks, agent.BuiltinBackground}},
		Observe: func(ctx context.Context, conversationID string, turn chat.Turn, run chat.Run) error {
			if hold := w.hold.Load(); hold != nil {
				(*hold)(conversationID)
			}
			return svc.Load().Observe(ctx, conversationID, turn, run)
		},
		NoticeTurns: noticeTurns,
		Desk:        desk,
	}
	if tweak != nil {
		tweak(&opts)
	}
	r, err := chatrunner.Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	w.runner = r
	s, err := chat.NewService(chat.Config{Runner: r, Log: streamhub.NewMemoryLog(), Control: memstore.NewControl(),
		Agents: func(context.Context, string) (chat.Agent, error) {
			return chat.Agent{Owner: "tester", Scope: "chat-test", Kind: "embedded"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	svc.Store(s)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	w.svc = s
	view, err := s.EnsureConversation(t.Context(), "desk")
	if err != nil {
		t.Fatal(err)
	}
	w.conv = view.ConversationID
	att, _, err := s.Attach(t.Context(), w.conv, chat.RoleController, chat.StreamCommitted)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := s.Events(t.Context(), w.conv, att.ID, chat.After(0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	w.stream = stream
	return w
}

// until reads the stream until match returns true, failing after a timeout.
func (w *wired) until(t *testing.T, what string, match func(chatstream.Event) bool) []chatstream.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var seen []chatstream.Event
	for {
		ev, err := w.stream.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for %s after %d events: %v", what, len(seen), err)
		}
		seen = append(seen, ev)
		if match(ev) {
			return seen
		}
	}
}

func activityValue(t *testing.T, ev chatstream.Event) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(ev.Value, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A host's background dispatch is published as a run on the wire, and its
// finish reaches the wire as a nanite.notice although no turn is running.
func TestBackgroundDispatchFinishReachesTheWire(t *testing.T) {
	w := newWired(t, false)
	starts := []agent.DispatchStart{{Goal: "gather facts", Variant: "worker", ReturnSpec: json.RawMessage(`"a line"`), Background: true}}
	out, err := w.runner.Dispatch(t.Context(), w.conv, starts)
	if err != nil {
		t.Fatal(err)
	}
	var handle struct{ ID, Kind string }
	if len(out) != 1 || out[0].Err != "" || json.Unmarshal(out[0].Result, &handle) != nil || handle.Kind != "subtask" {
		t.Fatalf("outcomes %+v", out)
	}
	started := false
	seen := w.until(t, "the notice", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbRunStart && (ev.Provider != "" || ev.Model != "") {
			// Only the dispatch run starts here, and it calls no model.
			t.Fatalf("the dispatch run names provider %q, model %q", ev.Provider, ev.Model)
		}
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivitySubagent {
			v := activityValue(t, ev)
			started = started || v["state"] == "start" && v["background"] == true
		}
		return ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice
	})
	notice := activityValue(t, seen[len(seen)-1])
	if !started || notice["kind"] != "subagent" || !strings.Contains(notice["body"].(string), handle.ID) {
		t.Fatalf("started %v, notice %+v", started, notice)
	}
	// The notice's run ends cleanly.
	w.until(t, "the notice run's end", func(ev chatstream.Event) bool {
		if ev.IsTerminal() && ev.Verb != chatstream.VerbRunFinish {
			t.Fatalf("the run ended with %s %s", ev.Verb, ev.Message)
		}
		return ev.IsTerminal()
	})
}

// A host's own notice, posted while no turn runs, can start a notice turn.
func TestNoticeStartsATurnWhenAsked(t *testing.T) {
	w := newWired(t, true)
	// The runner learns which Nanite conversation serves this chat on its
	// first run there.
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	w.until(t, "the first turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PostNotice(t.Context(), "host", "the report is ready"); err != nil {
		t.Fatal(err)
	}
	seen := w.until(t, "the notice turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	answered, announced := false, false
	for _, ev := range seen {
		announced = announced || ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice
		answered = answered || ev.Verb == chatstream.VerbPartEnd && strings.Contains(string(ev.Final), `"ok"`)
	}
	if !answered || !announced || seen[len(seen)-1].Verb != chatstream.VerbRunFinish {
		t.Fatalf("notice turn: answered %v, announced %v, events %+v", answered, announced, seen)
	}
	// The user message is the fixed notice text; the notice itself is in the
	// session slot, once.
	text := w.brain.last()
	if strings.Count(text, "the report is ready") != 1 || !strings.Contains(text, agent.NoticeTurnText) {
		t.Fatalf("the notice turn's request: %s", text)
	}
}

// Notice turns stop after three in a row before a user turn, and a chained
// notice (from work a notice turn started) never starts one.
func TestNoticeTurnsCannotKeepThemselvesGoing(t *testing.T) {
	w := newWired(t, true)
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	w.until(t, "the first turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	turns, announced := 0, 0
	for i := range 5 {
		if err := c.PostNotice(t.Context(), "host", fmt.Sprintf("result %d", i)); err != nil {
			t.Fatal(err)
		}
		seen := w.until(t, "the notice's run end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
		model := false
		for _, ev := range seen {
			model = model || ev.Verb == chatstream.VerbStepStart
		}
		if model {
			turns++
		} else {
			announced++
		}
	}
	if turns != 3 || announced != 2 {
		t.Fatalf("notice turns %d, announce-only runs %d", turns, announced)
	}
	// A user turn lets notice turns start again.
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "thanks"}); err != nil {
		t.Fatal(err)
	}
	w.until(t, "the user turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	if err := c.PostNotice(t.Context(), "host", "after the user"); err != nil {
		t.Fatal(err)
	}
	seen := w.until(t, "a notice turn again", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	again := false
	for _, ev := range seen {
		again = again || ev.Verb == chatstream.VerbStepStart
	}
	if !again {
		t.Fatal("a user turn did not reset the notice turn count")
	}
}

// A user's submit right after a notice gets a clean answer: the notice turn
// does not hold the conversation before the Service adopts it.
func TestSubmitRightAfterANoticeIsClean(t *testing.T) {
	w := newWired(t, true)
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	w.until(t, "the first turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if err := c.PostNotice(t.Context(), "host", "ping"); err != nil {
			t.Fatal(err)
		}
		_, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "are you there"})
		if err != nil && !errors.Is(err, chat.ErrBusy) {
			t.Fatalf("submit after a notice: %v", err)
		}
		// Drain until both have ended, whichever ran.
		ends := 0
		want := 2
		if err != nil {
			want = 1
		}
		w.until(t, "the runs' ends", func(ev chatstream.Event) bool {
			if ev.IsTerminal() {
				if ev.Verb != chatstream.VerbRunFinish {
					t.Fatalf("a run ended with %s %s", ev.Verb, ev.Message)
				}
				ends++
			}
			return ends == want
		})
	}
}

// prime runs one user turn on a chat conversation so the runner knows which
// Nanite conversation serves it.
func (w *wired) prime(t *testing.T, conv string, stream *chat.Stream) {
	t.Helper()
	if _, err := w.svc.Submit(t.Context(), conv, chat.Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for {
		ev, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ev.IsTerminal() {
			return
		}
	}
}

// A notice caused by work a notice turn started (its background subtask
// finishing) is announced, never made into another notice turn.
func TestChainedNoticeStartsNoTurn(t *testing.T) {
	w := newWired(t, true)
	w.brain.mu.Lock()
	w.brain.noticeBackground = "side job"
	w.brain.mu.Unlock()
	w.prime(t, w.conv, w.stream)
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PostNotice(t.Context(), "host", "kick off"); err != nil {
		t.Fatal(err)
	}
	// The notice turn starts the background subtask, then ends.
	w.until(t, "the notice turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	// The subtask's finish is chained: announced in a run with no model step.
	seen := w.until(t, "the chained notice's run end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	notice, step := false, false
	for _, ev := range seen {
		notice = notice || ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice
		step = step || ev.Verb == chatstream.VerbStepStart
	}
	if !notice || step {
		t.Fatalf("chained notice: announced %v, started a turn %v", notice, step)
	}
}

// One conversation's slow Observe does not hold up another's notices.
func TestNoticesArePublishedPerConversation(t *testing.T) {
	w := newWired(t, false)
	other, err := w.svc.EnsureConversation(t.Context(), "desk-2")
	if err != nil {
		t.Fatal(err)
	}
	att, _, err := w.svc.Attach(t.Context(), other.ConversationID, chat.RoleController, chat.StreamCommitted)
	if err != nil {
		t.Fatal(err)
	}
	otherStream, err := w.svc.Events(t.Context(), other.ConversationID, att.ID, chat.After(0))
	if err != nil {
		t.Fatal(err)
	}
	defer otherStream.Close()
	w.prime(t, w.conv, w.stream)
	w.prime(t, other.ConversationID, otherStream)
	release := make(chan struct{})
	defer close(release)
	slow := w.conv
	hold := func(conv string) {
		if conv == slow {
			<-release
		}
	}
	w.hold.Store(&hold)
	a, _ := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	b, _ := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(other.ConversationID))
	if err := a.PostNotice(t.Context(), "host", "for a"); err != nil {
		t.Fatal(err)
	}
	if err := b.PostNotice(t.Context(), "host", "for b"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		ev, err := otherStream.Next(ctx)
		if err != nil {
			t.Fatalf("b's notice was held up by a's: %v", err)
		}
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice {
			return
		}
	}
}

// A notice carried by a turn the runner did not start (the host sent it to
// the agent directly) is still published on the wire.
func TestNoticeOnAnotherRunIsPublished(t *testing.T) {
	w := newWired(t, false)
	w.prime(t, w.conv, w.stream)
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), agent.Input{Text: "long"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PostNotice(t.Context(), "host", "while busy"); err != nil {
		t.Fatal(err)
	}
	carried := false
	for ev := range run.Events() {
		if n, ok := ev.(agent.NoticePosted); ok && n.Body == "while busy" {
			carried = true
			run.Cancel()
		}
	}
	_, _ = run.Wait()
	if !carried {
		t.Fatal("the host's own run did not carry the notice")
	}
	seen := w.until(t, "the notice on the wire", func(ev chatstream.Event) bool {
		return ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice
	})
	if v := activityValue(t, seen[len(seen)-1]); v["body"] != "while busy" {
		t.Fatalf("notice %+v", v)
	}
}

// A conversation whose publisher stopped while idle gets a new one: no notice
// is lost to a publisher that is shutting down.
func TestNoticeAfterAnIdlePublisherStops(t *testing.T) {
	t.Cleanup(chatrunner.SetPublisherIdle(10 * time.Millisecond)) // restored after the runner closes
	w := newWired(t, false)
	w.prime(t, w.conv, w.stream)
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := c.PostNotice(t.Context(), "host", fmt.Sprintf("n%d", i)); err != nil {
			t.Fatal(err)
		}
		seen := w.until(t, "the notice", func(ev chatstream.Event) bool {
			return ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice
		})
		if v := activityValue(t, seen[len(seen)-1]); v["body"] != fmt.Sprintf("n%d", i) {
			t.Fatalf("notice %d: %+v", i, v)
		}
		time.Sleep(30 * time.Millisecond) // let the publisher go idle and stop
	}
}

// The owner may send the next message as soon as a turn's terminal event
// arrives, while the turn is still releasing the conversation: that message
// waits for the release instead of failing busy.
func TestTurnSentAtTheTerminalEventWaitsForTheRelease(t *testing.T) {
	w := newWired(t, false)
	x, err := w.runner.Start(t.Context(), chat.Turn{ConversationID: w.conv, Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	next := make(chan error, 1)
	for ev, err := range x.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if !ev.IsTerminal() {
			continue
		}
		go func() {
			y, err := w.runner.Start(t.Context(), chat.Turn{ConversationID: w.conv, Text: "second"})
			if err == nil {
				for range y.Events() {
				}
			}
			next <- err
		}()
		// The first turn still holds the conversation here, so the next
		// one cannot have started; it must not have failed either.
		select {
		case err := <-next:
			t.Fatalf("the next turn did not wait for the release: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	select {
	case err := <-next:
		if err != nil {
			t.Fatalf("the next turn: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the next turn never started")
	}
}
