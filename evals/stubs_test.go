package evals

import (
	"context"
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// stubScenario asks about two orders through a stub tool; its script answers
// with what the second call returned, and checks the stub was offered.
func stubScenario(t *testing.T) Scenario {
	t.Helper()
	files := fstest.MapFS{"task.md": {Data: []byte("What is the status of orders A1 and A2? End with ANSWER: <status of A2>.")}}
	for _, name := range []string{"agent.md", "host.yaml"} {
		data, err := fs.ReadFile(codeSearch().Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	answer := Step{decide: func(req llmtypes.ChatRequest) Step {
		if !slices.ContainsFunc(req.Tools, func(tool llmtypes.ToolDefinition) bool { return tool.Name == "lookup_order" }) {
			return Say("ANSWER: the stub was not offered")
		}
		last := req.Messages[len(req.Messages)-1]
		for _, b := range last.ContentBlocks {
			if b.Type == "tool_result" {
				return Say("ANSWER: " + b.Content)
			}
		}
		return Say("ANSWER: no tool result")
	}}
	return Scenario{
		Name:  "orders",
		Files: files,
		Stubs: []Stub{{
			Name: "lookup_order", Description: "Look up an order by id.", ReadOnly: true,
			Schema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
			Results: []StubResult{{Text: "A1 shipped"}, {Text: "A2 delayed"}},
		}},
		Grade: func(answer, _ string) (bool, string) {
			if strings.Contains(answer, "ANSWER: A2 delayed") {
				return true, ""
			}
			return false, "answer: " + answer
		},
		Script: func(Arm) Script {
			return Script{Root: []Step{Calls(Call("lookup_order", "id", "A1")), Calls(Call("lookup_order", "id", "A2")), answer}}
		},
	}
}

// TestStubToolsAnswerWithTheirResults: the agent is offered the scenario's
// stub as a host tool, each call gets the next canned result, and the report
// counts the calls. Every run starts from the first result.
func TestStubToolsAnswerWithTheirResults(t *testing.T) {
	rep, err := Run(context.Background(), Config{Parallel: 2}, []Scenario{stubScenario(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("results %+v", rep.Results)
	}
	for _, r := range rep.Results {
		if !r.Success || r.StubCalls["lookup_order"] != 2 {
			t.Errorf("%s: success %v (%s), stub calls %v", r.Arm, r.Success, r.Why, r.StubCalls)
		}
	}
}

func TestBadStubsAreRefused(t *testing.T) {
	for want, stubs := range map[string][]Stub{
		"needs a name":   {{Results: []StubResult{{Text: "x"}}}},
		"declared twice": {{Name: "a", Results: []StubResult{{Text: "x"}}}, {Name: "a", Results: []StubResult{{Text: "y"}}}},
		"no results":     {{Name: "a"}},
		"not valid JSON": {{Name: "a", Schema: json.RawMessage(`{`), Results: []StubResult{{Text: "x"}}}},
	} {
		s := stubScenario(t)
		s.Stubs = stubs
		if _, err := Run(context.Background(), Config{}, []Scenario{s}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}
