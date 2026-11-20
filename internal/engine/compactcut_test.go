package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

func text(role, s string) llmtypes.ChatMessage {
	return llmtypes.ChatMessage{Role: role, ContentBlocks: []llmtypes.ContentBlock{{Type: "text", Text: s}}}
}

func toolPair(id string) []llmtypes.ChatMessage {
	input := map[string]any{"path": "a"}
	return []llmtypes.ChatMessage{
		{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: id, Name: "read_file", Input: &input}}},
		{Role: "user", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: id, Content: strings.Repeat("file text ", 50)}}},
	}
}

// turns builds n turns: the person's message, a tool call and result, and
// the agent's answer.
func turns(n int) ([]llmtypes.ChatMessage, []string) {
	var msgs []llmtypes.ChatMessage
	for i := range n {
		msgs = append(msgs, text("user", strings.Repeat("question ", 40)))
		msgs = append(msgs, toolPair(string(rune('a'+i)))...)
		msgs = append(msgs, text("assistant", strings.Repeat("answer ", 40)))
	}
	ids := make([]string, len(msgs))
	now := time.Now().UTC()
	for i := range ids {
		ids[i] = store.NewID(now)
	}
	return msgs, ids
}

func TestKeepCutKeepsWholeTurns(t *testing.T) {
	msgs, ids := turns(5)
	perTurn := messagesTokens(msgs[:4])
	cases := []struct {
		name string
		keep int
		want int
	}{
		{"budget below one turn cuts inside the last turn, before its last step", 1, 19},
		{"two turns fit", 2*perTurn + perTurn/2, 12},
		{"everything fits: nothing to summarize", 100 * perTurn, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cut := keepCut(msgs, msgs, ids, tc.keep)
			if cut != tc.want {
				t.Fatalf("cut = %d, want %d", cut, tc.want)
			}
			if cut > 0 && !ownerMessage(msgs[cut]) && msgs[cut].Role != "assistant" {
				t.Fatalf("cut between a tool call and its result at %d", cut)
			}
		})
	}
	// A tool result is the user's but never starts a turn.
	if ownerMessage(msgs[2]) {
		t.Fatal("a tool result counted as the person's message")
	}
	// An earlier summary (no id) never starts a kept turn.
	ids[0] = ""
	if cut := keepCut(msgs[:4], msgs[:4], ids[:4], 1); cut != 0 {
		t.Fatalf("one turn behind a summary: cut %d", cut)
	}
}

func TestSummaryInputIsOneTranscriptMessage(t *testing.T) {
	msgs, _ := turns(3)
	for _, capTokens := range []int{1_000_000, 50} {
		got := summaryInput(msgs, capTokens)
		if len(got) != 1 || got[0].Role != "user" || len(got[0].ContentBlocks) != 0 {
			t.Fatalf("cap %d: %d messages", capTokens, len(got))
		}
		if !strings.HasSuffix(strings.TrimSpace(got[0].Content), "answer") {
			t.Fatalf("cap %d: the end of the span is kept: %q", capTokens, got[0].Content[len(got[0].Content)-40:])
		}
	}
	if got := summaryInput(msgs, 50)[0].Content; !strings.Contains(got, "[the start of this part is left out]") || len(got) > 50*4+128 {
		t.Fatalf("capped transcript: %d bytes", len(got))
	}
	if got := summaryInput(msgs, 1_000_000)[0].Content; !strings.Contains(got, "Agent called read_file") || !strings.Contains(got, "Person: question") {
		t.Fatalf("transcript: %q", got[:200])
	}
}

func TestRequestMessagesMergeTheSummary(t *testing.T) {
	msgs := []llmtypes.ChatMessage{{Role: "user", Content: "summary"}, text("user", "next"), text("assistant", "reply")}
	got := requestMessages(msgs, []string{"", "a", "b"})
	if len(got) != 2 || got[0].Role != "user" || len(got[0].ContentBlocks) != 2 || got[0].ContentBlocks[0].Text != "summary" || got[0].ContentBlocks[1].Text != "next" {
		t.Fatalf("merged: %+v", got)
	}
	if len(msgs[1].ContentBlocks) != 1 {
		t.Fatal("the history was changed")
	}
	// Before a step of the agent's the summary stays its own message.
	if got := requestMessages([]llmtypes.ChatMessage{{Role: "user", Content: "summary"}, text("assistant", "x")}, []string{"", "a"}); len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
}

