package reflexes

import shared "github.com/hollis-labs/go-reflexes"

// EvaluateTrigger evaluates authored predicate, event and interval triggers
// through the released steering library. State collection and validation stay
// in Nanite; no stored trigger JSON is rewritten here.
func EvaluateTrigger(triggerKind, triggerSpec string, state State) (bool, error) {
	return shared.EvaluateTrigger(triggerKind, triggerSpec, libraryState(state))
}
