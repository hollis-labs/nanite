package chatrunner_test

import (
	"context"
	"encoding/json"
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

const (
	resumeFixtureEnv = "NANITE_CHATRUNNER_RESUME_FIXTURE_DIR"
	// resumeFixtureDeskEnv makes the fixture plan its message into two
	// background steps that stall, so they are lost with it.
	resumeFixtureDeskEnv = "NANITE_CHATRUNNER_RESUME_FIXTURE_DESK"
)

// stallingPlan is the fixture's plan: two research steps that stall while the
// brain hangs and report their results otherwise.
var stallingPlan = desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
	{ID: "a", Kind: desk.KindResearch, Goal: "slow:alpha-result"},
	{ID: "b", Kind: desk.KindResearch, Goal: "slow:beta-result"},
}}

// messagePlanner plans every message as stallingPlan and records the messages.
type messagePlanner struct {
	mu   sync.Mutex
	seen []string
}

func (p *messagePlanner) Decompose(_ context.Context, req desk.Request) (desk.Plan, error) {
	p.mu.Lock()
	p.seen = append(p.seen, req.Message)
	p.mu.Unlock()
	plan := stallingPlan
	plan.Message, plan.Source = req.Message, "model"
	return plan, nil
}

func (p *messagePlanner) messages() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

// withDesk configures a front desk planning with planner, delivering
// within 200ms.
func withDesk(planner desk.Decomposer, tweak func(*chatrunner.Options)) func(*chatrunner.Options) {
	return func(o *chatrunner.Options) {
		o.Desk = &chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: planner}, Policy: mergePolicy(200 * time.Millisecond)}
		if tweak != nil {
			tweak(o)
		}
	}
}

// resumeScript is the owner's message the killed process was answering. Its
// list makes the front desk's screen treat it as a task.
const resumeScript = "resumable:recovered\n" + twoSteps

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
	w.att = att.ID
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
	withSteps := os.Getenv(resumeFixtureDeskEnv) != ""
	var tweak func(*chatrunner.Options)
	if withSteps {
		tweak = withDesk(&messagePlanner{}, nil)
	}
	w := newWiredIn(t, dir, true, tweak)
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: resumeScript}); err != nil {
		t.Fatal(err)
	}
	// The root turn is waiting for the model (a subtask's request may
	// quote the message too, so look for it as the message answered).
	for w.brain.answers(resumeScript) == 0 || withSteps && (!w.brain.saw("slow:alpha-result") || !w.brain.saw("slow:beta-result") || savedJobs(t, w) < 2) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "turn-running"), nil, 0600); err != nil { //nolint:gosec // G703: the parent test's temp dir
		t.Fatal(err)
	}
	<-t.Context().Done() // killed here
}

// savedJobs counts the jobs the desk's saved state links to its steps.
func savedJobs(t *testing.T, w *wired) int {
	states, err := w.runner.Agent().ConversationHostStates(t.Context(), "desk")
	if err != nil {
		return 0
	}
	n := 0
	for _, raw := range states {
		var saved struct {
			Plans []struct {
				Refs []json.RawMessage `json:"refs"`
			} `json:"plans"`
		}
		if json.Unmarshal([]byte(raw), &saved) == nil {
			for _, p := range saved.Plans {
				n += len(p.Refs)
			}
		}
	}
	return n
}

// crashedDir returns an agent directory whose process was killed while a
// turn on chat conv_1 waited for the model.
func crashedDir(t *testing.T) string {
	t.Helper()
	return crashed(t, false)
}

// crashedDeskDir is crashedDir where the killed turn's message was planned
// into two background steps, lost with the process.
func crashedDeskDir(t *testing.T) string {
	t.Helper()
	return crashed(t, true)
}