// recordingSummaries records each summary call's system prompt and input.
type recordingSummaries struct {
	*watchedProvider
	mu      sync.Mutex
	prompts []string
	inputs  [][]llmtypes.ChatMessage
}

func (r *recordingSummaries) Complete(_ context.Context, req llmtypes.ChatRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts = append(r.prompts, req.SystemPrompt)
	r.inputs = append(r.inputs, req.Messages)
	return "durable context", nil
}

// compactionExecution builds an execution over msgs whose window always
// needs compaction, with the Context slot holding context.
func compactionExecution(t *testing.T, p *recordingSummaries, cfg func(*Config), msgs []llmtypes.ChatMessage, ids []string, contextText string) (*execution, *Run) {
	t.Helper()
	e, _, id := setupEngine(t, p, "")
	if cfg != nil {
		cfg(&e.cfg)
	}
	def, err := definition.Load(e.cfg.Dir, e.cfg.GlobalSkillsDir)
	if err != nil {
		t.Fatal(err)
	}
	assembled, err := e.assemble(t.Context(), Input{}, slots.TurnInfo{ConversationID: id, Role: "chat"}, def, e.cfg.Slots.Snapshot(), nil, "", "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if contextText != "" {
		assembled.Window.SetContent(ctxpkg.SlotContext, contextText)
	}
	j, err := e.cfg.Store.OpenJournal(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	// Store the messages so a recorded summary can point at them.
	now := time.Now().UTC()
	turn := store.Turn{ID: store.NewID(now), UserMessageID: ids[0]}
	if err := j.StartTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		if err := j.AppendMessage(store.Message{ID: ids[i], TurnID: turn.ID, Role: m.Role, CreatedAt: now, Blocks: m.ContentBlocks}, nil); err != nil {
			t.Fatal(err)
		}
	}
	r := &Run{id: turn.ID, events: make(chan events.Event, 32), done: make(chan struct{})}
	x := &execution{engine: e, run: r, info: store.Conversation{ID: id, Role: "chat"}, definition: def, provider: p, assembled: assembled, journal: j,
		messages: msgs, messageIDs: ids}
	x.sizeWindow()
	return x, r
}

func drain(r *Run) []events.Event {
	close(r.events)
	var out []events.Event
	for ev := range r.events {
		out = append(out, ev)
	}
	return out
}

