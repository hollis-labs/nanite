package agentworkflow

import (
	"regexp"
	"sort"
)

// requiredInputRefPattern matches a {{input.<key>}} template reference the
// same way the built-in engine's step-config resolver does (see
// internal/service/workflow_engine.go's templateRefPattern + the
// "input."-prefixed branch of resolveTemplateRef), narrowed to only the
// input.<key> shape — a steps.<id>.<field> reference isn't a caller-
// supplied param, so it's not "required input" in the sense this file cares
// about.
var requiredInputRefPattern = regexp.MustCompile(`\{\{\s*input\.([a-zA-Z0-9_]+)\s*\}\}`)

// RequiredInputs statically scans every step's Config for {{input.<key>}}
// template references and returns the referenced keys, deduplicated, in
// first-seen order (steps in definition order, config keys sorted within a
// step for determinism).
//
// This is a best-effort scan, not a formal declaration — WorkflowDefinition
// has no separate "inputs" schema (CW-20260815-0022's finding: it never
// did), so "referenced anywhere in a step's Config" is the only signal
// available. It only covers the built-in engine's resolution surface (a
// step's Config map, recursively) — an external-engine definition's Steps
// exist only as human-readable documentation of intent (see
// WorkflowDefinition.Engine's doc comment) and are scanned the same way for
// whatever value that has, but the real requirement for those lives in the
// hand-authored Python graph/crew, which this cannot see.
//
// Exists so workflow_run callers (and its error path when a required input
// is missing) can be told concretely what a named workflow needs instead of
// discovering a missing one only when a step deep in the run fails to
// resolve it.
func RequiredInputs(def WorkflowDefinition) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, step := range def.Steps {
		scanConfigForInputRefs(step.Config, seen, &keys)
	}
	return keys
}

func scanConfigForInputRefs(v any, seen map[string]bool, keys *[]string) {
	switch val := v.(type) {
	case string:
		for _, m := range requiredInputRefPattern.FindAllStringSubmatch(val, -1) {
			key := m[1]
			if !seen[key] {
				seen[key] = true
				*keys = append(*keys, key)
			}
		}
	case map[string]any:
		mkeys := make([]string, 0, len(val))
		for k := range val {
			mkeys = append(mkeys, k)
		}
		sort.Strings(mkeys)
		for _, k := range mkeys {
			scanConfigForInputRefs(val[k], seen, keys)
		}
	case []any:
		for _, sub := range val {
			scanConfigForInputRefs(sub, seen, keys)
		}
	}
}
