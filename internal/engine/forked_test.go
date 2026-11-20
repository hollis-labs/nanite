package engine

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/core/toolresult"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
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
		"many includes":   {"include": []any{"plan", "todos", "handoff", "scratchpad:a", "scratchpad:b", "scratchpad:c", "scratchpad:d", "scratchpad:e", "scratchpad:f"}},
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

// testKey is a fake key shaped like one the redactor recognizes; it must
// never reach a child. It is built at run time so no key-shaped literal sits
// in the source.
var testKey = "sk-" + "ant-api03-" + strings.Repeat("AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", 2) + "-abcdefAA"

// appendParent writes messages straight into a conversation's history, one
// user text message each.
func appendParent(t *testing.T, db *store.Store, id string, texts ...string) {
	t.Helper()
	j, err := db.OpenJournal(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	now := time.Now().UTC()
	turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
	if err := j.StartTurn(turn, now); err != nil {
		t.Fatal(err)
	}
	for i, text := range texts {
		mid := store.NewID(now)
		if i == 0 {
			mid = turn.UserMessageID
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := j.AppendMessage(store.Message{ID: mid, TurnID: turn.ID, Role: role, CreatedAt: now,
			Blocks: []llmtypes.ContentBlock{{Type: "text", Text: text}}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	turn.Status = "completed"
	if err := j.FinishTurn(turn, now); err != nil {
		t.Fatal(err)
	}
}

func TestParentContextIsRedactedBeforeItIsCut(t *testing.T) {
	// The key straddles the 2,000-character cut of a seeded block, and sits
	// mid-window for a search snippet: both must be redacted whole first.
	// 60 characters between the key and the match put the snippet's start mid-key.
	long := strings.Repeat("alpha ", 330) + testKey + " " + strings.Repeat("m ", 30) + "zebra " + strings.Repeat("w ", 50)
	child := fake.New(callWithID("parent_history_search", "call-search", map[string]any{"query": "zebra"}), fake.Text("report"))
	input := map[string]any{"variant": "worker", "goal": "dig", "return_spec": "x", "context": map[string]any{"last_n": 5, "include": []any{"scratchpad:keys"}}}
	p := &routedProvider{root: fake.New(fake.Text("noted"), callWithID("subtask", "call-subtask", input), fake.Text("root done")),
		children: map[string]*fake.Provider{"dig": child}}
	e, db, id := setupEngine(t, p, "")
	appendParent(t, db, id, long, "ok")
	if err := db.SetScratch(t.Context(), id, "keys", strings.Repeat("b", 3984)+" "+testKey); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one more", "go"} {
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	leaks := func(where, text string) {
		t.Helper()
		for _, part := range []string{"AbCdEfGh", "0123456789-abcdefAA", "sk-ant-api03"} {
			if strings.Contains(text, part) {
				t.Errorf("%s leaks %q:\n%s", where, part, text)
			}
		}
	}
	leaks("seed", firstChildMessage(t, p, "dig"))
	results := toolResults(t, db, childOf(t, db, id).ID)
	if len(results) != 1 || !strings.Contains(results[0].Content, "[zebra]") {
		t.Fatalf("search results: %+v", results)
	}
	leaks("search", results[0].Content)
}

func TestSeedFitsItsBudget(t *testing.T) {
	var texts []string
	for i := 1; i <= 60; i++ {
		texts = append(texts, fmt.Sprintf("message %d %s", i, strings.Repeat("x", 3000)))
	}
	big := strings.Repeat("p", 20000)
	for name, ctx := range map[string]map[string]any{
		"full":    {"full": true, "include": []any{"plan"}},
		"last 50": {"last_n": 50, "include": []any{"plan"}},
	} {
		t.Run(name, func(t *testing.T) {
			input := map[string]any{"variant": "worker", "goal": "dig", "return_spec": "x", "context": ctx}
			p := &routedProvider{root: fake.New(callWithID("subtask", "call-subtask", input), fake.Text("root done")),
				children: map[string]*fake.Provider{"dig": fake.New(fake.Text("report"))}}
			e, db, id := setupEngine(t, p, "")
			appendParent(t, db, id, texts...)
			if err := db.SetScratch(t.Context(), id, "plan", big); err != nil {
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
			_, seed, _ := strings.Cut(first, "Context from the parent")
			if len(seed) > seedBudget+1000 {
				t.Fatalf("seed is %d characters, budget %d", len(seed), seedBudget)
			}
			// The newest messages are kept, the oldest left out and counted.
			if !strings.Contains(seed, "[#61 user] go") || strings.Contains(seed, "message 1 ") || !strings.Contains(seed, "older ones left out to fit") {
				t.Fatalf("seed did not keep the newest messages:\n%.600s", seed)
			}
			if strings.Contains(first, strings.Repeat("p", includeChars+1)) || !strings.Contains(first, "[shortened]") {
				t.Fatal("the plan was not cut to its own bound")
			}
		})
	}
}

func TestReusedToolCallIDsDoNotLeakTheInFlightCall(t *testing.T) {
	// Some providers number calls call_0 in every turn.
	input := map[string]any{"variant": "worker", "goal": "dig", "return_spec": "x", "context": map[string]any{"last_n": 10}}
	p := &routedProvider{
		root: fake.New(fake.Text("noted"), callWithID("todo_list", "call_0", map[string]any{}),
			callWithID("subtask", "call_0", input), fake.Text("root done")),
		children: map[string]*fake.Provider{"dig": fake.New(fake.Text("report"))},
	}
	e, _, id := setupEngine(t, p, "")
	for _, text := range []string{"hi", "go"} {
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	first := firstChildMessage(t, p, "dig")
	if strings.Contains(first, "[#6 ") || strings.Contains(first, "called subtask") {
		t.Fatalf("the in-flight call leaked into the window:\n%s", first)
	}
	if !strings.Contains(first, "[#5 tool] (result of todo_list:") {
		t.Fatalf("the result was not named from the call before it:\n%s", first)
	}
}

func TestToolNamesPairByPosition(t *testing.T) {
	call := store.Message{Role: "assistant", Blocks: []llmtypes.ContentBlock{
		{Type: "tool_use", ID: "call_0", Name: "read_file"}, {Type: "tool_use", ID: "call_0", Name: "list_dir"}, {Type: "tool_use", ID: "x", Name: "bash"}}}
	results := store.Message{Role: "tool", Blocks: []llmtypes.ContentBlock{
		{Type: "tool_result", ToolUseID: "call_0"}, {Type: "tool_result", ToolUseID: "call_0"}, {Type: "tool_result", ToolUseID: "x"}}}
	if got := toolNames(&call, results); !slices.Equal(got, []string{"read_file", "list_dir", "bash"}) {
		t.Fatalf("names = %v", got)
	}
	if got := toolNames(nil, results); !slices.Equal(got, []string{"a tool", "a tool", "a tool"}) {
		t.Fatalf("names without a call = %v", got)
	}
}

func TestChildFetchesItsParentsCachedResults(t *testing.T) {
	p := &routedProvider{root: fake.New(callWithID("subtask", "call-subtask", map[string]any{"variant": "worker", "goal": "dig", "return_spec": "x"}), fake.Text("root done"))}
	e, db, id := setupEngine(t, p, "")
	parentPointer, err := e.cache.Put(t.Context(), id, toolresult.Meta{Tool: "bash", CallID: "c"}, "parent's long output: ORCHID")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	other := store.NewID(now)
	if err := db.CreateConversation(t.Context(), store.Conversation{ID: other, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	otherPointer, err := e.cache.Put(t.Context(), other, toolresult.Meta{Tool: "bash", CallID: "c"}, "another conversation's output: TULIP")
	if err != nil {
		t.Fatal(err)
	}
	p.children = map[string]*fake.Provider{"dig": fake.New(
		callWithID("fetch_tool_result", "call-mine", map[string]any{"id": parentPointer.ID}),
		callWithID("fetch_tool_result", "call-theirs", map[string]any{"id": otherPointer.ID}),
		fake.Text("report"))}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(t, db, childOf(t, db, id).ID)
	if len(results) != 2 {
		t.Fatalf("child results: %+v", results)
	}
	if results[0].IsError || !strings.Contains(results[0].Content, "ORCHID") {
		t.Fatalf("child could not read its parent's cached result: %+v", results[0])
	}
	if !results[1].IsError || strings.Contains(results[1].Content, "TULIP") {
		t.Fatalf("child read an unrelated conversation's result: %+v", results[1])
	}
}

func TestSeedMentionsHistoryToolsOnlyWhenOffered(t *testing.T) {
	input := map[string]any{"variant": "narrow", "goal": "dig", "return_spec": "x"}
	p := &routedProvider{root: fake.New(fake.Text("noted"), callWithID("subtask", "call-subtask", input), fake.Text("root done")),
		children: map[string]*fake.Provider{"dig": fake.New(fake.Text("report"))}}
	e, _, id := setupEngine(t, p, "")
	writeVariant(t, e.cfg.Dir, "narrow", "---\ntools:\n  allow: [read_file]\n---\nOnly read.\n")
	for _, text := range []string{strings.Repeat("long ", 600), "go"} {
		run, err := e.Send(t.Context(), id, Input{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	first := firstChildMessage(t, p, "dig")
	if strings.Contains(first, "parent_history") || !strings.Contains(first, "[shortened]") {
		t.Fatalf("seed names tools the child lacks:\n%.400s", first)
	}
	if offered := p.offered("dig", 0); slices.Contains(offered, "parent_history_get") {
		t.Fatalf("narrow variant offered %v", offered)
	}
}

// seedFor runs one root turn that calls subtask with ctx, after prep has set
// up the parent, and returns the child's first message.
func seedFor(t *testing.T, ctx map[string]any, prep func(*Engine, *store.Store, string)) string {
	t.Helper()
	input := map[string]any{"variant": "worker", "goal": "dig", "return_spec": "x", "context": ctx}
	p := &routedProvider{root: fake.New(callWithID("subtask", "call-subtask", input), fake.Text("root done")),
		children: map[string]*fake.Provider{"dig": fake.New(fake.Text("report"))}}
	e, db, id := setupEngine(t, p, "")
	prep(e, db, id)
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	return firstChildMessage(t, p, "dig")
}

func TestManyIncludesStayWithinBudget(t *testing.T) {
	var items []any
	for i := range subagent.MaxInclude {
		items = append(items, fmt.Sprintf("scratchpad:k%d", i))
	}
	first := seedFor(t, map[string]any{"last_n": 5, "include": items}, func(_ *Engine, db *store.Store, id string) {
		for i := range subagent.MaxInclude {
			if err := db.SetScratch(t.Context(), id, fmt.Sprintf("k%d", i), strings.Repeat("z", 5000)); err != nil {
				t.Fatal(err)
			}
		}
	})
	if len(first) > seedBudget+2000 {
		t.Errorf("seed is %d characters with budget %d", len(first), seedBudget)
	}
	if !strings.Contains(first, "more include items left out to fit") {
		t.Errorf("includes past the budget were dropped without saying so")
	}
	if !strings.Contains(first, "[#1 user] go") {
		t.Errorf("includes crowded out the messages:\n%.300s", first)
	}
}

func TestHugeNewestMessageIsNotDroppedSilently(t *testing.T) {
	first := seedFor(t, map[string]any{"last_n": 10}, func(_ *Engine, db *store.Store, id string) {
		j, err := db.OpenJournal(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = j.Close() }()
		now := time.Now().UTC()
		turn := store.Turn{ID: store.NewID(now), UserMessageID: store.NewID(now)}
		if err := j.StartTurn(turn, now); err != nil {
			t.Fatal(err)
		}
		input := map[string]any{}
		var uses, results []llmtypes.ContentBlock
		for i := range 20 {
			uses = append(uses, llmtypes.ContentBlock{Type: "tool_use", ID: fmt.Sprintf("c%d", i), Name: "bash", Input: &input})
			results = append(results, llmtypes.ContentBlock{Type: "tool_result", ToolUseID: fmt.Sprintf("c%d", i), Content: strings.Repeat("r", 3000)})
		}
		for _, m := range []store.Message{
			{ID: turn.UserMessageID, Role: "user", Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "run 20 things"}}},
			{ID: store.NewID(now), Role: "assistant", Blocks: uses},
			{ID: store.NewID(now), Role: "tool", Blocks: results},
		} {
			m.TurnID, m.CreatedAt = turn.ID, now
			if err := j.AppendMessage(m, nil); err != nil {
				t.Fatal(err)
			}
		}
		turn.Status = "completed"
		if err := j.FinishTurn(turn, now); err != nil {
			t.Fatal(err)
		}
	})
	// The 60,000-character tool message cannot fit, so it and everything
	// older are left out, but the child is told, and the newer "go" is kept.
	if !strings.Contains(first, "[#4 user] go") || !strings.Contains(first, "3 older ones left out to fit") {
		t.Fatalf("seed:\n%.700s", first)
	}
}

// ghKey is a fake key shaped like a GitHub token, built at run time.
var ghKey = "ghp" + "_0123456789abcdefABCDEF0123456789abcd"

func TestToolBlocksRedactedBeforeCut(t *testing.T) {
	history := []store.Message{
		{Seq: 1, Role: "assistant", Blocks: []llmtypes.ContentBlock{{Type: "tool_use", ID: "a", Name: "bash", Input: &map[string]any{"cmd": strings.Repeat("i", 480) + " " + ghKey}}}},
		{Seq: 2, Role: "tool", Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "a", Content: strings.Repeat("c", 1980) + " " + ghKey}}},
	}
	e, _, _ := setupEngine(t, fake.New(), "")
	out := strings.Join(renderMessages(history, nil, seedBlockChars, e.scrub.Text, "parent_history_get"), "")
	for _, part := range []string{"ghp_0123", "sk-ant-api03"} {
		if strings.Contains(out, part) {
			t.Errorf("leaks %q:\n%s", part, out)
		}
	}
}

func TestResultScopeFollowsAncestorsOnly(t *testing.T) {
	e, db, root := setupEngine(t, fake.New(), "")
	now := time.Now().UTC()
	child := func(parent string) store.Conversation {
		t.Helper()
		c := store.Conversation{ID: store.NewID(time.Now()), ParentID: parent, Role: "worker", Status: "active", CreatedAt: now, UpdatedAt: now}
		if err := db.CreateConversation(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct IDs
		return c
	}
	a, b := child(root), child(root)
	lead := child(root)
	grand := child(lead.ID)
	put := func(scope, body string) string {
		t.Helper()
		ptr, err := e.cache.Put(t.Context(), scope, toolresult.Meta{Tool: "bash", CallID: "c"}, body)
		if err != nil {
			t.Fatal(err)
		}
		return ptr.ID
	}
	sibling, rootResult, leadResult := put(a.ID, "sibling A"), put(root, "root output"), put(lead.ID, "lead output")
	if got := (&execution{engine: e, info: b}).resultScope(t.Context(), sibling); got != b.ID {
		t.Errorf("a sibling's result resolved to scope %s", got)
	}
	if got := (&execution{engine: e, info: b}).resultScope(t.Context(), rootResult); got != root {
		t.Errorf("a depth-1 child did not reach the root's result: %s", got)
	}
	g := &execution{engine: e, info: grand}
	if got := g.resultScope(t.Context(), leadResult); got != lead.ID {
		t.Errorf("a depth-2 child did not reach the lead's result: %s", got)
	}
	if got := g.resultScope(t.Context(), rootResult); got != root {
		t.Errorf("a depth-2 child did not reach the root's result: %s", got)
	}
}
