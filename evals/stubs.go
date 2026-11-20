package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"sync"

	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/tools"
)

// StubGroup is the host tool group a run's stub tools are registered under.
const StubGroup = "eval-stubs"

// Stub is a host tool a scenario gives the agent in place of a real one: the
// model sees its name, description and input schema like any host tool, and
// each call gets a canned result. Stubs let a case exercise an agent that
// depends on host tools without calling them. An MCP tool cannot be stubbed
// under its mcp__ name, which is reserved; give the stub a plain name.
type Stub struct {
	// Name is the tool name: letters, digits, _ and -, at most 64, and not
	// a built-in or reserved name.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Schema is the JSON Schema object of the tool's input; empty is an
	// object with no declared properties.
	Schema json.RawMessage `json:"schema,omitempty"`
	// ReadOnly marks the tool read-only, as a host tool's ReadOnly does: a
	// read-only variant is offered it, and read-only calls in one model step
	// run at once. Whether a call asks for approval depends on the agent's
	// permission mode and rules, as for any host tool.
	ReadOnly bool `json:"read_only,omitempty"`
	// Results answer the calls. A call gets the first result whose When
	// matches its input; otherwise the next result without a When, in order,
	// the last of them repeating. Calls made at once in one model step may
	// run in any order, so give such calls a When to keep a run
	// deterministic. At least one result is required.
	Results []StubResult `json:"results"`
}

// StubResult is one canned answer.
type StubResult struct {
	// When, if set, answers only calls whose input has each of these fields
	// with an equal JSON value.
	When    map[string]any `json:"when,omitempty"`
	Text    string         `json:"text"`
	IsError bool           `json:"is_error,omitempty"`
}

var stubName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// checkStubs reports what is wrong with a scenario's stubs, applying the same
// rules the agent applies to host tools.
func checkStubs(stubs []Stub) error {
	if len(stubs) == 0 {
		return nil
	}
	for _, s := range stubs {
		switch {
		case !stubName.MatchString(s.Name):
			return fmt.Errorf("evals: stub name %q: use 1-64 letters, digits, _ or -", s.Name)
		case len(s.Results) == 0:
			return fmt.Errorf("evals: stub %q has no results", s.Name)
		}
	}
	var g tools.Groups
	if err := g.SetGroup(StubGroup, (&stubCalls{}).tools(stubs)...); err != nil {
		return errors.New("evals: stubs: " + err.Error())
	}
	return nil
}

// stubCalls holds one run's stub state: each stub's position in its ordered
// results and its call count.
type stubCalls struct {
	mu   sync.Mutex
	next map[string]int
	n    map[string]int
}

// tools are the run's stub tools.
func (c *stubCalls) tools(stubs []Stub) []agent.Tool {
	c.next, c.n = map[string]int{}, map[string]int{}
	out := make([]agent.Tool, 0, len(stubs))
	for _, s := range stubs {
		schema := s.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, agent.Tool{Name: s.Name, Description: s.Description, InputSchema: schema, ReadOnly: s.ReadOnly,
			Handler: func(_ context.Context, call agent.ToolCall) (agent.ToolResult, error) {
				r := c.answer(s, call.Input)
				return agent.ToolResult{Text: r.Text, IsError: r.IsError}, nil
			}})
	}
	return out
}

// answer picks the result for one call and counts it.
func (c *stubCalls) answer(s Stub, input json.RawMessage) StubResult {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n[s.Name]++
	for _, r := range s.Results {
		if r.When != nil && matches(r.When, in) {
			return r
		}
	}
	var ordered []StubResult
	for _, r := range s.Results {
		if r.When == nil {
			ordered = append(ordered, r)
		}
	}
	if len(ordered) == 0 {
		return StubResult{Text: fmt.Sprintf("stub %s has no result for this input", s.Name), IsError: true}
	}
	i := c.next[s.Name]
	c.next[s.Name] = i + 1
	return ordered[min(i, len(ordered)-1)]
}

// matches reports whether input has each of when's fields with an equal JSON
// value.
func matches(when, input map[string]any) bool {
	for k, want := range when {
		got, ok := input[k]
		if !ok {
			return false
		}
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(got)
		var x, y any
		_ = json.Unmarshal(a, &x)
		_ = json.Unmarshal(b, &y)
		if !reflect.DeepEqual(x, y) {
			return false
		}
	}
	return true
}

// reset forgets the calls so far (the prelude's), keeping the positions.
func (c *stubCalls) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n = map[string]int{}
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
