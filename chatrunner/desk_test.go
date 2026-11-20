package chatrunner_test

import (
	"context"
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

// fixedPlanner returns one plan for every message the screen passes.
type fixedPlanner struct{ plan desk.Plan }

func (f fixedPlanner) Decompose(_ context.Context, req desk.Request) (desk.Plan, error) {
	p := f.plan
	p.Message, p.Source = req.Message, "model"
	return p, nil
}

const twoSteps = "1. research alpha\n2. research beta"

func researchPlan() desk.Plan {
	return desk.Plan{Confidence: 0.95, TopicTitle: "Alpha and beta", Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
		{ID: "c", Kind: desk.KindAnalysis, Goal: "report:gamma-result", DependsOn: []string{"a", "b"}},
	}}
}

// mergePolicy merges results for a short window, so results that arrive
// together are released together.
func mergePolicy(window time.Duration) *desk.Policy {
	p := desk.DefaultPolicy()
	p.MergeWindow = window
	return &p
}

// deskNotices reads desk notices until, between them, they contain every
// text in has, and returns their bodies joined by "\n---\n".
func (w *wired) deskNotices(t *testing.T, what string, has ...string) string {
	t.Helper()
	var bodies []string
	w.until(t, what, func(ev chatstream.Event) bool {
		if ev.Verb != chatstream.VerbActivity || ev.Kind != chatrunner.ActivityNotice {
			return false
		}
		v := activityValue(t, ev)
		if v["kind"] != chatrunner.NoticeKindDesk {
			return false // a running turn carries its own notices
		}
		body, _ := v["body"].(string)
		bodies = append(bodies, body)
		all := strings.Join(bodies, "\n---\n")
		for _, h := range has {
			if !strings.Contains(all, h) {
				return false
			}
		}
		return true
	})
	return strings.Join(bodies, "\n---\n")
}

func (w *wired) deskNotice(t *testing.T, what string, has ...string) string {
	t.Helper()
	var body string
	w.until(t, what, func(ev chatstream.Event) bool {
		if ev.Verb != chatstream.VerbActivity || ev.Kind != chatrunner.ActivityNotice {
			return false
		}
		v := activityValue(t, ev)
		if v["kind"] != chatrunner.NoticeKindDesk {
			return false // a running turn carries its own notices
		}
		body, _ = v["body"].(string)
		for _, h := range has {
			if !strings.Contains(body, h) {
				return false
			}
		}
		return true
	})
	return body
}