func crashed(t *testing.T, withSteps bool) string {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeCrashFixture$") // #nosec G204 G702 -- Runs the current test binary with fixed test flags.
	cmd.Env = append(os.Environ(), resumeFixtureEnv+"="+dir)
	if withSteps {
		cmd.Env = append(cmd.Env, resumeFixtureDeskEnv+"=1")
	}
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
				last = scriptText(m)
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
	planner := &messagePlanner{}
	w := newWiredIn(t, dir, false, withDesk(planner, func(o *chatrunner.Options) { o.ResumeInterrupted = true }))
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
	// A later message is planned as usual; the resumed one never was.
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: twoSteps}); err != nil {
		t.Fatal(err)
	}
	w.until(t, "the next turn", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	if got := planner.messages(); !slices.Equal(got, []string{twoSteps}) {
		t.Fatalf("the desk planned %q; want only the later message", got)
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

// readUntil reads the stream until done reports true, giving each event to
// see first.
func (w *wired) readUntil(t *testing.T, what string, see func(chatstream.Event), done func() bool) {
	t.Helper()
	w.until(t, what, func(ev chatstream.Event) bool {
		see(ev)
		return done()
	})
}

// deskBody is a desk notice's body, "" for any other event.
func deskBody(t *testing.T, ev chatstream.Event) string {
	if ev.Verb != chatstream.VerbActivity || ev.Kind != chatrunner.ActivityNotice {
		return ""
	}
	v := activityValue(t, ev)
	if v["kind"] != chatrunner.NoticeKindDesk {
		return ""
	}
	body, _ := v["body"].(string)
	return body
}

// The desk's work on a chat waits while its turn is interrupted; the owner
// is told once, and the work starts when the turn is resumed, discarded,
// or its hold runs out.
func TestDeskWorkWaitsForTheInterruptedTurn(t *testing.T) {
	for _, release := range []string{"resume", "discard", "timeout"} {
		t.Run(release, func(t *testing.T) {
			dir := crashedDeskDir(t)
			planner := &messagePlanner{}
			w := newWiredIn(t, dir, false, withDesk(planner, func(o *chatrunner.Options) {
				if release == "timeout" {
					o.InterruptedHold = 500 * time.Millisecond
				}
			}))
			if _, err := w.runner.RecoverTurns(t.Context()); err != nil {
				t.Fatal(err)
			}
			var bodies []string
			var disposition map[string]any
			see := func(ev chatstream.Event) {
				if body := deskBody(t, ev); body != "" {
					bodies = append(bodies, body)
				}
				if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityInterrupted {
					if v := activityValue(t, ev); v["disposition"] != nil {
						disposition = v
					}
				}
			}
			has := func(text string) bool { return strings.Contains(strings.Join(bodies, "\n"), text) }
			w.readUntil(t, "the owner told the work waits", see, func() bool { return has("Work is waiting") })
			if has("alpha-result") || has("beta-result") {
				t.Fatal("the steps ran while the turn was interrupted")
			}
			switch release {
			case "resume":
				if err := w.runner.Resume(t.Context(), w.conv); err != nil {
					t.Fatal(err)
				}
			case "discard":
				if err := w.runner.Discard(t.Context(), w.conv); err != nil {
					t.Fatal(err)
				}
			}
			w.readUntil(t, "the steps' results", see, func() bool {
				done := has("alpha-result") && has("beta-result")
				if release == "timeout" {
					done = done && disposition != nil
				}
				return done
			})
			if n := strings.Count(strings.Join(bodies, "\n"), "Work is waiting"); n != 1 {
				t.Fatalf("the owner was told %d times", n)
			}
			if release == "timeout" && (disposition["disposition"] != "discarded" || disposition["reason"] != "timeout") {
				t.Fatalf("hold ran out with %v", disposition)
			}
			if got := planner.messages(); len(got) != 0 {
				t.Fatalf("the desk planned %q", got)
			}
		})
	}
}

// Record, an exchange the host answered itself, discards the interrupted
// turn, says so, and leaves the chat ready for new turns.
func TestRecordDiscardsTheInterruptedTurn(t *testing.T) {
	dir := crashedDir(t)
	w := newWiredIn(t, dir, false, nil)
	if err := w.runner.Record(t.Context(), chat.Turn{ConversationID: w.conv, Text: "what time is it"}, "noon"); err != nil {
		t.Fatal(err)
	}
	var disposition map[string]any
	w.until(t, "the discard", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityInterrupted {
			disposition = activityValue(t, ev)
		}
		return disposition != nil
	})
	if disposition["disposition"] != "discarded" || disposition["reason"] != "new message" {
		t.Fatalf("record discarded with %v", disposition)
	}
	if turn, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || turn != nil {
		t.Fatalf("still interrupted: %+v, %v", turn, err)
	}
}

// A message sent while the resumed turn runs does not disturb it: the
// resumed turn answers once and the message is handled after it or refused.
func TestMessageDuringAResume(t *testing.T) {
	dir := crashedDir(t)
	w := newWiredIn(t, dir, false, nil)
	if err := w.runner.Resume(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"})
	if !w.answered(t, "the resumed turn") {
		t.Fatal("the resumed turn did not answer the stored message")
	}
	if err == nil {
		w.until(t, "the new turn", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	}
	if n := w.brain.answers(resumeScript); n != 1 {
		t.Fatalf("the model answered the stored message %d times", n)
	}
}

// Canceling a resumed turn aborts it, and the chat takes new turns.
func TestCancelAResume(t *testing.T) {
	dir := crashedDir(t)
	w := newWiredIn(t, dir, true, nil) // the resumed turn stalls
	if err := w.runner.Resume(t.Context(), w.conv); err != nil {
		t.Fatal(err)
	}
	var runID string
	w.until(t, "the resumed run", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbRunStart {
			runID = ev.RunID
		}
		return runID != "" && w.brain.answers(resumeScript) == 1
	})
	if _, err := w.svc.AcquireLease(t.Context(), w.conv, w.att); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Cancel(t.Context(), w.conv, runID, w.att); err != nil {
		t.Fatal(err)
	}
	events := w.until(t, "the abort", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	if last := events[len(events)-1]; last.Verb != chatstream.VerbRunAbort {
		t.Fatalf("the cancelled resume ended %+v", last)
	}
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	events = w.until(t, "the next turn", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	if last := events[len(events)-1]; last.Verb != chatstream.VerbRunFinish {
		t.Fatalf("the next turn ended %+v", last)
	}
}

// A host turn (an errand or an automatic turn) does not replace the owner's
// interrupted turn: it is refused as busy, so the host can retry it, and the
// interrupted turn is still there for the owner.
func TestHostTurnWaitsForTheInterruptedTurn(t *testing.T) {
	dir := crashedDir(t)
	w := newWiredIn(t, dir, false, nil)
	turn, err := w.runner.Interrupted(t.Context(), w.conv)
	if err != nil || turn == nil {
		t.Fatalf("interrupted: %+v, %v", turn, err)
	}
	for _, meta := range []string{`{"errand":"late-answer"}`, `{"auto":true}`} {
		_, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "from the host", Meta: json.RawMessage(meta)})
		if !errors.Is(err, agent.ErrBusy) {
			t.Fatalf("%s: want busy, got %v", meta, err)
		}
	}
	if held, err := w.runner.Interrupted(t.Context(), w.conv); err != nil || held == nil || held.ID != turn.ID {
		t.Fatalf("the host turn discarded the owner's turn: %+v, %v", held, err)
	}
}
