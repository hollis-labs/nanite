package dispatch

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/classify"
)

// TestAssignRole_TableDriven covers the (tier, pattern) → role mapping.
func TestAssignRole_TableDriven(t *testing.T) {
	cases := []struct {
		name     string
		tier     classify.ScopeTier
		pattern  classify.ExecutionPattern
		wantRole Role
		wantSlug string
		wantMode string
	}{
		{
			name:     "trivial inline → worker (sync)",
			tier:     classify.TierTrivial,
			pattern:  classify.PatternInline,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "small inline → worker (sync)",
			tier:     classify.TierSmall,
			pattern:  classify.PatternInline,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "medium subagent → worker (sync)",
			tier:     classify.TierMedium,
			pattern:  classify.PatternSubagent,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "large subagent → worker (sync)",
			tier:     classify.TierLarge,
			pattern:  classify.PatternSubagent,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "open subagent → planner (sync)",
			tier:     classify.TierOpen,
			pattern:  classify.PatternSubagent,
			wantRole: RolePlanner,
			wantSlug: PlannerRoleSlug,
			wantMode: "sync",
		},
		{
			name:     "any background → worker (async)",
			tier:     classify.TierMedium,
			pattern:  classify.PatternBackground,
			wantRole: RoleWorker,
			wantSlug: WorkerRoleSlug,
			wantMode: "async",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssignRole(tc.tier, tc.pattern)
			if got.Role != tc.wantRole {
				t.Errorf("Role = %v, want %v", got.Role, tc.wantRole)
			}
			if got.AgentSlug != tc.wantSlug {
				t.Errorf("AgentSlug = %q, want %q", got.AgentSlug, tc.wantSlug)
			}
			if got.Mode != tc.wantMode {
				t.Errorf("Mode = %q, want %q", got.Mode, tc.wantMode)
			}
		})
	}
}

// TestRole_StringAndIsValid covers the enum helpers.
func TestRole_StringAndIsValid(t *testing.T) {
	cases := []struct {
		r       Role
		str     string
		isValid bool
	}{
		{RoleInvalid, "invalid", false},
		{RoleChat, "chat", true},
		{RoleWorker, "worker", true},
		{RolePlanner, "planner", true},
		{Role(99), "invalid", false},
	}
	for _, c := range cases {
		if got := c.r.String(); got != c.str {
			t.Errorf("Role(%d).String() = %q, want %q", c.r, got, c.str)
		}
		if got := c.r.IsValid(); got != c.isValid {
			t.Errorf("Role(%d).IsValid() = %v, want %v", c.r, got, c.isValid)
		}
	}
}
