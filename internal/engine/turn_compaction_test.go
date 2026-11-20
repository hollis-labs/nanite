package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/tools"
)

type compactionOrderProvider struct {
	*watchedProvider
	summaryCounts []int
}

func (p *compactionOrderProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.summaryCounts = append(p.summaryCounts, p.summaries)
	return p.watchedProvider.StreamChat(ctx, req)
}

func TestTurnContextSurvivesMidTurnCompaction(t *testing.T) {
	for _, slot := range []slots.Name{slots.UserContext, slots.Context} {
		t.Run(string(slot), func(t *testing.T) {
			p := &compactionOrderProvider{watchedProvider: &watchedProvider{Provider: fake.New(fake.Text("one"), toolResponse("large_result"), fake.Text("done"))}}
			e, db, id := setupEngine(t, p, "")
			seedLongHistory(t, db, id)
			e.cfg.CompactAt = 6500
			e.cfg.CompactKeepTokens = 800
			if err := e.cfg.Tools.SetGroup("app", tools.Tool{Name: "large_result", Description: "Read a long document", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, tools.Call) (tools.Result, error) {
				return tools.Result{Text: strings.Repeat("long document words ", 1000)}, nil
			}}); err != nil {
				t.Fatal(err)
			}
			const marker = "HOST-CONTEXT-MUST-SURVIVE"
			for _, text := range []string{"first", "use a tool"} {
				run, err := e.Send(t.Context(), id, Input{Text: text, Slots: []slots.Update{{Slot: slot, Entry: slots.Entry{Owner: "host", Key: "turn-context", Content: marker}}}})
				if err != nil {
					t.Fatal(err)
				}
				compactionEvents(t, run)
				if _, err := run.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			if len(p.requests) != 3 || p.summaryCounts[1] != 0 || p.summaryCounts[2] == 0 {
				t.Fatalf("expected mid-turn compaction, summaries at requests: %v", p.summaryCounts)
			}
			for i, req := range p.requests {
				raw, _ := json.Marshal(req)
				if !strings.Contains(string(raw), marker) {
					t.Fatalf("request %d lost host context", i)
				}
			}
			// The in-turn context was in the older message that was summarized;
			// its preservation must also be durable for a subsequent recovery.
			if slot == slots.UserContext {
				summary, _, _, err := db.ModelHistory(t.Context(), id)
				if err != nil || !strings.Contains(summary, marker) || !strings.Contains(summary, compactionContextLabel) {
					t.Fatalf("summary lost context: %q, %v", summary, err)
				}
			}
		})
	}
}

func TestTextOnlyTurnCompactsBeforeFirstRequest(t *testing.T) {
	p := &compactionOrderProvider{watchedProvider: &watchedProvider{Provider: fake.New(fake.Text("done"))}}
	e, db, id := setupEngine(t, p, "")
	seedLongHistory(t, db, id)
	e.cfg.CompactAt = 4000
	e.cfg.CompactKeepTokens = 500
	run, err := e.Send(t.Context(), id, Input{Text: "continue with text only"})
	if err != nil {
		t.Fatal(err)
	}
	var started, completed int
	for event := range run.Events() {
		switch ev := event.(type) {
		case events.CompactionStarted:
			started++
		case events.Compacted:
			completed++
			if ev.RequestAfter >= e.cfg.CompactAt {
				t.Errorf("compacted request %d exceeds budget %d", ev.RequestAfter, e.cfg.CompactAt)
			}
		case events.CompactionFailed:
			t.Errorf("compaction failed: %v", ev.Err)
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if started != 1 || completed != 1 || len(p.requests) != 1 || p.summaryCounts[0] != 1 {
		t.Fatalf("starts=%d completed=%d summaries at requests=%v", started, completed, p.summaryCounts)
	}
	req := p.requests[0]
	est := ctxpkg.DefaultEstimator{}
	total := messagesTokens(req.Messages)
	for _, b := range req.SlotBlocks {
		total += est.Estimate(b.Content)
	}
	raw, _ := json.Marshal(req.Tools)
	total += est.Estimate(string(raw))
	if total >= e.cfg.CompactAt {
		t.Fatalf("first request estimate %d exceeds budget %d", total, e.cfg.CompactAt)
	}
}

func TestStepCompactionPreservesLatestToolResults(t *testing.T) {
	for _, keep := range []int{0, 800} {
		t.Run(fmt.Sprintf("keep_%d", keep), func(t *testing.T) {
			response := fake.Response{Events: []llmtypes.StreamEvent{
				{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call-1", Name: "large_result"}},
				{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call-2", Name: "large_result"}},
				{Type: llmtypes.EventDone},
			}}
			p := &watchedProvider{Provider: fake.New(response, fake.Text("done"))}
			e, _, id := setupEngine(t, p, "runtime:\n  context_window: 6000\n")
			e.cfg.CompactKeepTokens = keep
			want := strings.Repeat("CURRENT-RESULT ", 1000)
			if err := e.cfg.Tools.SetGroup("app", tools.Tool{Name: "large_result", Description: "Read a document", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`), Handler: func(context.Context, tools.Call) (tools.Result, error) {
				return tools.Result{Text: want}, nil
			}}); err != nil {
				t.Fatal(err)
			}
			run, err := e.Send(t.Context(), id, Input{Text: "Read the document."})
			if err != nil {
				t.Fatal(err)
			}
			var toolFinished, compactedAfterTool, noProgress bool
			for ev := range run.Events() {
				switch ev.(type) {
				case events.ToolCallFinished:
					toolFinished = true
				case events.CompactionFailed:
					noProgress = true
				case events.CompactionStarted:
					if !toolFinished {
						t.Fatal("compaction ran before the tool")
					}
					compactedAfterTool = true
				}
			}
			if _, err := run.Wait(); err != nil {
				t.Fatal(err)
			}
			if !compactedAfterTool || len(p.requests) != 2 {
				t.Fatalf("step compaction=%v requests=%d", compactedAfterTool, len(p.requests))
			}
			for _, callID := range []string{"call-1", "call-2"} {
				var useIndex, resultIndex = -1, -1
				for i, m := range p.requests[1].Messages {
					for _, b := range m.ContentBlocks {
						if b.Type == "tool_use" && b.ID == callID {
							useIndex = i
						}
						if b.Type == "tool_result" && b.ToolUseID == callID {
							resultIndex = i
							if b.Content != want {
								t.Fatalf("latest result changed: %d bytes, want %d", len(b.Content), len(want))
							}
						}
					}
				}
				if useIndex < 0 || resultIndex != useIndex+1 {
					t.Fatalf("latest tool pair lost or separated: use=%d result=%d", useIndex, resultIndex)
				}
			}
			if !noProgress {
				t.Fatal("restored tool results were counted as compaction savings")
			}
		})
	}
}
