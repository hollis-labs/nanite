package selftools

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/describer"
)

// TestRegisterSelfToolDescribers_PopulatesInitialAdopters confirms the
// three initial adopters (task_execute, tool_list, skill_list) are
// registered after a single RegisterSelfToolDescribers call. This is
// the wiring smoke test — if a name drifts, the materialization site
// will silently fall back to the static description and the acceptance
// test downstream wouldn't catch the typo.
func TestRegisterSelfToolDescribers_PopulatesInitialAdopters(t *testing.T) {
	reg := describer.NewRegistry()
	RegisterSelfToolDescribers(reg)
	for _, name := range []string{"task_execute", "tool_list", "skill_list"} {
		if !reg.Has(name) {
			t.Errorf("expected describer registered for %q after RegisterSelfToolDescribers", name)
		}
	}
}

// TestDescribeTaskExecute_TwoCallersTwoDescriptions is the production
// acceptance test (ticket Acceptance §3): the real describeTaskExecute
// renderer must produce different descriptions for two callers with
// different DispatchAllowlist values.
//
// Caller A: [researcher, planner, worker] → sees all three role slugs
// listed in description.
// Caller B: [worker] → sees only worker.
// Caller C (empty allowlist, today's default until W2A lands): sees
// the static base description — proves the W2A-pending fallback path.
func TestDescribeTaskExecute_TwoCallersTwoDescriptions(t *testing.T) {
	ctx := context.Background()

	callerA := describer.CallerAgent{
		ID:                "agent-A",
		Slug:              "default",
		DispatchAllowlist: []string{"researcher", "planner", "worker"},
	}
	callerB := describer.CallerAgent{
		ID:                "agent-B",
		Slug:              "restricted",
		DispatchAllowlist: []string{"worker"},
	}
	callerC := describer.CallerAgent{
		ID:   "agent-C",
		Slug: "default",
		// no DispatchAllowlist — current production state pre-W2A.
	}

	descA := describeTaskExecute(ctx, callerA)
	descB := describeTaskExecute(ctx, callerB)
	descC := describeTaskExecute(ctx, callerC)

	if descA == descB {
		t.Fatalf("descA == descB — task_execute Describer is not threading DispatchAllowlist through")
	}
	if !strings.Contains(descA, "planner") || !strings.Contains(descA, "researcher") || !strings.Contains(descA, "worker") {
		t.Errorf("descA missing one of [planner, researcher, worker]: %q", descA)
	}
	if !strings.Contains(descB, "worker") {
		t.Errorf("descB missing worker: %q", descB)
	}
	if strings.Contains(descB, "planner") || strings.Contains(descB, "researcher") {
		t.Errorf("descB leaked roles not in allowlist: %q", descB)
	}
	if descC != taskExecuteBaseDescription {
		t.Errorf("descC must equal baseline (empty allowlist) — got mismatch")
	}
	if !strings.Contains(descA, taskExecuteBaseDescription) {
		t.Errorf("descA must include baseline body — got %q", descA)
	}
}

// TestDescribeToolList_CallerSlugContextAdded covers the per-caller
// context note appended by describeToolList when Slug is set.
func TestDescribeToolList_CallerSlugContextAdded(t *testing.T) {
	ctx := context.Background()

	desc := describeToolList(ctx, describer.CallerAgent{Slug: "worker"})
	if !strings.Contains(desc, "you are agent `worker`") {
		t.Errorf("describeToolList missing caller-slug note: %q", desc)
	}
	if !strings.Contains(desc, toolListBaseDescription) {
		t.Errorf("describeToolList missing baseline body")
	}

	// No Slug → baseline only.
	bare := describeToolList(ctx, describer.CallerAgent{})
	if bare != toolListBaseDescription {
		t.Errorf("describeToolList with empty CallerAgent must equal baseline; got mismatch")
	}
}

// TestDescribeSkillList_AlwaysFallsThrough documents the v1 contract:
// skill_list's Describer always returns "" so the materialization site
// keeps the static description. Wired now to give W2A + later sprints
// a hook to extend without re-touching cmd/nanite/main.go.
func TestDescribeSkillList_AlwaysFallsThrough(t *testing.T) {
	ctx := context.Background()
	for _, caller := range []describer.CallerAgent{
		{},
		{Slug: "default"},
		{Slug: "worker", DispatchAllowlist: []string{"researcher"}},
	} {
		if got := describeSkillList(ctx, caller); got != "" {
			t.Errorf("describeSkillList(%+v) = %q, want empty (fall-through)", caller, got)
		}
	}
}
