package reflexes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// EvaluateTrigger parses a trigger_spec JSON blob and evaluates it
// against a State. Returns true when the reflex should fire. Errors
// are non-fatal at the call site — a malformed trigger should not
// halt the monitor loop; the caller logs and treats as "not fired."
func EvaluateTrigger(triggerKind, triggerSpec string, state State) (bool, error) {
	switch triggerKind {
	case "predicate":
		var spec map[string]interface{}
		if err := json.Unmarshal([]byte(triggerSpec), &spec); err != nil {
			return false, fmt.Errorf("parse predicate spec: %w", err)
		}
		return evalPredicateNode(spec, state)
	case "event":
		var spec struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(triggerSpec), &spec); err != nil {
			return false, fmt.Errorf("parse event spec: %w", err)
		}
		return evalEvent(spec.Name, state), nil
	case "interval":
		var spec struct {
			EveryNTicks int `json:"every_n_ticks"`
		}
		if err := json.Unmarshal([]byte(triggerSpec), &spec); err != nil {
			return false, fmt.Errorf("parse interval spec: %w", err)
		}
		if spec.EveryNTicks <= 0 {
			return false, nil
		}
		return state.TickN > 0 && state.TickN%spec.EveryNTicks == 0, nil
	default:
		return false, fmt.Errorf("unknown trigger_kind %q", triggerKind)
	}
}

// evalPredicateNode recursively interprets the predicate AST. Supported
// node kinds (in `kind` field):
//
//	AND, OR              — boolean combinators over `clauses` array
//	tool_calls_window    — all of last N turns satisfy op(value)
//	cache_read_window    — same, against cache_read
//	input_tokens_window  — same, against input_tokens
//	output_growth_window — each turn's output ≥ factor × prior
//	regex_match_window   — at least one turn in window matches pattern
//	user_regex_window    — at least one user turn in window matches pattern
//	text_regex_window    — regex over user, assistant, or all text
//	entity_mention_window — built-in entity regex over recent text
//	tool_name_window     — presence/absence of named tool calls
//	envelope_type_window — presence/absence of emitted envelope refs
//	mail_unread_count    — numeric comparison over unread agent mail
//	identical_output_window — last N outputs byte-identical
//	prefix_pressure      — prefix_tokens ≥ factor × value (context window heuristic)
func evalPredicateNode(node map[string]interface{}, state State) (bool, error) {
	kind, _ := node["kind"].(string)
	switch kind {
	case "AND":
		clauses, ok := node["clauses"].([]interface{})
		if !ok || len(clauses) == 0 {
			return false, nil
		}
		for _, c := range clauses {
			cm, ok := c.(map[string]interface{})
			if !ok {
				return false, fmt.Errorf("AND clause not an object")
			}
			ok2, err := evalPredicateNode(cm, state)
			if err != nil {
				return false, err
			}
			if !ok2 {
				return false, nil
			}
		}
		return true, nil
	case "OR":
		clauses, ok := node["clauses"].([]interface{})
		if !ok || len(clauses) == 0 {
			return false, nil
		}
		for _, c := range clauses {
			cm, ok := c.(map[string]interface{})
			if !ok {
				return false, fmt.Errorf("OR clause not an object")
			}
			ok2, err := evalPredicateNode(cm, state)
			if err != nil {
				return false, err
			}
			if ok2 {
				return true, nil
			}
		}
		return false, nil
	case "tool_calls_window":
		return evalNumericWindow(node, state, func(m MessageSignal) int { return m.ToolCalls })
	case "cache_read_window":
		return evalNumericWindow(node, state, func(m MessageSignal) int { return m.CacheRead })
	case "input_tokens_window":
		return evalNumericWindow(node, state, func(m MessageSignal) int { return m.InputTokens })
	case "output_growth_window":
		return evalOutputGrowthWindow(node, state)
	case "regex_match_window":
		return evalTextRegexWindow(node, state, "assistant", "regex_match_window")
	case "user_regex_window":
		return evalTextRegexWindow(node, state, "user", "user_regex_window")
	case "text_regex_window":
		scope, _ := node["scope"].(string)
		if scope == "" {
			scope = "all"
		}
		return evalTextRegexWindow(node, state, scope, "text_regex_window")
	case "entity_mention_window":
		return evalEntityMentionWindow(node, state)
	case "tool_name_window":
		return evalNameWindow(node, state, "tool_name_window", func(m MessageSignal) []string { return m.ToolNames })
	case "envelope_type_window":
		return evalNameWindow(node, state, "envelope_type_window", func(m MessageSignal) []string { return m.EnvelopeTypes })
	case "mail_unread_count":
		op, _ := node["op"].(string)
		if op == "" {
			op = ">"
		}
		return compare(state.MailUnreadCount, op, intArg(node, "value", 0)), nil
	case "identical_output_window":
		return evalIdenticalOutputWindow(node, state)
	case "prefix_pressure":
		return evalPrefixPressure(node, state)
	default:
		return false, fmt.Errorf("unknown predicate kind %q", kind)
	}
}