func TestCompactionKeepsWholeTurnsWithThePrompt(t *testing.T) {
	msgs, ids := turns(5)
	perTurn := messagesTokens(msgs[:4])
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	x, r := compactionExecution(t, p, func(c *Config) {
		c.CompactKeepTokens = 2*perTurn + perTurn/2
		c.CompactionPrompt = "Summarize for the test."
	}, msgs, ids, "")
	if err := x.compact(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	drain(r)
	if len(p.prompts) != 1 || p.prompts[0] != "Summarize for the test." {
		t.Fatalf("summary prompts: %q", p.prompts)
	}
	if len(p.inputs[0]) != 1 || strings.Count(p.inputs[0][0].Content, "Person: question") != 3 {
		t.Fatalf("summary input: %d messages, %d turns", len(p.inputs[0]), strings.Count(p.inputs[0][0].Content, "Person: question"))
	}
	// The summary, then the last two turns whole, starting at the person's
	// message.
	if len(x.messages) != 9 || !ownerMessage(x.messages[1]) || x.messageIDs[1] != ids[12] {
		t.Fatalf("kept %d messages, first kept id %q", len(x.messages), x.messageIDs[1])
	}
}

func TestCompactionDefaultPrompt(t *testing.T) {
	msgs, ids := turns(4)
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	x, r := compactionExecution(t, p, nil, msgs, ids, "")
	if err := x.compact(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	drain(r)
	if len(p.prompts) != 1 || p.prompts[0] != DefaultCompactionPrompt {
		t.Fatalf("summary prompts: %q", p.prompts)
	}
}

// The Context slot's blocks are fixed in the request, so dropping it frees
// nothing; compaction must summarize instead of failing with "freed too
// little" on every turn.
func TestContextSlotDoesNotStandInForASummary(t *testing.T) {
	msgs, ids := turns(4)
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	contextText := strings.Repeat("standing context line\n", 400)
	x, r := compactionExecution(t, p, nil, msgs, ids, contextText)
	// Over budget by less than the Context slot holds.
	x.assembled.Window.TotalBudget = staticTokens(x.assembled.Window) + messagesTokens(msgs) - 600
	if err := x.compact(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	var started, done *events.Compacted
	var failed error
	for _, ev := range drain(r) {
		switch e := ev.(type) {
		case events.Compacted:
			done = &e
		case events.CompactionFailed:
			failed = e.Err
		case events.CompactionStarted:
			started = &events.Compacted{Before: e.Before, RequestBefore: e.RequestBefore}
		}
	}
	if failed != nil || done == nil || len(p.prompts) != 1 {
		t.Fatalf("compaction failed=%v done=%v summaries=%d", failed, done != nil, len(p.prompts))
	}
	if x.assembled.Window.Slot(ctxpkg.SlotContext).Content != contextText {
		t.Fatal("the Context slot was cleared")
	}
	// Request sizes count the fixed part too.
	if started.RequestBefore <= started.Before || done.RequestBefore != started.RequestBefore || done.RequestAfter >= done.RequestBefore ||
		done.RequestBefore-done.RequestAfter != done.Before-done.After {
		t.Fatalf("sizes: %+v (started %+v)", *done, *started)
	}
}

// With four messages the pipeline's own stage keeps everything; the
// turn-based cut still summarizes the first turn.
func TestKeepCutSummarizesWhereThePipelineWouldNot(t *testing.T) {
	msgs := []llmtypes.ChatMessage{
		text("user", strings.Repeat("first question ", 200)), text("assistant", strings.Repeat("first answer ", 200)),
		text("user", "second question"), text("assistant", "second answer"),
	}
	ids := make([]string, len(msgs))
	now := time.Now().UTC()
	for i := range ids {
		ids[i] = store.NewID(now)
	}
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	x, r := compactionExecution(t, p, func(c *Config) { c.CompactKeepTokens = 100 }, msgs, ids, "")
	if err := x.compact(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	drain(r)
	if len(p.inputs) != 1 || !strings.Contains(p.inputs[0][0].Content, "first answer") || len(x.messages) != 3 || x.messageIDs[1] != ids[2] {
		t.Fatalf("summaries=%d kept=%d", len(p.inputs), len(x.messages))
	}
}

// longTurn is one person's message followed by n tool steps.
func longTurn(n int) ([]llmtypes.ChatMessage, []string) {
	msgs := []llmtypes.ChatMessage{text("user", "write the document")}
	for i := range n {
		input := map[string]any{"part": i}
		msgs = append(msgs,
			llmtypes.ChatMessage{Role: "assistant", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: "t" + string(rune('a'+i)), Name: "write", Input: &input}}},
			llmtypes.ChatMessage{Role: "user", ContentBlocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "t" + string(rune('a'+i)), Content: strings.Repeat("written part ", 80) + string(rune('a'+i))}}})
	}
	ids := make([]string, len(msgs))
	now := time.Now().UTC()
	for i := range ids {
		ids[i] = store.NewID(now)
	}
	return msgs, ids
}

// A single long turn compacts inside itself, on the forced overflow pass
// and on the step path, keeping the latest steps whole.
func TestLongTurnCompactsInsideItself(t *testing.T) {
	for _, force := range []bool{true, false} {
		msgs, ids := longTurn(10)
		p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
		step := messagesTokens(msgs[1:3])
		x, r := compactionExecution(t, p, func(c *Config) { c.CompactKeepTokens = 2*step + step/2 }, msgs, ids, "")
		if !force {
			x.assembled.Window.TotalBudget = staticTokens(x.assembled.Window) + messagesTokens(msgs)/2
		}
		if err := x.compact(t.Context(), force); err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		var failed error
		for _, ev := range drain(r) {
			if e, ok := ev.(events.CompactionFailed); ok {
				failed = e.Err
			}
		}
		if failed != nil || len(p.inputs) != 1 {
			t.Fatalf("force=%v: failed=%v summaries=%d", force, failed, len(p.inputs))
		}
		if !strings.Contains(p.inputs[0][0].Content, "write the document") {
			t.Fatalf("force=%v: the turn's message is not in the summary", force)
		}
		// The summary, then the last two steps.
		if len(x.messages) != 5 || x.messages[1].Role != "assistant" || x.messageIDs[1] != ids[17] {
			t.Fatalf("force=%v: kept %d messages from %q", force, len(x.messages), x.messageIDs[1])
		}
		req := requestMessages(x.messages, x.messageIDs)
		for i := 1; i < len(req); i++ {
			if req[i].Role == req[i-1].Role {
				t.Fatalf("force=%v: two %s messages in a row at %d", force, req[i].Role, i)
			}
		}
	}
}

