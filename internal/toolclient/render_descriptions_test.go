package toolclient

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/describer"
)

// TestRenderDescriptions_StaticToolsUnchanged is the regression test for
// the opt-in contract: tools without a registered Describer pass
// through with their static description unchanged.
func TestRenderDescriptions_StaticToolsUnchanged(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	// Intentionally register NO Describers — only the static path
	// should fire.

	tools := []llmtypes.ToolDefinition{
		{Name: "todo_create", Description: "Create a todo."},
		{Name: "skill_create", Description: "Create a skill."},
	}
	out := tb.RenderDescriptions(context.Background(), tools, describer.CallerAgent{ID: "agent-a", Slug: "default"})

	if len(out) != len(tools) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(tools))
	}
	for i, got := range out {
		if got.Description != tools[i].Description {
			t.Errorf("tool[%d] %q: description changed (%q → %q)", i, got.Name, tools[i].Description, got.Description)
		}
	}
}

// TestRenderDescriptions_FallThroughOnEmptyString verifies that a
// Describer returning "" falls back to the tool's static description.
// This is the documented contract that lets Describers no-op for
// callers without specialized rendering.
func TestRenderDescriptions_FallThroughOnEmptyString(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	tb.Describers.Register("tool_x", describer.Func(func(_ context.Context, _ describer.CallerAgent) string { return "" }))

	tools := []llmtypes.ToolDefinition{
		{Name: "tool_x", Description: "STATIC"},
	}
	out := tb.RenderDescriptions(context.Background(), tools, describer.CallerAgent{ID: "agent-a"})
	if out[0].Description != "STATIC" {
		t.Errorf("empty-string return: description = %q, want STATIC", out[0].Description)
	}
}

// TestRenderDescriptions_TwoCallersTwoDescriptions is the acceptance
// test called out in the ticket: same tool, two callers with different
// DispatchAllowlist values, two distinct rendered descriptions. The
// Describer here is a stand-in for the production task_execute renderer
// — the registry contract is the unit under test, not the production
// renderer wording.
//
// Caller A: DispatchAllowlist = [worker, planner, researcher] — sees
// all three roles listed in its description.
// Caller B: DispatchAllowlist = [worker] — sees only worker.
// Expectation: out_A != out_B, and both differ from the static body.
func TestRenderDescriptions_TwoCallersTwoDescriptions(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())

	staticBody := "task_execute base description."
	tb.Describers.Register("task_execute", describer.Func(func(_ context.Context, caller describer.CallerAgent) string {
		if len(caller.DispatchAllowlist) == 0 {
			return staticBody
		}
		return staticBody + " roles=" + strings.Join(caller.DispatchAllowlist, ",")
	}))

	tools := []llmtypes.ToolDefinition{
		{Name: "task_execute", Description: staticBody},
	}

	callerA := describer.CallerAgent{
		ID:                "agent-A",
		Slug:              "default",
		DispatchAllowlist: []string{"worker", "planner", "researcher"},
	}
	callerB := describer.CallerAgent{
		ID:                "agent-B",
		Slug:              "restricted",
		DispatchAllowlist: []string{"worker"},
	}

	outA := tb.RenderDescriptions(context.Background(), tools, callerA)
	outB := tb.RenderDescriptions(context.Background(), tools, callerB)

	if outA[0].Description == outB[0].Description {
		t.Fatalf("caller-A and caller-B descriptions are identical (%q) — Describer hook is not threading caller through", outA[0].Description)
	}
	if !strings.Contains(outA[0].Description, "worker,planner,researcher") {
		t.Errorf("caller-A description missing full allowlist: %q", outA[0].Description)
	}
	if !strings.Contains(outB[0].Description, "roles=worker") || strings.Contains(outB[0].Description, "planner") {
		t.Errorf("caller-B description has wrong allowlist: %q", outB[0].Description)
	}

	// And the static body must be untouched — RenderDescriptions copies
	// the input slice so callers can keep using their tool template.
	if tools[0].Description != staticBody {
		t.Errorf("input slice mutated: tools[0].Description = %q, want %q", tools[0].Description, staticBody)
	}
}

// TestRenderDescriptions_NilDescribersOnToolClient verifies that a
// ToolClient with Describers == nil (constructed manually rather than
// via New) is safe — RenderDescriptions returns the input unchanged.
func TestRenderDescriptions_NilDescribersOnToolClient(t *testing.T) {
	tb := &ToolClient{} // intentionally no Describers
	tools := []llmtypes.ToolDefinition{{Name: "foo", Description: "BAR"}}
	out := tb.RenderDescriptions(context.Background(), tools, describer.CallerAgent{})
	if len(out) != 1 || out[0].Description != "BAR" {
		t.Errorf("nil Describers: out = %+v, want unchanged", out)
	}
}
