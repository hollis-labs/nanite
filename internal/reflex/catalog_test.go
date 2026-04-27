package reflex_test

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/reflex"
)

// knownPatterns are the valid pattern slugs from docs/agent-pattern-catalog.md.
var knownPatterns = map[string]bool{
	"chat":       true,
	"strategist": true,
	"planner":    true,
	"researcher": true,
	"documentor": true,
	"worker":     true,
	"reviewer":   true,
}

// knownRoles are the valid dispatch role strings from internal/dispatch/role.go.
var knownRoles = map[string]bool{
	"chat":    true,
	"worker":  true,
	"planner": true,
}

// TestBuiltinReflexes_Schema verifies that every built-in reflex:
//   - has a non-empty ID
//   - resolves to a known pattern slug (docs/agent-pattern-catalog.md)
//   - resolves to a known dispatch role (internal/dispatch/role.go)
//   - has at least one trigger phrase
//   - has a unique ID within the set
func TestBuiltinReflexes_Schema(t *testing.T) {
	reflexes := reflex.BuiltinReflexes()
	if len(reflexes) == 0 {
		t.Fatal("BuiltinReflexes returned empty set")
	}

	seen := map[string]bool{}
	for i, r := range reflexes {
		if r.ID == "" {
			t.Errorf("reflex[%d]: empty ID", i)
		}
		if seen[r.ID] {
			t.Errorf("reflex[%d]: duplicate ID %q", i, r.ID)
		}
		seen[r.ID] = true

		if !knownPatterns[r.ResolvesTo.Pattern] {
			t.Errorf("reflex %q: unknown pattern slug %q (must be in docs/agent-pattern-catalog.md)", r.ID, r.ResolvesTo.Pattern)
		}
		if !knownRoles[r.ResolvesTo.Role] {
			t.Errorf("reflex %q: unknown role %q (must be a dispatch.Role string from internal/dispatch/role.go)", r.ID, r.ResolvesTo.Role)
		}
		if len(r.Triggers.UserPhraseAnyOf) == 0 {
			t.Errorf("reflex %q: no trigger phrases — every reflex must have at least one", r.ID)
		}
	}
}

// TestBuiltinReflexes_Priority verifies that priorities are in range [0,100].
func TestBuiltinReflexes_Priority(t *testing.T) {
	for _, r := range reflex.BuiltinReflexes() {
		if r.Priority < 0 || r.Priority > 100 {
			t.Errorf("reflex %q: priority %d out of range [0,100]", r.ID, r.Priority)
		}
	}
}

// TestBuiltinReflexes_Count ensures the v1 set has the expected number of
// built-in entries. Update this number when adding or removing entries.
func TestBuiltinReflexes_Count(t *testing.T) {
	const wantMin = 5
	got := len(reflex.BuiltinReflexes())
	if got < wantMin {
		t.Errorf("BuiltinReflexes: got %d entries, want at least %d", got, wantMin)
	}
}