func TestKeepOverTheWindowIsClamped(t *testing.T) {
	msgs, ids := turns(6)
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	x, r := compactionExecution(t, p, func(c *Config) { c.CompactKeepTokens = 10_000_000 }, msgs, ids, "")
	x.assembled.Window.TotalBudget = staticTokens(x.assembled.Window) + messagesTokens(msgs)/2
	if err := x.compact(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	drain(r)
	if len(p.inputs) != 1 || len(x.messages) >= len(msgs) {
		t.Fatalf("summaries=%d kept=%d", len(p.inputs), len(x.messages))
	}
	if keep := x.keepTokens(x.assembled.Window); keep > x.assembled.Window.TotalBudget/2 {
		t.Fatalf("keep %d over half of %d", keep, x.assembled.Window.TotalBudget)
	}
}

// Dedupe works on a copy: the history's own tool results stay as they were.
func TestDedupeLeavesTheLiveHistory(t *testing.T) {
	msgs, ids := turns(4) // every tool result is the same call and content
	before := make([]string, len(msgs))
	for i, m := range msgs {
		if len(m.ContentBlocks) > 0 {
			before[i] = m.ContentBlocks[0].Content
		}
	}
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	x, r := compactionExecution(t, p, nil, msgs, ids, "")
	if err := x.compact(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	drain(r)
	for i, m := range msgs {
		if len(m.ContentBlocks) > 0 && m.ContentBlocks[0].Content != before[i] {
			t.Fatalf("message %d changed: %q", i, m.ContentBlocks[0].Content)
		}
	}
}

func TestTwoCompactionsInARowWithKeep(t *testing.T) {
	msgs, ids := turns(6)
	perTurn := messagesTokens(msgs[:4])
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	x, r := compactionExecution(t, p, func(c *Config) { c.CompactKeepTokens = 2*perTurn + perTurn/2 }, msgs, ids, "")
	if err := x.compact(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	// One more turn, then compact again: the earlier summary is summarized
	// with the turn that no longer fits.
	more, _ := turns(1)
	now := time.Now().UTC()
	for _, m := range more {
		x.messages = append(x.messages, m)
		x.messageIDs = append(x.messageIDs, store.NewID(now))
	}
	if err := x.compact(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	var done int
	for _, ev := range drain(r) {
		if _, ok := ev.(events.Compacted); ok {
			done++
		}
	}
	if done != 2 || len(p.inputs) != 2 || !strings.Contains(p.inputs[1][0].Content, "[Conversation summary") {
		t.Fatalf("done=%d summaries=%d", done, len(p.inputs))
	}
	if x.messageIDs[0] != "" || !ownerMessage(x.messages[1]) || len(x.messages) != 9 {
		t.Fatalf("after two compactions: %d messages, first kept owner=%v", len(x.messages), ownerMessage(x.messages[1]))
	}
}

// A conversation slot the window marks non-compactable is never summarized.
func TestKeepRespectsTheConversationSlot(t *testing.T) {
	msgs, ids := turns(5)
	p := &recordingSummaries{watchedProvider: &watchedProvider{Provider: fake.New()}}
	x, r := compactionExecution(t, p, func(c *Config) { c.CompactKeepTokens = 100 }, msgs, ids, "")
	x.assembled.Window.Slot(ctxpkg.SlotConversation).Compactable = false
	_ = x.compact(t.Context(), true)
	drain(r)
	if len(p.inputs) != 0 {
		t.Fatalf("summaries=%d", len(p.inputs))
	}
	if x.assembled.Window.Slot(ctxpkg.SlotConversation).Compactable {
		t.Fatal("the slot's compactability was not restored")
	}
}
