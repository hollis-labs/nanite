package subagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/agent/authored"
)

// DefaultLastN is how many parent messages a child starts with when neither
// the call nor its variant says.
const DefaultLastN = 6

// MaxInclude bounds how many context.include items one subtask may ask for.
const MaxInclude = 8

// ContextSpec is a subtask's context argument: what the child starts with
// from its parent conversation.
type ContextSpec struct {
	// LastN is how many of the parent's latest messages the child sees. Nil
	// defers to the variant, then DefaultLastN; 0 means none.
	LastN *int `json:"last_n,omitempty"`
	// Include names parent state to copy in: plan, todos, handoff or
	// scratchpad:<key>.
	Include []string `json:"include,omitempty"`
	// Full copies the parent's whole history instead of the last N messages.
	Full bool `json:"full,omitempty"`
}

// ContextSpec parses and checks the request's context argument. An absent
// argument is the zero spec.
func (r Request) ContextSpec() (ContextSpec, error) {
	var spec ContextSpec
	raw := bytes.TrimSpace(r.Context)
	if len(raw) == 0 || string(raw) == "null" {
		return spec, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return spec, fmt.Errorf("subtask context: %w", err)
	}
	if spec.LastN != nil && (*spec.LastN < 0 || *spec.LastN > authored.MaxLastN) {
		return spec, fmt.Errorf("subtask context.last_n must be between 0 and %d; use context.full for the whole history", authored.MaxLastN)
	}
	if len(spec.Include) > MaxInclude {
		return spec, fmt.Errorf("subtask context.include takes at most %d items", MaxInclude)
	}
	for _, item := range spec.Include {
		if _, ok := IncludeKey(item); !ok {
			return spec, fmt.Errorf("subtask context.include item %q: use plan, todos, handoff or scratchpad:<key>", item)
		}
	}
	return spec, nil
}

// Count is how many parent messages the child starts with: the call's
// last_n, else the variant's, else DefaultLastN.
func (s ContextSpec) Count(v Variant) int {
	switch {
	case s.LastN != nil:
		return *s.LastN
	case v.LastN > 0:
		return v.LastN
	default:
		return DefaultLastN
	}
}

// IncludeKey checks one include item. For scratchpad:<key> it returns the
// key; for the others, the item itself.
func IncludeKey(item string) (string, bool) {
	switch item {
	case "plan", "todos", "handoff":
		return item, true
	}
	if key, ok := strings.CutPrefix(item, "scratchpad:"); ok && strings.TrimSpace(key) != "" {
		return key, true
	}
	return "", false
}
