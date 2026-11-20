package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/tools"
)

// manyTools registers n host tools whose schemas together are large, as an
// embedding app with a big catalog would.
func manyTools(t *testing.T, e *Engine, n int) {
	t.Helper()
	var group []tools.Tool
	for i := range n {
		schema := fmt.Sprintf(`{"type":"object","properties":{"query":{"type":"string","description":%q},"limit":{"type":"integer"}}}`, strings.Repeat("Describes what this field selects. ", 6))
		group = append(group, tools.Tool{Name: fmt.Sprintf("tool_%03d", i), Description: "An app tool with a typical description of what it does and when to use it.",
			ReadOnly: true, InputSchema: json.RawMessage(schema),
			Handler: func(context.Context, tools.Call) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
	}
	if err := e.cfg.Tools.SetGroup("app", group...); err != nil {
		t.Fatal(err)
	}
}

func countStarts(t *testing.T, e *Engine, id string) (int, *Run) {
	t.Helper()
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(strings.Join(compactionEvents(t, run), ";"), "started"), run
}

func TestToolSchemasCountTowardCompaction(t *testing.T) {
	// A 32,000-token window (budget 25,600): the fixed slots and 200 tool
	// schemas (~25,000 together) fit, but not with the seeded conversation
	// (~2,000) beside them. Without the schemas counted, ~3,000 of fixed
	// slots leave plenty of room and nothing compacts.
	p := &watchedProvider{Provider: fake.New(toolResponse("tool_000"), fake.Text("done"))}
	e, db, id := setupEngine(t, p, "runtime:\n  context_window: 32000\n")
	seedLongHistory(t, db, id)
	manyTools(t, e, 200)
	starts, run := countStarts(t, e, id)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if starts == 0 || p.summaries == 0 {
		t.Fatalf("no compaction with 250 tool schemas: starts=%d summaries=%d", starts, p.summaries)
	}
}

func TestReportedInputTriggersCompaction(t *testing.T) {
	// The estimate is small, but the provider reports a request near the
	// default window; the next step compacts.
	big := toolResponse("unknown")
	big.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 2000, CacheReadTokens: 170000}}}, big.Events...)
	p := &watchedProvider{Provider: fake.New(big, fake.Text("done"))}
	e, db, id := setupEngine(t, p, "")
	seedLongHistory(t, db, id)
	starts, run := countStarts(t, e, id)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if starts == 0 || p.summaries == 0 {
		t.Fatalf("reported input did not trigger compaction: starts=%d summaries=%d", starts, p.summaries)
	}
}

func TestCompactAtOption(t *testing.T) {
	// The fixed slots and tool schemas are about 3,000 tokens; 4,000 leaves
	// no room for the seeded conversation.
	for _, at := range []int{0, 4000} {
		p := &watchedProvider{Provider: fake.New(toolResponse("unknown"), fake.Text("done"))}
		e, db, id := setupEngine(t, p, "")
		e.cfg.CompactAt = at
		seedLongHistory(t, db, id)
		starts, run := countStarts(t, e, id)
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
		if (starts > 0) != (at > 0) {
			t.Fatalf("CompactAt=%d: compaction starts=%d", at, starts)
		}
	}
}

func TestFixedContentOverBudgetSkipsCompaction(t *testing.T) {
	// The fixed slots and schemas alone exceed the budget: compaction cannot
	// help, so it does not run.
	p := &watchedProvider{Provider: fake.New(toolResponse("unknown"), fake.Text("done"))}
	e, db, id := setupEngine(t, p, "")
	e.cfg.CompactAt = 500
	seedLongHistory(t, db, id)
	starts, run := countStarts(t, e, id)
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if starts != 0 || p.summaries != 0 {
		t.Fatalf("compaction ran though it could not help: starts=%d summaries=%d", starts, p.summaries)
	}
}
