package chatrunner

import (
	"testing"

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
