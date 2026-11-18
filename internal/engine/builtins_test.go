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
	got := offered(p.requests[0])
	// The test engine has no workspace root, so bash and background_start are absent here.
	for _, name := range []string{"ask_user", "background_status", "edit_file", "read_file", "skill_get", "subagent_spawn", "todo_write"} {
		if !slices.Contains(got, name) {
			t.Errorf("%s missing from the default tool list %v", name, got)
		}
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
