package reflexes

import (
	"encoding/json"
	"errors"

	shared "github.com/hollis-labs/substrate/agent/reflexes"
)

var errUnsupportedAttr = errors.New(`unknown predicate kind "attr"`)

// EvaluateTrigger evaluates authored predicate, event and interval triggers
// through the released steering library. State collection and validation stay
// in Nanite; no stored trigger JSON is rewritten here.
func EvaluateTrigger(triggerKind, triggerSpec string, state State) (bool, error) {
	if err := unsupportedHostPredicate(triggerKind, triggerSpec); err != nil {
		return false, err
	}
	return shared.EvaluateTrigger(triggerKind, triggerSpec, libraryState(state))
}

// The library's generic attr predicate is outside Nanite's trigger contract.
// Reject it at any depth under the supported combinators before delegation;
// malformed JSON and other unsupported kinds retain the library's error path.
func unsupportedHostPredicate(triggerKind, triggerSpec string) error {
	if triggerKind != "predicate" {
		return nil
	}
	var node map[string]any
	if err := json.Unmarshal([]byte(triggerSpec), &node); err != nil {
		return nil //nolint:nilerr // The delegated evaluator owns malformed-JSON errors.
	}
	if containsAttrPredicate(node) {
		return errUnsupportedAttr
	}
	return nil
}

func containsAttrPredicate(node map[string]any) bool {
	kind, _ := node["kind"].(string)
	if kind == "attr" {
		return true
	}
	if kind != "AND" && kind != "OR" {
		return false
	}
	clauses, _ := node["clauses"].([]any)
	for _, clause := range clauses {
		child, _ := clause.(map[string]any)
		if containsAttrPredicate(child) {
			return true
		}
	}
	return false
}