// A dispatched plan's first wave starts before the turn, its results come
// back as one composed desk notice, the dependent step starts with their
// results, and the ledger records it all.
func TestDeskDispatchesAPlanAndComposesItsResults(t *testing.T) {
	var plans []desk.Result
	var mu sync.Mutex
	w := newWiredWith(t, false, &chatrunner.DeskOptions{
		Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{researchPlan()}},
		Policy: mergePolicy(time.Second),
		OnPlan: func(_ string, r desk.Result) { mu.Lock(); plans = append(plans, r); mu.Unlock() },
	})
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: twoSteps}); err != nil {
		t.Fatal(err)
	}
	var planEvent map[string]any
	w.until(t, "the turn's end", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityDeskPlan {
			planEvent = activityValue(t, ev)
		}
		return ev.IsTerminal()
	})
	if planEvent == nil || planEvent["disposition"] != "dispatch" || !strings.Contains(planEvent["card"].(string), "3 items in 2 steps") {
		t.Fatalf("plan activity %+v", planEvent)
	}
	if !w.brain.saw("The front desk started this plan") {
		t.Fatal("the chat agent was not told about the plan")
	}
	// Every result reaches the owner through the desk, grouped under the
	// topic when several go out together.
	all := w.deskNotices(t, "every result", "alpha-result", "beta-result", "gamma-result")
	if strings.Count(all, "Alpha and beta:") == 0 && strings.Count(all, "\n---\n") < 2 {
		t.Fatalf("results neither grouped nor separate:\n%s", all)
	}
	if !w.brain.saw("Result of an earlier step (a):\nalpha-result") {
		t.Fatal("the dependent step did not get the earlier results")
	}
	topics := w.runner.Desk().Ledger().List(nil)
	if len(topics) != 1 || topics[0].Title != "Alpha and beta" || len(topics[0].Work) != 3 || len(topics[0].InFlight()) != 0 {
		t.Fatalf("ledger %+v", topics)
	}
	for _, work := range topics[0].Work {
		if work.State != desk.WorkDone || work.Result == "" {
			t.Fatalf("work %+v", work)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(plans) != 1 || plans[0].Disposition != desk.Dispatch {
		t.Fatalf("OnPlan saw %+v", plans)
	}
}

// A suggested plan starts nothing until the owner accepts it; accepting it
// is recorded as a correction and runs it.
func TestDeskSuggestsAPlanAndRunsItWhenAccepted(t *testing.T) {
	var corrections []desk.Correction
	var mu sync.Mutex
	plan := desk.Plan{Confidence: 0.5, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	w := newWiredWith(t, false, &chatrunner.DeskOptions{
		Intake:       desk.Intake{Mode: desk.IntakeSuggest, Planner: fixedPlanner{plan}},
		Policy:       mergePolicy(time.Second),
		OnCorrection: func(_ string, c desk.Correction) { mu.Lock(); corrections = append(corrections, c); mu.Unlock() },
	})
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: twoSteps}); err != nil {
		t.Fatal(err)
	}
	var id string
	w.until(t, "the turn's end", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityDeskPlan {
			v := activityValue(t, ev)
			if v["disposition"] != "suggest" {
				t.Fatalf("plan activity %+v", v)
			}
			id, _ = v["id"].(string)
		}
		return ev.IsTerminal()
	})
	if id == "" || len(w.runner.Desk().Ledger().List(nil)) != 0 {
		t.Fatal("a suggested plan started")
	}
	if !w.brain.saw("The front desk suggested this plan") {
		t.Fatal("the chat agent was not told about the suggestion")
	}
	if err := w.runner.Desk().AcceptPlan(t.Context(), w.conv, "nope", nil); err == nil {
		t.Fatal("accepted an unknown plan")
	}
	edited := plan
	edited.Items = plan.Items[:1]
	if err := w.runner.Desk().AcceptPlan(t.Context(), w.conv, id, &edited); err != nil {
		t.Fatal(err)
	}
	w.deskNotice(t, "the accepted plan's result", "alpha-result")
	mu.Lock()
	defer mu.Unlock()
	if len(corrections) != 1 || !corrections[0].Accepted() || !corrections[0].Edited || len(corrections[0].Final.Items) != 1 {
		t.Fatalf("corrections %+v", corrections)
	}
	if err := w.runner.Desk().DeclinePlan(w.conv, id); err == nil {
		t.Fatal("declined a plan already accepted")
	}
}

// With the desk on and intake off, notices that arrive together while no
// turn runs are released as one composed message.
func TestDeskMergesNoticesIntoOneMessage(t *testing.T) {
	w := newWiredWith(t, false, &chatrunner.DeskOptions{Policy: mergePolicy(500 * time.Millisecond)})
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	w.until(t, "the first turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"first report ready", "second report ready"} {
		if err := c.PostNotice(t.Context(), "host", body); err != nil {
			t.Fatal(err)
		}
	}
	body := w.deskNotices(t, "the notices", "first report ready", "second report ready")
	if strings.Contains(body, "\n---\n") {
		t.Fatalf("notices were not merged:\n%s", body)
	}
	if held := w.runner.Desk().Held(w.conv); held[desk.Merge] != 0 {
		t.Fatalf("still held %v", held)
	}
}

