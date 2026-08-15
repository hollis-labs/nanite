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

// dispatchableProfileSlugs are profile slugs we know are seeded as agent
// profiles and therefore safe to dispatch to. An empty slug is also safe —
// it falls through to dispatch.AssignRole's default (WorkerRoleSlug or
// PlannerRoleSlug), both of which are seeded.
//
// Keep this set in sync with the builtin agent profiles under
// internal/agent/builtin/profiles/ — the file source-of-truth ingested
// at boot by AutoIngestAgents. Adding a new dispatchable profile slug
// here without also adding the agent profile file will break dispatch
// at runtime — see internal/service/subagent_runner.go::resolveRole
// (and the fail-fast Spawn gate from CW-20260519-0123, which also
// rejects unknown roles at the spawn boundary).
var dispatchableProfileSlugs = map[string]bool{
	"":           true, // empty → AssignRole default (worker or planner)
	"worker":     true,
	"planner":    true,
	"researcher": true,
	"reviewer":   true,
}

// TestBuiltinReflexes_ProfileResolves guards against the broker-v1 failure
// mode surfaced in the CW-20260509-0050 catalog audit: a reflex whose
// Profile slug does not resolve to a seeded agent profile causes the
// subagent runner to fail at dispatch with errRoleResolveFailed once the
// broker treats reflex matches as the highest-priority routing rule.
//
// If you need a new Profile slug here, first add the agent profile file
// under internal/agent/builtin/profiles/ AND extend dispatchableProfileSlugs
// above.
func TestBuiltinReflexes_ProfileResolves(t *testing.T) {
	for _, r := range reflex.BuiltinReflexes() {
		if !dispatchableProfileSlugs[r.ResolvesTo.Profile] {
			t.Errorf("reflex %q: Profile %q is not a seeded agent profile slug — "+
				"dispatch will fail at runtime. Either ship the agent profile or "+
				"set Profile to \"\" to fall back to AssignRole's default.",
				r.ID, r.ResolvesTo.Profile)
		}
	}
}
