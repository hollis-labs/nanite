// Package subagent defines subtask variants, requests, statuses and the
// concurrency limit for child conversations.
package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
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
	// Context says how much of the parent conversation the child starts
	// with; see ContextSpec.
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
	if _, err := r.Schema(); err != nil {
		return err
	}
	_, err := r.ContextSpec()
	return err
}

// Schema resolves return_spec when it is a JSON Schema object; prose (a JSON
// string) has no schema and yields nil. A malformed schema is an error the
// caller sees before any child starts.
func (r Request) Schema() (*jsonschema.Resolved, error) {
	spec := strings.TrimSpace(string(r.ReturnSpec))
	if !strings.HasPrefix(spec, "{") {
		var prose string
		if err := json.Unmarshal(r.ReturnSpec, &prose); err != nil {
			return nil, fmt.Errorf("subtask return_spec must be prose or a JSON Schema object")
		}
		return nil, nil
	}
	resolved, err := elicitation.ResolveLimited(r.ReturnSpec)
	if err != nil {
		return nil, fmt.Errorf("subtask return_spec is not a valid JSON Schema: %w", err)
	}
	return resolved, nil
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
	b.WriteString("\n\nFinish by calling " + ReturnToolName + " with the result. Only what you pass to it reaches your parent.")
	return b.String()
}

// Return is a child's return_result call.
type Return struct {
	Status    Status          `json:"status"`
	Result    json.RawMessage `json:"result"`
	Artifacts []string        `json:"artifacts,omitempty"`
	Notes     string          `json:"notes,omitempty"`
}

// Status is how a subtask ended.
type Status string

const (
	StatusOK        Status = "ok"
	StatusPartial   Status = "partial"
	StatusLimit     Status = "limit"
	StatusError     Status = "error"
	StatusCancelled Status = "cancelled"
	// StatusDenied: the owner denied an approval the subtask asked for, and
	// the subtask then ended without reaching a limit or an error. Its
	// result is still returned; the reason names the denied tools.
	StatusDenied Status = "denied"
)

// DeniedPrefix starts the recorded reason of a background subtask that ended
// denied, so its job status maps back to StatusDenied.
const DeniedPrefix = "denied: "

// Failed reports whether the parent model must treat the result as an error.
func (s Status) Failed() bool { return s != StatusOK }

// AllowsTool reports whether a conversation offers a tool. A root chat offers
// everything. A child offers a tool only when every variant in chain allows
// it: its own and, at depth 2, the lead's that started it, so a child never
// has a tool its parent lacked. The subtask tool also needs canSpawn.
func AllowsTool(root bool, chain []Variant, canSpawn bool, name string, readOnly bool) bool {
	if root {
		return true
	}
	if name == "ask_user" {
		return false
	}
	switch name {
	case ReturnToolName:
		// Every child must be able to end its subtask, whatever its variant
		// narrows.
		return true
	case "fetch_tool_result", "search_tool_result":
		// A child reads the pointers its tools and subtasks hand back, so an
		// allow list need not name these; an explicit deny still removes
		// them.
		for _, v := range chain {
			if matchAny(v.Deny, name) {
				return false
			}
		}
		return true
	}
	for _, v := range chain {
		if !v.Allows(name, readOnly) {
			return false
		}
	}
	return name != ToolName || canSpawn
}

// StatusOf maps a finished background subtask job's status and reason to
// the subtask vocabulary; "running" while it runs.
func StatusOf(job, reason string) string {
	switch job {
	case "succeeded":
		if strings.HasPrefix(reason, DeniedPrefix) {
			return string(StatusDenied)
		}
		if reason != "" {
			return string(StatusPartial)
		}
		return string(StatusOK)
	case "limit":
		return string(StatusLimit)
	case "cancelled":
		return string(StatusCancelled)
	case "failed", "lost":
		return string(StatusError)
	}
	return job
}

// ToolName is the subtask tool's name.
const ToolName = "subtask"

// ReturnToolName is the tool a child calls to end its subtask with a result.
const ReturnToolName = "return_result"

// DefaultResultCap is the result size cap, in tokens, when neither the
// variant nor the agent sets one.
const DefaultResultCap = 6000

// BytesPerToken converts a token cap to bytes of result JSON.
const BytesPerToken = 4

// ResultCapBytes is the configured cap on a child's whole result, in bytes,
// never more than maxBytes (when positive):
// the variant's result_cap, else the agent's, else DefaultResultCap. The
// engine clamps it to what one tool result can carry.
func ResultCapBytes(variant Variant, agent, maxBytes int) int {
	tokens := DefaultResultCap
	switch {
	case variant.ResultCap > 0:
		tokens = variant.ResultCap
	case agent > 0:
		tokens = agent
	}
	// Clamp in tokens first, so a huge setting cannot overflow the product.
	if maxBytes > 0 {
		tokens = min(tokens, maxBytes/BytesPerToken)
	}
	return tokens * BytesPerToken
}

// CheckLimit refuses a subtask limit the agent cannot run; zero is the
// default.
func CheckLimit(n int) error {
	if n < 0 || n > MaxLimit {
		return fmt.Errorf("subtask_limit %d is out of range: it must be between 0 (the default, %d) and %d", n, DefaultLimit, MaxLimit)
	}
	return nil
}

// DefaultLimit is how many subtasks may run at once on one agent when the
// host sets no limit; MaxLimit is the most a host may set.
const (
	DefaultLimit = 4
	MaxLimit     = 8
)

// Limit bounds how many subtasks of one kind run at once across the whole
// agent: every conversation and background subtasks included. The engine
// keeps two: one for leads (variants that may start subtasks) and one for
// every other subtask, so a lead never holds a slot its own children need.
type Limit chan struct{}

// NewLimit returns a limit of n subtasks at once; n <= 0 is DefaultLimit and
// n above MaxLimit is MaxLimit.
func NewLimit(n int) Limit {
	switch {
	case n <= 0:
		n = DefaultLimit
	case n > MaxLimit:
		n = MaxLimit
	}
	return make(Limit, n)
}

// Acquire takes a slot, waiting for one while ctx lasts. Waiting cannot
// deadlock: a subtask waiting for a slot of its kind holds none, and a lead
// holds a lead slot only while its workers wait on the other limit.
func (l Limit) Acquire(ctx context.Context) (func(), error) {
	select {
	case l <- struct{}{}:
		return sync.OnceFunc(func() { <-l }), nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for one of the %d subtask slots: %w", cap(l), context.Cause(ctx))
	}
}

// TryAcquire takes a slot or refuses at once, for a background subtask,
// which must not hold up the turn that starts it.
func (l Limit) TryAcquire() (func(), error) {
	select {
	case l <- struct{}{}:
		return sync.OnceFunc(func() { <-l }), nil
	default:
		return nil, fmt.Errorf("%s already running, the most this agent runs at once; this background subtask did not start. Wait for one to finish", subtaskCount(cap(l)))
	}
}

func subtaskCount(n int) string {
	if n == 1 {
		return "1 subtask is"
	}
	return fmt.Sprintf("%d subtasks are", n)
}