// A plan's question goes to the owner at once; the owner's next message
// answers it, and the step that waited on it starts with the answer.
func TestDeskQuestionIsAnsweredByTheNextMessage(t *testing.T) {
	plan := desk.Plan{Confidence: 0.9, Items: []desk.PlanItem{
		{ID: "q", Kind: desk.KindQuestion, Goal: "Which host should I use?"},
		{ID: "r", Kind: desk.KindResearch, Goal: "report:host-chosen", DependsOn: []string{"q"}},
	}}
	w := newWiredWith(t, false, &chatrunner.DeskOptions{
		Intake: desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}},
		Policy: mergePolicy(100 * time.Millisecond),
	})
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: twoSteps}); err != nil {
		t.Fatal(err)
	}
	w.deskNotice(t, "the question", "Question: Which host should I use?")
	topic := w.runner.Desk().Ledger().List(nil)[0]
	if len(topic.OpenQuestions()) != 1 {
		t.Fatalf("question not recorded: %+v", topic)
	}
	w.until(t, "the first turn's end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "use build-2"}); err != nil {
		t.Fatal(err)
	}
	w.deskNotice(t, "the dependent step's result", "host-chosen")
	if !w.brain.saw("Result of an earlier step (q):\nuse build-2") {
		t.Fatal("the dependent step did not get the owner's answer")
	}
	topic, _ = w.runner.Desk().Ledger().Get(topic.ID)
	if len(topic.OpenQuestions()) != 0 || topic.Questions[0].Answer != "use build-2" {
		t.Fatalf("question not answered: %+v", topic.Questions)
	}
}

// The desk publishes through Observe, so it needs it.
func TestDeskRequiresObserve(t *testing.T) {
	_, err := chatrunner.Open(t.Context(), chatrunner.Options{Agent: agent.Options{Dir: t.TempDir()}, Desk: &chatrunner.DeskOptions{}})
	if err == nil || !strings.Contains(err.Error(), "Observe") {
		t.Fatalf("open without Observe: %v", err)
	}
}

// A plan accepted while a turn runs starts when the turn ends.
func TestDeskAcceptWhileATurnRuns(t *testing.T) {
	plan := desk.Plan{Confidence: 0.5, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	w := newWiredWith(t, false, &chatrunner.DeskOptions{
		Intake: desk.Intake{Mode: desk.IntakeSuggest, Planner: fixedPlanner{plan}},
		Policy: mergePolicy(200 * time.Millisecond),
	})
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: twoSteps}); err != nil {
		t.Fatal(err)
	}
	var id string
	w.until(t, "the suggestion", func(ev chatstream.Event) bool {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityDeskPlan {
			id, _ = activityValue(t, ev)["id"].(string)
		}
		return ev.IsTerminal()
	})
	// A turn the runner runs holds the conversation.
	x, err := w.runner.Start(t.Context(), chat.Turn{ConversationID: w.conv, Text: "long"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.runner.Desk().AcceptPlan(t.Context(), w.conv, id, nil); err != nil {
		t.Fatalf("accept while a turn runs: %v", err)
	}
	topic := w.runner.Desk().Ledger().List(nil)[0]
	if len(topic.InFlight()) != 2 || topic.Work[0].State != desk.WorkPlanned {
		t.Fatalf("work started under a running turn: %+v", topic.Work)
	}
	x.Cancel()
	for range x.Events() {
	}
	w.deskNotices(t, "the accepted plan's results", "alpha-result", "beta-result")
}

// An item whose dispatch keeps failing is given up after a few attempts
// and the owner is told.
func TestDeskGivesUpOnAnItemThatCannotStart(t *testing.T) {
	plan := desk.Plan{Confidence: 0.95, Items: []desk.PlanItem{
		{ID: "a", Kind: desk.KindResearch, Goal: "report:alpha-result"},
		{ID: "b", Kind: desk.KindResearch, Goal: "report:beta-result"},
	}}
	w := newWiredWith(t, false, &chatrunner.DeskOptions{
		Intake:   desk.Intake{Mode: desk.IntakeAuto, Planner: fixedPlanner{plan}},
		Policy:   mergePolicy(200 * time.Millisecond),
		Variants: map[desk.WorkKind]string{desk.KindResearch: "no-such-variant"},
	})
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: twoSteps}); err != nil {
		t.Fatal(err)
	}
	w.deskNotices(t, "the failures", "Failed: report:alpha-result: could not start", "Failed: report:beta-result: could not start")
	topic := w.runner.Desk().Ledger().List(nil)[0]
	for _, work := range topic.Work {
		if work.State != desk.WorkFailed {
			t.Fatalf("work %+v", work)
		}
	}
}
