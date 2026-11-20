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
		{"budget below one turn keeps the last turn", 1, 16},
		{"two turns fit", 2*perTurn + perTurn/2, 12},
		{"everything fits: nothing to summarize", 100 * perTurn, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cut := keepCut(msgs, ids, tc.keep)
			if cut != tc.want {
				t.Fatalf("cut = %d, want %d", cut, tc.want)
			}
			if cut > 0 && !ownerMessage(msgs[cut]) {
				t.Fatalf("cut at a non-owner message %d", cut)
			}
		})
	}
	// A tool result is the user's but never starts a turn.
	if ownerMessage(msgs[2]) {
		t.Fatal("a tool result counted as the person's message")
	}
	// An earlier summary (no id) never starts the kept part.
	ids[0] = ""
	if cut := keepCut(msgs[:4], ids[:4], 1); cut != 0 {
		t.Fatalf("one turn behind a summary: cut %d", cut)
	}
}

func TestSummaryInputIsCappedAsATranscript(t *testing.T) {
	msgs, _ := turns(3)
	if got := summaryInput(msgs, 1_000_000); len(got) != len(msgs) {
		t.Fatalf("under the cap the messages are sent as they are: %d", len(got))
	}
	got := summaryInput(msgs, 50)
	if len(got) != 1 || !strings.HasPrefix(got[0].Content, "[the start of this part is left out]") || len(got[0].Content) > 50*4+64 {
		t.Fatalf("capped input: %d messages, %q", len(got), got[0].Content[:min(80, len(got[0].Content))])
	}
	if !strings.HasSuffix(strings.TrimSpace(got[0].Content), "answer") {
		t.Fatalf("the end of the span is kept: %q", got[0].Content)
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
	if len(p.inputs[0]) != 12 {
		t.Fatalf("summarized %d messages, want the first three turns (12)", len(p.inputs[0]))
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
	if len(p.inputs) != 1 || len(p.inputs[0]) != 2 || len(x.messages) != 3 || x.messageIDs[1] != ids[2] {
		t.Fatalf("summaries=%d kept=%d", len(p.inputs), len(x.messages))
	}
}
