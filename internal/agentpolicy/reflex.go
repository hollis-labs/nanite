package agentpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	shared "github.com/hollis-labs/substrate/agent/reflexes"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// ReflexBundle is one pinned declarative file. It holds no mutable enablement,
// recurrence counters, ownership assertions, credentials or runtime identifiers.
type ReflexBundle struct {
	Version string       `json:"version"`
	Rules   []ReflexRule `json:"rules"`
}
type ReflexRule struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Priority int           `json:"priority"`
	Trigger  ReflexTrigger `json:"trigger"`
	Action   ReflexAction  `json:"action"`
}
type ReflexTrigger struct {
	Kind string          `json:"kind"`
	Spec json.RawMessage `json:"spec"`
}
type ReflexAction struct {
	Kind     string `json:"kind"`
	Body     string `json:"body,omitempty"`
	Reason   string `json:"reason,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

// ParseReflexBundle verifies exact file bytes before decoding behavior. Hosts
// resolve URI custody; this function has no path, network or process authority.
func ParseReflexBundle(data []byte, pin agentdef.Ref) (ReflexBundle, error) {
	var b ReflexBundle
	if len(data) > 1<<20 {
		return b, errors.New("reflex bundle exceeds 1 MiB")
	}
	if agentdef.ArtifactDigest(data) != pin.Digest {
		return b, errors.New("reflex bundle digest mismatch")
	}
	if err := decode(data, &b); err != nil {
		return b, err
	}
	if b.Version != Version {
		return b, errors.New("unsupported reflex bundle version")
	}
	if len(b.Rules) == 0 || len(b.Rules) > 128 {
		return b, errors.New("reflex bundle requires one to 128 rules")
	}
	seen := map[string]bool{}
	for _, rule := range b.Rules {
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Name) == "" || seen[rule.ID] {
			return b, errors.New("reflex rule requires unique id and name")
		}
		seen[rule.ID] = true
		if err := rule.Trigger.Validate(); err != nil {
			return b, fmt.Errorf("reflex %s: %w", rule.ID, err)
		}
		if err := rule.Action.Validate(); err != nil {
			return b, fmt.Errorf("reflex %s: %w", rule.ID, err)
		}
	}
	return b, nil
}

func (t ReflexTrigger) Validate() error {
	switch t.Kind {
	case "event":
		var s struct {
			Name string `json:"name"`
		}
		if err := decode(t.Spec, &s); err != nil {
			return err
		}
		if strings.TrimSpace(s.Name) == "" {
			return errors.New("event name is required")
		}
	case "interval":
		var s struct {
			EveryNTicks int `json:"every_n_ticks"`
		}
		if err := decode(t.Spec, &s); err != nil {
			return err
		}
		if s.EveryNTicks <= 0 {
			return errors.New("interval must be positive")
		}
	case "predicate":
		var s map[string]any
		if err := decode(t.Spec, &s); err != nil {
			return err
		}
		return validatePredicate(s, 0)
	default:
		return fmt.Errorf("unsupported reflex trigger %q", t.Kind)
	}
	return nil
}

func (a ReflexAction) Validate() error {
	switch a.Kind {
	case shared.ActionInjectReminder:
		if strings.TrimSpace(a.Body) == "" || a.Reason != "" || a.ToolName != "" {
			return errors.New("inject_reminder requires only body")
		}
	case shared.ActionHaltSession:
		if strings.TrimSpace(a.Reason) == "" || a.Body != "" || a.ToolName != "" {
			return errors.New("halt_session requires only reason")
		}
	case shared.ActionForceToolChoice:
		if strings.TrimSpace(a.ToolName) == "" || a.Body != "" || a.Reason != "" {
			return errors.New("force_tool_choice requires only tool_name")
		}
	default:
		return fmt.Errorf("unsupported intrinsic reflex action %q", a.Kind)
	}
	return nil
}

func validatePredicate(n map[string]any, depth int) error {
	if depth > 16 {
		return errors.New("predicate nesting exceeds 16")
	}
	if err := nonNull(n); err != nil {
		return err
	}
	kind, _ := n["kind"].(string)
	allowed := map[string]bool{"kind": true}
	allow := func(keys ...string) {
		for _, key := range keys {
			allowed[key] = true
		}
	}
	switch kind {
	case "AND", "OR":
		allow("clauses")
		clauses, ok := n["clauses"].([]any)
		if !ok || len(clauses) == 0 || len(clauses) > 128 {
			return errors.New("boolean predicate requires bounded nonempty clauses")
		}
		for _, clause := range clauses {
			child, ok := clause.(map[string]any)
			if !ok {
				return errors.New("predicate clause must be an object")
			}
			if err := validatePredicate(child, depth+1); err != nil {
				return err
			}
		}
	case "tool_calls_window", "cache_read_window", "input_tokens_window":
		allow("window", "op", "value")
	case "mail_unread_count":
		allow("op", "value")
	case "identical_output_window":
		allow("window")
	case "output_growth_window":
		allow("window", "factor")
	case "prefix_pressure":
		allow("ratio", "context_window")
	case "regex_match_window", "user_regex_window", "text_regex_window":
		allow("window", "pattern")
		if kind == "text_regex_window" {
			allow("scope")
		}
		p, ok := n["pattern"].(string)
		if !ok || p == "" {
			return errors.New("regex pattern is required")
		}
		if _, err := regexp.Compile(p); err != nil {
			return err
		}
	case "scope_tier", "execution_pattern":
		allow("op", "value")
		if v, ok := n["value"].(string); !ok || v == "" {
			return errors.New("string predicate value is required")
		}
	default:
		return fmt.Errorf("unsupported intrinsic predicate %q", kind)
	}
	for key, value := range n {
		if !allowed[key] {
			return fmt.Errorf("unknown predicate field %q", key)
		}
		switch key {
		case "window", "context_window":
			v, ok := value.(float64)
			if !ok || v <= 0 || math.Trunc(v) != v || v > 1e9 {
				return fmt.Errorf("%s must be a bounded positive integer", key)
			}
		case "value":
			if kind != "scope_tier" && kind != "execution_pattern" {
				v, ok := value.(float64)
				if !ok || v < 0 || math.Trunc(v) != v || v > 1e9 {
					return errors.New("numeric value must be a bounded nonnegative integer")
				}
			}
		case "factor", "ratio":
			v, ok := value.(float64)
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
				return fmt.Errorf("%s must be finite and positive", key)
			}
		case "op":
			v, ok := value.(string)
			if !ok || !oneOf(v, "=", "==", "!=", "<", ">", "<=", ">=") {
				return errors.New("unsupported predicate operator")
			}
			if (kind == "scope_tier" || kind == "execution_pattern") && !oneOf(v, "=", "==", "!=") {
				return errors.New("unsupported string predicate operator")
			}
		case "scope":
			v, ok := value.(string)
			if !ok || !oneOf(v, "user", "assistant", "all") {
				return errors.New("unsupported text scope")
			}
		}
	}
	return nil
}

// Evaluate applies the published reflex evaluator and returns behavior requests.
// Effects remain host-owned. In particular a force-tool request cannot select an
// ungranted tool, and halting requires the caller's current run ownership.
func (b ReflexBundle) Evaluate(ctx context.Context, state shared.State) ([]ReflexRule, error) {
	var fired []ReflexRule
	for _, rule := range b.Rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ok, err := shared.EvaluateTrigger(rule.Trigger.Kind, string(rule.Trigger.Spec), state)
		if err != nil {
			return nil, err
		}
		if ok {
			fired = append(fired, rule)
		}
	}
	sort.SliceStable(fired, func(i, j int) bool {
		if fired[i].Priority != fired[j].Priority {
			return fired[i].Priority > fired[j].Priority
		}
		return fired[i].ID < fired[j].ID
	})
	// Retain deny-overrides: the highest-priority halt suppresses softer requests.
	for _, rule := range fired {
		if rule.Action.Kind == shared.ActionHaltSession {
			return []ReflexRule{rule}, nil
		}
	}
	return fired, nil
}
