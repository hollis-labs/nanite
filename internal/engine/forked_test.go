package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

// callWithID is a tool call with its own ID and input, so a test's calls do
// not share the fixture's "call-1".
func callWithID(name, id string, input map[string]any) fake.Response {
	call := toolResponse(name)
	call.Events[0].ToolUse.ID = id
	call.Events[0].ToolUse.Input = input
	return call
}

// firstChildMessage is the text of the first message the child with goal was sent.
func firstChildMessage(t *testing.T, p *routedProvider, goal string) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := p.requests[goal]
	if len(requests) == 0 {
		t.Fatalf("child %q never ran", goal)
	}
	var b strings.Builder
	for _, block := range requests[0].Messages[0].ContentBlocks {
		b.WriteString(block.Text)
	}
	return b.String()
}

// forkedRoot runs two root turns: one that states a codename, then one that
// calls a tool and then subtask with ctx as its context argument. The parent
// history is then:
//
//	#1 user "remember: the codename is ALBATROSS"   #2 assistant "noted"
//	#3 user "go"   #4 assistant todo_list call   #5 todo_list result
//	#6 assistant subtask call (in flight while the child runs)
func forkedRoot(t *testing.T, ctx map[string]any, child *fake.Provider) (*routedProvider, *Engine, *store.Store, string) {
	t.Helper()
	input := map[string]any{"variant": "worker", "goal": "dig", "return_spec": "a short report"}
	if ctx != nil {
		input["context"] = ctx
	}
	p := &routedProvider{
		root: fake.New(fake.Text("noted"), callWithID("todo_list", "call-todos", map[string]any{}),
			callWithID("subtask", "call-subtask", input), fake.Text("root done")),
		children: map[string]*fake.Provider{"dig": child},
	}
	e, db, id := setupEngine(t, p, "")
	for _, text := range []string{"remember: the codename is ALBATROSS", "go"} {
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	return p, e, db, id
}

func TestChildSeesTheLastNParentMessages(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ctx       map[string]any
		want, not []string
	}{
		// The in-flight subtask call (#6) is never shown: its result does not exist yet.
		{"last two", map[string]any{"last_n": 2}, []string{"[#4 assistant] (called todo_list", "[#5 tool] (result of todo_list:"}, []string{"[#3 ", "[#6 ", "ALBATROSS"}},
		// The window would start on #5, a result whose call (#4) fell outside it: the result is dropped, not shown alone.
		{"no split pair", map[string]any{"last_n": 1}, nil, []string{"<parent_messages>", "[#5 "}},
		{"none", map[string]any{"last_n": 0}, nil, []string{"<parent_messages>"}},
		// The default is six messages, which here is all five settled ones.
		{"default", nil, []string{"[#1 user] remember: the codename is ALBATROSS", "[#5 tool]", "the last 5 messages"}, []string{"[#6 "}},
		{"full", map[string]any{"full": true, "last_n": 1}, []string{"the whole conversation", "[#1 user]", "[#5 tool]"}, []string{"[#6 "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _, _ := forkedRoot(t, tc.ctx, fake.New(fake.Text("report")))
			first := firstChildMessage(t, p, "dig")
			if !strings.HasPrefix(first, "Goal: dig\n") {
				t.Fatalf("child message must start with the goal: %q", first)
			}
			for _, want := range tc.want {
				if !strings.Contains(first, want) {
					t.Errorf("child message lacks %q:\n%s", want, first)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(first, not) {
					t.Errorf("child message has %q:\n%s", not, first)
				}
			}
		})
	}
}

func TestVariantLastNIsTheDefault(t *testing.T) {
	input := map[string]any{"variant": "brief", "goal": "dig", "return_spec": "x"}
	p := &routedProvider{root: fake.New(fake.Text("noted"), callWithID("subtask", "call-subtask", input), fake.Text("root done")),
		children: map[string]*fake.Provider{"dig": fake.New(fake.Text("report"))}}
	e, _, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "brief", "---\nlast_n: 1\n---\nBe brief.\n")
	for _, text := range []string{"older words", "go"} {
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	first := firstChildMessage(t, p, "dig")
	if !strings.Contains(first, "[#3 user] go") || strings.Contains(first, "[#2 ") || strings.Contains(first, "older words") {
		t.Fatalf("variant last_n 1 should show only #3:\n%s", first)
	}
}

func TestIncludeCopiesParentState(t *testing.T) {
	ctx := map[string]any{"last_n": 0, "include": []any{"plan", "todos", "handoff", "scratchpad:notes", "scratchpad:empty"}}
	input := map[string]any{"variant": "worker", "goal": "dig", "return_spec": "x", "context": ctx}
	p := &routedProvider{root: fake.New(callWithID("subtask", "call-subtask", input), fake.Text("root done")),
		children: map[string]*fake.Provider{"dig": fake.New(fake.Text("report"))}}
	e, db, id := setupEngine(t, p, "")
	if err := db.SetScratch(t.Context(), id, "plan", "1. measure 2. cut"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetScratch(t.Context(), id, "notes", "the API key lives in the vault"); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceTodos(t.Context(), id, []store.Todo{{Text: "write tests", Status: "in_progress"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.cfg.Slots.Put(slots.Handoff, slots.Entry{Owner: "engine", Key: "h", Content: "last session stopped at step 2"}); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	first := firstChildMessage(t, p, "dig")
	for _, want := range []string{
		"<parent_plan>\n1. measure 2. cut\n</parent_plan>",
		"<parent_todos>\n- [in_progress] write tests\n</parent_todos>",
		"<parent_handoff>\nlast session stopped at step 2\n</parent_handoff>",
		"<parent_scratchpad key=\"notes\">\nthe API key lives in the vault\n</parent_scratchpad key=\"notes\">",
		"<parent_scratchpad key=\"empty\">\n(none)\n",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("child message lacks %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "<parent_messages>") {
		t.Errorf("last_n 0 still copied messages:\n%s", first)
	}
}

func TestBadContextIsRefused(t *testing.T) {
	for name, ctx := range map[string]map[string]any{
		"unknown include": {"include": []any{"secrets"}},
		"empty key":       {"include": []any{"scratchpad:"}},
		"too many":        {"last_n": 51},
		"unknown field":   {"lastn": 3},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("NANITE_AUTO_REPAIR", "false")
			p, _, db, id := forkedRoot(t, ctx, fake.New(fake.Text("must never run")))
			p.mu.Lock()
			ran := len(p.requests["dig"])
			p.mu.Unlock()
			if ran != 0 {
				t.Fatal("a child ran with a bad context")
			}
			results := toolResults(t, db, id)
			if last := results[len(results)-1]; !last.IsError || !strings.Contains(last.Content, "context") {
				t.Fatalf("expected a context error, got %+v", last)
			}
		})
	}
}

func TestParentHistorySearchFindsWhatTheChildWasNotGiven(t *testing.T) {
	child := fake.New(
		callWithID("parent_history_search", "call-search", map[string]any{"query": "codename"}),
		callWithID("parent_history_get", "call-get", map[string]any{"from": 1, "to": 1}),
		fake.Text("the codename is ALBATROSS"))
	p, _, db, id := forkedRoot(t, map[string]any{"last_n": 2}, child)
	if first := firstChildMessage(t, p, "dig"); strings.Contains(first, "ALBATROSS") {
		t.Fatalf("the seed already had the old message:\n%s", first)
	}
	results := toolResults(t, db, childOf(t, db, id).ID)
	if len(results) != 2 {
		t.Fatalf("child tool results: %+v", results)
	}
	if search := results[0].Content; results[0].IsError || !strings.Contains(search, "[#1 user") || !strings.Contains(search, "[codename]") {
		t.Fatalf("search did not find message #1: %q", search)
	}
	if got := results[1].Content; results[1].IsError || !strings.Contains(got, "[#1 user] remember: the codename is ALBATROSS") {
		t.Fatalf("get did not return message #1: %q", got)
	}
}

func TestParentHistoryToolsAreForChildrenOnly(t *testing.T) {
	p, _, _, _ := forkedRoot(t, nil, fake.New(fake.Text("report")))
	root, child := p.offered("", 0), p.offered("dig", 0)
	for _, name := range []string{"parent_history_search", "parent_history_get"} {
		if slices.Contains(root, name) {
			t.Errorf("root conversation offered %s", name)
		}
		if !slices.Contains(child, name) {
			t.Errorf("child not offered %s: %v", name, child)
		}
	}
}

func TestLastMessagesAndSettled(t *testing.T) {
	use := func(id string) llmtypes.ContentBlock {
		return llmtypes.ContentBlock{Type: "tool_use", ID: id, Name: "t"}
	}
	result := func(id string) llmtypes.ContentBlock {
		return llmtypes.ContentBlock{Type: "tool_result", ToolUseID: id}
	}
	text := llmtypes.ContentBlock{Type: "text", Text: "x"}
	history := []store.Message{
		{Seq: 1, Role: "user", Blocks: []llmtypes.ContentBlock{text}},
		{Seq: 2, Role: "assistant", Blocks: []llmtypes.ContentBlock{use("a"), use("b")}},
		{Seq: 3, Role: "tool", Blocks: []llmtypes.ContentBlock{result("a"), result("b")}},
		{Seq: 4, Role: "assistant", Blocks: []llmtypes.ContentBlock{text, use("c")}},
	}
	seqs := func(ms []store.Message) []int {
		var out []int
		for _, m := range ms {
			out = append(out, m.Seq)
		}
		return out
	}
	settledHistory := settled(history)
	if got := seqs(settledHistory); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("settled = %v", got)
	}
	for n, want := range map[int][]int{0: nil, 1: nil, 2: {2, 3}, 3: {1, 2, 3}, 10: {1, 2, 3}} {
		if got := seqs(lastMessages(settledHistory, n)); !slices.Equal(got, want) {
			t.Errorf("lastMessages(%d) = %v, want %v", n, got, want)
		}
	}
}
