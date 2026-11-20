package chatrunner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/desk"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
)

// restartHost runs one agent directory through several runner processes,
// collecting the desk's notices without a chat Service in between.
type restartHost struct {
	dir string
	mu  sync.Mutex
	got []string
}

const restartChat = "restart-chat"

func newRestartHost(t *testing.T) *restartHost {
	t.Helper()
	dir := t.TempDir()
	definition := "---\nschema_version: \"2\"\ndefinition_id: def:chat-test\nrevision: \"1\"\nname: chat-test\ndescription: Chat test.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\nTest instructions.\n"
	profile := "kind: embedded\nlifetime: application\napplication_id: chat-test\nruntime:\n  provider: anthropic\n  model: test-model\n"
	for name, text := range map[string]string{"agent.md": definition, "host.yaml": profile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &restartHost{dir: dir}
}

// open starts a runner process on the directory.
func (h *restartHost) open(t *testing.T, b *brain, d chatrunner.DeskOptions) *chatrunner.Runner {
	t.Helper()
	r, err := chatrunner.Open(t.Context(), chatrunner.Options{
		Agent: agent.Options{Dir: h.dir, Provider: b, GlobalSkillsDir: "-", Builtins: []agent.BuiltinGroup{agent.BuiltinSubtasks, agent.BuiltinBackground}},
		Observe: func(_ context.Context, _ string, _ chat.Turn, run chat.Run) error {
			for ev, err := range run.Events() {
				if err != nil {
					return err
				}
				if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice {
					v := activityValue(t, ev)
					if v["kind"] == chatrunner.NoticeKindDesk {
						body, _ := v["body"].(string)
						h.mu.Lock()
						h.got = append(h.got, body)
						h.mu.Unlock()
					}
				}
			}
			return nil
		},
		Desk: &d,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// turn runs a user turn to its end and returns its events.
func turn(t *testing.T, r *chatrunner.Runner, text string) []chatstream.Event {
	t.Helper()
	x, err := r.Start(t.Context(), chat.Turn{ConversationID: restartChat, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	var evs []chatstream.Event
	for ev, err := range x.Events() {
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	return evs
}

func (h *restartHost) notices() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.got, "\n---\n")
}

// waitNotices waits until the desk's notices hold every text in has.
func (h *restartHost) waitNotices(t *testing.T, what string, has ...string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		all := h.notices()
		missing := false
		for _, s := range has {
			if !strings.Contains(all, s) {
				missing = true
			}
		}
		if !missing {
			return all
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; notices so far:\n%s", what, all)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFor polls cond for up to 15s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// once checks each text appears exactly once in the notices, after a pause
// that would let a duplicate through.
func (h *restartHost) once(t *testing.T, texts ...string) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	all := h.notices()
	for _, s := range texts {
		if n := strings.Count(all, s); n != 1 {
			t.Fatalf("%q delivered %d times:\n%s", s, n, all)
		}
	}
}

func workStates(r *chatrunner.Runner) []desk.WorkState {
	var out []desk.WorkState
	for _, topic := range r.Desk().Ledger().List(nil) {
		for _, w := range topic.Work {
			out = append(out, w.State)
		}
	}
	return out
}

// Jobs a restart stopped are started again by the next process, and their
// results reach the owner once. The ledger the desk owns comes back too.
func TestDeskRestartsJobsARestartStopped(t *testing.T) {
	h := newRestartHost(t)
	plan := desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "slow:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "slow:beta-result"},
	}}
	opts := chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}

	first := h.open(t, &brain{hang: true}, opts)
	turn(t, first, twoSteps)
	waitFor(t, "both jobs running", func() bool {
		st := workStates(first)
		return len(st) == 2 && st[0] == desk.WorkRunning && st[1] == desk.WorkRunning
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.notices(); got != "" {
		t.Fatalf("the first process delivered: %s", got)
	}

	second := h.open(t, &brain{}, opts)
	if topics := second.Desk().Ledger().List(nil); len(topics) != 1 {
		t.Fatalf("the ledger did not come back: %+v", topics)
	}
	h.waitNotices(t, "the restarted jobs' results", "alpha-result", "beta-result")
	h.once(t, ": alpha-result", ": beta-result")
	waitFor(t, "the work recorded done", func() bool {
		st := workStates(second)
		return len(st) == 2 && st[0] == desk.WorkDone && st[1] == desk.WorkDone
	})
}

// A suggested plan survives a restart and can be accepted afterwards.
func TestDeskSuggestedPlanSurvivesARestart(t *testing.T) {
	h := newRestartHost(t)
	plan := desk.Plan{Confidence: 0.5, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	opts := chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeSuggest, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}

	first := h.open(t, &brain{}, opts)
	var id string
	for _, ev := range turn(t, first, twoSteps) {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityDeskPlan {
			id, _ = activityValue(t, ev)["id"].(string)
		}
	}
	if id == "" {
		t.Fatal("no suggestion")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := h.open(t, &brain{}, opts)
	if err := second.Desk().AcceptPlan(t.Context(), restartChat, id, nil); err != nil {
		t.Fatalf("accept after a restart: %v", err)
	}
	h.waitNotices(t, "the accepted plan's results", "alpha-result", "beta-result")
	h.once(t, ": alpha-result", ": beta-result")
	if err := second.Desk().AcceptPlan(t.Context(), restartChat, id, nil); err == nil {
		t.Fatal("a plan was accepted twice")
	}
}

// What the delivery editor held when the process stopped is delivered by
// the next one, once.
func TestDeskHeldDeliverySurvivesARestart(t *testing.T) {
	h := newRestartHost(t)
	plan := desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	opts := chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}}, Policy: mergePolicy(100 * time.Millisecond)}

	first := h.open(t, &brain{}, opts)
	first.Desk().SetOwnerTyping(restartChat, true) // the owner is typing: hold everything
	turn(t, first, twoSteps)
	waitFor(t, "both results held", func() bool {
		n := 0
		for _, c := range first.Desk().Held(restartChat) {
			n += c
		}
		return n == 2
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.notices(); got != "" {
		t.Fatalf("the first process delivered while the owner typed: %s", got)
	}

	h.open(t, &brain{}, opts)
	h.waitNotices(t, "the held results", "alpha-result", "beta-result")
	h.once(t, ": alpha-result", ": beta-result")
}
