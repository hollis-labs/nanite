package chatrunner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/chat/memstore"
	"github.com/hollis-labs/libs/agent/desk"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	streamhub "github.com/hollis-labs/libs/ui/ui-go/streamhub"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
)

const resumeFixtureEnv = "NANITE_CHATRUNNER_RESUME_FIXTURE_DIR"

// resumeScript is the owner's message the killed process was answering.
const resumeScript = "resumable:recovered"

// newWiredIn is newWiredOpts on a given agent directory, with chat IDs
// that are the same in every process, so a restarted runner serves the
// same chat conversation.
func newWiredIn(t *testing.T, dir string, hang bool, tweak func(*chatrunner.Options)) *wired {
	t.Helper()
	definition := "---\nschema_version: \"2\"\ndefinition_id: def:chat-test\nrevision: \"1\"\nname: chat-test\ndescription: Chat test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest instructions.\n"
	profile := "kind: embedded\nlifetime: application\napplication_id: chat-test\nruntime:\n  provider: anthropic\n  model: test-model\n"
	for name, text := range map[string]string{"agent.md": definition, "host.yaml": profile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil { //nolint:gosec // G703: a test temp dir
			t.Fatal(err)
		}
	}
	w := &wired{brain: &brain{hang: hang}}
	var svcMu sync.Mutex
	var svc *chat.Service
	opts := chatrunner.Options{
		Agent: agent.Options{Dir: dir, Provider: w.brain, GlobalSkillsDir: "-", Builtins: []agent.BuiltinGroup{agent.BuiltinSubtasks, agent.BuiltinBackground}},
		Observe: func(ctx context.Context, conversationID string, turn chat.Turn, run chat.Run) error {
			svcMu.Lock()
			s := svc
			svcMu.Unlock()
			if s == nil {
				return errors.New("no chat service yet")
			}
			return s.Observe(ctx, conversationID, turn, run)
		},
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
	var idMu sync.Mutex
	ids := map[string]int{}
	s, err := chat.NewService(chat.Config{Runner: r, Log: streamhub.NewMemoryLog(), Control: memstore.NewControl(),
		NewID: func(prefix string) string {
			idMu.Lock()
			defer idMu.Unlock()
			ids[prefix]++
			return fmt.Sprintf("%s_%d", prefix, ids[prefix])
		},
		Agents: func(context.Context, string) (chat.Agent, error) {
			return chat.Agent{Owner: "tester", Scope: "chat-test", Kind: "embedded"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	svcMu.Lock()
	svc = s
	svcMu.Unlock()
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

// TestResumeCrashFixture is the process the resume tests kill mid-turn. It
// runs only when started by them.
func TestResumeCrashFixture(t *testing.T) {
	dir := os.Getenv(resumeFixtureEnv)
	if dir == "" {
		t.Skip("started only by the resume tests")
	}
	w := newWiredIn(t, dir, true, nil)
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: resumeScript}); err != nil {
		t.Fatal(err)
	}
	for !w.brain.saw(resumeScript) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "turn-running"), nil, 0600); err != nil { //nolint:gosec // G703: the parent test's temp dir
		t.Fatal(err)
	}
	<-t.Context().Done() // killed here
}

// crashedDir returns an agent directory whose process was killed while a
// turn on chat conv_1 waited for the model.
func crashedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeCrashFixture$") // #nosec G204 G702 -- Runs the current test binary with fixed test flags.
	cmd.Env = append(os.Environ(), resumeFixtureEnv+"="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for {
		if _, err := os.Stat(filepath.Join(dir, "turn-running")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("the fixture never started its turn")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exited *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exited) {
		t.Fatalf("kill wait: %v", err)
	}
	return dir
}

// answered reads the stream to the end of a run and reports whether it
// carried the resumed answer.
func (w *wired) answered(t *testing.T, what string) bool {
	t.Helper()
	events := w.until(t, what, func(ev chatstream.Event) bool { return ev.IsTerminal() })
	return slices.ContainsFunc(events, func(ev chatstream.Event) bool {
		return strings.Contains(ev.Text+string(ev.Final), "recovered")
	})
}

func (b *brain) answers(text string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, req := range b.requests {
		// The request answers its last user message; earlier ones are
		// history.
		last := ""
		for _, m := range req.Messages {
			if m.Role == "user" && !isToolResult(m) {
				last = messageText(m)
			}
		}
		if last == text {
			n++
		}
	}
	return n
}

// With ResumeInterrupted, a turn killed mid-run is resumed once from its
// stored message, and the front desk does not plan it again.
func TestRestartResumesTheInterruptedTurn(t *testing.T) {
	dir := crashedDir(t)
	planner := &recordingPlanner{}
	w := newWiredIn(t, dir, false, func(o *chatrunner.Options) {
		o.ResumeInterrupted = true
		o.Desk = &chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: planner}}
	})
	if got, err := w.runner.InterruptedChats(t.Context()); err != nil || !slices.Equal(got, []string{w.conv}) {
		t.Fatalf("interrupted chats %v, %v; want [%s]", got, err, w.conv)
	}
	handled, err := w.runner.RecoverTurns(t.Context())
	if err != nil || !slices.Equal(handled, []string{w.conv}) {
		t.Fatalf("recover: %v, %v", handled, err)
	}
	if !w.answered(t, "the resumed turn") {
		t.Fatal("the resumed turn did not answer the stored message")
	}
	if n := w.brain.answers(resumeScript); n != 1 {
		t.Fatalf("the model answered the stored message %d times", n)
	}
	if got := planner.seen(); len(got) != 0 {
		t.Fatalf("the desk planned the resumed turn: %v", got)
	}
	if turn, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || turn != nil {
		t.Fatalf("still interrupted: %+v, %v", turn, err)
	}
	if handled, err := w.runner.RecoverTurns(t.Context()); err != nil || len(handled) != 0 {
		t.Fatalf("a second recovery found %v, %v", handled, err)
	}
}

// Without ResumeInterrupted, the owner is offered the turn, and Resume
// completes it.
func TestRestartOffersTheInterruptedTurn(t *testing.T) {
	dir := crashedDir(t)
	w := newWiredIn(t, dir, false, nil)
	turn, err := w.runner.Interrupted(t.Context(), w.conv)
	if err != nil || turn == nil {
		t.Fatalf("interrupted: %+v, %v", turn, err)
	}
	if _, err := w.runner.RecoverTurns(t.Context()); err != nil {
		t.Fatal(err)
	}
	var offered map[string]any
	w.until(t, "the offer", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityInterrupted {
			offered = activityValue(t, ev)
		}
		return ev.IsTerminal()
	})
	if offered["turn"] != turn.ID {
		t.Fatalf("offer %v, want turn %s", offered, turn.ID)
	}
	if n := w.brain.answers(resumeScript); n != 0 {
		t.Fatal("the turn ran before the owner chose")
	}
	if err := w.runner.Resume(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	if !w.answered(t, "the resumed turn") {
		t.Fatal("the resumed turn did not answer the stored message")
	}
	if err := w.runner.Resume(t.Context(), w.conv); err == nil {
		t.Fatal("resumed a turn twice")
	}
}

// Discard abandons the turn; a new message is then answered as usual. A
// new message alone also discards it: the owner has moved on.
func TestDiscardTheInterruptedTurn(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			dir := crashedDir(t)
			w := newWiredIn(t, dir, false, nil)
			if explicit {
				if err := w.runner.Discard(t.Context(), w.conv); err != nil {
					t.Fatal(err)
				}
				if turn, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || turn != nil {
					t.Fatalf("still interrupted: %+v, %v", turn, err)
				}
			}
			if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"}); err != nil {
				t.Fatal(err)
			}
			events := w.until(t, "the new turn", func(ev chatstream.Event) bool { return ev.IsTerminal() })
			if last := events[len(events)-1]; last.Verb != chatstream.VerbRunFinish {
				t.Fatalf("the new turn ended %+v", last)
			}
			if n := w.brain.answers(resumeScript); n != 0 {
				t.Fatal("the discarded turn ran")
			}
			if turn, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || turn != nil {
				t.Fatalf("still interrupted: %+v, %v", turn, err)
			}
		})
	}
}
