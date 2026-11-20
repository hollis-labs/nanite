package chatrunner_test

import (
	"context"
	"encoding/json"
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
	stream *chat.Stream
}

func newWired(t *testing.T, noticeTurns bool) *wired {
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
	r, err := chatrunner.Open(t.Context(), chatrunner.Options{
		Agent: agent.Options{Dir: dir, Provider: w.brain, GlobalSkillsDir: "-", Builtins: []agent.BuiltinGroup{agent.BuiltinSubtasks}},
		Observe: func(ctx context.Context, conversationID string, turn chat.Turn, run chat.Run) error {
			return svc.Load().Observe(ctx, conversationID, turn, run)
		},
		NoticeTurns: noticeTurns,
	})
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
	if text := w.brain.last(); !strings.Contains(text, "[notice host] the report is ready") {
		t.Fatalf("the notice turn's request: %s", text)
	}
}
