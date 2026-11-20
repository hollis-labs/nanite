package chatrunner

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/desk"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// Hold the dispatch response after the child starts. Its completion must
// wait for the response's job-to-plan registration before classification.
func TestDeskCompletionBeforeDispatchReturns(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	r := openRunner(t, func(o *Options) {
		o.Agent.Provider = fake.New(fake.Response{Events: []llmtypes.StreamEvent{
			{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "ret-1", Name: "return_result", Input: map[string]any{"status": "ok", "result": "early result"}}},
			{Type: llmtypes.EventDone},
		}}, fake.Text("done"))
		o.Agent.Builtins = []agent.BuiltinGroup{agent.BuiltinSubtasks, agent.BuiltinBackground}
		o.Agent.TraceSink = func(_ context.Context, rec agent.TraceRecord) {
			if rec.Source == "host" {
				close(entered)
				<-release
			}
		}
	})
	defer unblock()
	d, err := newDesk(r, DeskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.stop()
	if _, err := r.conversation(t.Context(), "early-result"); err != nil {
		t.Fatal(err)
	}
	notices := r.agent.SubscribeNotices(t.Context())
	plan, err := d.newRun("early-result", desk.Plan{Confidence: 1, Items: []desk.PlanItem{{ID: "a", Kind: desk.KindResearch, Goal: "report a result"}}})
	if err != nil {
		t.Fatal(err)
	}
	dispatched := make(chan error, 1)
	go func() { dispatched <- d.advance(t.Context(), "early-result") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch never reached trace")
	}
	var notice agent.NoticePosted
	select {
	case notice = <-notices:
	case <-time.After(5 * time.Second):
		t.Fatal("child never completed")
	}
	if notice.Kind != subtaskJobKind {
		t.Fatalf("notice %+v", notice)
	}
	classified := make(chan struct{})
	go func() { d.notice(t.Context(), notice); close(classified) }()
	premature := false
	select {
	case <-classified:
		premature = true
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	if err := <-dispatched; err != nil {
		t.Fatal(err)
	}
	select {
	case <-classified:
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not settle")
	}
	topic, err := d.ledger.Get(plan.topic)
	if err != nil {
		t.Fatal(err)
	}
	if premature || len(topic.Work) != 1 || topic.Work[0].State != desk.WorkDone || topic.Work[0].Result != "early result" {
		t.Fatalf("completion classified before registration=%v; work=%+v", premature, topic.Work)
	}
}

// A release blocked on persistence owns its place in the delivery order.
// Another pump or explicit Flush must not remove the next release yet.
func TestDeskSerializesReleaseThroughPublish(t *testing.T) {
	for _, flush := range []bool{false, true} {
		t.Run(fmt.Sprintf("flush=%v", flush), func(t *testing.T) {
			bodies := make(chan string, 2)
			r := openRunner(t, func(o *Options) {
				o.Observe = func(_ context.Context, _ string, _ chat.Turn, run chat.Run) error {
					bodies <- run.(*noticeRun).notice.Body
					return nil
				}
			})
			if _, err := r.conversation(t.Context(), "ordered"); err != nil {
				t.Fatal(err)
			}
			d, err := newDesk(r, DeskOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer d.stop()
			composed := make(chan string, 2)
			ed, err := desk.NewEditor(desk.DefaultPolicy(), desk.WithComposer(func(items []desk.Queued, _ map[desk.TopicID]string) string {
				text := items[0].Item.Text
				composed <- text
				return text
			}))
			if err != nil {
				t.Fatal(err)
			}
			d.chat("ordered").editor = ed
			ed.Submit(desk.Item{ID: "first", Text: "first", Kind: desk.ItemQuestion}, desk.State{})
			d.saveMu.Lock()
			var once sync.Once
			unlock := func() { once.Do(d.saveMu.Unlock) }
			defer unlock()
			firstDone := make(chan struct{})
			go func() { d.pump("ordered"); close(firstDone) }()
			<-composed
			// Submit takes the editor lock, so the first release has completed.
			ed.Submit(desk.Item{ID: "second", Text: "second", Kind: desk.ItemQuestion}, desk.State{})
			secondDone := make(chan struct{})
			go func() {
				if flush {
					d.Flush("ordered")
				} else {
					d.pump("ordered")
				}
				close(secondDone)
			}()
			premature := false
			select {
			case <-composed:
				premature = true
			case <-time.After(100 * time.Millisecond):
			}
			unlock()
			<-firstDone
			<-secondDone
			for _, want := range []string{"first", "second"} {
				select {
				case got := <-bodies:
					if got != want {
						t.Errorf("published %q, want %q", got, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("release was not published")
				}
			}
			if premature {
				t.Fatal("second release overtook the first release's persistence")
			}
		})
	}
}
