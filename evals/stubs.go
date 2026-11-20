package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/hollis-labs/nanite/agent"
)

// StubGroup is the host tool group a run's stub tools are registered under.
const StubGroup = "eval-stubs"

// Stub is a host tool a scenario gives the agent in place of a real one: the
// model sees its name, description and input schema like any host tool, and
// each call gets the next canned result. Stubs let a case exercise an agent
// that depends on host or MCP tools without calling them.
type Stub struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Schema is the JSON Schema of the tool's input; empty is an object with
	// no declared properties.
	Schema json.RawMessage `json:"schema,omitempty"`
	// ReadOnly marks the tool read-only. A tool that is not read-only asks
	// for approval, which the harness grants and counts.
	ReadOnly bool `json:"read_only,omitempty"`
	// Results are returned in order, one a call; once they run out, the last
	// is returned again. At least one is required.
	Results []StubResult `json:"results"`
}

// StubResult is one canned answer.
type StubResult struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error,omitempty"`
}

// check reports what is wrong with the scenario's stubs.
func checkStubs(stubs []Stub) error {
	seen := map[string]bool{}
	for _, s := range stubs {
		switch {
		case s.Name == "":
			return errors.New("evals: a stub needs a name")
		case seen[s.Name]:
			return fmt.Errorf("evals: stub %q is declared twice", s.Name)
		case len(s.Results) == 0:
			return fmt.Errorf("evals: stub %q has no results", s.Name)
		case len(s.Schema) > 0 && !json.Valid(s.Schema):
			return fmt.Errorf("evals: stub %q: schema is not valid JSON", s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}

// stubCalls counts one run's calls to its stubs.
type stubCalls struct {
	mu sync.Mutex
	n  map[string]int
}

// tools are the run's stub tools, each with its own position in its results.
func (c *stubCalls) tools(stubs []Stub) []agent.Tool {
	c.n = map[string]int{}
	out := make([]agent.Tool, 0, len(stubs))
	for _, s := range stubs {
		schema := s.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, agent.Tool{Name: s.Name, Description: s.Description, InputSchema: schema, ReadOnly: s.ReadOnly,
			Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
				c.mu.Lock()
				i := c.n[s.Name]
				c.n[s.Name] = i + 1
				c.mu.Unlock()
				r := s.Results[min(i, len(s.Results)-1)]
				return agent.ToolResult{Text: r.Text, IsError: r.IsError}, nil
			}})
	}
	return out
}

// counts is how many times each stub was called; nil when none was.
func (c *stubCalls) counts() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int{}
	for _, name := range slices.Sorted(maps.Keys(c.n)) {
		if c.n[name] > 0 {
			out[name] = c.n[name]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
