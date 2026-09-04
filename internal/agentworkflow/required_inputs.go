package agentworkflow

import (
	"regexp"
	"sort"
	"strings"
)

// templateRefPattern captures everything between {{ }} (anything but a
// closing brace), with no restriction on the captured content. The shared
// host's Nanite value resolver then strips the "input." prefix with no
// further validation on the key shape, so a key can contain hyphens, dots,
// or anything else that isn't
// "}". An earlier version of this file narrowed the key to [a-zA-Z0-9_]+,
// which silently missed real references like {{input.task-id}} — this
// pattern + the prefix-strip below must stay in lock-step with the host's
// actual parsing, not a guessed-at subset of it.
var templateRefPattern = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

// RequiredInputs statically scans every step's Config for {{input.<key>}}
// template references and returns the referenced keys, deduplicated, in
// first-seen order (steps in definition order, config keys sorted within a
// step for determinism).
//
// This is a best-effort scan, not a formal declaration — WorkflowDefinition
// has no separate "inputs" schema (CW-20260815-0022's finding: it never
// did), so "referenced anywhere in a step's Config" is the only signal
// available. It only covers the native StepKind translation surface (a step's
// Config map, recursively) — an external-engine definition's Steps
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
		for _, m := range templateRefPattern.FindAllStringSubmatch(val, -1) {
			ref := m[1]
			if !strings.HasPrefix(ref, "input.") {
				continue // e.g. steps.<id>.<field> — not a caller-supplied param
			}
			key := strings.TrimPrefix(ref, "input.")
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			*keys = append(*keys, key)
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
