package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
)

func TestOversizedHostContextArrivesTrimmed(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("ok"))}
	e, _, id := setupEngine(t, p, "")
	e.cfg.SlotBudgets = map[slots.Name]int{slots.UserContext: 300}
	var sunk []trace.Turn
	e.cfg.TraceSink = func(_ context.Context, rec trace.Turn) { sunk = append(sunk, rec) }
	big := "START " + strings.Repeat("page context ", 3000)
	run, err := e.Send(t.Context(), id, Input{Text: "hi", Slots: []slots.Update{{Slot: slots.UserContext, Entry: slots.Entry{Owner: "host", Key: "page", Content: big}}}})
	if err != nil {
		t.Fatal(err)
	}
	var report slots.Report
	for ev := range run.Events() {
		if a, ok := ev.(events.SlotsAssembled); ok {
			report = a.Report
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if d := report[slots.UserContext].Decisions; len(d) != 1 || d[0].Disposition != slots.Trim {
		t.Fatalf("decisions %+v", d)
	}
	if !slices.ContainsFunc(p.requests[0].SlotBlocks, func(b llmtypes.SlotBlock) bool {
		return b.Name == string(slots.UserContext) && strings.HasPrefix(b.Content, "START ") && strings.Contains(b.Content, "[trimmed to fit the user_context slot budget of 300 tokens]")
	}) {
		t.Fatal("the request lost the trimmed context")
	}
	if len(sunk) != 1 || !slices.Contains(sunk[0].Trimmed, "user_context/host/page") {
		t.Fatalf("trace trimmed %v", sunk)
	}
	if got := e.SlotBudgets()[slots.UserContext]; got != 300 {
		t.Fatalf("budget %d", got)
	}
	e.cfg.SlotBudgets[slots.Universal] = 10
	if got := e.SlotBudgets()[slots.Universal]; got != universalBudget {
		t.Fatalf("universal budget overridden to %d", got)
	}
}
