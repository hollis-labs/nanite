package reflex_test

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/reflex"
)

// TestAssignRoleWithReflex_ReviewerMention_ResolvesToReviewerProfile is the
// CW-20260815-0002 regression guard: "review this"-style input must resolve
// to the real `reviewer` agent profile now that it exists
// (internal/agent/builtin/profiles/reviewer.md), not silently fall back to
// Worker the way the stale catalog entry used to.
func TestAssignRoleWithReflex_ReviewerMention_ResolvesToReviewerProfile(t *testing.T) {
	merged := reflex.MergeReflexes(reflex.BuiltinReflexes(), nil)
	assignment := reflex.AssignRoleWithReflex(
		"review this",
		classify.TierSmall,
		classify.PatternInline,
		merged,
		"sess-003",
		"",
		nil,
	)
	if assignment.AgentSlug != "reviewer" {
		t.Errorf("AgentSlug = %q, want reviewer", assignment.AgentSlug)
	}
	if assignment.Role != dispatch.RoleWorker {
		t.Errorf("Role = %v, want RoleWorker", assignment.Role)
	}
}

// TestAssignRoleWithReflex_ResearcherMention_ResolvesToResearcherProfile
// mirrors the reviewer guard above for the researcher-mention reflex, which
// gained a real profile (internal/agent/builtin/profiles/researcher.md) in
// the same CW-20260815-0002 fix.
func TestAssignRoleWithReflex_ResearcherMention_ResolvesToResearcherProfile(t *testing.T) {
	merged := reflex.MergeReflexes(reflex.BuiltinReflexes(), nil)
	assignment := reflex.AssignRoleWithReflex(
		"research the competitive landscape",
		classify.TierSmall,
		classify.PatternInline,
		merged,
		"sess-004",
		"",
		nil,
	)
	if assignment.AgentSlug != "researcher" {
		t.Errorf("AgentSlug = %q, want researcher", assignment.AgentSlug)
	}
}
