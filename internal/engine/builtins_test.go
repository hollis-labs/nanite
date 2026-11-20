package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/tools"
)

func offered(req llmtypes.ChatRequest) []string {
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestWithheldBuiltinsAreNotOffered(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("one"), fake.Text("two"))}
	e, _, id := setupEngine(t, p, "")
	for _, groups := range [][]tools.BuiltinGroup{{tools.BuiltinFilesRead}, {}} {
		set, err := tools.NewBuiltins(groups)
		if err != nil {
			t.Fatal(err)
		}
		e.cfg.Builtins = set
		run, err := e.Send(t.Context(), id, Input{Text: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	always := []string{"fetch_tool_result", "search_tool_result"}
	want := slices.Sorted(slices.Values(append([]string{"find_files", "list_dir", "read_file", "search_files"}, always...)))
	if got := offered(p.requests[0]); !slices.Equal(got, want) {
		t.Fatalf("read-only agent was offered %v, want %v", got, want)
	}
	if got := offered(p.requests[1]); !slices.Equal(got, always) {
		t.Fatalf("agent with no groups was offered %v, want %v", got, always)
	}
	for _, block := range p.requests[1].SlotBlocks {
		if block.Name == "skills" && strings.TrimSpace(block.Content) != "" {
			t.Fatalf("skills were listed without the skill tools: %q", block.Content)
		}
	}
}

func TestNilBuiltinsOfferEverything(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("one"))}
	e, _, id := setupEngine(t, p, "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	// The complete default list. The test engine has no workspace root, so
	// bash and background_start, which need one, are absent here.
	want := strings.Fields("ask_user background_cancel background_status edit_file fetch_tool_result find_files history_get history_search list_dir read_file " +
		"scratchpad_read scratchpad_write search_files search_tool_result skill_get skill_list " +
		"subtask todo_list todo_write tool_validate write_file")
	if got := offered(p.requests[0]); !slices.Equal(got, want) {
		t.Fatalf("default tool list changed:\n got %v\nwant %v", got, want)
	}
}

func TestUnknownBuiltinGroupIsRejected(t *testing.T) {
	if _, err := tools.NewBuiltins([]tools.BuiltinGroup{"everything"}); err == nil {
		t.Fatal("unknown group accepted")
	}
	// Every grouped tool name must be one the engine treats as built in.
	for _, group := range tools.AllBuiltins() {
		set, err := tools.NewBuiltins([]tools.BuiltinGroup{group})
		if err != nil || !set.Has(group) {
			t.Fatalf("%s: %v", group, err)
		}
	}
}

func TestCallToWithheldBuiltinIsRefused(t *testing.T) {
	call := fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "call-1", Name: "scratchpad_write", Input: map[string]any{"key": "k", "content": "should not be stored"}}},
		{Type: llmtypes.EventDone},
	}}
	p := &watchedProvider{Provider: fake.New(call, fake.Text("done"))}
	e, db, id := setupEngine(t, p, "")
	set, err := tools.NewBuiltins([]tools.BuiltinGroup{tools.BuiltinFilesRead})
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.Builtins = set
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 2 {
		t.Fatalf("model was called %d times", len(p.requests))
	}
	messages := p.requests[1].Messages
	result := messages[len(messages)-1].ContentBlocks[0]
	if result.Type != "tool_result" || !result.IsError || result.Content != "tool unavailable: scratchpad_write" {
		t.Fatalf("withheld tool call answered with %+v", result)
	}
	if stored, _ := db.Scratch(t.Context(), id, "k"); stored != "" {
		t.Fatalf("withheld tool ran and stored %q", stored)
	}
}
