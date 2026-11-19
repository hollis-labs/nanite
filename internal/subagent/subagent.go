// Package subagent defines subtask variants, requests, statuses and the
// concurrency limit for child conversations.
package subagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// MaxDepth is the deepest a child conversation may run. A root chat is depth
// 0 and its subtasks depth 1; only a variant with can_spawn may start depth 2.
const MaxDepth = 2

// Request is one subtask call.
type Request struct {
	Goal       string          `json:"goal"`
	ReturnSpec json.RawMessage `json:"return_spec"`
	Variant    string          `json:"variant"`
	Inputs     json.RawMessage `json:"inputs,omitempty"`
	// Context is recorded but not yet applied; forked context is B2.
	Context    json.RawMessage `json:"context,omitempty"`
	Background bool            `json:"background,omitempty"`
}

// Validate checks the fields every subtask needs. The variant must be one
// of variants; there is no default.
func (r Request) Validate(variants map[string]Variant) error {
	if strings.TrimSpace(r.Goal) == "" {
		return fmt.Errorf("subtask goal required")
	}
	if strings.TrimSpace(r.Variant) == "" {
		return fmt.Errorf("subtask variant required: choose one of %s", names(variants))
	}
	if _, ok := variants[r.Variant]; !ok {
		return fmt.Errorf("unknown subtask variant %q: choose one of %s", r.Variant, names(variants))
	}
	if spec := strings.TrimSpace(string(r.ReturnSpec)); spec == "" || spec == "null" || spec == `""` {
		return fmt.Errorf("subtask return_spec required")
	}
	return nil
}

// Message is the child's first user message: the goal, its inputs and what
// it must return.
func (r Request) Message() string {
	var b strings.Builder
	b.WriteString("Goal: ")
	b.WriteString(strings.TrimSpace(r.Goal))
	if in := strings.TrimSpace(string(r.Inputs)); in != "" && in != "null" && in != "{}" {
		b.WriteString("\n\nInputs:\n")
		b.WriteString(in)
	}
	b.WriteString("\n\nReturn: ")
	var prose string
	if json.Unmarshal(r.ReturnSpec, &prose) == nil {
		b.WriteString(prose)
	} else {
		b.WriteString("a result matching this JSON Schema:\n")
		b.WriteString(string(r.ReturnSpec))
	}
	return b.String()
}

// Status is how a subtask ended.
type Status string

const (
	StatusOK        Status = "ok"
	StatusPartial   Status = "partial"
	StatusLimit     Status = "limit"
	StatusError     Status = "error"
	StatusCancelled Status = "cancelled"
)

// Failed reports whether the parent model must treat the result as an error.
func (s Status) Failed() bool { return s != StatusOK }

// AllowsTool reports whether a conversation offers a tool. A root chat offers
// everything; a child offers what its variant allows. canSpawn is whether this
// child may run subtask itself.
func AllowsTool(root bool, v Variant, canSpawn bool, name string, readOnly bool) bool {
	if root {
		return true
	}
	if name == "ask_user" {
		return false
	}
	if name == ToolName {
		return canSpawn
	}
	return v.Allows(name, readOnly)
}

// ToolName is the subtask tool's name.
const ToolName = "subtask"

type Limit chan struct{}

func NewLimit() Limit { return make(Limit, 4) }
func (l Limit) Acquire() (func(), error) {
	select {
	case l <- struct{}{}:
		return sync.OnceFunc(func() { <-l }), nil
	default:
		return nil, fmt.Errorf("four subtasks are already running")
	}
}
