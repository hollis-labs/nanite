package chatrunner_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/desk"
	"github.com/hollis-labs/libs/agent/steer"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
)

// recordingPlanner records the mode each request carried.
type recordingPlanner struct {
	mu    sync.Mutex
	modes []string
}

func (p *recordingPlanner) Decompose(_ context.Context, req desk.Request) (desk.Plan, error) {
	p.mu.Lock()
	p.modes = append(p.modes, req.Mode)
	p.mu.Unlock()
	return desk.Plan{Message: req.Message, Source: "model", Confidence: 0.1}, nil
}

func (p *recordingPlanner) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.modes)
}

// steeredRunner wires a runner with a front desk and steering: a "/stop"
// halt, a "ping" reflex whose action goes to the host, and a notes slot
// chosen when the host says notes is in front.
func steeredRunner(t *testing.T, planner desk.Decomposer, seen *[]agent.Steered, mu *sync.Mutex) *wired {
	t.Helper()
	reg, err := steer.NewRegistry(steer.DefaultPresets()...)
	if err != nil {
		t.Fatal(err)
	}
	vocab := steer.NewVocabulary()
	if err := vocab.Register("ring", nil); err != nil {
		t.Fatal(err)
	}
	reflexes := []steer.Reflex{
		{Name: "stop", TriggerKind: steer.TriggerText, Trigger: json.RawMessage(`{"pattern":"^/stop"}`), Enabled: true,
			ActionKind: steer.ActionHalt, Action: json.RawMessage(`{"message":"Stopped."}`)},
		{Name: "ping", TriggerKind: steer.TriggerText, Trigger: json.RawMessage(`{"pattern":"ping"}`), Enabled: true,
			ActionKind: "ring", Action: json.RawMessage(`{"bell":"front"}`)},
	}
	notes := steer.Slot{Name: "notes", About: "notes", Views: []string{"notes"}}
	return newWiredOpts(t, false, &chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeShadow, Planner: planner}}, func(o *chatrunner.Options) {
		o.Agent.Steering = &agent.Steering{Registry: reg, Vocabulary: vocab,
			Slots:    func() []steer.Slot { return []steer.Slot{notes} },
			Reflexes: func() []steer.Reflex { return reflexes }}
		o.Situation = func(chat.Turn) steer.Situation { return steer.Situation{Focused: &steer.Focus{View: "notes"}} }
		o.OnSteered = func(chatID string, s agent.Steered) {
			mu.Lock()
			*seen = append(*seen, s)
			mu.Unlock()
		}
	})
}

func (w *wired) turn(t *testing.T, text string) []chatstream.Event {
	t.Helper()
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: text}); err != nil {
		t.Fatal(err)
	}
	return w.until(t, "the end of "+text, func(ev chatstream.Event) bool { return ev.IsTerminal() })
}

// A message a reflex halts is answered by the reflex, and the desk does not
// plan it. The host's situation reaches steering, and OnSteered gets the
// reflex actions, which stay off the wire.
func TestHaltedMessageSkipsTheDeskAndHostSeesActions(t *testing.T) {
	var seen []agent.Steered
	var mu sync.Mutex
	planner := &recordingPlanner{}
	w := steeredRunner(t, planner, &seen, &mu)

	w.turn(t, "/stop\n"+twoSteps)
	if got := planner.seen(); len(got) != 0 {
		t.Fatalf("the desk planned a halted message: %v", got)
	}
	if w.brain.saw("/stop\n") {
		t.Fatal("the model saw a halted message")
	}

	events := w.turn(t, "ping\n"+twoSteps)
	if got := planner.seen(); len(got) != 1 {
		t.Fatalf("the desk did not plan the next message: %v", got)
	}
	for _, ev := range events {
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivitySteer {
			if _, ok := activityValue(t, ev)["actions"]; ok {
				t.Fatal("reflex actions went on the wire")
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || !seen[0].Halted || !slices.Equal(seen[0].Fired, []string{"stop"}) {
		t.Fatalf("OnSteered: %+v", seen)
	}
	if len(seen[1].Actions) != 1 || seen[1].Actions[0].Kind != "ring" || !slices.Equal(seen[1].Slots, []string{"notes"}) {
		t.Fatalf("OnSteered actions or situation: %+v", seen[1])
	}
}

// With steering on, the desk plans in the conversation's deliberate mode.
func TestDeskModeDefaultsToTheConversationMode(t *testing.T) {
	var seen []agent.Steered
	var mu sync.Mutex
	planner := &recordingPlanner{}
	w := steeredRunner(t, planner, &seen, &mu)
	w.turn(t, twoSteps)
	c, err := w.runner.Agent().Conversation(t.Context(), chatrunner.ConversationID(w.conv))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(t.Context(), "focus"); err != nil {
		t.Fatal(err)
	}
	w.turn(t, twoSteps)
	if got := planner.seen(); !slices.Equal(got, []string{"", "focus"}) {
		t.Fatalf("desk modes: %v", got)
	}
}
