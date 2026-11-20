package chatrunner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/steer"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// Background subtask usage is not this turn's spend: the usage deltas still
// sum to the final usage, and the background usage arrives as activity.
func TestUsageDeltasSumToFinal(t *testing.T) {
	x := &run{asked: map[string]bool{}}
	var out []chatstream.Event
	for _, ev := range []agent.Event{
		agent.Usage{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 2},
		agent.Usage{InputTokens: 100, OutputTokens: 50, Subtask: "bg", Background: true},
		agent.Usage{InputTokens: 3, OutputTokens: 1, CacheWriteTokens: 4, Subtask: "fg"},
		agent.RunFinished{Result: agent.Result{Usage: agent.Usage{InputTokens: 13, OutputTokens: 6, CacheReadTokens: 2, CacheWriteTokens: 4}}},
	} {
		out = append(out, x.translate(ev)...)
	}
	var sum chatstream.Usage
	var final *chatstream.Usage
	var background bool
	for _, ev := range out {
		switch {
		case ev.Verb == chatstream.VerbUsage && ev.Usage.Scope == chatstream.UsageDelta:
			sum.UncachedInput += ev.Usage.UncachedInput
			sum.Output += ev.Usage.Output
			sum.CacheRead += ev.Usage.CacheRead
			sum.CacheWrite += ev.Usage.CacheWrite
		case ev.Verb == chatstream.VerbRunFinish:
			final = ev.Usage
		case ev.Verb == chatstream.VerbActivity && ev.Kind == ActivitySubagent:
			background = true
		}
	}
	if final == nil || sum.UncachedInput != final.UncachedInput || sum.Output != final.Output || sum.CacheRead != final.CacheRead || sum.CacheWrite != final.CacheWrite {
		t.Fatalf("deltas %+v, final %+v", sum, final)
	}
	if !background {
		t.Fatal("background usage was not forwarded as activity")
	}
}

// A running subtask's progress is a nanite.subagent activity with state
// progress; it never becomes text the model or the transcript sees. Start,
// progress and done all carry the parent and depth, so a depth-2 subtask can
// be placed under its lead; a background subtask is marked as one.
func TestSubtaskActivityCarriesParentAndDepth(t *testing.T) {
	x := &run{asked: map[string]bool{}}
	value := func(ev agent.Event) map[string]any {
		t.Helper()
		out := x.translate(ev)
		if len(out) != 1 || out[0].Verb != chatstream.VerbActivity || out[0].Kind != ActivitySubagent {
			t.Fatalf("%T mapped to %+v", ev, out)
		}
		var v map[string]any
		if err := json.Unmarshal(out[0].Value, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	start := value(agent.SubagentStarted{ID: "w", ParentID: "lead", Depth: 2, Variant: "worker"})
	if start["state"] != "start" || start["parent_id"] != "lead" || start["depth"] != float64(2) || start["background"] != nil {
		t.Fatalf("start %v", start)
	}
	progress := value(agent.SubtaskProgress{ID: "w", ParentID: "lead", Depth: 2, Variant: "worker", Step: 2, Tool: "web_fetch", StatusLine: "step 2: calling web_fetch"})
	if progress["state"] != "progress" || progress["parent_id"] != "lead" || progress["depth"] != float64(2) || progress["step"] != float64(2) ||
		progress["tool"] != "web_fetch" || progress["status_line"] != "step 2: calling web_fetch" || progress["status"] != nil {
		t.Fatalf("progress %v", progress)
	}
	done := value(agent.SubagentFinished{ID: "w", ParentID: "lead", Depth: 2, Variant: "worker", Status: "ok"})
	if done["state"] != "done" || done["parent_id"] != "lead" || done["depth"] != float64(2) || done["status"] != "ok" {
		t.Fatalf("done %v", done)
	}
	bg := value(agent.SubtaskProgress{ID: "b", ParentID: "root", Depth: 1, Variant: "worker", Step: 3, StatusLine: "done (ok)", Background: true, Status: "ok"})
	if bg["background"] != true || bg["status"] != "ok" {
		t.Fatalf("background progress %v", bg)
	}
	if started := value(agent.SubagentStarted{ID: "b", ParentID: "root", Depth: 1, Variant: "worker", Background: true}); started["background"] != true {
		t.Fatalf("background start %v", started)
	}
}

// What steering decided is a nanite.steer activity: the mode and its source,
// the slots, and suggestions the person may accept. Reflex actions stay with
// the host.
func TestSteeredIsActivity(t *testing.T) {
	x := &run{asked: map[string]bool{}}
	out := x.translate(agent.Steered{Mode: "focus", ModeSource: "deliberate", Slots: []string{"notes"}, Why: "focus", Fired: []string{"hush"},
		Suggestions: []agent.SteerVerdict{{Facet: "slot", Label: "mail", P: 0.9, Outcome: "suggest"}},
		Actions:     []steer.Action{{Reflex: "r", Kind: "call_tool", Spec: json.RawMessage(`{"tool":"secret"}`)}}})
	if len(out) != 1 || out[0].Kind != ActivitySteer {
		t.Fatalf("mapped to %+v", out)
	}
	var v map[string]any
	if err := json.Unmarshal(out[0].Value, &v); err != nil {
		t.Fatal(err)
	}
	s, _ := v["suggestions"].([]any)
	if v["mode"] != "focus" || v["mode_source"] != "deliberate" || len(s) != 1 || s[0].(map[string]any)["label"] != "mail" {
		t.Fatalf("value %v", v)
	}
	if strings.Contains(string(out[0].Value), "secret") {
		t.Fatal("a reflex action reached the wire")
	}
}
