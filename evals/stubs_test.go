package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// stubScenario warms up with one stub call in a prelude turn, then asks
// about two orders in one step (the calls run at once) and checks a status
// stub three times. Its script answers with every tool result it saw, in
// order, errors marked with "!".
func stubScenario(t *testing.T) Scenario {
	t.Helper()
	files := fstest.MapFS{
		"task.md":     {Data: []byte("What is the status of orders A1 and A2, and is the service up? End with ANSWER: <results>.")},
		"prelude.txt": {Data: []byte("Warm up.\n")},
	}
	for _, name := range []string{"agent.md", "host.yaml"} {
		data, err := fs.ReadFile(codeSearch().Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	answer := Step{decide: func(req llmtypes.ChatRequest) Step {
		i := slices.IndexFunc(req.Tools, func(tool llmtypes.ToolDefinition) bool { return tool.Name == "service_status" })
		if i < 0 || !slices.ContainsFunc(req.Tools, func(tool llmtypes.ToolDefinition) bool { return tool.Name == "lookup_order" }) {
			return Say("ANSWER: a stub was not offered")
		}
		if d, s := req.Tools[i].Description, req.Tools[i].InputSchema; d != "Service status." || s["type"] != "object" {
			return Say(fmt.Sprintf("ANSWER: service_status offered as %q %v", d, s))
		}
		var seen []string
		for _, m := range req.Messages {
			for _, b := range m.ContentBlocks {
				if b.Type == "tool_result" {
					mark := ""
					if b.IsError {
						mark = "!"
					}
					seen = append(seen, b.Content+mark)
				}
			}
		}
		return Say("ANSWER: " + strings.Join(seen, "|"))
	}}
	return Scenario{
		Name:  "orders",
		Files: files,
		Stubs: []Stub{{
			Name: "lookup_order", Description: "Look up an order by id.", ReadOnly: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
			Results: []StubResult{{When: map[string]any{"id": "A0"}, Text: "A0 new"},
				{When: map[string]any{"id": "A1"}, Text: "A1 shipped"}, {When: map[string]any{"id": "A2"}, Text: "A2 delayed"}},
		}, {
			Name: "service_status", Description: "Service status.", ReadOnly: true,
			Results: []StubResult{{Text: "up"}, {Text: "down", IsError: true}},
		}},
		Grade: func(answer, _ string) (bool, string) {
			if strings.Contains(answer, "ANSWER: A0 new|A1 shipped|A2 delayed|up|down!|down!") {
				return true, ""
			}
			return false, "answer: " + answer
		},
		Script: func(Arm) Script {
			return Script{Root: []Step{
				Calls(Call("lookup_order", "id", "A0")), Say("Ready."),
				Calls(Call("lookup_order", "id", "A1"), Call("lookup_order", "id", "A2")),
				Calls(Call("service_status", "try", 1)), Calls(Call("service_status", "try", 2)), Calls(Call("service_status", "try", 3)),
				answer,
			}}
		},
	}
}

// TestStubToolsAnswerWithTheirResults: the agent is offered the stubs as
// host tools with their descriptions and schemas; calls made at once get the
// result matched to their input; ordered results repeat the last; error
// results reach the model as errors; and the report counts the task turn's
// calls, not the prelude's. Every run has its own stub state.
func TestStubToolsAnswerWithTheirResults(t *testing.T) {
	for range 5 {
		rep, err := Run(context.Background(), Config{Parallel: 2}, []Scenario{stubScenario(t)})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Results) != 2 {
			t.Fatalf("results %+v", rep.Results)
		}
		for _, r := range rep.Results {
			if !r.Success || r.StubCalls["lookup_order"] != 2 || r.StubCalls["service_status"] != 3 {
				t.Fatalf("%s: success %v (%s), stub calls %v", r.Arm, r.Success, r.Why, r.StubCalls)
			}
		}
	}
}

func TestBadStubsAreRefused(t *testing.T) {
	ok := []StubResult{{Text: "x"}}
	for want, stubs := range map[string][]Stub{
		"stub name \"\"":           {{Results: ok}},
		"stub name \"bad name.x\"": {{Name: "bad name.x", Results: ok}},
		"reserved or duplicated":   {{Name: "a", Results: ok}, {Name: "a", Results: ok}},
		"no results":               {{Name: "a"}},
		"JSON Schema object":       {{Name: "a", Schema: json.RawMessage(`{`), Results: ok}},
		"JSON Schema object ":      {{Name: "b", Schema: json.RawMessage(`[1]`), Results: ok}},
		"reserved":                 {{Name: "read_file", Results: ok}},
		"reserved ":                {{Name: "mcp__fs__read", Results: ok}},
	} {
		s := stubScenario(t)
		s.Stubs = stubs
		rep, err := Run(context.Background(), Config{}, []Scenario{s})
		if err == nil || !strings.Contains(err.Error(), strings.TrimSpace(want)) || len(rep.Results) != 0 {
			t.Errorf("%s: %v (%d results)", want, err, len(rep.Results))
		}
	}
}

func TestMatches(t *testing.T) {
	in := map[string]any{"id": "A1", "n": float64(2), "tags": []any{"x"}}
	for when, want := range map[string]bool{
		`{"id":"A1"}`: true, `{"n":2}`: true, `{"tags":["x"]}`: true, `{"id":"A2"}`: false, `{"missing":1}`: false,
	} {
		var w map[string]any
		_ = json.Unmarshal([]byte(when), &w)
		if got := matches(w, in); got != want {
			t.Errorf("%s: %v", when, got)
		}
	}
}