// evalNumericWindow returns true when ALL of the last N messages
// satisfy op(extractor(message), value). op ∈ {=, <, >, <=, >=, !=}.
// If the window is larger than the available messages, returns false
// (cold-start property — no false-positives during warmup).
func evalNumericWindow(node map[string]interface{}, state State, extract func(MessageSignal) int) (bool, error) {
	window := intArg(node, "window", 3)
	op, _ := node["op"].(string)
	if op == "" {
		op = "="
	}
	value := intArg(node, "value", 0)
	if window <= 0 {
		return false, nil
	}
	if len(state.Messages) < window {
		return false, nil
	}
	for i := 0; i < window; i++ {
		got := extract(state.Messages[i])
		if !compare(got, op, value) {
			return false, nil
		}
	}
	return true, nil
}

// evalOutputGrowthWindow returns true when each of the last N turns
// has output_tokens ≥ factor × the IMMEDIATELY PRIOR turn's output.
// Window=3 means we check 2 growth-pairs (msg[0]/msg[1] and msg[1]/msg[2]).
// factor defaults to 1.5.
func evalOutputGrowthWindow(node map[string]interface{}, state State) (bool, error) {
	window := intArg(node, "window", 3)
	if window < 2 {
		return false, nil
	}
	if len(state.Messages) < window {
		return false, nil
	}
	factor := floatArg(node, "factor", 1.5)
	// Walk from latest (i=0) backwards. Compare each msg[i] to msg[i+1].
	// All comparisons must satisfy out[i] >= factor * out[i+1].
	for i := 0; i < window-1; i++ {
		cur := state.Messages[i].OutputTokens
		prev := state.Messages[i+1].OutputTokens
		if prev <= 0 {
			// no growth signal possible — be conservative, don't fire.
			return false, nil
		}
		if float64(cur) < factor*float64(prev) {
			return false, nil
		}
	}
	return true, nil
}

// evalTextRegexWindow returns true when AT LEAST ONE of the last N messages
// in scope matches pattern. regex_match_window retains its historical
// assistant-only behavior; newer authoring can use text_regex_window with
// scope=user|assistant|all.
func evalTextRegexWindow(node map[string]interface{}, state State, scope, label string) (bool, error) {
	window := intArg(node, "window", 3)
	pattern, _ := node["pattern"].(string)
	if pattern == "" {
		return false, fmt.Errorf("%s: pattern is required", label)
	}
	if window <= 0 {
		return false, nil
	}
	messages, err := scopedMessages(scope, state)
	if err != nil {
		return false, err
	}
	if len(messages) < window {
		return false, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Errorf("%s: compile pattern: %w", label, err)
	}
	for i := 0; i < window; i++ {
		if re.MatchString(messages[i].Content) {
			return true, nil
		}
	}
	return false, nil
}

func evalEntityMentionWindow(node map[string]interface{}, state State) (bool, error) {
	entity, _ := node["entity"].(string)
	pattern, _ := node["pattern"].(string)
	if pattern == "" {
		pattern = entityPattern(entity)
	}
	if pattern == "" {
		return false, fmt.Errorf("entity_mention_window: unknown entity %q", entity)
	}
	scope, _ := node["scope"].(string)
	if scope == "" {
		scope = "all"
	}
	node["pattern"] = pattern
	node["scope"] = scope
	return evalTextRegexWindow(node, state, scope, "entity_mention_window")
}

func entityPattern(entity string) string {
	switch entity {
	case "task_id", "torque_task_id":
		return `\b[A-Z][A-Z0-9]{1,15}-[0-9]+\b`
	case "path", "file_path":
		return `(?:^|\s)(?:~?/|\.{1,2}/|[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)`
	case "agent", "agent_id", "agent_urn":
		return `(?:\bagent:[A-Za-z0-9._:-]+\b|@[A-Za-z][A-Za-z0-9._-]+\b)`
	case "url":
		return `https?://[^\s)]+`
	default:
		return ""
	}
}

func evalNameWindow(node map[string]interface{}, state State, label string, extract func(MessageSignal) []string) (bool, error) {
	window := intArg(node, "window", 3)
	if window <= 0 {
		return false, nil
	}
	mode, _ := node["mode"].(string)
	if mode == "" {
		mode = "any"
	}
	names := stringSliceArg(node, "names")
	pattern, _ := node["pattern"].(string)
	if len(names) == 0 && pattern == "" {
		return false, fmt.Errorf("%s: names or pattern is required", label)
	}
	if len(state.Messages) < window {
		return false, nil
	}
	var re *regexp.Regexp
	if pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return false, fmt.Errorf("%s: compile pattern: %w", label, err)
		}
		re = compiled
	}

	matches := 0
	seenNames := make(map[string]bool, len(names))
	for _, name := range names {
		seenNames[name] = false
	}
	for i := 0; i < window; i++ {
		for _, got := range extract(state.Messages[i]) {
			if nameMatches(got, names, re) {
				matches++
				if _, ok := seenNames[got]; ok {
					seenNames[got] = true
				}
			}
		}
	}
	switch mode {
	case "any":
		return matches > 0, nil
	case "none", "absent":
		return matches == 0, nil
	case "all":
		if len(names) == 0 {
			return matches > 0, nil
		}
		for _, seen := range seenNames {
			if !seen {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, fmt.Errorf("%s: unknown mode %q", label, mode)
	}
}

// evalIdenticalOutputWindow returns true when the last N messages have
// byte-identical first-1KB content bodies. Used by the echo detector;
// 1KB cap mirrors FU-21 Detector C's pragmatic choice.
func evalIdenticalOutputWindow(node map[string]interface{}, state State) (bool, error) {
	window := intArg(node, "window", 3)
	if window <= 0 {
		return false, nil
	}
	if len(state.Messages) < window {
		return false, nil
	}
	first := head1KB(state.Messages[0].Content)
	for i := 1; i < window; i++ {
		if head1KB(state.Messages[i].Content) != first {
			return false, nil
		}
	}
	return true, nil
}

// evalPrefixPressure returns true when state.PrefixTokens >= ratio *
// context_window. context_window defaults to 200000 (Claude Sonnet 4 /
// Opus default); ratio defaults to 0.85.
func evalPrefixPressure(node map[string]interface{}, state State) (bool, error) {
	ratio := floatArg(node, "ratio", 0.85)
	contextWindow := intArg(node, "context_window", 200000)
	if state.PrefixTokens <= 0 || contextWindow <= 0 {
		return false, nil
	}
	return float64(state.PrefixTokens) >= ratio*float64(contextWindow), nil
}

// evalEvent is a thin event matcher: fires when an event with the
// given name is present in the state's recent event slice, or in the
// special-case wake_on_mail path when there are unread messages.
func evalEvent(name string, state State) bool {
	switch name {
	case "mail_received":
		return state.MailUnreadCount > 0
	}
	for _, e := range state.Events {
		if e.EventType == name {
			return true
		}
	}
	return false
}

// compare evaluates op as an integer comparison.
func compare(got int, op string, want int) bool {
	switch op {
	case "=", "==":
		return got == want
	case "!=":
		return got != want
	case "<":
		return got < want
	case "<=":
		return got <= want
	case ">":
		return got > want
	case ">=":
		return got >= want
	}
	return false
}

func scopedMessages(scope string, state State) ([]MessageSignal, error) {
	switch scope {
	case "", "assistant":
		return state.Messages, nil
	case "user":
		return state.UserMessages, nil
	case "all":
		out := make([]MessageSignal, 0, len(state.UserMessages)+len(state.Messages))
		out = append(out, state.UserMessages...)
		out = append(out, state.Messages...)
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].CreatedAt == "" || out[j].CreatedAt == "" {
				return false
			}
			return out[i].CreatedAt > out[j].CreatedAt
		})
		return out, nil
	default:
		return nil, fmt.Errorf("unknown text scope %q", scope)
	}
}

func nameMatches(got string, names []string, re *regexp.Regexp) bool {
	if got == "" {
		return false
	}
	for _, name := range names {
		if got == name {
			return true
		}
	}
	return re != nil && re.MatchString(got)
}

func stringSliceArg(node map[string]interface{}, key string) []string {
	raw, ok := node[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// intArg fetches an integer-shaped value from a predicate node spec.
// JSON unmarshals numbers as float64, so we accept either.
func intArg(node map[string]interface{}, key string, def int) int {
	if v, ok := node[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case int64:
			return int(t)
		}
	}
	return def
}

func floatArg(node map[string]interface{}, key string, def float64) float64 {
	if v, ok := node[key]; ok {
		switch t := v.(type) {
		case float64:
			return t
		case int:
			return float64(t)
		case int64:
			return float64(t)
		}
	}
	return def
}

func head1KB(s string) string {
	const cap = 1024
	if len(s) <= cap {
		return s
	}
	// Equality-compare only — both sides take the same byte cut, so
	// splitting mid-rune doesn't matter for matching.
	return s[:cap]
}
